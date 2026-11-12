package booking

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func integrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not set; MySQL integration test skipped")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func clearTestBookings(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range []string{"no_show_records", "fulfillment_events", "cancellation_requests", "refund_attempts", "refund_orders", "payment_notifications", "payment_orders", "audit_logs"} {
		require.NoError(t, db.Exec("DELETE FROM "+table).Error)
	}
	require.NoError(t, db.Exec("DELETE FROM bookings").Error)
}

func TestOwnerSharedRecordViewIncludesCancellationFulfillmentAndNoShow(t *testing.T) {
	db := integrationDB(t)
	clearTestBookings(t, db)
	t.Cleanup(func() { clearTestBookings(t, db) })
	now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	service := NewService(db, fixedClock{value: now})
	require.NoError(t, db.Exec("INSERT INTO bookings (booking_no,request_key,slot_id,owner_id,pet_id,amount_cents,status,hold_expires_at,version,created_at,updated_at) VALUES ('BK-OWNER-PROGRESS','REQ-OWNER-PROGRESS',1,1,1,9900,'completed',?,3,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", now).Error)
	var completedID uint64
	require.NoError(t, db.Raw("SELECT id FROM bookings WHERE booking_no='BK-OWNER-PROGRESS'").Scan(&completedID).Error)
	require.NoError(t, db.Exec("INSERT INTO cancellation_requests (booking_id,requester_id,reason,status,requested_at,reviewed_by,reviewed_at,review_reason,created_at,updated_at) VALUES (?,1,'临时不便','rejected',?,5,?,'已到店不能取消',?,?)", completedID, now, now, now, now).Error)
	for index, eventType := range []string{"checked_in", "service_started", "completed"} {
		require.NoError(t, db.Exec("INSERT INTO fulfillment_events (booking_id,event_type,operator_id,occurred_at,idempotency_key,created_at) VALUES (?,?,?,?,?,?)", completedID, eventType, map[bool]uint64{true: 3, false: 4}[index == 0], now.Add(time.Duration(index)*time.Minute), "owner-progress-"+eventType, now).Error)
	}
	require.NoError(t, db.Exec("INSERT INTO bookings (booking_no,request_key,slot_id,owner_id,pet_id,amount_cents,status,hold_expires_at,version,created_at,updated_at) VALUES ('BK-OWNER-NOSHOW','REQ-OWNER-NOSHOW',1,1,1,9900,'no_show',?,1,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", now).Error)
	var noShowID uint64
	require.NoError(t, db.Raw("SELECT id FROM bookings WHERE booking_no='BK-OWNER-NOSHOW'").Scan(&noShowID).Error)
	require.NoError(t, db.Exec("INSERT INTO no_show_records (booking_id,marked_by,reason,marked_at) VALUES (?,2,'用户未出现',?)", noShowID, now).Error)

	bookings, err := service.ListOwnerBookings(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, bookings, 2)
	byNumber := map[string]Booking{}
	for _, item := range bookings {
		byNumber[item.BookingNo] = item
	}
	progress := byNumber["BK-OWNER-PROGRESS"]
	require.Equal(t, "rejected", progress.CancellationStatus)
	require.Equal(t, "已到店不能取消", progress.CancellationReviewReason)
	require.NotNil(t, progress.CheckedInAt)
	require.NotNil(t, progress.ServiceStartedAt)
	require.NotNil(t, progress.ServiceCompletedAt)
	require.False(t, progress.SlotStartsAt.IsZero())
	require.Equal(t, "用户未出现", byNumber["BK-OWNER-NOSHOW"].NoShowReason)
}

