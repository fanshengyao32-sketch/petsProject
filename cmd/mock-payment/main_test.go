package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHealthAndPaymentLifecycle(t *testing.T) {
	server := newMockServer([]byte("test-secret"), nil)
	mux := newMux(server)

	response := serve(t, mux, http.MethodGet, "/health/live", "")
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"status":"ok"}`, response.Body.String())

	for _, scenario := range []string{"success", "failure", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			requestNo := "PAY-" + scenario
			body := `{"request_no":"` + requestNo + `","booking_no":"BK-1","amount_cents":9900,"scenario":"` + scenario + `"}`
			created := serve(t, mux, http.MethodPost, "/v1/payments", body)
			require.Equal(t, http.StatusOK, created.Code)

			queried := serve(t, mux, http.MethodGet, "/v1/payments/"+requestNo, "")
			require.Equal(t, http.StatusOK, queried.Code)
			var result channelResult
			require.NoError(t, json.Unmarshal(queried.Body.Bytes(), &result))
			switch scenario {
			case "success":
				require.Equal(t, "succeeded", result.Status)
			case "failure":
				require.Equal(t, "failed", result.Status)
				require.Equal(t, "MOCK_DECLINED", result.FailureCode)
			case "timeout":
				require.Equal(t, "unknown", result.Status)
				require.Equal(t, "MOCK_TIMEOUT", result.FailureCode)
			}
		})
	}

	require.Equal(t, http.StatusBadRequest, serve(t, mux, http.MethodPost, "/v1/payments", `{`).Code)
	require.Equal(t, http.StatusBadRequest, serve(t, mux, http.MethodPost, "/v1/payments", `{"request_no":"x"}`).Code)
	require.Equal(t, http.StatusBadRequest, serve(t, mux, http.MethodPost, "/v1/payments", `{"request_no":"x","booking_no":"b","amount_cents":1,"scenario":"other"}`).Code)
	require.Equal(t, http.StatusNotFound, serve(t, mux, http.MethodGet, "/v1/payments/missing", "").Code)
}

func TestPaymentNotifications(t *testing.T) {
	secret := []byte("notification-secret")
	var calls atomic.Int32
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(body)
		require.Equal(t, hex.EncodeToString(mac.Sum(nil)), r.Header.Get("X-Mock-Signature"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callback.Close()

	server := newMockServer(secret, callback.Client())
	mux := newMux(server)
	body := `{"request_no":"PAY-NOTIFY","booking_no":"BK-1","amount_cents":9900,"scenario":"success","callback_url":"` + callback.URL + `"}`
	require.Equal(t, http.StatusOK, serve(t, mux, http.MethodPost, "/v1/payments", body).Code)

	triggered := serve(t, mux, http.MethodPost, "/v1/payment-notifications", `{"request_no":"PAY-NOTIFY","duplicate":true}`)
	require.Equal(t, http.StatusOK, triggered.Code)
	require.Equal(t, int32(2), calls.Load())

	require.Equal(t, http.StatusBadRequest, serve(t, mux, http.MethodPost, "/v1/payment-notifications", `{`).Code)
	require.Equal(t, http.StatusBadRequest, serve(t, mux, http.MethodPost, "/v1/payment-notifications", `{}`).Code)
	require.Equal(t, http.StatusNotFound, serve(t, mux, http.MethodPost, "/v1/payment-notifications", `{"request_no":"missing"}`).Code)

	noCallback := `{"request_no":"PAY-NO-CALLBACK","booking_no":"BK-1","amount_cents":9900,"scenario":"success"}`
	require.Equal(t, http.StatusOK, serve(t, mux, http.MethodPost, "/v1/payments", noCallback).Code)
	require.Equal(t, http.StatusNotFound, serve(t, mux, http.MethodPost, "/v1/payment-notifications", `{"request_no":"PAY-NO-CALLBACK"}`).Code)
}

func TestNotificationFailuresAndAutomaticDuplicate(t *testing.T) {
	var calls atomic.Int32
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer callback.Close()

	server := newMockServer([]byte("secret"), callback.Client())
	mux := newMux(server)
	body := `{"request_no":"PAY-BAD-CALLBACK","booking_no":"BK-1","amount_cents":9900,"scenario":"success","callback_url":"` + callback.URL + `"}`
	require.Equal(t, http.StatusOK, serve(t, mux, http.MethodPost, "/v1/payments", body).Code)
	failed := serve(t, mux, http.MethodPost, "/v1/payment-notifications", `{"request_no":"PAY-BAD-CALLBACK"}`)
	require.Equal(t, http.StatusBadGateway, failed.Code)
	require.JSONEq(t, `{"error":"NOTIFICATION_DELIVERY_FAILED","message":"支付结果通知发送失败，请稍后重试"}`, failed.Body.String())
	require.NotContains(t, failed.Body.String(), callback.URL)

	goodCallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer goodCallback.Close()
	server.client = goodCallback.Client()
	payment := paymentRequest{RequestNo: "DIRECT", Amount: 9900, CallbackURL: goodCallback.URL}
	require.NoError(t, server.sendNotification(payment, false))

	badURL := paymentRequest{RequestNo: "BAD-URL", Amount: 9900, CallbackURL: "://bad"}
	require.Error(t, server.sendNotification(badURL, false))

	autoBody := `{"request_no":"PAY-DUP","booking_no":"BK-1","amount_cents":9900,"scenario":"duplicate","callback_url":"` + goodCallback.URL + `"}`
	require.Equal(t, http.StatusOK, serve(t, mux, http.MethodPost, "/v1/payments", autoBody).Code)
	require.Eventually(t, func() bool { return calls.Load() >= 4 }, 1000000000, 10000000)
}

func TestNotificationTransportErrorDoesNotExposeCallbackDetails(t *testing.T) {
	const sensitiveCallback = "http://internal-payment-callback.local/notify?token=sensitive-marker"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp internal-payment-callback.local: secret transport detail")
	})}
	server := newMockServer([]byte("secret"), client)
	mux := newMux(server)
	body := `{"request_no":"PAY-SAFE-ERROR","booking_no":"BK-1","amount_cents":9900,"scenario":"success","callback_url":"` + sensitiveCallback + `"}`
	require.Equal(t, http.StatusOK, serve(t, mux, http.MethodPost, "/v1/payments", body).Code)

	failed := serve(t, mux, http.MethodPost, "/v1/payment-notifications", `{"request_no":"PAY-SAFE-ERROR"}`)
	require.Equal(t, http.StatusBadGateway, failed.Code)
	require.JSONEq(t, `{"error":"NOTIFICATION_DELIVERY_FAILED","message":"支付结果通知发送失败，请稍后重试"}`, failed.Body.String())
	for _, forbidden := range []string{sensitiveCallback, "internal-payment-callback", "sensitive-marker", "dial tcp", "secret transport detail"} {
		require.NotContains(t, failed.Body.String(), forbidden)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestRefundLifecycle(t *testing.T) {
	mux := newMux(newMockServer([]byte("secret"), nil))
	for _, scenario := range []string{"success", "failure", "timeout", ""} {
		t.Run("scenario_"+scenario, func(t *testing.T) {
			requestNo := "RF-" + scenario
			body := `{"RequestNo":"` + requestNo + `","Amount":9900,"SimulationScenario":"` + scenario + `"}`
			created := serve(t, mux, http.MethodPost, "/v1/refunds", body)
			require.Equal(t, http.StatusOK, created.Code)
			queried := serve(t, mux, http.MethodGet, "/v1/refunds/"+requestNo, "")
			require.Equal(t, http.StatusOK, queried.Code)
			var result channelResult
			require.NoError(t, json.Unmarshal(queried.Body.Bytes(), &result))
			if scenario == "failure" {
				require.Equal(t, "failed", result.Status)
			} else if scenario == "timeout" {
				require.Equal(t, "unknown", result.Status)
			} else {
				require.Equal(t, "succeeded", result.Status)
			}
		})
	}
	require.Equal(t, http.StatusBadRequest, serve(t, mux, http.MethodPost, "/v1/refunds", `{`).Code)
	require.Equal(t, http.StatusBadRequest, serve(t, mux, http.MethodPost, "/v1/refunds", `{"RequestNo":"x","Amount":0}`).Code)
	require.Equal(t, http.StatusNotFound, serve(t, mux, http.MethodGet, "/v1/refunds/missing", "").Code)
}

func serve(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
