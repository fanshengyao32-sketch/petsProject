package booking

import "time"

const (
	StatusPendingPayment = "pending_payment"
	StatusConfirmed      = "confirmed"
	StatusCancelPending  = "cancel_pending"
	StatusCheckedIn      = "checked_in"
	StatusInService      = "in_service"
	StatusCompleted      = "completed"
	StatusCancelled      = "cancelled"
	StatusExpired        = "expired"
	StatusClosed         = "closed"
	StatusNoShow         = "no_show"
	PriceCents           = int64(9900)
)

var occupyingStatuses = []string{
	StatusPendingPayment,
	StatusConfirmed,
	StatusCancelPending,
	StatusCheckedIn,
	StatusInService,
	// A completed service consumed the slot even though it is a terminal
	// booking state; it must never make capacity appear available again.
	StatusCompleted,
}

type Store struct {
	ID       uint64
	Code     string
	Name     string
	Timezone string
}

type ServiceItem struct {
	ID         uint64
	Code       string
	Name       string
	PriceCents int64
}

type User struct {
	ID          uint64
	ExternalID  string
	DisplayName string
	Role        string
	StoreID     *uint64
}

type Pet struct {
	ID      uint64
	OwnerID uint64
	Name    string
	Breed   string
}

type ServiceSlot struct {
	ID            uint64
	StoreID       uint64
	ServiceItemID uint64
	StartsAt      time.Time
	EndsAt        time.Time
	Capacity      int
	Version       int64
	CreatedBy     uint64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (ServiceSlot) TableName() string { return "service_slots" }

type Booking struct {
	ID                       uint64
	BookingNo                string
	RequestKey               string
	SlotID                   uint64
	OwnerID                  uint64
	PetID                    uint64
	AmountCents              int64
	Status                   string
	HoldExpiresAt            time.Time
	CapacityReleasedAt       *time.Time
	Version                  int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
	NoShowReason             string     `gorm:"column:no_show_reason;->"`
	SlotStartsAt             time.Time  `gorm:"column:slot_starts_at;->"`
	SlotEndsAt               time.Time  `gorm:"column:slot_ends_at;->"`
	CancellationStatus       string     `gorm:"column:cancellation_status;->"`
	CancellationReason       string     `gorm:"column:cancellation_reason;->"`
	CancellationReviewReason string     `gorm:"column:cancellation_review_reason;->"`
	CheckedInAt              *time.Time `gorm:"column:checked_in_at;->"`
	ServiceStartedAt         *time.Time `gorm:"column:service_started_at;->"`
	ServiceCompletedAt       *time.Time `gorm:"column:service_completed_at;->"`
}

func (Booking) TableName() string { return "bookings" }

type SlotView struct {
	ServiceSlot
	Occupied  int
	Available int
}