func TestMySQLCapacityIdempotencyReleaseAndExpiry(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	service := NewService(db, fixedClock{value: now})

	t.Run("four owners race for three places", func(t *testing.T) {
		clearTestBookings(t, db)
		type attempt struct {
			booking Booking
			err     error
		}
		attempts := make(chan attempt, 4)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for index, ownerID := range []uint64{1, 7, 8, 9} {
			wg.Add(1)
			go func(index int, ownerID uint64) {
				defer wg.Done()
				<-start
				booking, err := service.CreateBooking(ctx, CreateBookingInput{OwnerID: ownerID, PetID: uint64(index + 1), SlotID: 1, RequestKey: "capacity-race-" + string(rune('a'+index))})
				attempts <- attempt{booking: booking, err: err}
			}(index, ownerID)
		}
		close(start)
		wg.Wait()
		close(attempts)

		var successes []Booking
		full := 0
		for result := range attempts {
			if result.err == nil {
				successes = append(successes, result.booking)
			} else if errors.Is(result.err, ErrCapacityFull) {
				full++
			} else {
				t.Fatalf("unexpected race result: %v", result.err)
			}
		}
		require.Len(t, successes, 3)
		require.Equal(t, 1, full)

		cancelled, err := service.CancelUnpaid(ctx, successes[0].OwnerID, successes[0].BookingNo)
		require.NoError(t, err)
		require.Equal(t, StatusClosed, cancelled.Status)
		repeated, err := service.CancelUnpaid(ctx, successes[0].OwnerID, successes[0].BookingNo)
		require.NoError(t, err)
		require.Equal(t, cancelled.ID, repeated.ID)

		var unusedOwner, unusedPet uint64
		for index, ownerID := range []uint64{1, 7, 8, 9} {
			found := false
			for _, created := range successes {
				if created.OwnerID == ownerID {
					found = true
				}
			}
			if !found {
				unusedOwner, unusedPet = ownerID, uint64(index+1)
			}
		}
		_, err = service.CreateBooking(ctx, CreateBookingInput{OwnerID: unusedOwner, PetID: unusedPet, SlotID: 1, RequestKey: "after-release"})
		require.NoError(t, err)
	})

	t.Run("same request is idempotent and same pet cannot duplicate", func(t *testing.T) {
		clearTestBookings(t, db)
		const workers = 4
		ids := make(chan uint64, workers)
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				created, err := service.CreateBooking(ctx, CreateBookingInput{OwnerID: 1, PetID: 1, SlotID: 1, RequestKey: "same-request"})
				ids <- created.ID
				errs <- err
			}()
		}
		wg.Wait()
		close(ids)
		close(errs)
		var first uint64
		for err := range errs {
			require.NoError(t, err)
		}
		for id := range ids {
			if first == 0 {
				first = id
			}
			require.Equal(t, first, id)
		}
		_, err := service.CreateBooking(ctx, CreateBookingInput{OwnerID: 1, PetID: 1, SlotID: 1, RequestKey: "different-request"})
		require.ErrorIs(t, err, ErrDuplicateActive)
		_, err = service.CreateBooking(ctx, CreateBookingInput{OwnerID: 1, PetID: 1, SlotID: 2, RequestKey: "same-request"})
		require.ErrorIs(t, err, ErrInvalidRequest, "an idempotency key cannot be reused for a different payload")
	})

	t.Run("expired holds release capacity", func(t *testing.T) {
		clearTestBookings(t, db)
		for index, ownerID := range []uint64{1, 7, 8} {
			_, err := service.CreateBooking(ctx, CreateBookingInput{OwnerID: ownerID, PetID: uint64(index + 1), SlotID: 1, RequestKey: "expiring-" + string(rune('a'+index))})
			require.NoError(t, err)
		}
		later := NewService(db, fixedClock{value: now.Add(6 * time.Minute)})
		_, err := later.CreateBooking(ctx, CreateBookingInput{OwnerID: 9, PetID: 4, SlotID: 1, RequestKey: "after-expiry"})
		require.NoError(t, err)
		var expired int64
		require.NoError(t, db.Model(&Booking{}).Where("status = ?", StatusExpired).Count(&expired).Error)
		require.EqualValues(t, 3, expired)
	})
}

