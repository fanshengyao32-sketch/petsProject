package closing

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

func closingDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MYSQL_CLOSING_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_CLOSING_TEST_DSN is not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NowFunc: func() time.Time { return time.Now().UTC() }})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func clearClosing(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{"DELETE FROM no_show_records", "DELETE FROM fulfillment_events", "DELETE FROM cancellation_requests", "DELETE FROM refund_attempts", "DELETE FROM refund_orders", "DELETE FROM payment_notifications", "DELETE FROM payment_orders", "DELETE FROM audit_logs", "DELETE FROM bookings", "DELETE FROM service_slots WHERE id > 3"} {
		require.NoError(t, db.Exec(statement).Error)
	}
}

func insertClosingSlot(t *testing.T, db *gorm.DB, startsAt, endsAt time.Time, storeID uint64) uint64 {
	t.Helper()
	require.NoError(t, db.Exec("INSERT INTO service_slots (store_id,service_item_id,starts_at,ends_at,capacity,version,created_by,created_at,updated_at) VALUES (?,1,?,?,3,0,2,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", storeID, startsAt.UTC(), endsAt.UTC()).Error)
	var id uint64
	require.NoError(t, db.Raw("SELECT id FROM service_slots WHERE store_id = ? AND starts_at = ? ORDER BY id DESC LIMIT 1", storeID, startsAt.UTC()).Scan(&id).Error)
	return id
}

func insertClosingBooking(t *testing.T, db *gorm.DB, bookingNo, status string, slotID uint64) uint64 {
	t.Helper()
	require.NoError(t, db.Exec("INSERT INTO bookings (booking_no,request_key,slot_id,owner_id,pet_id,amount_cents,status,hold_expires_at,version,created_at,updated_at) VALUES (?,?,?,1,1,9900,?,?,0,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", bookingNo, "REQ-"+bookingNo, slotID, status, time.Now().UTC()).Error)
	var id uint64
	require.NoError(t, db.Raw("SELECT id FROM bookings WHERE booking_no = ?", bookingNo).Scan(&id).Error)
	return id
}

func TestMarkNoShowAtEndIsIdempotentAndDoesNotRefund(t *testing.T) {
	db := closingDB(t)
	clearClosing(t, db)
	t.Cleanup(func() { clearClosing(t, db) })
	now := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)
	service := NewService(db, fixedClock{value: now})
	slotID := insertClosingSlot(t, db, now.Add(-time.Hour), now, 1)
	bookingID := insertClosingBooking(t, db, "BK-NO-SHOW", "confirmed", slotID)
	require.NoError(t, db.Exec("INSERT INTO payment_orders (request_no,booking_id,provider,channel_trade_no,amount_cents,status,paid_at,created_at,updated_at) VALUES ('PAY-NO-SHOW',?,'mock','TRADE-NO-SHOW',9900,'succeeded',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", bookingID).Error)

	result, err := service.MarkNoShow(t.Context(), MarkNoShowInput{ManagerID: 2, BookingNo: "BK-NO-SHOW", Reason: "  用户未按时到店  "})
	require.NoError(t, err)
	require.Equal(t, "用户未按时到店", result.Reason)
	replayed, err := service.MarkNoShow(t.Context(), MarkNoShowInput{ManagerID: 2, BookingNo: "BK-NO-SHOW", Reason: "再次点击"})
	require.NoError(t, err)
	require.Equal(t, result.ID, replayed.ID)

	var record struct {
		Status             string
		CapacityReleasedAt *time.Time
	}
	require.NoError(t, db.Table("bookings").Select("status,capacity_released_at").Where("id = ?", bookingID).Scan(&record).Error)
	require.Equal(t, "no_show", record.Status)
	require.Nil(t, record.CapacityReleasedAt)
	var noShows, refunds, events, payments, audits int64
	require.NoError(t, db.Model(&NoShowRecord{}).Where("booking_id = ?", bookingID).Count(&noShows).Error)
	require.NoError(t, db.Table("refund_orders").Where("booking_id = ?", bookingID).Count(&refunds).Error)
	require.NoError(t, db.Table("fulfillment_events").Where("booking_id = ?", bookingID).Count(&events).Error)
	require.NoError(t, db.Table("payment_orders").Where("booking_id = ? AND status = 'succeeded'", bookingID).Count(&payments).Error)
	require.NoError(t, db.Table("audit_logs").Where("resource_id = ?", "BK-NO-SHOW").Count(&audits).Error)
	require.EqualValues(t, 1, noShows)
	require.Zero(t, refunds)
	require.Zero(t, events)
	require.EqualValues(t, 1, payments)
	require.EqualValues(t, 1, audits)
	views, err := service.ListNoShows(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, "用户未按时到店", views[0].Reason)
	require.False(t, views[0].CanMark)
}

