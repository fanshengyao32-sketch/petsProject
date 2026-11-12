package fulfillment

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fanshengyao32-sketch/petsProject/internal/cancellation"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCancellationAndCheckInConflictHasOneLegalResult(t *testing.T) {
	db := fulfillmentDB(t)
	now := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)
	clock := fixedClock{value: now}
	cancellationService := cancellation.NewService(db, clock)
	fulfillmentService := NewService(db, clock)

	for iteration := 0; iteration < 20; iteration++ {
		t.Run(fmt.Sprintf("near_start_%02d", iteration), func(t *testing.T) {
			clearFulfillment(t, db)
			bookingNo := fmt.Sprintf("BK-CONFLICT-NEAR-%02d", iteration)
			slotID := insertConflictSlot(t, db, now.Add(time.Hour), now.Add(2*time.Hour))
			insertConflictBooking(t, db, bookingNo, slotID)

			cancelErr, checkinErr := runConflict(
				func() error {
					_, err := cancellationService.Request(t.Context(), 1, bookingNo, "临近开始无法到店")
					return err
				},
				func() error {
					_, err := fulfillmentService.CheckIn(t.Context(), OperateInput{OperatorID: 3, BookingNo: bookingNo, IdempotencyKey: fmt.Sprintf("near-checkin-%02d", iteration)})
					return err
				},
			)
			assertNearConflictFacts(t, db, bookingNo, cancelErr, checkinErr)
		})
	}

	for iteration := 0; iteration < 20; iteration++ {
		t.Run(fmt.Sprintf("automatic_%02d", iteration), func(t *testing.T) {
			clearFulfillment(t, db)
			bookingNo := fmt.Sprintf("BK-CONFLICT-AUTO-%02d", iteration)
			slotID := insertConflictSlot(t, db, now.Add(3*time.Hour), now.Add(4*time.Hour))
			bookingID := insertConflictBooking(t, db, bookingNo, slotID)
			insertSuccessfulPayment(t, db, bookingID, iteration)

			cancelErr, checkinErr := runConflict(
				func() error {
					_, err := cancellationService.Request(t.Context(), 1, bookingNo, "提前取消")
					return err
				},
				func() error {
					_, err := fulfillmentService.CheckIn(t.Context(), OperateInput{OperatorID: 3, BookingNo: bookingNo, IdempotencyKey: fmt.Sprintf("auto-checkin-%02d", iteration)})
					return err
				},
			)
			assertAutomaticConflictFacts(t, db, bookingNo, cancelErr, checkinErr)
		})
	}
	clearFulfillment(t, db)
}

func runConflict(cancel func() error, checkin func() error) (error, error) {
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(2)
	var done sync.WaitGroup
	done.Add(2)
	var cancelErr, checkinErr error
	go func() { defer done.Done(); ready.Done(); <-start; cancelErr = cancel() }()
	go func() { defer done.Done(); ready.Done(); <-start; checkinErr = checkin() }()
	ready.Wait()
	close(start)
	done.Wait()
	return cancelErr, checkinErr
}

func insertConflictSlot(t *testing.T, db *gorm.DB, startsAt, endsAt time.Time) uint64 {
	t.Helper()
	result := db.Exec("INSERT INTO service_slots (store_id,service_item_id,starts_at,ends_at,capacity,version,created_by,created_at,updated_at) VALUES (1,1,?,?,3,0,2,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", startsAt.UTC(), endsAt.UTC())
	require.NoError(t, result.Error)
	var id uint64
	require.NoError(t, db.Raw("SELECT LAST_INSERT_ID()").Scan(&id).Error)
	return id
}

func insertConflictBooking(t *testing.T, db *gorm.DB, bookingNo string, slotID uint64) uint64 {
	t.Helper()
	result := db.Exec("INSERT INTO bookings (booking_no,request_key,slot_id,owner_id,pet_id,amount_cents,status,hold_expires_at,version,created_at,updated_at) VALUES (?,?,?,1,1,9900,'confirmed',?,0,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", bookingNo, "REQ-"+bookingNo, slotID, time.Now().UTC().Add(time.Hour))
	require.NoError(t, result.Error)
	var id uint64
	require.NoError(t, db.Raw("SELECT id FROM bookings WHERE booking_no = ?", bookingNo).Scan(&id).Error)
	return id
}

