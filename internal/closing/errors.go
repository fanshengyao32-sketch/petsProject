package closing

import "errors"

var (
	ErrInvalidRequest      = errors.New("invalid no-show request")
	ErrBookingNotFound     = errors.New("booking not found")
	ErrForbidden           = errors.New("operation forbidden")
	ErrInvalidBookingState = errors.New("booking state does not allow no-show")
	ErrSlotNotEnded        = errors.New("service slot has not ended")
)
