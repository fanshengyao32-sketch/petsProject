package payment

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ExecuteRefundInput struct {
	ActorID  uint64
	RefundNo string
	Scenario string
}

func (s *Service) ExecuteRefund(ctx context.Context, input ExecuteRefundInput) (RefundOrder, error) {
	if input.ActorID == 0 || input.RefundNo == "" || !validRefundScenario(input.Scenario) {
		return RefundOrder{}, ErrInvalidRequest
	}
	var refund RefundOrder
	var payment PaymentOrder
	var attempt RefundAttempt
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lookup RefundOrder
		if err := tx.Where("refund_no = ?", input.RefundNo).First(&lookup).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRefundNotFound
			}
			return err
		}
		var booking paymentBooking
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&booking, lookup.BookingID).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&refund, lookup.ID).Error; err != nil {
			return err
		}
		if refund.Status == RefundSucceeded {
			return nil
		}
		if refund.Status == RefundProcessing {
			return ErrRefundInProgress
		}
		if refund.Status != RefundPending && refund.Status != RefundFailed && refund.Status != RefundUnknown {
			return ErrRefundNotAllowed
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&payment, refund.PaymentOrderID).Error; err != nil {
			return err
		}
		if payment.Status != OrderSucceeded || payment.ChannelTradeNo == nil || *payment.ChannelTradeNo == "" {
			return ErrRefundNotAllowed
		}
		var sequence int
		if err := tx.Model(&RefundAttempt{}).Where("refund_order_id = ?", refund.ID).Select("COALESCE(MAX(sequence_no), 0)").Scan(&sequence).Error; err != nil {
			return err
		}
		sequence++
		now := s.clock.Now().UTC()
		attempt = RefundAttempt{RefundOrderID: refund.ID, RequestNo: fmt.Sprintf("%s-%02d", refund.RefundNo, sequence), SequenceNo: sequence, Status: RefundProcessing, RequestedAt: now}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		return tx.Model(&refund).Updates(map[string]any{"status": RefundProcessing, "failure_code": nil, "updated_at": now}).Error
	})
	if err != nil || refund.Status == RefundSucceeded {
		return refund, err
	}

	result, providerErr := s.provider.CreateRefund(ctx, RefundRequest{RequestNo: attempt.RequestNo, RefundNo: refund.RefundNo, PaymentTradeNo: *payment.ChannelTradeNo, Amount: refund.AmountCents, AttemptSequence: attempt.SequenceNo, SimulationScenario: input.Scenario})
	if providerErr != nil {
		result = Result{Status: StatusUnknown, FailureCode: "PROVIDER_UNAVAILABLE", OccurredAt: s.clock.Now().UTC()}
	}
	if err := s.applyRefundResult(ctx, refund.ID, attempt.ID, input.ActorID, result); err != nil {
		return RefundOrder{}, err
	}
	if err := s.db.WithContext(ctx).First(&refund, refund.ID).Error; err != nil {
		return RefundOrder{}, err
	}
	return refund, nil
}

func (s *Service) applyRefundResult(ctx context.Context, refundID, attemptID, actorID uint64, result Result) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lookup RefundOrder
		if err := tx.First(&lookup, refundID).Error; err != nil {
			return err
		}
		var booking paymentBooking
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&booking, lookup.BookingID).Error; err != nil {
			return err
		}
		var refund RefundOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&refund, refundID).Error; err != nil {
			return err
		}
		var attempt RefundAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&attempt, attemptID).Error; err != nil {
			return err
		}
		if attempt.Status != RefundProcessing {
			return nil
		}
		now := s.clock.Now().UTC()
		status, failure := RefundUnknown, result.FailureCode
		if result.Status == StatusSucceeded {
			status, failure = RefundSucceeded, ""
		}
		if result.Status == StatusFailed {
			status = RefundFailed
		}
		attemptUpdates := map[string]any{"status": status, "failure_code": nullableString(failure), "completed_at": now}
		if err := tx.Model(&attempt).Updates(attemptUpdates).Error; err != nil {
			return err
		}
		refundUpdates := map[string]any{"status": status, "failure_code": nullableString(failure), "updated_at": now}
		if status == RefundSucceeded {
			refundUpdates["refunded_at"] = now
		}
		if err := tx.Model(&refund).Updates(refundUpdates).Error; err != nil {
			return err
		}
		detail := fmt.Sprintf(`{"refund_no":%q,"attempt":%d,"status":%q}`, refund.RefundNo, attempt.SequenceNo, status)
		audit := auditLog{ActorID: &actorID, Action: "refund.execute", ResourceType: "booking", ResourceID: booking.BookingNo, Result: status, DetailJSON: detail, OccurredAt: now}
		return tx.Create(&audit).Error
	})
}

func (s *Service) ListRefunds(ctx context.Context) ([]RefundView, error) {
	var views []RefundView
	err := s.db.WithContext(ctx).Table("refund_orders r").
		Select("r.refund_no, b.booking_no, r.amount_cents, r.status, r.reason, COALESCE(r.failure_code, '') AS failure_code, COUNT(a.id) AS attempt_count, r.updated_at").
		Joins("JOIN bookings b ON b.id = r.booking_id").Joins("LEFT JOIN refund_attempts a ON a.refund_order_id = r.id").
		Group("r.id, r.refund_no, b.booking_no, r.amount_cents, r.status, r.reason, r.failure_code, r.updated_at").Order("r.updated_at DESC").Scan(&views).Error
	return views, err
}

func validRefundScenario(value string) bool {
	return value == "success" || value == "failure" || value == "timeout"
}
