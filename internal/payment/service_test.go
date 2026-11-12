package payment

import (
	"context"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type mutableClock struct {
	mu    sync.RWMutex
	value time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.value
}

func (c *mutableClock) Set(value time.Time) {
	c.mu.Lock()
	c.value = value
	c.mu.Unlock()
}

type fakeProvider struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (p *fakeProvider) CreatePayment(_ context.Context, request PayRequest) (Result, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.err != nil {
		return Result{}, p.err
	}
	result := Result{ChannelTradeNo: "FAKE-" + request.RequestNo, OccurredAt: time.Now().UTC()}
	switch request.SimulationScenario {
	case "success", "duplicate":
		result.Status = StatusSucceeded
	case "failure":
		result.Status, result.FailureCode = StatusFailed, "DECLINED"
	case "timeout":
		result.Status, result.FailureCode = StatusUnknown, "TIMEOUT"
	}
	return result, nil
}

func (p *fakeProvider) QueryPayment(context.Context, string) (Result, error) {
	return Result{}, nil
}
func (p *fakeProvider) CreateRefund(_ context.Context, request RefundRequest) (Result, error) {
	result := Result{ChannelTradeNo: "FAKE-RF-" + request.RequestNo, OccurredAt: time.Now().UTC()}
	switch request.SimulationScenario {
	case "success":
		result.Status = StatusSucceeded
	case "failure":
		result.Status, result.FailureCode = StatusFailed, "MOCK_REFUND_REJECTED"
	case "timeout":
		result.Status, result.FailureCode = StatusUnknown, "MOCK_REFUND_TIMEOUT"
	}
	return result, nil
}

func TestRefundFailureUnknownRetryAndSuccess(t *testing.T) {
	db := paymentIntegrationDB(t)
	clearPaymentData(t, db)
	clock := &mutableClock{value: time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)}
	service := NewService(db, &fakeProvider{}, clock)
	_, err := service.ExecuteRefund(t.Context(), ExecuteRefundInput{})
	require.ErrorIs(t, err, ErrInvalidRequest)
	_, err = service.ExecuteRefund(t.Context(), ExecuteRefundInput{ActorID: 5, RefundNo: "missing", Scenario: "success"})
	require.ErrorIs(t, err, ErrRefundNotFound)
	bookingID := insertPaymentBooking(t, db, "BK-REFUND", 1, 1, "cancelled", clock.Now().Add(time.Hour))
	tradeNo := "TRADE-REFUND"
	paymentOrder := PaymentOrder{RequestNo: "PAY-REFUND", BookingID: bookingID, Provider: "mock", ChannelTradeNo: &tradeNo, AmountCents: 9900, Status: OrderSucceeded, PaidAt: ptrTime(clock.Now())}
	require.NoError(t, db.Create(&paymentOrder).Error)
	refund := RefundOrder{RefundNo: "RF-RETRY", BookingID: bookingID, PaymentOrderID: paymentOrder.ID, AmountCents: 9900, Status: RefundPending, Reason: "approved_cancellation"}
	require.NoError(t, db.Create(&refund).Error)
	require.NoError(t, db.Model(&refund).Update("status", RefundProcessing).Error)
	_, err = service.ExecuteRefund(t.Context(), ExecuteRefundInput{ActorID: 5, RefundNo: refund.RefundNo, Scenario: "success"})
	require.ErrorIs(t, err, ErrRefundInProgress)
	require.NoError(t, db.Model(&refund).Update("status", "invalid_state").Error)
	_, err = service.ExecuteRefund(t.Context(), ExecuteRefundInput{ActorID: 5, RefundNo: refund.RefundNo, Scenario: "success"})
	require.ErrorIs(t, err, ErrRefundNotAllowed)
	require.NoError(t, db.Model(&refund).Update("status", RefundPending).Error)

	failed, err := service.ExecuteRefund(t.Context(), ExecuteRefundInput{ActorID: 5, RefundNo: refund.RefundNo, Scenario: "failure"})
	require.NoError(t, err)
	require.Equal(t, RefundFailed, failed.Status)
	require.NotNil(t, failed.FailureCode)

	unknown, err := service.ExecuteRefund(t.Context(), ExecuteRefundInput{ActorID: 5, RefundNo: refund.RefundNo, Scenario: "timeout"})
	require.NoError(t, err)
	require.Equal(t, RefundUnknown, unknown.Status)

	succeeded, err := service.ExecuteRefund(t.Context(), ExecuteRefundInput{ActorID: 5, RefundNo: refund.RefundNo, Scenario: "success"})
	require.NoError(t, err)
	require.Equal(t, RefundSucceeded, succeeded.Status)
	require.NotNil(t, succeeded.RefundedAt)

	repeated, err := service.ExecuteRefund(t.Context(), ExecuteRefundInput{ActorID: 5, RefundNo: refund.RefundNo, Scenario: "success"})
	require.NoError(t, err)
	require.Equal(t, RefundSucceeded, repeated.Status)
	var attempts int64
	require.NoError(t, db.Model(&RefundAttempt{}).Where("refund_order_id = ?", refund.ID).Count(&attempts).Error)
	require.EqualValues(t, 3, attempts)
	var booking paymentBooking
	require.NoError(t, db.First(&booking, bookingID).Error)
	require.Equal(t, "cancelled", booking.Status)
	views, err := service.ListRefunds(t.Context())
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, 3, views[0].AttemptCount)
}

