package fulfillment

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fixedClock struct{ value time.Time }

func (c fixedClock) Now() time.Time { return c.value }

func fulfillmentDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MYSQL_FULFILLMENT_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_FULFILLMENT_TEST_DSN is not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NowFunc: func() time.Time { return time.Now().UTC() }})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func clearFulfillment(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{"DELETE FROM fulfillment_events", "DELETE FROM cancellation_requests", "DELETE FROM refund_attempts", "DELETE FROM refund_orders", "DELETE FROM payment_notifications", "DELETE FROM payment_orders", "DELETE FROM audit_logs", "DELETE FROM bookings", "DELETE FROM service_slots WHERE id > 3"} {
		require.NoError(t, db.Exec(statement).Error)
	}
}

func insertFulfillmentBooking(t *testing.T, db *gorm.DB, bookingNo, status string, slotID uint64) {
	t.Helper()
	result := db.Exec("INSERT INTO bookings (booking_no,request_key,slot_id,owner_id,pet_id,amount_cents,status,hold_expires_at,version,created_at,updated_at) VALUES (?,?,?,1,1,9900,?,?,0,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", bookingNo, "REQ-"+bookingNo, slotID, status, time.Now().UTC().Add(time.Hour))
	require.NoError(t, result.Error)
}

func TestFulfillmentOrderedFlowIdempotencyAndStoreScope(t *testing.T) {
	db := fulfillmentDB(t)
	clearFulfillment(t, db)
	t.Cleanup(func() { clearFulfillment(t, db) })
	now := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)
	service := NewService(db, fixedClock{value: now})
	insertFulfillmentBooking(t, db, "BK-FLOW", "confirmed", 1)

	_, err := service.StartService(t.Context(), OperateInput{OperatorID: 4, BookingNo: "BK-FLOW", IdempotencyKey: "start-too-early"})
	require.ErrorIs(t, err, ErrInvalidBookingState)
	checkedIn, err := service.CheckIn(t.Context(), OperateInput{OperatorID: 3, BookingNo: "BK-FLOW", IdempotencyKey: "checkin-1"})
	require.NoError(t, err)
	require.Equal(t, EventCheckedIn, checkedIn.EventType)
	repeatedCheckin, err := service.CheckIn(t.Context(), OperateInput{OperatorID: 3, BookingNo: "BK-FLOW", IdempotencyKey: "checkin-2"})
	require.NoError(t, err)
	require.Equal(t, checkedIn.ID, repeatedCheckin.ID)

	started, err := service.StartService(t.Context(), OperateInput{OperatorID: 4, BookingNo: "BK-FLOW", IdempotencyKey: "start-1"})
	require.NoError(t, err)
	require.Equal(t, EventServiceStart, started.EventType)
	completed, err := service.CompleteService(t.Context(), OperateInput{OperatorID: 4, BookingNo: "BK-FLOW", IdempotencyKey: "complete-1"})
	require.NoError(t, err)
	require.Equal(t, EventCompleted, completed.EventType)
	repeatedComplete, err := service.CompleteService(t.Context(), OperateInput{OperatorID: 4, BookingNo: "BK-FLOW", IdempotencyKey: "complete-2"})
	require.NoError(t, err)
	require.Equal(t, completed.ID, repeatedComplete.ID)

	var record booking
	require.NoError(t, db.Where("booking_no = ?", "BK-FLOW").First(&record).Error)
	require.Equal(t, "completed", record.Status)
	var events int64
	require.NoError(t, db.Model(&Event{}).Where("booking_id = ?", record.ID).Count(&events).Error)
	require.EqualValues(t, 3, events)
	var audits int64
	require.NoError(t, db.Model(&auditLog{}).Where("resource_id = ?", "BK-FLOW").Count(&audits).Error)
	require.EqualValues(t, 3, audits)

	views, err := service.ListStore(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, "completed", views[0].Status)
	require.NotNil(t, views[0].CheckinAt)
	require.NotNil(t, views[0].StartedAt)
	require.NotNil(t, views[0].CompletedAt)
}

func TestFulfillmentPermissionsPendingCancellationAndValidation(t *testing.T) {
	db := fulfillmentDB(t)
	clearFulfillment(t, db)
	t.Cleanup(func() { clearFulfillment(t, db) })
	service := NewService(db, fixedClock{value: time.Now().UTC()})
	insertFulfillmentBooking(t, db, "BK-PERM", "confirmed", 1)

	_, err := service.CheckIn(t.Context(), OperateInput{})
	require.ErrorIs(t, err, ErrInvalidRequest)
	_, err = service.CheckIn(t.Context(), OperateInput{OperatorID: 3, BookingNo: "missing", IdempotencyKey: "missing"})
	require.ErrorIs(t, err, ErrBookingNotFound)
	_, err = service.CheckIn(t.Context(), OperateInput{OperatorID: 4, BookingNo: "BK-PERM", IdempotencyKey: "wrong-role"})
	require.ErrorIs(t, err, ErrForbidden)
	_, err = service.CheckIn(t.Context(), OperateInput{OperatorID: 6, BookingNo: "BK-PERM", IdempotencyKey: "other-store"})
	require.ErrorIs(t, err, ErrForbidden)

	var record booking
	require.NoError(t, db.Where("booking_no = ?", "BK-PERM").First(&record).Error)
	require.NoError(t, db.Exec("INSERT INTO cancellation_requests (booking_id,requester_id,reason,status,requested_at,created_at,updated_at) VALUES (?,1,'临时原因','pending',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", record.ID).Error)
	_, err = service.CheckIn(t.Context(), OperateInput{OperatorID: 3, BookingNo: "BK-PERM", IdempotencyKey: "pending-cancel"})
	require.ErrorIs(t, err, ErrCancellationPending)
	require.NoError(t, db.Model(&record).Update("status", "cancel_pending").Error)
	_, err = service.CheckIn(t.Context(), OperateInput{OperatorID: 3, BookingNo: "BK-PERM", IdempotencyKey: "cancel-status"})
	require.ErrorIs(t, err, ErrInvalidBookingState)
	_, err = service.ListStore(t.Context(), 0)
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestConcurrentDuplicateCheckInCreatesOneEvent(t *testing.T) {
	db := fulfillmentDB(t)
	clearFulfillment(t, db)
	t.Cleanup(func() { clearFulfillment(t, db) })
	service := NewService(db, fixedClock{value: time.Now().UTC()})
	insertFulfillmentBooking(t, db, "BK-CONCURRENT-CHECKIN", "confirmed", 1)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for index := 0; index < 8; index++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := service.CheckIn(t.Context(), OperateInput{OperatorID: 3, BookingNo: "BK-CONCURRENT-CHECKIN", IdempotencyKey: "concurrent-" + string(rune('a'+i))})
			errs <- err
		}(index)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, db.Model(&Event{}).Where("event_type = ?", EventCheckedIn).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
