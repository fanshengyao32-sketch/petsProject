package fulfillment

import "errors"

var (
	ErrInvalidRequest      = errors.New("invalid fulfillment request")
	ErrBookingNotFound     = errors.New("booking not found")
	ErrForbidden           = errors.New("operation forbidden")
	ErrInvalidBookingState = errors.New("booking state does not allow fulfillment operation")
	ErrCancellationPending = errors.New("pending cancellation prevents check-in")
)