func ptrTime(value time.Time) *time.Time { return &value }
func (p *fakeProvider) QueryRefund(context.Context, string) (Result, error) {
	return Result{}, nil
}
func (p *fakeProvider) ParsePaymentNotification(http.Header, []byte) (Notification, error) {
	return Notification{}, nil
}

func paymentIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MYSQL_PAYMENT_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_PAYMENT_TEST_DSN is not set; payment MySQL tests skipped")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NowFunc: func() time.Time { return time.Now().UTC() }})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func clearPaymentData(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		"DELETE FROM refund_attempts",
		"DELETE FROM refund_orders",
		"DELETE FROM payment_notifications",
		"DELETE FROM payment_orders",
		"DELETE FROM audit_logs",
		"DELETE FROM bookings",
	} {
		require.NoError(t, db.Exec(statement).Error)
	}
}

func insertPaymentBooking(t *testing.T, db *gorm.DB, bookingNo string, ownerID, petID uint64, status string, holdExpiresAt time.Time) uint64 {
	t.Helper()
	result := db.Exec("INSERT INTO bookings (booking_no,request_key,slot_id,owner_id,pet_id,amount_cents,status,hold_expires_at,version,created_at,updated_at) VALUES (?,?,?,?,?,9900,?,?,0,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", bookingNo, "REQ-"+bookingNo, 1, ownerID, petID, status, holdExpiresAt.UTC())
	require.NoError(t, result.Error)
	var id uint64
	require.NoError(t, db.Raw("SELECT id FROM bookings WHERE booking_no = ?", bookingNo).Scan(&id).Error)
	return id
}

