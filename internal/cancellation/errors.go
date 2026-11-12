package cancellation

import "errors"

var (
	ErrInvalidRequest      = errors.New("invalid cancellation request")
	ErrBookingNotFound     = errors.New("booking not found")
	ErrRequestNotFound     = errors.New("cancellation request not found")
	ErrForbidden           = errors.New("operation forbidden")
	ErrInvalidBookingState = errors.New("booking state does not allow cancellation")
	ErrServiceStarted      = errors.New("service slot has started")
	ErrReasonRequired      = errors.New("cancellation reason is required within two hours")
	ErrPaymentNotFound     = errors.New("successful payment not found")
	ErrAlreadyReviewed     = errors.New("cancellation request has already been reviewed")
)