func TestExpiryWorkerAndBookingCreationUseConsistentLockOrder(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	service := NewService(db, fixedClock{value: now})

	for round := 0; round < 20; round++ {
		clearTestBookings(t, db)
		expired := Booking{
			BookingNo: "BK-EXPIRED-LOCK-" + string(rune('A'+round)), RequestKey: "expired-lock-" + string(rune('A'+round)),
			SlotID: 1, OwnerID: 1, PetID: 1, AmountCents: PriceCents, Status: StatusPendingPayment,
			HoldExpiresAt: now.Add(-time.Minute),
		}
		require.NoError(t, db.Create(&expired).Error)

		start := make(chan struct{})
		errorsCh := make(chan error, 5)
		var wait sync.WaitGroup
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errorsCh <- service.ExpireHolds(ctx)
		}()
		for index, ownerID := range []uint64{1, 7, 8, 9} {
			wait.Add(1)
			go func(index int, ownerID uint64) {
				defer wait.Done()
				<-start
				_, err := service.CreateBooking(ctx, CreateBookingInput{
					OwnerID: ownerID, PetID: uint64(index + 1), SlotID: 1,
					RequestKey: "expiry-create-" + string(rune('A'+round)) + "-" + string(rune('a'+index)),
				})
				errorsCh <- err
			}(index, ownerID)
		}
		close(start)
		wait.Wait()
		close(errorsCh)

		var success, capacityFull int
		for err := range errorsCh {
			switch {
			case err == nil:
				success++
			case errors.Is(err, ErrCapacityFull):
				capacityFull++
			default:
				t.Fatalf("round %d returned unstable error: %v", round, err)
			}
		}
		require.Equal(t, 4, success, "expiry worker plus three successful bookings")
		require.Equal(t, 1, capacityFull)

		var occupying int64
		require.NoError(t, db.Model(&Booking{}).Where("slot_id = ? AND status IN ?", 1, occupyingStatuses).Count(&occupying).Error)
		require.EqualValues(t, 3, occupying)
		var expiredCount int64
		require.NoError(t, db.Model(&Booking{}).Where("id = ? AND status = ? AND capacity_released_at IS NOT NULL", expired.ID, StatusExpired).Count(&expiredCount).Error)
		require.EqualValues(t, 1, expiredCount)
	}
}

func TestMySQLConcurrentOverlappingSlotCreation(t *testing.T) {
	db := integrationDB(t)
	require.NoError(t, db.Exec("DELETE FROM service_slots WHERE id > 3").Error)
	t.Cleanup(func() { _ = db.Exec("DELETE FROM service_slots WHERE id > 3").Error })
	service := NewService(db, fixedClock{value: time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)})
	inputs := []CreateSlotInput{
		{ManagerID: 2, StoreID: 1, ServiceItemID: 1, StartsAt: time.Date(2026, 11, 16, 5, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC)},
		{ManagerID: 2, StoreID: 1, ServiceItemID: 1, StartsAt: time.Date(2026, 11, 16, 5, 30, 0, 0, time.UTC), EndsAt: time.Date(2026, 11, 16, 6, 30, 0, 0, time.UTC)},
	}
	results := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, input := range inputs {
		wg.Add(1)
		go func(input CreateSlotInput) {
			defer wg.Done()
			<-start
			_, err := service.CreateSlot(context.Background(), input)
			results <- err
		}(input)
	}
	close(start)
	wg.Wait()
	close(results)
	var success, overlap int
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrSlotOverlap) {
			overlap++
		} else {
			t.Fatalf("unexpected slot result: %v", err)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, overlap)
}

