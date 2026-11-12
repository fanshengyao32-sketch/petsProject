package cancellation

import "time"

const (
	StatusPending      = "pending"
	StatusApproved     = "approved"
	StatusRejected     = "rejected"
	StatusAutoApproved = "auto_approved"
)

type Request struct {
	ID           uint64
	BookingID    uint64
	RequesterID  uint64
	Reason       string
	Status       string
	RequestedAt  time.Time
	ReviewedBy   *uint64
	ReviewedAt   *time.Time
	ReviewReason *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (Request) TableName() string { return "cancellation_requests" }

type booking struct {
	ID                 uint64
	BookingNo          string
	SlotID             uint64
	OwnerID            uint64
	AmountCents        int64
	Status             string
	CapacityReleasedAt *time.Time
	Version            int64
}

func (booking) TableName() string { return "bookings" }

type slot struct {
	ID       uint64
	StartsAt time.Time
}

func (slot) TableName() string { return "service_slots" }

type user struct {
	ID   uint64
	Role string
}

func (user) TableName() string { return "users" }

type paymentOrder struct {
	ID          uint64
	RequestNo   string
	BookingID   uint64
	AmountCents int64
	Status      string
}

func (paymentOrder) TableName() string { return "payment_orders" }

type refundOrder struct {
	ID             uint64
	RefundNo       string
	BookingID      uint64
	PaymentOrderID uint64
	AmountCents    int64
	Status         string
	Reason         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (refundOrder) TableName() string { return "refund_orders" }

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

type View struct {
	ID           uint64
	BookingNo    string
	OwnerName    string
	Reason       string
	Status       string
	RequestedAt  time.Time
	ReviewReason string
}
