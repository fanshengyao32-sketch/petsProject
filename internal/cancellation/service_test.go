package cancellation

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fixedClock struct{ value time.Time }

func (c fixedClock) Now() time.Time { return c.value }

func cancellationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MYSQL_CANCELLATION_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_CANCELLATION_TEST_DSN is not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NowFunc: func() time.Time { return time.Now().UTC() }})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func clearCancellationData(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		"DELETE FROM cancellation_requests", "DELETE FROM refund_attempts", "DELETE FROM refund_orders",
		"DELETE FROM payment_notifications", "DELETE FROM payment_orders", "DELETE FROM audit_logs", "DELETE FROM bookings",
		"DELETE FROM service_slots WHERE id > 3",
	} {
		require.NoError(t, db.Exec(statement).Error)
	}
}

func insertConfirmed(t *testing.T, db *gorm.DB, bookingNo string, ownerID, petID, slotID uint64) uint64 {
	t.Helper()
	result := db.Exec("INSERT INTO bookings (booking_no,request_key,slot_id,owner_id,pet_id,amount_cents,status,hold_expires_at,version,created_at,updated_at) VALUES (?,?,?,?,?,9900,'confirmed',UTC_TIMESTAMP(6),0,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", bookingNo, "REQ-"+bookingNo, slotID, ownerID, petID)
	require.NoError(t, result.Error)
	var bookingID uint64
	require.NoError(t, db.Raw("SELECT id FROM bookings WHERE booking_no = ?", bookingNo).Scan(&bookingID).Error)
	require.NoError(t, db.Exec("INSERT INTO payment_orders (request_no,booking_id,provider,channel_trade_no,amount_cents,status,paid_at,created_at,updated_at) VALUES (?,?, 'mock', ?,9900,'succeeded',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", "PAY-"+bookingNo, bookingID, "TRADE-"+bookingNo).Error)
	return bookingID
}

func insertSlot(t *testing.T, db *gorm.DB, id uint64, startsAt time.Time) {
	t.Helper()
	require.NoError(t, db.Exec("INSERT INTO service_slots (id,store_id,service_item_id,starts_at,ends_at,capacity,version,created_by,created_at,updated_at) VALUES (?,1,1,?,?,3,0,2,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", id, startsAt.UTC(), startsAt.Add(time.Hour).UTC()).Error)
}

func TestAutomaticCancellationAtAndBeyondTwoHours(t *testing.T) {
	db := cancellationDB(t)
	clearCancellationData(t, db)
	now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	insertSlot(t, db, 20, now.Add(2*time.Hour))
	bookingID := insertConfirmed(t, db, "BK-AUTO", 1, 1, 20)
	service := NewService(db, fixedClock{value: now})

	result, err := service.Request(context.Background(), 1, "BK-AUTO", "")
	require.NoError(t, err)
	require.Equal(t, StatusAutoApproved, result.Status)
	var booking booking
	require.NoError(t, db.First(&booking, bookingID).Error)
	require.Equal(t, "cancelled", booking.Status)
	require.NotNil(t, booking.CapacityReleasedAt)
	var refund refundOrder
	require.NoError(t, db.Where("booking_id = ?", bookingID).First(&refund).Error)
	require.Equal(t, "pending", refund.Status)
	require.Equal(t, "approved_cancellation", refund.Reason)
	require.EqualValues(t, 9900, refund.AmountCents)

	replayed, err := service.Request(context.Background(), 1, "BK-AUTO", "again")
	require.NoError(t, err)
	require.Equal(t, result.ID, replayed.ID)
	var refundCount int64
	require.NoError(t, db.Model(&refundOrder{}).Where("booking_id = ?", bookingID).Count(&refundCount).Error)
	require.EqualValues(t, 1, refundCount)
}

func TestNearCancellationRejectReapplyAndApprove(t *testing.T) {
	db := cancellationDB(t)
	clearCancellationData(t, db)
	now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	insertSlot(t, db, 21, now.Add(time.Hour))
	bookingID := insertConfirmed(t, db, "BK-NEAR", 1, 1, 21)
	service := NewService(db, fixedClock{value: now})

	_, err := service.Request(context.Background(), 1, "BK-NEAR", "")
	require.ErrorIs(t, err, ErrReasonRequired)
	first, err := service.Request(context.Background(), 1, "BK-NEAR", "宠物临时不适")
	require.NoError(t, err)
	require.Equal(t, StatusPending, first.Status)
	var bookingRecord booking
	require.NoError(t, db.First(&bookingRecord, bookingID).Error)
	require.Equal(t, "cancel_pending", bookingRecord.Status)
	require.Nil(t, bookingRecord.CapacityReleasedAt)
	repeated, err := service.Request(context.Background(), 1, "BK-NEAR", "再次点击")
	require.NoError(t, err)
	require.Equal(t, first.ID, repeated.ID)

	rejected, err := service.Review(context.Background(), 5, first.ID, StatusRejected, "门店已准备资源")
	require.NoError(t, err)
	require.Equal(t, StatusRejected, rejected.Status)
	bookingRecord = booking{}
	require.NoError(t, db.First(&bookingRecord, bookingID).Error)
	require.Equal(t, "confirmed", bookingRecord.Status)
	require.Nil(t, bookingRecord.CapacityReleasedAt)
	repeatedReview, err := service.Review(context.Background(), 5, first.ID, StatusRejected, "重复审核")
	require.NoError(t, err)
	require.Equal(t, rejected.ID, repeatedReview.ID)

	second, err := service.Request(context.Background(), 1, "BK-NEAR", "仍然无法到店")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	approved, err := service.Review(context.Background(), 5, second.ID, StatusApproved, "情况属实")
	require.NoError(t, err)
	require.Equal(t, StatusApproved, approved.Status)
	bookingRecord = booking{}
	require.NoError(t, db.First(&bookingRecord, bookingID).Error)
	require.Equal(t, "cancelled", bookingRecord.Status)
	require.NotNil(t, bookingRecord.CapacityReleasedAt)
	_, err = service.Review(context.Background(), 5, first.ID, StatusApproved, "试图改判")
	require.ErrorIs(t, err, ErrAlreadyReviewed)

	views, err := service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, views, 2)
}

func TestCancellationOwnershipStartStateAndSupportPermission(t *testing.T) {
	db := cancellationDB(t)
	clearCancellationData(t, db)
	now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	insertSlot(t, db, 22, now.Add(-time.Minute))
	insertConfirmed(t, db, "BK-STARTED", 1, 1, 22)
	service := NewService(db, fixedClock{value: now})

	_, err := service.Request(context.Background(), 7, "BK-STARTED", "wrong owner")
	require.ErrorIs(t, err, ErrForbidden)
	_, err = service.Request(context.Background(), 1, "MISSING", "none")
	require.ErrorIs(t, err, ErrBookingNotFound)
	_, err = service.Request(context.Background(), 1, "BK-STARTED", "too late")
	require.ErrorIs(t, err, ErrServiceStarted)
	_, err = service.Request(context.Background(), 0, "", "")
	require.ErrorIs(t, err, ErrInvalidRequest)
	_, err = service.Review(context.Background(), 2, 999, StatusApproved, "not support")
	require.ErrorIs(t, err, ErrForbidden)
	_, err = service.Review(context.Background(), 5, 999, StatusApproved, "missing")
	require.ErrorIs(t, err, ErrRequestNotFound)
	_, err = service.Review(context.Background(), 5, 0, "bad", "")
	require.ErrorIs(t, err, ErrInvalidRequest)
}
