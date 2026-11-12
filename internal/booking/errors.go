package booking

import "errors"

var (
	ErrSlotNotFound        = errors.New("slot not found")
	ErrSlotStarted         = errors.New("slot has started")
	ErrCapacityFull        = errors.New("slot capacity is full")
	ErrPetNotOwned         = errors.New("pet does not belong to owner")
	ErrDuplicateActive     = errors.New("pet already has an active booking for slot")
	ErrBookingNotFound     = errors.New("booking not found")
	ErrInvalidBookingState = errors.New("booking state does not allow operation")
	ErrForbidden           = errors.New("operation forbidden")
	ErrSlotOverlap         = errors.New("service slot overlaps an existing slot")
	ErrInvalidRequest      = errors.New("invalid request")
	ErrInvalidSlotTime     = errors.New("slot end must be after start and start must be in the future")
	ErrTemporarilyBusy     = errors.New("booking service is temporarily busy")
)
