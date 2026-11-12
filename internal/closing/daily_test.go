package closing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDailyReportSeparatesServiceRevenueAndMoneyFacts(t *testing.T) {
	db := closingDB(t)
	clearClosing(t, db)
	t.Cleanup(func() { clearClosing(t, db) })
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	start := time.Date(2026, 11, 11, 16, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	service := NewService(db, fixedClock{value: start.Add(30 * time.Minute)})
	require.Equal(t, "2026-11-12", service.Today(location))
	slotID := insertClosingSlot(t, db, start.Add(2*time.Hour), start.Add(3*time.Hour), 1)

	completedID := insertClosingBooking(t, db, "BK-DAILY-COMPLETED", "completed", slotID)
	paidAndRefundedID := insertClosingBooking(t, db, "BK-DAILY-REFUNDED", "cancelled", slotID)
	noShowID := insertClosingBooking(t, db, "BK-DAILY-NO-SHOW", "no_show", slotID)
	failedRefundID := insertClosingBooking(t, db, "BK-DAILY-REFUND-FAILED", "cancelled", slotID)
	insertClosingBooking(t, db, "BK-DAILY-CONFIRMED", "confirmed", slotID)

	completedPayment1 := insertDailyPayment(t, db, completedID, "PAY-D-C-1", "TRADE-D-C-1")
	completedPayment2 := insertDailyPayment(t, db, completedID, "PAY-D-C-2", "TRADE-D-C-2")
	refundedPayment := insertDailyPayment(t, db, paidAndRefundedID, "PAY-D-R", "TRADE-D-R")
	insertDailyPayment(t, db, noShowID, "PAY-D-N", "TRADE-D-N")
	failedPayment := insertDailyPayment(t, db, failedRefundID, "PAY-D-F", "TRADE-D-F")
	require.NoError(t, db.Exec("INSERT INTO fulfillment_events (booking_id,event_type,operator_id,occurred_at,idempotency_key,created_at) VALUES (?,'completed',4,?,'daily-completed',?)", completedID, start.Add(3*time.Hour), start.Add(3*time.Hour)).Error)
	insertDailyRefund(t, db, completedID, completedPayment2, "RF-D-DUP", "pending", "duplicate_payment")
	insertDailyRefund(t, db, paidAndRefundedID, refundedPayment, "RF-D-SUCCESS", "succeeded", "approved_cancellation")
	insertDailyRefund(t, db, failedRefundID, failedPayment, "RF-D-FAILED", "failed", "approved_cancellation")
	require.NoError(t, db.Exec("INSERT INTO no_show_records (booking_id,marked_by,reason,marked_at) VALUES (?,2,'用户未出现',?)", noShowID, start.Add(4*time.Hour)).Error)
	_ = completedPayment1

	report, err := service.BuildDailyReport(t.Context(), 1, "2026-11-12", start, end)
	require.NoError(t, err)
	require.Len(t, report.Slots, 1)
	require.Len(t, report.Details, 5)
	require.Equal(t, 5, report.Totals.BookingCount)
	require.Equal(t, 3, report.Totals.EffectiveCount)
	require.Equal(t, 1, report.Totals.CompletedCount)
	require.Equal(t, 2, report.Totals.CancelledCount)
	require.Equal(t, 1, report.Totals.NoShowCount)
	require.Equal(t, 2, report.Totals.UnresolvedRefundCount)
	require.EqualValues(t, 49500, report.Totals.CollectedCents)
	require.EqualValues(t, 9900, report.Totals.RefundedCents)
	require.EqualValues(t, 39600, report.Totals.UnrefundedCents)
	require.EqualValues(t, 9900, report.Totals.CompletedIncomeCents, "duplicate successful charge must not double completed income")
	require.EqualValues(t, 9900, report.Totals.NoShowCollectedCents)
	require.EqualValues(t, 19800, report.Totals.UnresolvedRefundCents)
	require.Equal(t, report.Totals, report.Slots[0].Metrics)

	var noShowFound bool
	for _, detail := range report.Details {
		if detail.BookingNo == "BK-DAILY-NO-SHOW" {
			noShowFound = true
			require.Equal(t, "用户未出现", detail.NoShowReason)
			require.Zero(t, detail.CompletedIncomeCents)
		}
	}
	require.True(t, noShowFound)

	empty, err := service.BuildDailyReport(t.Context(), 1, "2026-11-13", end, end.Add(24*time.Hour))
	require.NoError(t, err)
	require.Empty(t, empty.Slots)
	require.Empty(t, empty.Details)
	_, err = service.BuildDailyReport(t.Context(), 0, "", end, start)
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func insertDailyPayment(t *testing.T, db *gorm.DB, bookingID uint64, requestNo, tradeNo string) uint64 {
	t.Helper()
	require.NoError(t, db.Exec("INSERT INTO payment_orders (request_no,booking_id,provider,channel_trade_no,amount_cents,status,paid_at,created_at,updated_at) VALUES (?,?,'mock',?,9900,'succeeded',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", requestNo, bookingID, tradeNo).Error)
	var id uint64
	require.NoError(t, db.Raw("SELECT id FROM payment_orders WHERE request_no = ?", requestNo).Scan(&id).Error)
	return id
}

func insertDailyRefund(t *testing.T, db *gorm.DB, bookingID, paymentID uint64, refundNo, status, reason string) {
	t.Helper()
	var refundedAt any
	if status == "succeeded" {
		refundedAt = time.Now().UTC()
	}
	require.NoError(t, db.Exec("INSERT INTO refund_orders (refund_no,booking_id,payment_order_id,amount_cents,status,reason,failure_code,refunded_at,created_at,updated_at) VALUES (?,?,?,9900,?,?,NULL,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", refundNo, bookingID, paymentID, status, reason, refundedAt).Error)
}
