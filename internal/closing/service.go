package closing

import (
	"context"
	"encoding/json"
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

type MarkNoShowInput struct {
	ManagerID uint64
	BookingNo string
	Reason    string
}

func NewService(db *gorm.DB, clock Clock) *Service { return &Service{db: db, clock: clock} }

func (s *Service) MarkNoShow(ctx context.Context, input MarkNoShowInput) (NoShowRecord, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ManagerID == 0 || input.BookingNo == "" || input.Reason == "" || len(input.Reason) > 500 {
		return NoShowRecord{}, ErrInvalidRequest
	}
	var result NoShowRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var manager user
		if err := tx.First(&manager, input.ManagerID).Error; err != nil || manager.Role != "manager" || manager.StoreID == nil {
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
		if serviceSlot.StoreID != *manager.StoreID {
			return ErrForbidden
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("booking_id = ?", record.ID).First(&result).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if record.Status != "confirmed" {
			return ErrInvalidBookingState
		}
		now := s.clock.Now().UTC()
		if now.Before(serviceSlot.EndsAt) {
			return ErrSlotNotEnded
		}
		result = NoShowRecord{BookingID: record.ID, MarkedBy: manager.ID, Reason: input.Reason, MarkedAt: now}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if err := tx.Model(&record).Updates(map[string]any{"status": "no_show", "updated_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
			return err
		}
		detailBytes, _ := json.Marshal(map[string]string{"reason": input.Reason})
		audit := auditLog{ActorID: &manager.ID, Action: "closing.mark_no_show", ResourceType: "booking", ResourceID: record.BookingNo, Result: "succeeded", DetailJSON: string(detailBytes), OccurredAt: now}
		return tx.Create(&audit).Error
	})
	return result, err
}

func (s *Service) ListNoShows(ctx context.Context, storeID uint64) ([]NoShowView, error) {
	if storeID == 0 {
		return nil, ErrInvalidRequest
	}
	var views []NoShowView
	err := s.db.WithContext(ctx).Table("bookings b").
		Select("b.booking_no, owner.display_name AS owner_name, p.name AS pet_name, s.starts_at, s.ends_at, b.status, COALESCE(n.reason, '') AS reason, n.marked_at, COALESCE(marker.display_name, '') AS marked_by").
		Joins("JOIN service_slots s ON s.id = b.slot_id").Joins("JOIN users owner ON owner.id = b.owner_id").Joins("JOIN pets p ON p.id = b.pet_id").
		Joins("LEFT JOIN no_show_records n ON n.booking_id = b.id").Joins("LEFT JOIN users marker ON marker.id = n.marked_by").
		Where("s.store_id = ? AND b.status IN ?", storeID, []string{"confirmed", "no_show"}).Order("s.ends_at DESC, b.id").Scan(&views).Error
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC()
	for index := range views {
		views[index].CanMark = views[index].Status == "confirmed" && !now.Before(views[index].EndsAt)
	}
	return views, nil
}
