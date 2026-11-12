package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMockClientProviderOperations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/payments":
			var request mockPaymentRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			require.Equal(t, "success", request.Scenario)
			_ = json.NewEncoder(w).Encode(mockResult{Status: StatusSucceeded, ChannelTradeNo: "TRADE-1", OccurredAt: time.Now().UTC()})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/payments/PAY-1":
			_ = json.NewEncoder(w).Encode(mockResult{Status: StatusUnknown, FailureCode: "TIMEOUT"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/refunds":
			_ = json.NewEncoder(w).Encode(mockResult{Status: StatusSucceeded, ChannelTradeNo: "REFUND-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/refunds/RF-1":
			_ = json.NewEncoder(w).Encode(mockResult{Status: StatusFailed, FailureCode: "DECLINED"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/payment-notifications":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewMockClient(server.URL+"/", "http://callback", "secret", server.Client())
	created, err := client.CreatePayment(context.Background(), PayRequest{RequestNo: "PAY-1", BookingNo: "BK-1", Amount: 9900, SimulationScenario: "success"})
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, created.Status)
	queried, err := client.QueryPayment(context.Background(), "PAY-1")
	require.NoError(t, err)
	require.Equal(t, StatusUnknown, queried.Status)
	refunded, err := client.CreateRefund(context.Background(), RefundRequest{RequestNo: "RF-1", Amount: 9900})
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, refunded.Status)
	refundQuery, err := client.QueryRefund(context.Background(), "RF-1")
	require.NoError(t, err)
	require.Equal(t, StatusFailed, refundQuery.Status)
	require.NoError(t, client.TriggerPaymentNotification(context.Background(), "PAY-1", true))
}

func TestMockClientNotificationSignatureAndErrors(t *testing.T) {
	client := NewMockClient("http://unused", "http://callback", "secret", nil)
	notification := Notification{NotificationNo: "N-1", RequestNo: "PAY-1", ChannelTradeNo: "T-1", Amount: 9900, Status: StatusSucceeded, OccurredAt: time.Now().UTC()}
	body, err := json.Marshal(notification)
	require.NoError(t, err)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	headers := make(http.Header)
	headers.Set("X-Mock-Signature", hex.EncodeToString(mac.Sum(nil)))
	parsed, err := client.ParsePaymentNotification(headers, body)
	require.NoError(t, err)
	require.Equal(t, notification.RequestNo, parsed.RequestNo)

	headers.Set("X-Mock-Signature", "bad")
	_, err = client.ParsePaymentNotification(headers, body)
	require.Error(t, err)
	headers.Set("X-Mock-Signature", hex.EncodeToString(mac.Sum(nil)))
	_, err = client.ParsePaymentNotification(headers, []byte("not-json"))
	require.Error(t, err)

	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad-json" {
			_, _ = w.Write([]byte("not-json"))
			return
		}
		http.Error(w, "channel down", http.StatusBadGateway)
	}))
	defer badServer.Close()
	badClient := NewMockClient(badServer.URL, "http://callback", "secret", badServer.Client())
	_, err = badClient.QueryPayment(context.Background(), "missing")
	require.Error(t, err)
	var output mockResult
	err = badClient.doJSON(context.Background(), http.MethodGet, "/bad-json", nil, &output)
	require.Error(t, err)
}
