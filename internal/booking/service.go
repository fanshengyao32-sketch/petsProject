package booking

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Clock interface{ Now() time.Time }

type Service struct {
	db    *gorm.DB
	clock Clock
}

type CreateBookingInput struct {
	OwnerID    uint64
	PetID      uint64
	SlotID     uint64
	RequestKey string
}

type CreateSlotInput struct {
	ManagerID     uint64
	StoreID       uint64
	ServiceItemID uint64
	StartsAt      time.Time
	EndsAt        time.Time
}

func NewService(db *gorm.DB, clock Clock) *Service { return &Service{db: db, clock: clock} }

func (s *Service) CreateSlot(ctx context.Context, input CreateSlotInput) (ServiceSlot, error) {
	if !input.EndsAt.After(input.StartsAt) || !input.StartsAt.After(s.clock.Now()) {
		return ServiceSlot{}, ErrInvalidSlotTime
	}
	var result ServiceSlot
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// MySQL has no exclusion constraint. Locking the store serializes overlap
		// checks for slot creation in that store.
		var store Store
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&store, input.StoreID).Error; err != nil {
			return ErrForbidden
		}
		var manager User
		if err := tx.First(&manager, input.ManagerID).Error; err != nil || manager.Role != "manager" || manager.StoreID == nil || *manager.StoreID != input.StoreID {
			return ErrForbidden
		}
		var count int64
		if err := tx.Model(&ServiceSlot{}).
			Where("store_id = ? AND service_item_id = ? AND starts_at < ? AND ends_at > ?", input.StoreID, input.ServiceItemID, input.EndsAt.UTC(), input.StartsAt.UTC()).
			Count(&count).Error; err != nil {
			return fmt.Errorf("check slot overlap: %w", err)
		}
		if count > 0 {
			return ErrSlotOverlap
		}
		result = ServiceSlot{StoreID: input.StoreID, ServiceItemID: input.ServiceItemID, StartsAt: input.StartsAt.UTC(), EndsAt: input.EndsAt.UTC(), Capacity: 3, CreatedBy: input.ManagerID}
		if err := tx.Create(&result).Error; err != nil {
			return fmt.Errorf("create slot: %w", err)
		}
		return nil
	})
	return result, err
}

func (s *Service) ListSlots(ctx context.Context, storeID uint64) ([]SlotView, error) {
	if err := s.ExpireHolds(ctx); err != nil {
		return nil, err
	}
	var slots []ServiceSlot
	if err := s.db.WithContext(ctx).Where("store_id = ?", storeID).Order("starts_at").Find(&slots).Error; err != nil {
		return nil, fmt.Errorf("list slots: %w", err)
	}
	views := make([]SlotView, 0, len(slots))
	for _, slot := range slots {
		var occupied int64
		if err := s.db.WithContext(ctx).Model(&Booking{}).Where("slot_id = ? AND status IN ?", slot.ID, occupyingStatuses).Count(&occupied).Error; err != nil {
			return nil, fmt.Errorf("count occupied: %w", err)
		}
		available := slot.Capacity - int(occupied)
		if available < 0 {
			available = 0
		}
		views = append(views, SlotView{ServiceSlot: slot, Occupied: int(occupied), Available: available})
	}
	return views, nil
}

