package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type MockClient struct {
	baseURL     string
	callbackURL string
	secret      []byte
	httpClient  *http.Client
}

func NewMockClient(baseURL, callbackURL, secret string, client *http.Client) *MockClient {
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	return &MockClient{baseURL: strings.TrimRight(baseURL, "/"), callbackURL: callbackURL, secret: []byte(secret), httpClient: client}
}

type mockPaymentRequest struct {
	RequestNo   string `json:"request_no"`
	BookingNo   string `json:"booking_no"`
	Amount      int64  `json:"amount_cents"`
	Scenario    string `json:"scenario"`
	CallbackURL string `json:"callback_url"`
}

type mockResult struct {
	Status         Status    `json:"status"`
	ChannelTradeNo string    `json:"channel_trade_no"`
	FailureCode    string    `json:"failure_code"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func (m *MockClient) CreatePayment(ctx context.Context, request PayRequest) (Result, error) {
	payload := mockPaymentRequest{RequestNo: request.RequestNo, BookingNo: request.BookingNo, Amount: request.Amount, Scenario: request.SimulationScenario, CallbackURL: m.callbackURL}
	var response mockResult
	if err := m.doJSON(ctx, http.MethodPost, "/v1/payments", payload, &response); err != nil {
		return Result{}, err
	}
	return Result(response), nil
}

func (m *MockClient) QueryPayment(ctx context.Context, requestNo string) (Result, error) {
	var response mockResult
	if err := m.doJSON(ctx, http.MethodGet, "/v1/payments/"+requestNo, nil, &response); err != nil {
		return Result{}, err
	}
	return Result(response), nil
}

func (m *MockClient) CreateRefund(ctx context.Context, request RefundRequest) (Result, error) {
	var response mockResult
	if err := m.doJSON(ctx, http.MethodPost, "/v1/refunds", request, &response); err != nil {
		return Result{}, err
	}
	return Result(response), nil
}

func (m *MockClient) QueryRefund(ctx context.Context, requestNo string) (Result, error) {
	var response mockResult
	if err := m.doJSON(ctx, http.MethodGet, "/v1/refunds/"+requestNo, nil, &response); err != nil {
		return Result{}, err
	}
	return Result(response), nil
}

func (m *MockClient) TriggerPaymentNotification(ctx context.Context, requestNo string, duplicate bool) error {
	payload := map[string]any{"request_no": requestNo, "duplicate": duplicate}
	return m.doJSON(ctx, http.MethodPost, "/v1/payment-notifications", payload, nil)
}

func (m *MockClient) ParsePaymentNotification(headers http.Header, body []byte) (Notification, error) {
	provided, err := hex.DecodeString(headers.Get("X-Mock-Signature"))
	if err != nil || len(provided) == 0 {
		return Notification{}, errors.New("invalid mock payment signature")
	}
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write(body)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return Notification{}, errors.New("invalid mock payment signature")
	}
	var notification Notification
	if err := json.Unmarshal(body, &notification); err != nil {
		return Notification{}, fmt.Errorf("decode payment notification: %w", err)
	}
	if notification.NotificationNo == "" || notification.RequestNo == "" || notification.Amount <= 0 {
		return Notification{}, errors.New("invalid payment notification")
	}
	return notification, nil
}

func (m *MockClient) doJSON(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, m.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := m.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("mock payment status %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return fmt.Errorf("decode mock payment response: %w", err)
	}
	return nil
}