func TestPaymentSuccessFailureIdempotencyAndOwnership(t *testing.T) {
	db := paymentIntegrationDB(t)
	clearPaymentData(t, db)
	clock := &mutableClock{value: time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)}
	provider := &fakeProvider{}
	service := NewService(db, provider, clock)
	insertPaymentBooking(t, db, "BK-PAY-SUCCESS", 1, 1, "pending_payment", clock.Now().Add(5*time.Minute))

	order, err := service.Start(context.Background(), StartInput{OwnerID: 1, BookingNo: "BK-PAY-SUCCESS", RequestNo: "PAY-SUCCESS", Scenario: "success"})
	require.NoError(t, err)
	require.Equal(t, OrderSucceeded, order.Status)
	var booking paymentBooking
	require.NoError(t, db.Where("booking_no = ?", "BK-PAY-SUCCESS").First(&booking).Error)
	require.Equal(t, "confirmed", booking.Status)

	replayed, err := service.Start(context.Background(), StartInput{OwnerID: 1, BookingNo: "BK-PAY-SUCCESS", RequestNo: "PAY-SUCCESS", Scenario: "success"})
	require.NoError(t, err)
	require.Equal(t, order.ID, replayed.ID)
	require.Equal(t, 1, provider.calls)

	duplicate := Notification{NotificationNo: "NOTIFY-SUCCESS", RequestNo: "PAY-SUCCESS", ChannelTradeNo: "FAKE-PAY-SUCCESS", Amount: 9900, Status: StatusSucceeded}
	require.NoError(t, service.HandleNotification(context.Background(), duplicate, []byte("duplicate")))
	require.NoError(t, service.HandleNotification(context.Background(), duplicate, []byte("duplicate")))
	var notificationCount int64
	require.NoError(t, db.Model(&PaymentNotification{}).Where("notification_no = ?", duplicate.NotificationNo).Count(&notificationCount).Error)
	require.EqualValues(t, 1, notificationCount)

	insertPaymentBooking(t, db, "BK-PAY-FAIL", 7, 2, "pending_payment", clock.Now().Add(5*time.Minute))
	failed, err := service.Start(context.Background(), StartInput{OwnerID: 7, BookingNo: "BK-PAY-FAIL", RequestNo: "PAY-FAIL", Scenario: "failure"})
	require.NoError(t, err)
	require.Equal(t, OrderFailed, failed.Status)
	retried, err := service.Start(context.Background(), StartInput{OwnerID: 7, BookingNo: "BK-PAY-FAIL", RequestNo: "PAY-RETRY", Scenario: "success"})
	require.NoError(t, err)
	require.Equal(t, OrderSucceeded, retried.Status)

	_, err = service.Start(context.Background(), StartInput{OwnerID: 8, BookingNo: "BK-PAY-FAIL", RequestNo: "PAY-FORBIDDEN", Scenario: "success"})
	require.ErrorIs(t, err, ErrForbidden)
	_, err = service.Start(context.Background(), StartInput{OwnerID: 1, BookingNo: "MISSING", RequestNo: "PAY-MISSING", Scenario: "success"})
	require.ErrorIs(t, err, ErrBookingNotFound)
	_, err = service.Start(context.Background(), StartInput{})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestPaymentUnknownLateSuccessAndExactExpiry(t *testing.T) {
	db := paymentIntegrationDB(t)
	clearPaymentData(t, db)
	clock := &mutableClock{value: time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)}
	service := NewService(db, &fakeProvider{}, clock)
	bookingID := insertPaymentBooking(t, db, "BK-LATE", 1, 1, "pending_payment", clock.Now().Add(5*time.Minute))

	unknown, err := service.Start(context.Background(), StartInput{OwnerID: 1, BookingNo: "BK-LATE", RequestNo: "PAY-LATE", Scenario: "timeout"})
	require.NoError(t, err)
	require.Equal(t, OrderUnknown, unknown.Status)
	_, err = service.Start(context.Background(), StartInput{OwnerID: 1, BookingNo: "BK-LATE", RequestNo: "PAY-SECOND", Scenario: "success"})
	require.ErrorIs(t, err, ErrPaymentExists)

	require.NoError(t, db.Model(&paymentBooking{}).Where("id = ?", bookingID).Updates(map[string]any{"status": "closed", "capacity_released_at": clock.Now()}).Error)
	notification := Notification{NotificationNo: "NOTIFY-LATE", RequestNo: "PAY-LATE", ChannelTradeNo: "FAKE-LATE", Amount: 9900, Status: StatusSucceeded}
	require.NoError(t, service.HandleNotification(context.Background(), notification, []byte("late")))
	require.NoError(t, service.HandleNotification(context.Background(), notification, []byte("late")))
	var booking paymentBooking
	require.NoError(t, db.First(&booking, bookingID).Error)
	require.Equal(t, "closed", booking.Status)
	var refunds []RefundOrder
	require.NoError(t, db.Where("payment_order_id = ?", unknown.ID).Find(&refunds).Error)
	require.Len(t, refunds, 1)
	require.Equal(t, "late_payment", refunds[0].Reason)
	require.Equal(t, RefundPending, refunds[0].Status)

	clearPaymentData(t, db)
	holdExpires := clock.Now().Add(5 * time.Minute)
	secondID := insertPaymentBooking(t, db, "BK-EXACT", 1, 1, "pending_payment", holdExpires)
	unknown, err = service.Start(context.Background(), StartInput{OwnerID: 1, BookingNo: "BK-EXACT", RequestNo: "PAY-EXACT", Scenario: "timeout"})
	require.NoError(t, err)
	clock.Set(holdExpires)
	notification = Notification{NotificationNo: "NOTIFY-EXACT", RequestNo: "PAY-EXACT", ChannelTradeNo: "FAKE-EXACT", Amount: 9900, Status: StatusSucceeded}
	require.NoError(t, service.HandleNotification(context.Background(), notification, []byte("exact")))
	booking = paymentBooking{}
	require.NoError(t, db.First(&booking, secondID).Error)
	require.Equal(t, "expired", booking.Status, "success received exactly at hold expiry is late")
	require.NotNil(t, booking.CapacityReleasedAt)

	views, err := service.ListOwner(context.Background(), 1)
	require.NoError(t, err)
	require.NotEmpty(t, views)
	require.Equal(t, "pending", views[0].RefundStatus)
}