func TestNoShowBoundariesStateAndPermissions(t *testing.T) {
	db := closingDB(t)
	clearClosing(t, db)
	t.Cleanup(func() { clearClosing(t, db) })
	now := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)
	service := NewService(db, fixedClock{value: now})
	futureSlot := insertClosingSlot(t, db, now, now.Add(time.Minute), 1)
	insertClosingBooking(t, db, "BK-FUTURE-NO-SHOW", "confirmed", futureSlot)

	_, err := service.MarkNoShow(t.Context(), MarkNoShowInput{})
	require.ErrorIs(t, err, ErrInvalidRequest)
	_, err = service.MarkNoShow(t.Context(), MarkNoShowInput{ManagerID: 2, BookingNo: "missing", Reason: "未到店"})
	require.ErrorIs(t, err, ErrBookingNotFound)
	_, err = service.MarkNoShow(t.Context(), MarkNoShowInput{ManagerID: 3, BookingNo: "BK-FUTURE-NO-SHOW", Reason: "未到店"})
	require.ErrorIs(t, err, ErrForbidden)
	_, err = service.MarkNoShow(t.Context(), MarkNoShowInput{ManagerID: 2, BookingNo: "BK-FUTURE-NO-SHOW", Reason: "未到店"})
	require.ErrorIs(t, err, ErrSlotNotEnded)

	pastSlot := insertClosingSlot(t, db, now.Add(-2*time.Hour), now.Add(-time.Hour), 1)
	insertClosingBooking(t, db, "BK-CHECKED-IN-NO-SHOW", "checked_in", pastSlot)
	_, err = service.MarkNoShow(t.Context(), MarkNoShowInput{ManagerID: 2, BookingNo: "BK-CHECKED-IN-NO-SHOW", Reason: "未到店"})
	require.ErrorIs(t, err, ErrInvalidBookingState)
	otherStoreSlot := insertClosingSlot(t, db, now.Add(-3*time.Hour), now.Add(-2*time.Hour), 2)
	insertClosingBooking(t, db, "BK-OTHER-STORE-NO-SHOW", "confirmed", otherStoreSlot)
	_, err = service.MarkNoShow(t.Context(), MarkNoShowInput{ManagerID: 2, BookingNo: "BK-OTHER-STORE-NO-SHOW", Reason: "未到店"})
	require.ErrorIs(t, err, ErrForbidden)
	_, err = service.ListNoShows(t.Context(), 0)
	require.ErrorIs(t, err, ErrInvalidRequest)
	views, err := service.ListNoShows(t.Context(), 1)
	require.NoError(t, err)
	require.NotEmpty(t, views)
	for _, view := range views {
		if view.BookingNo == "BK-FUTURE-NO-SHOW" {
			require.False(t, view.CanMark)
		}
	}
}

func TestConcurrentNoShowCreatesOneRecord(t *testing.T) {
	db := closingDB(t)
	clearClosing(t, db)
	t.Cleanup(func() { clearClosing(t, db) })
	now := time.Now().UTC()
	service := NewService(db, fixedClock{value: now})
	slotID := insertClosingSlot(t, db, now.Add(-2*time.Hour), now.Add(-time.Hour), 1)
	bookingID := insertClosingBooking(t, db, "BK-NO-SHOW-CONCURRENT", "confirmed", slotID)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for index := 0; index < 8; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.MarkNoShow(t.Context(), MarkNoShowInput{ManagerID: 2, BookingNo: "BK-NO-SHOW-CONCURRENT", Reason: "并发重复标记"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, db.Model(&NoShowRecord{}).Where("booking_id = ?", bookingID).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
