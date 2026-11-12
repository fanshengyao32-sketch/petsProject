package cancellation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Clock interface{ Now() time.Time }

type Service struct {
	db    *gorm.DB
	clock Clock
}

func NewService(db *gorm.DB, clock Clock) *Service { return &Service{db: db, clock: clock} }

func (s *Service) Request(ctx context.Context, ownerID uint64, bookingNo, reason string) (Request, error) {
	if ownerID == 0 || bookingNo == "" || len(reason) > 500 {
		return Request{}, ErrInvalidRequest
	}
	reason = strings.TrimSpace(reason)
	var result Request
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var booking booking
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("booking_no = ?", bookingNo).First(&booking).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBookingNotFound
			}
			return err
		}
		if booking.OwnerID != ownerID {
			return ErrForbidden
		}
		if booking.Status == "cancelled" {
			if err := tx.Where("booking_id = ? AND status IN ?", booking.ID, []string{StatusApproved, StatusAutoApproved}).Order("id DESC").First(&result).Error; err == nil {
				return nil
			}
		}
		if booking.Status == "cancel_pending" {
			if err := tx.Where("booking_id = ? AND status = ?", booking.ID, StatusPending).First(&result).Error; err == nil {
				return nil
			}
		}
		if booking.Status != "confirmed" {
			return ErrInvalidBookingState
		}
		var slot slot
		if err := tx.First(&slot, booking.SlotID).Error; err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		if !now.Before(slot.StartsAt) {
			return ErrServiceStarted
		}
		result = Request{BookingID: booking.ID, RequesterID: ownerID, Reason: reason, RequestedAt: now}
		if slot.StartsAt.Sub(now) >= 2*time.Hour {
			if result.Reason == "" {
				result.Reason = "提前至少2小时取消"
			}
			result.Status = StatusAutoApproved
			reviewerTime := now
			result.ReviewedAt = &reviewerTime
			if err := tx.Create(&result).Error; err != nil {
				return err
			}
			if err := approveBooking(tx, booking, ownerID, now, "cancellation.auto_approved"); err != nil {
				return err
			}
			return nil
		}
		if reason == "" {
			return ErrReasonRequired
		}
		result.Status = StatusPending
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		return tx.Model(&booking).Updates(map[string]any{"status": "cancel_pending", "updated_at": now, "version": gorm.Expr("version + 1")}).Error
	})
	return result, err
}

func (s *Service) Review(ctx context.Context, supportID, requestID uint64, decision, reviewReason string) (Request, error) {
	if supportID == 0 || requestID == 0 || (decision != StatusApproved && decision != StatusRejected) || len(reviewReason) > 500 {
		return Request{}, ErrInvalidRequest
	}
	reviewReason = strings.TrimSpace(reviewReason)
	var result Request
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var support user
		if err := tx.First(&support, supportID).Error; err != nil || support.Role != "support" {
			return ErrForbidden
		}
		var lookup Request
		if err := tx.First(&lookup, requestID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRequestNotFound
			}
			return err
		}
		var booking booking
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&booking, lookup.BookingID).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&result, requestID).Error; err != nil {
			return err
		}
		if result.Status != StatusPending {
			if result.Status == decision {
				return nil
			}
			return ErrAlreadyReviewed
		}
		if booking.Status != "cancel_pending" {
			return ErrInvalidBookingState
		}
		now := s.clock.Now().UTC()
		updates := map[string]any{"status": decision, "reviewed_by": supportID, "reviewed_at": now, "review_reason": nullableString(reviewReason), "updated_at": now}
		if err := tx.Model(&result).Updates(updates).Error; err != nil {
			return err
		}
		if decision == StatusApproved {
			if err := approveBooking(tx, booking, supportID, now, "cancellation.support_approved"); err != nil {
				return err
			}
		} else if err := tx.Model(&booking).Updates(map[string]any{"status": "confirmed", "updated_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
			return err
		}
		return tx.First(&result, requestID).Error
	})
	return result, err
}

func approveBooking(tx *gorm.DB, booking booking, actorID uint64, now time.Time, action string) error {
	if booking.CapacityReleasedAt != nil {
		return ErrInvalidBookingState
	}
	if err := createCancellationRefund(tx, booking); err != nil {
		return err
	}
	if err := tx.Model(&booking).Updates(map[string]any{"status": "cancelled", "capacity_released_at": now, "updated_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
		return err
	}
	detail := `{"reason":"approved_cancellation"}`
	audit := auditLog{ActorID: &actorID, Action: action, ResourceType: "booking", ResourceID: booking.BookingNo, Result: "refund_pending", DetailJSON: detail, OccurredAt: now}
	return tx.Create(&audit).Error
}

func createCancellationRefund(tx *gorm.DB, booking booking) error {
	var payment paymentOrder
	err := tx.Table("payment_orders p").
		Select("p.*").Joins("LEFT JOIN refund_orders r ON r.payment_order_id = p.id").
		Where("p.booking_id = ? AND p.status = ? AND r.id IS NULL", booking.ID, "succeeded").
		Order("p.paid_at ASC, p.id ASC").First(&payment).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrPaymentNotFound
		}
		return err
	}
	refund := refundOrder{RefundNo: cancellationRefundNo(booking.BookingNo, payment.RequestNo), BookingID: booking.ID, PaymentOrderID: payment.ID, AmountCents: payment.AmountCents, Status: "pending", Reason: "approved_cancellation"}
	return tx.Create(&refund).Error
}

func (s *Service) List(ctx context.Context) ([]View, error) {
	var views []View
	err := s.db.WithContext(ctx).Table("cancellation_requests c").
		Select("c.id, b.booking_no, u.display_name AS owner_name, c.reason, c.status, c.requested_at, COALESCE(c.review_reason, '') AS review_reason").
		Joins("JOIN bookings b ON b.id = c.booking_id").Joins("JOIN users u ON u.id = c.requester_id").
		Order("CASE WHEN c.status = 'pending' THEN 0 ELSE 1 END, c.requested_at DESC").Scan(&views).Error
	return views, err
}

func cancellationRefundNo(bookingNo, requestNo string) string {
	hash := sha256.Sum256([]byte(bookingNo + ":" + requestNo))
	return "RF" + hex.EncodeToString(hash[:16])
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