func TestPaymentNotificationAmountMismatchIsPersisted(t *testing.T) {
	db := paymentIntegrationDB(t)
	clearPaymentData(t, db)
	clock := &mutableClock{value: time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)}
	service := NewService(db, &fakeProvider{}, clock)
	insertPaymentBooking(t, db, "BK-MISMATCH", 1, 1, "pending_payment", clock.Now().Add(5*time.Minute))
	_, err := service.Start(context.Background(), StartInput{OwnerID: 1, BookingNo: "BK-MISMATCH", RequestNo: "PAY-MISMATCH", Scenario: "timeout"})
	require.NoError(t, err)
	notification := Notification{NotificationNo: "NOTIFY-MISMATCH", RequestNo: "PAY-MISMATCH", ChannelTradeNo: "FAKE-MISMATCH", Amount: 1, Status: StatusSucceeded}
	err = service.HandleNotification(context.Background(), notification, []byte("mismatch"))
	require.ErrorIs(t, err, ErrAmountMismatch)
	var record PaymentNotification
	require.NoError(t, db.Where("notification_no = ?", notification.NotificationNo).First(&record).Error)
	require.Equal(t, "amount_mismatch", record.Result)
}

func TestPaymentAdditionalStateBranches(t *testing.T) {
	db := paymentIntegrationDB(t)
	clock := &mutableClock{value: time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)}
	ctx := context.Background()

	clearPaymentData(t, db)
	provider := &fakeProvider{err: context.DeadlineExceeded}
	service := NewService(db, provider, clock)
	insertPaymentBooking(t, db, "BK-PROVIDER-ERROR", 1, 1, "pending_payment", clock.Now().Add(5*time.Minute))
	order, err := service.Start(ctx, StartInput{OwnerID: 1, BookingNo: "BK-PROVIDER-ERROR", RequestNo: "PAY-PROVIDER-ERROR", Scenario: "success"})
	require.NoError(t, err)
	require.Equal(t, OrderUnknown, order.Status)
	require.Equal(t, "PROVIDER_UNAVAILABLE", *order.FailureCode)
	_, err = service.Start(ctx, StartInput{OwnerID: 1, BookingNo: "OTHER", RequestNo: "PAY-PROVIDER-ERROR", Scenario: "success"})
	require.ErrorIs(t, err, ErrBookingNotFound)

	clearPaymentData(t, db)
	service = NewService(db, &fakeProvider{}, clock)
	insertPaymentBooking(t, db, "BK-CONFIRMED", 1, 1, "confirmed", clock.Now().Add(5*time.Minute))
	_, err = service.Start(ctx, StartInput{OwnerID: 1, BookingNo: "BK-CONFIRMED", RequestNo: "PAY-CONFIRMED", Scenario: "success"})
	require.ErrorIs(t, err, ErrInvalidBookingState)
	clearPaymentData(t, db)
	expiredID := insertPaymentBooking(t, db, "BK-START-EXPIRED", 1, 1, "pending_payment", clock.Now())
	_, err = service.Start(ctx, StartInput{OwnerID: 1, BookingNo: "BK-START-EXPIRED", RequestNo: "PAY-START-EXPIRED", Scenario: "success"})
	require.ErrorIs(t, err, ErrInvalidBookingState)
	var booking paymentBooking
	require.NoError(t, db.First(&booking, expiredID).Error)
	require.Equal(t, "expired", booking.Status)
	require.NotNil(t, booking.CapacityReleasedAt)

	err = service.HandleNotification(ctx, Notification{}, nil)
	require.ErrorIs(t, err, ErrInvalidNotification)
	unknownNotification := Notification{NotificationNo: "UNKNOWN-NOTIFY", RequestNo: "UNKNOWN-PAY", ChannelTradeNo: "UNKNOWN-TRADE", Amount: 9900, Status: StatusSucceeded}
	err = service.HandleNotification(ctx, unknownNotification, []byte("unknown"))
	require.ErrorIs(t, err, ErrPaymentNotFound)
	var notificationRecord PaymentNotification
	require.NoError(t, db.Where("notification_no = ?", unknownNotification.NotificationNo).First(&notificationRecord).Error)
	require.Equal(t, "unknown_request", notificationRecord.Result)

	clearPaymentData(t, db)
	insertPaymentBooking(t, db, "BK-NOTIFY-FAIL", 1, 1, "pending_payment", clock.Now().Add(5*time.Minute))
	order, err = service.Start(ctx, StartInput{OwnerID: 1, BookingNo: "BK-NOTIFY-FAIL", RequestNo: "PAY-NOTIFY-FAIL", Scenario: "timeout"})
	require.NoError(t, err)
	failedNotification := Notification{NotificationNo: "NOTIFY-FAIL", RequestNo: order.RequestNo, Amount: 9900, Status: StatusFailed, FailureCode: "CHANNEL_FAILED"}
	require.NoError(t, service.HandleNotification(ctx, failedNotification, []byte("failed")))
	require.NoError(t, db.First(&order, order.ID).Error)
	require.Equal(t, OrderFailed, order.Status)

	clearPaymentData(t, db)
	bookingID := insertPaymentBooking(t, db, "BK-NO-TRANSITION", 1, 1, "pending_payment", clock.Now().Add(5*time.Minute))
	order, err = service.Start(ctx, StartInput{OwnerID: 1, BookingNo: "BK-NO-TRANSITION", RequestNo: "PAY-NO-TRANSITION", Scenario: "timeout"})
	require.NoError(t, err)
	require.NoError(t, db.Model(&paymentBooking{}).Where("id = ?", bookingID).Update("status", "confirmed").Error)
	successNotification := Notification{NotificationNo: "NOTIFY-NO-TRANSITION", RequestNo: order.RequestNo, ChannelTradeNo: "TRADE-NO-TRANSITION", Amount: 9900, Status: StatusSucceeded}
	require.NoError(t, service.HandleNotification(ctx, successNotification, []byte("success")))
	require.ErrorIs(t, service.RequireOwnerPayment(ctx, 1, "MISSING"), ErrPaymentNotFound)
	require.ErrorIs(t, service.RequireOwnerPayment(ctx, 0, ""), ErrInvalidRequest)
}

