package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fanshengyao32-sketch/petsProject/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fakeSimulator struct {
	calls int
	err   error
}

func (s *fakeSimulator) TriggerPaymentNotification(context.Context, string, bool) error {
	s.calls++
	return s.err
}

func paymentRouter(t *testing.T, service *Service, parser Provider, simulator SimulationController) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	templates := template.New("")
	for _, name := range []string{"payments.html", "refunds.html", "error.html"} {
		_, err := templates.New(name).Parse("{{define \"" + name + "\"}}ok{{.Error}}{{.Success}}{{end}}")
		require.NoError(t, err)
	}
	router.SetHTMLTemplate(templates)
	router.Use(auth.Middleware())
	NewHTTPHandler(service, parser, simulator).Register(router)
	return router
}

func paymentRequest(router http.Handler, method, target string, body []byte, contentType string, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	if authenticated {
		request.AddCookie(&http.Cookie{Name: "pet_session_owner", Value: "owner-lin-session"})
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func supportPaymentRequest(router http.Handler, method, target string, body []byte, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.AddCookie(&http.Cookie{Name: "pet_session_support", Value: "support-gu-session"})
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestPaymentHTTPFlowAndCallback(t *testing.T) {
	db := paymentIntegrationDB(t)
	clearPaymentData(t, db)
	clock := &mutableClock{value: time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)}
	provider := &fakeProvider{}
	service := NewService(db, provider, clock)
	parser := NewMockClient("http://unused", "http://callback", "secret", nil)
	simulator := &fakeSimulator{}
	router := paymentRouter(t, service, parser, simulator)
	insertPaymentBooking(t, db, "BK-HTTP", 1, 1, "pending_payment", clock.Now().Add(5*time.Minute))

	require.Equal(t, http.StatusFound, paymentRequest(router, http.MethodGet, "/owner/payments", nil, "", false).Code)
	require.Equal(t, http.StatusOK, paymentRequest(router, http.MethodGet, "/owner/payments", nil, "", true).Code)
	require.Equal(t, http.StatusBadRequest, paymentRequest(router, http.MethodPost, "/api/payments", []byte(`{`), "application/json", true).Code)
	missingPayload := []byte(`{"booking_no":"MISSING","request_no":"PAY-API-MISSING","scenario":"success"}`)
	require.Equal(t, http.StatusNotFound, paymentRequest(router, http.MethodPost, "/api/payments", missingPayload, "application/json", true).Code)

	payload := []byte(`{"booking_no":"BK-HTTP","request_no":"PAY-HTTP","scenario":"success"}`)
	require.Equal(t, http.StatusOK, paymentRequest(router, http.MethodPost, "/api/payments", payload, "application/json", true).Code)
	require.Equal(t, http.StatusOK, paymentRequest(router, http.MethodGet, "/api/payments", nil, "", true).Code)

	badForm := url.Values{"request_no": {"PAY-MISSING"}, "scenario": {"success"}}
	badResponse := paymentRequest(router, http.MethodPost, "/owner/bookings/MISSING/pay", []byte(badForm.Encode()), "application/x-www-form-urlencoded", true)
	require.Equal(t, http.StatusFound, badResponse.Code)
	require.Contains(t, badResponse.Header().Get("Location"), "error=")
	badLocation, err := url.QueryUnescape(badResponse.Header().Get("Location"))
	require.NoError(t, err)
	require.Contains(t, badLocation, "预约记录不存在")
	result := db.Exec("INSERT INTO bookings (booking_no,request_key,slot_id,owner_id,pet_id,amount_cents,status,hold_expires_at,version,created_at,updated_at) VALUES ('BK-FORM','REQ-BK-FORM',3,1,1,9900,'pending_payment',?,0,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", clock.Now().Add(5*time.Minute))
	require.NoError(t, result.Error)
	form := url.Values{"request_no": {"PAY-FORM-FAIL"}, "scenario": {"failure"}}
	formResponse := paymentRequest(router, http.MethodPost, "/owner/bookings/BK-FORM/pay", []byte(form.Encode()), "application/x-www-form-urlencoded", true)
	require.Equal(t, http.StatusFound, formResponse.Code)
	require.Contains(t, formResponse.Header().Get("Location"), "success=")

	result = db.Exec("INSERT INTO bookings (booking_no,request_key,slot_id,owner_id,pet_id,amount_cents,status,hold_expires_at,version,created_at,updated_at) VALUES ('BK-CALLBACK','REQ-BK-CALLBACK',2,1,1,9900,'pending_payment',?,0,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", clock.Now().Add(5*time.Minute))
	require.NoError(t, result.Error)
	_, err = service.Start(context.Background(), StartInput{OwnerID: 1, BookingNo: "BK-CALLBACK", RequestNo: "PAY-CALLBACK", Scenario: "timeout"})
	require.NoError(t, err)

	require.Equal(t, http.StatusUnauthorized, paymentRequest(router, http.MethodPost, "/api/payment/notifications/mock", []byte(`{}`), "application/json", false).Code)
	notification := Notification{NotificationNo: "HTTP-NOTIFY", RequestNo: "PAY-CALLBACK", ChannelTradeNo: "HTTP-TRADE", Amount: 9900, Status: StatusSucceeded, OccurredAt: clock.Now()}
	notificationBody, err := json.Marshal(notification)
	require.NoError(t, err)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(notificationBody)
	request := httptest.NewRequest(http.MethodPost, "/api/payment/notifications/mock", bytes.NewReader(notificationBody))
	request.Header.Set("X-Mock-Signature", hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)

	lateResponse := paymentRequest(router, http.MethodPost, "/owner/payments/PAY-CALLBACK/late-success", nil, "", true)
	require.Equal(t, http.StatusFound, lateResponse.Code)
	require.Equal(t, 1, simulator.calls)
	missingLate := paymentRequest(router, http.MethodPost, "/owner/payments/OTHER/late-success", nil, "", true)
	require.Contains(t, missingLate.Header().Get("Location"), "error=")

	nilSimulatorRouter := paymentRouter(t, service, parser, nil)
	nilSimulatorResponse := paymentRequest(nilSimulatorRouter, http.MethodPost, "/owner/payments/PAY-CALLBACK/late-success", nil, "", true)
	require.Contains(t, nilSimulatorResponse.Header().Get("Location"), "error=")
	simulator.err = context.DeadlineExceeded
	simulatorError := paymentRequest(router, http.MethodPost, "/owner/payments/PAY-CALLBACK/late-success", nil, "", true)
	require.Contains(t, simulatorError.Header().Get("Location"), "error=")

	_, err = service.Start(context.Background(), StartInput{OwnerID: 1, BookingNo: "BK-FORM", RequestNo: "PAY-FORM-UNKNOWN", Scenario: "timeout"})
	require.NoError(t, err)
	mismatch := Notification{NotificationNo: "HTTP-MISMATCH", RequestNo: "PAY-FORM-UNKNOWN", ChannelTradeNo: "HTTP-MISMATCH", Amount: 1, Status: StatusSucceeded}
	mismatchBody, err := json.Marshal(mismatch)
	require.NoError(t, err)
	mac = hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(mismatchBody)
	request = httptest.NewRequest(http.MethodPost, "/api/payment/notifications/mock", bytes.NewReader(mismatchBody))
	request.Header.Set("X-Mock-Signature", hex.EncodeToString(mac.Sum(nil)))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusConflict, response.Code)
}

func TestPaymentErrorMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		err    error
		status int
	}{
		{ErrInvalidRequest, http.StatusBadRequest},
		{ErrBookingNotFound, http.StatusNotFound},
		{ErrPaymentNotFound, http.StatusNotFound},
		{ErrForbidden, http.StatusForbidden},
		{ErrInvalidBookingState, http.StatusConflict},
		{ErrPaymentExists, http.StatusConflict},
		{ErrAmountMismatch, http.StatusConflict},
		{ErrInvalidNotification, http.StatusBadRequest},
		{ErrRefundNotFound, http.StatusNotFound},
		{ErrRefundInProgress, http.StatusConflict},
		{ErrRefundNotAllowed, http.StatusConflict},
		{errors.New("unexpected"), http.StatusInternalServerError},
	} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		writePaymentError(ctx, test.err)
		require.Equal(t, test.status, response.Code)
		require.Contains(t, response.Body.String(), paymentErrorMessage(test.err))
		require.NotContains(t, response.Body.String(), test.err.Error())
	}
}