func insertSuccessfulPayment(t *testing.T, db *gorm.DB, bookingID uint64, iteration int) {
	t.Helper()
	requestNo := fmt.Sprintf("PAY-CONFLICT-%02d", iteration)
	tradeNo := fmt.Sprintf("TRADE-CONFLICT-%02d", iteration)
	require.NoError(t, db.Exec("INSERT INTO payment_orders (request_no,booking_id,provider,channel_trade_no,amount_cents,status,paid_at,created_at,updated_at) VALUES (?,?,'mock',?,9900,'succeeded',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", requestNo, bookingID, tradeNo).Error)
}

func assertNearConflictFacts(t *testing.T, db *gorm.DB, bookingNo string, cancelErr, checkinErr error) {
	t.Helper()
	require.NotEqual(t, cancelErr == nil, checkinErr == nil, "exactly one competing operation must succeed")
	var record struct {
		ID                 uint64
		Status             string
		CapacityReleasedAt *time.Time
	}
	require.NoError(t, db.Table("bookings").Select("id,status,capacity_released_at").Where("booking_no = ?", bookingNo).Scan(&record).Error)
	var cancellations, events, refunds int64
	require.NoError(t, db.Table("cancellation_requests").Where("booking_id = ?", record.ID).Count(&cancellations).Error)
	require.NoError(t, db.Table("fulfillment_events").Where("booking_id = ?", record.ID).Count(&events).Error)
	require.NoError(t, db.Table("refund_orders").Where("booking_id = ?", record.ID).Count(&refunds).Error)
	require.Nil(t, record.CapacityReleasedAt)
	require.Zero(t, refunds)
	if cancelErr == nil {
		require.Equal(t, "cancel_pending", record.Status)
		require.EqualValues(t, 1, cancellations)
		require.Zero(t, events)
		require.True(t, errors.Is(checkinErr, ErrInvalidBookingState) || errors.Is(checkinErr, ErrCancellationPending))
	} else {
		require.Equal(t, "checked_in", record.Status)
		require.Zero(t, cancellations)
		require.EqualValues(t, 1, events)
		require.ErrorIs(t, cancelErr, cancellation.ErrInvalidBookingState)
	}
}

func assertAutomaticConflictFacts(t *testing.T, db *gorm.DB, bookingNo string, cancelErr, checkinErr error) {
	t.Helper()
	require.NotEqual(t, cancelErr == nil, checkinErr == nil, "exactly one competing operation must succeed")
	var record struct {
		ID                 uint64
		Status             string
		CapacityReleasedAt *time.Time
	}
	require.NoError(t, db.Table("bookings").Select("id,status,capacity_released_at").Where("booking_no = ?", bookingNo).Scan(&record).Error)
	var cancellations, events, refunds, successfulPayments int64
	require.NoError(t, db.Table("cancellation_requests").Where("booking_id = ?", record.ID).Count(&cancellations).Error)
	require.NoError(t, db.Table("fulfillment_events").Where("booking_id = ?", record.ID).Count(&events).Error)
	require.NoError(t, db.Table("refund_orders").Where("booking_id = ?", record.ID).Count(&refunds).Error)
	require.NoError(t, db.Table("payment_orders").Where("booking_id = ? AND status = 'succeeded'", record.ID).Count(&successfulPayments).Error)
	require.EqualValues(t, 1, successfulPayments, "payment fact must not be changed by the race")
	if cancelErr == nil {
		require.Equal(t, "cancelled", record.Status)
		require.NotNil(t, record.CapacityReleasedAt)
		require.EqualValues(t, 1, cancellations)
		require.Zero(t, events)
		require.EqualValues(t, 1, refunds)
		require.ErrorIs(t, checkinErr, ErrInvalidBookingState)
	} else {
		require.Equal(t, "checked_in", record.Status)
		require.Nil(t, record.CapacityReleasedAt)
		require.Zero(t, cancellations)
		require.EqualValues(t, 1, events)
		require.Zero(t, refunds)
		require.ErrorIs(t, cancelErr, cancellation.ErrInvalidBookingState)
	}
}
