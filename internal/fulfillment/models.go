package fulfillment

import "time"

const (
	EventCheckedIn    = "checked_in"
	EventServiceStart = "service_started"
	EventCompleted    = "completed"
)

type Event struct {
	ID             uint64
	BookingID      uint64
	EventType      string
	OperatorID     uint64
	OccurredAt     time.Time
	IdempotencyKey string
	CreatedAt      time.Time
}

func (Event) TableName() string { return "fulfillment_events" }

type booking struct {
	ID        uint64
	BookingNo string
	SlotID    uint64
	OwnerID   uint64
	PetID     uint64
	Status    string
	Version   int64
}

func (booking) TableName() string { return "bookings" }

type slot struct {
	ID       uint64
	StoreID  uint64
	StartsAt time.Time
	EndsAt   time.Time
}

func (slot) TableName() string { return "service_slots" }

type user struct {
	ID          uint64
	DisplayName string
	Role        string
	StoreID     *uint64
}

func (user) TableName() string { return "users" }

type cancellationRequest struct {
	ID        uint64
	BookingID uint64
	Status    string
}

func (cancellationRequest) TableName() string { return "cancellation_requests" }

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

type WorkView struct {
	BookingNo   string
	OwnerName   string
	PetName     string
	StartsAt    time.Time
	EndsAt      time.Time
	Status      string
	CheckinAt   *time.Time
	CheckinBy   string
	StartedAt   *time.Time
	StartedBy   string
	CompletedAt *time.Time
	CompletedBy string
}