func TestRefundHTTPPageFailureAndRetry(t *testing.T) {
	db := paymentIntegrationDB(t)
	clearPaymentData(t, db)
	clock := &mutableClock{value: time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)}
	provider := &fakeProvider{}
	service := NewService(db, provider, clock)
	router := paymentRouter(t, service, provider, nil)
	bookingID := insertPaymentBooking(t, db, "BK-RF-HTTP", 1, 1, "cancelled", clock.Now().Add(time.Hour))
	tradeNo := "TRADE-RF-HTTP"
	paidAt := clock.Now()
	order := PaymentOrder{RequestNo: "PAY-RF-HTTP", BookingID: bookingID, Provider: "mock", ChannelTradeNo: &tradeNo, AmountCents: 9900, Status: OrderSucceeded, PaidAt: &paidAt}
	require.NoError(t, db.Create(&order).Error)
	refund := RefundOrder{RefundNo: "RF-HTTP", BookingID: bookingID, PaymentOrderID: order.ID, AmountCents: 9900, Status: RefundPending, Reason: "approved_cancellation"}
	require.NoError(t, db.Create(&refund).Error)

	require.Equal(t, http.StatusFound, paymentRequest(router, http.MethodGet, "/support/refunds", nil, "", false).Code)
	require.Equal(t, http.StatusOK, supportPaymentRequest(router, http.MethodGet, "/support/refunds", nil, "").Code)
	require.Equal(t, http.StatusBadRequest, supportPaymentRequest(router, http.MethodPost, "/api/refunds/RF-HTTP/execute", []byte(`{`), "application/json").Code)
	missing := supportPaymentRequest(router, http.MethodPost, "/api/refunds/missing/execute", []byte(`{"scenario":"success"}`), "application/json")
	require.Equal(t, http.StatusNotFound, missing.Code)

	failureForm := url.Values{"scenario": {"failure"}}
	failed := supportPaymentRequest(router, http.MethodPost, "/support/refunds/RF-HTTP/execute", []byte(failureForm.Encode()), "application/x-www-form-urlencoded")
	require.Equal(t, http.StatusFound, failed.Code)
	require.Contains(t, failed.Header().Get("Location"), "success=")

	succeeded := supportPaymentRequest(router, http.MethodPost, "/api/refunds/RF-HTTP/execute", []byte(`{"scenario":"success"}`), "application/json")
	require.Equal(t, http.StatusOK, succeeded.Code)
	require.Contains(t, succeeded.Body.String(), RefundSucceeded)
	require.Equal(t, "待退款", refundStatusMessage(RefundPending))
	require.Equal(t, "退款处理中", refundStatusMessage(RefundProcessing))
	require.Equal(t, "退款待确认", refundStatusMessage(RefundUnknown))
	require.Equal(t, "退款成功", refundStatusMessage(RefundSucceeded))
	require.Equal(t, "退款失败", refundStatusMessage(RefundFailed))
	require.Equal(t, "custom", refundStatusMessage("custom"))
}
