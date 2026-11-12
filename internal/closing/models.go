package closing

import "time"

type NoShowRecord struct {
	ID        uint64
	BookingID uint64
	MarkedBy  uint64
	Reason    string
	MarkedAt  time.Time
}

func (NoShowRecord) TableName() string { return "no_show_records" }

type booking struct {
	ID        uint64
	BookingNo string
	SlotID    uint64
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
	ID      uint64
	Role    string
	StoreID *uint64
}

func (user) TableName() string { return "users" }

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

type NoShowView struct {
	BookingNo string
	OwnerName string
	PetName   string
	StartsAt  time.Time
	EndsAt    time.Time
	Status    string
	Reason    string
	MarkedAt  *time.Time
	MarkedBy  string
	CanMark   bool `gorm:"-"`
}
