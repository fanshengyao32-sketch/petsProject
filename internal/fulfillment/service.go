package fulfillment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Clock interface{ Now() time.Time }

type Service struct {
	db    *gorm.DB
	clock Clock
}

type OperateInput struct {
	OperatorID     uint64
	BookingNo      string
	IdempotencyKey string
}

func NewService(db *gorm.DB, clock Clock) *Service { return &Service{db: db, clock: clock} }

func (s *Service) CheckIn(ctx context.Context, input OperateInput) (Event, error) {
	return s.transition(ctx, input, "front_desk", "confirmed", "checked_in", EventCheckedIn, "fulfillment.check_in")
}

func (s *Service) StartService(ctx context.Context, input OperateInput) (Event, error) {
	return s.transition(ctx, input, "service_staff", "checked_in", "in_service", EventServiceStart, "fulfillment.service_start")
}

func (s *Service) CompleteService(ctx context.Context, input OperateInput) (Event, error) {
	return s.transition(ctx, input, "service_staff", "in_service", "completed", EventCompleted, "fulfillment.complete")
}

func (s *Service) transition(ctx context.Context, input OperateInput, requiredRole, fromStatus, toStatus, eventType, action string) (Event, error) {
	if input.OperatorID == 0 || input.BookingNo == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 64 {
		return Event{}, ErrInvalidRequest
	}
	var result Event
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var operator user
		if err := tx.First(&operator, input.OperatorID).Error; err != nil || operator.Role != requiredRole || operator.StoreID == nil {
			return ErrForbidden
		}
		var record booking
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("booking_no = ?", input.BookingNo).First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBookingNotFound
			}
			return err
		}
		var serviceSlot slot
		if err := tx.First(&serviceSlot, record.SlotID).Error; err != nil {
			return err
		}
		if serviceSlot.StoreID != *operator.StoreID {
			return ErrForbidden
		}

		// Use a locking current read. Under MySQL REPEATABLE READ, a plain read
		// could keep the transaction snapshot from before a concurrent first
		// operation committed and misclassify an idempotent retry as invalid.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("booking_id = ? AND event_type = ?", record.ID, eventType).First(&result).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if record.Status != fromStatus {
			return ErrInvalidBookingState
		}
		if eventType == EventCheckedIn {
			var pending int64
			if err := tx.Model(&cancellationRequest{}).Where("booking_id = ? AND status = ?", record.ID, "pending").Count(&pending).Error; err != nil {
				return err
			}
			if pending > 0 {
				return ErrCancellationPending
			}
		}
		now := s.clock.Now().UTC()
		result = Event{BookingID: record.ID, EventType: eventType, OperatorID: operator.ID, OccurredAt: now, IdempotencyKey: input.IdempotencyKey, CreatedAt: now}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if err := tx.Model(&record).Updates(map[string]any{"status": toStatus, "updated_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
			return err
		}
		detail := fmt.Sprintf(`{"from":%q,"to":%q}`, fromStatus, toStatus)
		audit := auditLog{ActorID: &operator.ID, Action: action, ResourceType: "booking", ResourceID: record.BookingNo, Result: "succeeded", DetailJSON: detail, OccurredAt: now}
		return tx.Create(&audit).Error
	})
	return result, err
}

func (s *Service) ListStore(ctx context.Context, storeID uint64) ([]WorkView, error) {
	if storeID == 0 {
		return nil, ErrInvalidRequest
	}
	var views []WorkView
	err := s.db.WithContext(ctx).Table("bookings b").
		Select(`b.booking_no, owner.display_name AS owner_name, p.name AS pet_name, s.starts_at, s.ends_at, b.status,
		MAX(CASE WHEN e.event_type = 'checked_in' THEN e.occurred_at END) AS checkin_at,
		COALESCE(MAX(CASE WHEN e.event_type = 'checked_in' THEN operator.display_name END), '') AS checkin_by,
		MAX(CASE WHEN e.event_type = 'service_started' THEN e.occurred_at END) AS started_at,
		COALESCE(MAX(CASE WHEN e.event_type = 'service_started' THEN operator.display_name END), '') AS started_by,
		MAX(CASE WHEN e.event_type = 'completed' THEN e.occurred_at END) AS completed_at,
		COALESCE(MAX(CASE WHEN e.event_type = 'completed' THEN operator.display_name END), '') AS completed_by`).
		Joins("JOIN service_slots s ON s.id = b.slot_id").Joins("JOIN users owner ON owner.id = b.owner_id").Joins("JOIN pets p ON p.id = b.pet_id").
		Joins("LEFT JOIN fulfillment_events e ON e.booking_id = b.id").Joins("LEFT JOIN users operator ON operator.id = e.operator_id").
		Where("s.store_id = ? AND b.status IN ?", storeID, []string{"confirmed", "cancel_pending", "checked_in", "in_service", "completed"}).
		Group("b.id, b.booking_no, owner.display_name, p.name, s.starts_at, s.ends_at, b.status").Order("s.starts_at, b.id").Scan(&views).Error
	return views, err
}
