package payment

import "errors"

var (
	ErrInvalidRequest      = errors.New("invalid payment request")
	ErrBookingNotFound     = errors.New("booking not found")
	ErrPaymentNotFound     = errors.New("payment not found")
	ErrForbidden           = errors.New("operation forbidden")
	ErrInvalidBookingState = errors.New("booking state does not allow payment")
	ErrPaymentExists       = errors.New("an active payment already exists for booking")
	ErrAmountMismatch      = errors.New("payment amount does not match booking")
	ErrInvalidNotification = errors.New("invalid payment notification")
	ErrRefundNotFound      = errors.New("refund not found")
	ErrRefundInProgress    = errors.New("refund is already processing")
	ErrRefundNotAllowed    = errors.New("refund state does not allow operation")
)
