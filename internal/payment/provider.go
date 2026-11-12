package payment

import (
	"context"
	"net/http"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusUnknown   Status = "unknown"
)

type PayRequest struct {
	RequestNo          string
	BookingNo          string
	Amount             int64
	SimulationScenario string
}

type RefundRequest struct {
	RequestNo          string
	RefundNo           string
	PaymentTradeNo     string
	Amount             int64
	AttemptSequence    int
	SimulationScenario string
}

type Result struct {
	Status         Status
	ChannelTradeNo string
	FailureCode    string
	OccurredAt     time.Time
}

type Notification struct {
	NotificationNo string
	RequestNo      string
	ChannelTradeNo string
	Amount         int64
	Status         Status
	FailureCode    string
	OccurredAt     time.Time
}

// Provider isolates booking rules from a concrete payment channel. A future
// Alipay adapter must implement this interface without changing booking state
// transitions.
type Provider interface {
	CreatePayment(context.Context, PayRequest) (Result, error)
	QueryPayment(context.Context, string) (Result, error)
	CreateRefund(context.Context, RefundRequest) (Result, error)
	QueryRefund(context.Context, string) (Result, error)
	ParsePaymentNotification(http.Header, []byte) (Notification, error)
}

// SimulationController is implemented only by the teaching mock channel. A
// future real provider such as Alipay does not need to implement it.
type SimulationController interface {
	TriggerPaymentNotification(context.Context, string, bool) error
}