func TestFailedPaymentLateSuccessAfterSuccessfulRetryCreatesExtraRefund(t *testing.T) {
	db := paymentIntegrationDB(t)
	clearPaymentData(t, db)
	clock := &mutableClock{value: time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)}
	service := NewService(db, &fakeProvider{}, clock)
	ctx := context.Background()
	insertPaymentBooking(t, db, "BK-DUPLICATE-CHARGE", 1, 1, "pending_payment", clock.Now().Add(5*time.Minute))

	failed, err := service.Start(ctx, StartInput{OwnerID: 1, BookingNo: "BK-DUPLICATE-CHARGE", RequestNo: "PAY-FIRST-FAILED", Scenario: "failure"})
	require.NoError(t, err)
	require.Equal(t, OrderFailed, failed.Status)
	retry, err := service.Start(ctx, StartInput{OwnerID: 1, BookingNo: "BK-DUPLICATE-CHARGE", RequestNo: "PAY-RETRY-SUCCESS", Scenario: "success"})
	require.NoError(t, err)
	require.Equal(t, OrderSucceeded, retry.Status)

	lateFirst := Notification{NotificationNo: "NOTIFY-FIRST-LATE", RequestNo: failed.RequestNo, ChannelTradeNo: "TRADE-FIRST-LATE", Amount: 9900, Status: StatusSucceeded}
	require.NoError(t, service.HandleNotification(ctx, lateFirst, []byte("first-late")))
	var firstOrder PaymentOrder
	require.NoError(t, db.First(&firstOrder, failed.ID).Error)
	require.Equal(t, OrderSucceeded, firstOrder.Status)
	var refund RefundOrder
	require.NoError(t, db.Where("payment_order_id = ?", failed.ID).First(&refund).Error)
	require.Equal(t, "duplicate_payment", refund.Reason)
	require.Equal(t, RefundPending, refund.Status)
	var booking paymentBooking
	require.NoError(t, db.Where("booking_no = ?", "BK-DUPLICATE-CHARGE").First(&booking).Error)
	require.Equal(t, "confirmed", booking.Status)
}

func TestConcurrentSamePaymentRequestIsIdempotent(t *testing.T) {
	db := paymentIntegrationDB(t)
	clearPaymentData(t, db)
	clock := &mutableClock{value: time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)}
	provider := &fakeProvider{}
	service := NewService(db, provider, clock)
	insertPaymentBooking(t, db, "BK-PAY-RACE", 1, 1, "pending_payment", clock.Now().Add(5*time.Minute))

	const workers = 4
	ids := make(chan uint64, workers)
	errs := make(chan error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			order, err := service.Start(context.Background(), StartInput{OwnerID: 1, BookingNo: "BK-PAY-RACE", RequestNo: "PAY-SAME-RACE", Scenario: "success"})
			ids <- order.ID
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var first uint64
	for id := range ids {
		if first == 0 {
			first = id
		}
		require.Equal(t, first, id)
	}
	require.Equal(t, 1, provider.calls)
	var count int64
	require.NoError(t, db.Model(&PaymentOrder{}).Where("request_no = ?", "PAY-SAME-RACE").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