func (s *Service) CreateBooking(ctx context.Context, input CreateBookingInput) (Booking, error) {
	if input.RequestKey == "" || len(input.RequestKey) > 64 || input.OwnerID == 0 || input.PetID == 0 || input.SlotID == 0 {
		return Booking{}, ErrInvalidRequest
	}
	var result Booking
	err := withMySQLTransactionRetry(ctx, func() error {
		result = Booking{}
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var existing Booking
			err := tx.Where("owner_id = ? AND request_key = ?", input.OwnerID, input.RequestKey).First(&existing).Error
			if err == nil {
				if existing.PetID != input.PetID || existing.SlotID != input.SlotID {
					return ErrInvalidRequest
				}
				result = existing
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("find idempotent booking: %w", err)
			}

			var slot ServiceSlot
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&slot, input.SlotID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrSlotNotFound
				}
				return fmt.Errorf("lock slot: %w", err)
			}
			now := s.clock.Now().UTC()
			if !slot.StartsAt.After(now) {
				return ErrSlotStarted
			}
			if err := expireHolds(tx, now, slot.ID); err != nil {
				return err
			}

			var pet Pet
			if err := tx.First(&pet, input.PetID).Error; err != nil || pet.OwnerID != input.OwnerID {
				return ErrPetNotOwned
			}
			// This must be a locking read. Under MySQL REPEATABLE READ a plain COUNT
			// can keep the snapshot created by the idempotency lookup and miss a
			// booking committed while this transaction waited for the slot lock.
			var occupiedBookings []Booking
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("slot_id = ? AND status IN ?", slot.ID, occupyingStatuses).
				Find(&occupiedBookings).Error; err != nil {
				return fmt.Errorf("count capacity: %w", err)
			}
			if len(occupiedBookings) >= slot.Capacity {
				return ErrCapacityFull
			}

			booking := Booking{
				BookingNo: newBookingNo(now, input.OwnerID, input.RequestKey), RequestKey: input.RequestKey,
				SlotID: slot.ID, OwnerID: input.OwnerID, PetID: input.PetID, AmountCents: PriceCents,
				Status: StatusPendingPayment, HoldExpiresAt: now.Add(5 * time.Minute),
			}
			if err := tx.Create(&booking).Error; err != nil {
				var mysqlErr *mysql.MySQLError
				if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
					// MySQL may report either unique index first. Resolve the exact
					// meaning after rollback by looking up the request key.
					return errUniqueRace
				}
				return fmt.Errorf("create booking: %w", err)
			}
			result = booking
			return nil
		})
	})
	if errors.Is(err, errUniqueRace) {
		findErr := s.db.WithContext(ctx).Where("owner_id = ? AND request_key = ?", input.OwnerID, input.RequestKey).First(&result).Error
		if findErr == nil {
			if result.PetID != input.PetID || result.SlotID != input.SlotID {
				return Booking{}, ErrInvalidRequest
			}
			return result, nil
		}
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			return Booking{}, ErrDuplicateActive
		}
		return Booking{}, fmt.Errorf("resolve unique booking conflict: %w", findErr)
	}
	return result, err
}

func (s *Service) CancelUnpaid(ctx context.Context, ownerID uint64, bookingNo string) (Booking, error) {
	var result Booking
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var booking Booking
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("booking_no = ?", bookingNo).First(&booking).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBookingNotFound
			}
			return err
		}
		if booking.OwnerID != ownerID {
			return ErrForbidden
		}
		if booking.Status == StatusClosed || booking.Status == StatusExpired {
			result = booking
			return nil
		}
		if booking.Status != StatusPendingPayment {
			return ErrInvalidBookingState
		}
		now := s.clock.Now().UTC()
		updated := tx.Model(&Booking{}).Where("id = ? AND status = ? AND capacity_released_at IS NULL", booking.ID, StatusPendingPayment).
			Updates(map[string]any{"status": StatusClosed, "capacity_released_at": now, "updated_at": now, "version": gorm.Expr("version + 1")})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return ErrInvalidBookingState
		}
		if err := tx.First(&result, booking.ID).Error; err != nil {
			return err
		}
		return nil
	})
	return result, err
}

