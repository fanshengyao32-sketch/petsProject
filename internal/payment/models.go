package payment

import "time"

const (
	OrderPending     = "pending"
	OrderUnknown     = "unknown"
	OrderSucceeded   = "succeeded"
	OrderFailed      = "failed"
	RefundPending    = "pending"
	RefundProcessing = "processing"
	RefundUnknown    = "unknown"
	RefundSucceeded  = "succeeded"
	RefundFailed     = "failed"
)

type PaymentOrder struct {
	ID             uint64     `json:"id"`
	RequestNo      string     `json:"request_no"`
	BookingID      uint64     `json:"booking_id"`
	Provider       string     `json:"provider"`
	ChannelTradeNo *string    `json:"channel_trade_no"`
	AmountCents    int64      `json:"amount_cents"`
	Status         string     `json:"status"`
	FailureCode    *string    `json:"failure_code"`
	PaidAt         *time.Time `json:"paid_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (PaymentOrder) TableName() string { return "payment_orders" }

type PaymentNotification struct {
	ID             uint64
	Provider       string
	NotificationNo string
	RequestNo      string
	PayloadHash    string
	ReceivedAt     time.Time
	Result         string
}

func (PaymentNotification) TableName() string { return "payment_notifications" }

type RefundOrder struct {
	ID             uint64
	RefundNo       string
	BookingID      uint64
	PaymentOrderID uint64
	AmountCents    int64
	Status         string
	Reason         string
	FailureCode    *string
	RefundedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (RefundOrder) TableName() string { return "refund_orders" }

type RefundAttempt struct {
	ID            uint64
	RefundOrderID uint64
	RequestNo     string
	SequenceNo    int
	Status        string
	FailureCode   *string
	RequestedAt   time.Time
	CompletedAt   *time.Time
}

func (RefundAttempt) TableName() string { return "refund_attempts" }

type RefundView struct {
	RefundNo     string
	BookingNo    string
	AmountCents  int64
	Status       string
	Reason       string
	FailureCode  string
	AttemptCount int
	UpdatedAt    time.Time
}

type paymentBooking struct {
	ID                 uint64
	BookingNo          string
	OwnerID            uint64
	AmountCents        int64
	Status             string
	HoldExpiresAt      time.Time
	CapacityReleasedAt *time.Time
	Version            int64
}

func (paymentBooking) TableName() string { return "bookings" }

type PaymentView struct {
	BookingNo     string
	BookingStatus string
	AmountCents   int64
	RequestNo     string
	PaymentStatus string
	FailureCode   string
	RefundStatus  string
	RefundReason  string
}

type auditLog struct {
	ActorID      *uint64
	Action       string
	ResourceType string
	ResourceID   string
	Result       string
	DetailJSON   string
	OccurredAt   time.Time
}

func (auditLog) TableName() string { return "audit_logs" }
