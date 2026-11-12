package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type paymentRequest struct {
	RequestNo   string `json:"request_no"`
	BookingNo   string `json:"booking_no"`
	Amount      int64  `json:"amount_cents"`
	Scenario    string `json:"scenario"`
	CallbackURL string `json:"callback_url"`
}

type channelResult struct {
	Status         string    `json:"status"`
	ChannelTradeNo string    `json:"channel_trade_no"`
	FailureCode    string    `json:"failure_code,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type notification struct {
	NotificationNo string    `json:"NotificationNo"`
	RequestNo      string    `json:"RequestNo"`
	ChannelTradeNo string    `json:"ChannelTradeNo"`
	Amount         int64     `json:"Amount"`
	Status         string    `json:"Status"`
	FailureCode    string    `json:"FailureCode,omitempty"`
	OccurredAt     time.Time `json:"OccurredAt"`
}

type mockServer struct {
	mu       sync.RWMutex
	payments map[string]paymentRequest
	results  map[string]channelResult
	refunds  map[string]channelResult
	secret   []byte
	client   *http.Client
}

func main() {
	secret := os.Getenv("MOCK_PAYMENT_CALLBACK_SECRET")
	if secret == "" {
		log.Fatal("MOCK_PAYMENT_CALLBACK_SECRET is required")
	}
	server := newMockServer([]byte(secret), nil)
	mux := newMux(server)

	addr := os.Getenv("MOCK_PAYMENT_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	httpServer := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("mock payment listening on %s", addr)
	log.Fatal(httpServer.ListenAndServe())
}

func newMockServer(secret []byte, client *http.Client) *mockServer {
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	return &mockServer{
		payments: make(map[string]paymentRequest),
		results:  make(map[string]channelResult),
		refunds:  make(map[string]channelResult),
		secret:   secret,
		client:   client,
	}
}

func newMux(server *mockServer) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/payments", server.createPayment)
	mux.HandleFunc("GET /v1/payments/{requestNo}", server.queryPayment)
	mux.HandleFunc("POST /v1/payment-notifications", server.triggerNotification)
	mux.HandleFunc("POST /v1/refunds", server.createRefund)
	mux.HandleFunc("GET /v1/refunds/{requestNo}", server.queryRefund)
	return mux
}

func (m *mockServer) createPayment(w http.ResponseWriter, r *http.Request) {
	var payload paymentRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.RequestNo == "" || payload.BookingNo == "" || payload.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	result := channelResult{OccurredAt: time.Now().UTC(), ChannelTradeNo: "MOCK-" + payload.RequestNo}
	switch payload.Scenario {
	case "success", "duplicate":
		result.Status = "succeeded"
	case "failure":
		result.Status = "failed"
		result.FailureCode = "MOCK_DECLINED"
	case "timeout":
		result.Status = "unknown"
		result.FailureCode = "MOCK_TIMEOUT"
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported scenario"})
		return
	}
	m.mu.Lock()
	m.payments[payload.RequestNo] = payload
	m.results[payload.RequestNo] = result
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, result)

	if payload.Scenario == "duplicate" && payload.CallbackURL != "" {
		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = m.sendNotification(payload, true)
		}()
	}
}

func (m *mockServer) queryPayment(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	result, ok := m.results[r.PathValue("requestNo")]
	m.mu.RUnlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *mockServer) triggerNotification(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		RequestNo string `json:"request_no"`
		Duplicate bool   `json:"duplicate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.RequestNo == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	m.mu.RLock()
	payment, ok := m.payments[payload.RequestNo]
	m.mu.RUnlock()
	if !ok || payment.CallbackURL == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
		return
	}
	if err := m.sendNotification(payment, payload.Duplicate); err != nil {
		// The provider error may contain the callback URL, query parameters or
		// network details. Keep the external response stable and non-sensitive.
		log.Printf("mock payment notification delivery failed request_no=%s", payload.RequestNo)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":   "NOTIFICATION_DELIVERY_FAILED",
			"message": "支付结果通知发送失败，请稍后重试",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (m *mockServer) sendNotification(payment paymentRequest, duplicate bool) error {
	message := notification{
		NotificationNo: "MOCK-NOTIFY-" + payment.RequestNo,
		RequestNo:      payment.RequestNo,
		ChannelTradeNo: "MOCK-" + payment.RequestNo,
		Amount:         payment.Amount,
		Status:         "succeeded",
		OccurredAt:     time.Now().UTC(),
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	count := 1
	if duplicate {
		count = 2
	}
	for index := 0; index < count; index++ {
		mac := hmac.New(sha256.New, m.secret)
		_, _ = mac.Write(body)
		request, err := http.NewRequest(http.MethodPost, payment.CallbackURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Mock-Signature", hex.EncodeToString(mac.Sum(nil)))
		response, err := m.client.Do(request)
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("callback returned %d", response.StatusCode)
		}
	}
	return nil
}

func (m *mockServer) createRefund(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		RequestNo string `json:"RequestNo"`
		Amount    int64  `json:"Amount"`
		Scenario  string `json:"SimulationScenario"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.RequestNo == "" || payload.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	result := channelResult{Status: "succeeded", ChannelTradeNo: "MOCK-RF-" + payload.RequestNo, OccurredAt: time.Now().UTC()}
	switch payload.Scenario {
	case "failure":
		result.Status, result.FailureCode = "failed", "MOCK_REFUND_REJECTED"
	case "timeout":
		result.Status, result.FailureCode = "unknown", "MOCK_REFUND_TIMEOUT"
	}
	m.mu.Lock()
	m.refunds[payload.RequestNo] = result
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, result)
}

func (m *mockServer) queryRefund(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	result, ok := m.refunds[r.PathValue("requestNo")]
	m.mu.RUnlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "refund not found"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