func TestMySQLBookingBoundariesOwnershipAndQueries(t *testing.T) {
	db := integrationDB(t)
	clearTestBookings(t, db)
	require.NoError(t, db.Exec("DELETE FROM service_slots WHERE id > 3").Error)
	t.Cleanup(func() {
		_ = db.Exec("DELETE FROM bookings").Error
		_ = db.Exec("DELETE FROM service_slots WHERE id > 3").Error
	})
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	service := NewService(db, fixedClock{value: now})

	_, err := service.CreateBooking(ctx, CreateBookingInput{OwnerID: 1, PetID: 1, SlotID: 1})
	require.ErrorIs(t, err, ErrInvalidRequest)
	_, err = service.CreateBooking(ctx, CreateBookingInput{OwnerID: 1, PetID: 1, SlotID: 99, RequestKey: "missing-slot"})
	require.ErrorIs(t, err, ErrSlotNotFound)
	_, err = service.CreateBooking(ctx, CreateBookingInput{OwnerID: 1, PetID: 2, SlotID: 1, RequestKey: "wrong-owner"})
	require.ErrorIs(t, err, ErrPetNotOwned)

	pastSlot := ServiceSlot{ID: 20, StoreID: 1, ServiceItemID: 1, StartsAt: now.Add(-2 * time.Hour), EndsAt: now.Add(-time.Hour), Capacity: 3, CreatedBy: 2}
	require.NoError(t, db.Create(&pastSlot).Error)
	_, err = service.CreateBooking(ctx, CreateBookingInput{OwnerID: 1, PetID: 1, SlotID: 20, RequestKey: "past-slot"})
	require.ErrorIs(t, err, ErrSlotStarted)

	created, err := service.CreateBooking(ctx, CreateBookingInput{OwnerID: 1, PetID: 1, SlotID: 1, RequestKey: "query-test"})
	require.NoError(t, err)
	views, err := service.ListSlots(ctx, 1)
	require.NoError(t, err)
	var seededSlotAvailable int
	for _, view := range views {
		if view.ID == 1 {
			seededSlotAvailable = view.Available
		}
	}
	require.Equal(t, 2, seededSlotAvailable)
	require.NoError(t, db.Model(&Booking{}).Where("id = ?", created.ID).Update("status", StatusCompleted).Error)
	views, err = service.ListSlots(ctx, 1)
	require.NoError(t, err)
	for _, view := range views {
		if view.ID == 1 {
			require.Equal(t, 2, view.Available, "completed service must keep consumed capacity")
		}
	}
	require.NoError(t, db.Model(&Booking{}).Where("id = ?", created.ID).Update("status", StatusPendingPayment).Error)
	bookings, err := service.ListOwnerBookings(ctx, 1)
	require.NoError(t, err)
	require.Len(t, bookings, 1)
	pets, err := service.ListOwnerPets(ctx, 1)
	require.NoError(t, err)
	require.Len(t, pets, 1)

	_, err = service.CancelUnpaid(ctx, 7, created.BookingNo)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = service.CancelUnpaid(ctx, 1, "does-not-exist")
	require.ErrorIs(t, err, ErrBookingNotFound)
	require.NoError(t, db.Model(&Booking{}).Where("id = ?", created.ID).Update("status", StatusConfirmed).Error)
	_, err = service.CancelUnpaid(ctx, 1, created.BookingNo)
	require.ErrorIs(t, err, ErrInvalidBookingState)

	_, err = service.CreateSlot(ctx, CreateSlotInput{ManagerID: 2, StoreID: 1, ServiceItemID: 1, StartsAt: now, EndsAt: now.Add(time.Hour)})
	require.ErrorIs(t, err, ErrInvalidSlotTime)
	_, err = service.CreateSlot(ctx, CreateSlotInput{ManagerID: 1, StoreID: 1, ServiceItemID: 1, StartsAt: now.Add(48 * time.Hour), EndsAt: now.Add(49 * time.Hour)})
	require.ErrorIs(t, err, ErrForbidden)
	first, err := service.CreateSlot(ctx, CreateSlotInput{ManagerID: 2, StoreID: 1, ServiceItemID: 1, StartsAt: now.Add(48 * time.Hour), EndsAt: now.Add(49 * time.Hour)})
	require.NoError(t, err)
	require.NotZero(t, first.ID)
	_, err = service.CreateSlot(ctx, CreateSlotInput{ManagerID: 2, StoreID: 1, ServiceItemID: 1, StartsAt: now.Add(49 * time.Hour), EndsAt: now.Add(50 * time.Hour)})
	require.NoError(t, err, "touching endpoints must be allowed")
	_, err = service.CreateSlot(ctx, CreateSlotInput{ManagerID: 2, StoreID: 1, ServiceItemID: 1, StartsAt: now.Add(48*time.Hour + 30*time.Minute), EndsAt: now.Add(49*time.Hour + 30*time.Minute)})
	require.ErrorIs(t, err, ErrSlotOverlap)
}