func (s *Service) ListOwnerBookings(ctx context.Context, ownerID uint64) ([]Booking, error) {
	if err := s.ExpireHolds(ctx); err != nil {
		return nil, err
	}
	var bookings []Booking
	if err := s.db.WithContext(ctx).Table("bookings b").Select(`b.*, s.starts_at AS slot_starts_at, s.ends_at AS slot_ends_at,
		COALESCE(n.reason, '') AS no_show_reason,
		COALESCE((SELECT c.status FROM cancellation_requests c WHERE c.booking_id = b.id ORDER BY c.id DESC LIMIT 1), '') AS cancellation_status,
		COALESCE((SELECT c.reason FROM cancellation_requests c WHERE c.booking_id = b.id ORDER BY c.id DESC LIMIT 1), '') AS cancellation_reason,
		COALESCE((SELECT c.review_reason FROM cancellation_requests c WHERE c.booking_id = b.id ORDER BY c.id DESC LIMIT 1), '') AS cancellation_review_reason,
		(SELECT e.occurred_at FROM fulfillment_events e WHERE e.booking_id = b.id AND e.event_type = 'checked_in' LIMIT 1) AS checked_in_at,
		(SELECT e.occurred_at FROM fulfillment_events e WHERE e.booking_id = b.id AND e.event_type = 'service_started' LIMIT 1) AS service_started_at,
		(SELECT e.occurred_at FROM fulfillment_events e WHERE e.booking_id = b.id AND e.event_type = 'completed' LIMIT 1) AS service_completed_at`).
		Joins("JOIN service_slots s ON s.id = b.slot_id").Joins("LEFT JOIN no_show_records n ON n.booking_id = b.id").Where("b.owner_id = ?", ownerID).Order("b.created_at DESC").Scan(&bookings).Error; err != nil {
		return nil, err
	}
	return bookings, nil
}

func (s *Service) ListOwnerPets(ctx context.Context, ownerID uint64) ([]Pet, error) {
	var pets []Pet
	if err := s.db.WithContext(ctx).Where("owner_id = ?", ownerID).Order("id").Find(&pets).Error; err != nil {
		return nil, fmt.Errorf("list owner pets: %w", err)
	}
	return pets, nil
}

func (s *Service) ExpireHolds(ctx context.Context) error {
	now := s.clock.Now().UTC()
	var slotIDs []uint64
	if err := s.db.WithContext(ctx).Model(&Booking{}).
		Distinct("slot_id").
		Where("status = ? AND hold_expires_at <= ? AND capacity_released_at IS NULL", StatusPendingPayment, now).
		Pluck("slot_id", &slotIDs).Error; err != nil {
		if isRetryableMySQLError(err) {
			return ErrTemporarilyBusy
		}
		return fmt.Errorf("list expired booking slots: %w", err)
	}
	for _, slotID := range slotIDs {
		if err := s.expireSlotHolds(ctx, now, slotID); err != nil {
			return err
		}
	}
	return nil
}

func expireHolds(tx *gorm.DB, now time.Time, slotID uint64) error {
	query := tx.Model(&Booking{}).Where("status = ? AND hold_expires_at <= ? AND capacity_released_at IS NULL", StatusPendingPayment, now)
	query = query.Where("slot_id = ?", slotID)
	return query.Updates(map[string]any{"status": StatusExpired, "capacity_released_at": now, "updated_at": now, "version": gorm.Expr("version + 1")}).Error
}

func (s *Service) expireSlotHolds(ctx context.Context, now time.Time, slotID uint64) error {
	err := withMySQLTransactionRetry(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Every flow that changes capacity locks the slot before its bookings.
			// This keeps the background expiry worker in the same lock order as
			// CreateBooking and prevents slot->booking / booking->slot deadlocks.
			var slot ServiceSlot
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&slot, slotID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			return expireHolds(tx, now, slotID)
		})
	})
	if err == nil || errors.Is(err, ErrTemporarilyBusy) {
		return err
	}
	return fmt.Errorf("expire booking holds for slot %d: %w", slotID, err)
}

const mysqlTransactionAttempts = 3

func isRetryableMySQLError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && (mysqlErr.Number == 1213 || mysqlErr.Number == 1205)
}

func withMySQLTransactionRetry(ctx context.Context, operation func() error) error {
	for attempt := 0; attempt < mysqlTransactionAttempts; attempt++ {
		err := operation()
		if !isRetryableMySQLError(err) {
			return err
		}
		if attempt+1 == mysqlTransactionAttempts || !waitForRetry(ctx, attempt) {
			return ErrTemporarilyBusy
		}
	}
	return ErrTemporarilyBusy
}

func waitForRetry(ctx context.Context, attempt int) bool {
	timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func newBookingNo(now time.Time, ownerID uint64, requestKey string) string {
	digest := sha256.Sum256([]byte(requestKey))
	return fmt.Sprintf("BK%s%04d%x", now.Format("20060102150405"), ownerID%10000, digest[:6])
}

var errUniqueRace = errors.New("unique booking request raced")
