package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Clock interface{ Now() time.Time }

type Service struct {
	db       *gorm.DB
	provider Provider
	clock    Clock
}

type StartInput struct {
	OwnerID   uint64
	BookingNo string
	RequestNo string
	Scenario  string
}

func NewService(db *gorm.DB, provider Provider, clock Clock) *Service {
	return &Service{db: db, provider: provider, clock: clock}
}

func (s *Service) Start(ctx context.Context, input StartInput) (PaymentOrder, error) {
	if input.OwnerID == 0 || input.BookingNo == "" || input.RequestNo == "" || len(input.RequestNo) > 64 || !validScenario(input.Scenario) {
		return PaymentOrder{}, ErrInvalidRequest
	}
	var order PaymentOrder
	created := false
	var businessErr error
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var booking paymentBooking
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("booking_no = ?", input.BookingNo).First(&booking).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBookingNotFound
			}
			return err
		}
		if booking.OwnerID != input.OwnerID {
			return ErrForbidden
		}
		// Query the idempotency key after locking the booking. This is a
		// current/locking read, so a concurrent first request that committed
		// while we waited is returned instead of being reported as a conflict.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_no = ?", input.RequestNo).First(&order).Error; err == nil {
			if order.BookingID != booking.ID {
				return ErrInvalidRequest
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := s.clock.Now().UTC()
		if booking.Status == "pending_payment" && !now.Before(booking.HoldExpiresAt) {
			if err := tx.Model(&paymentBooking{}).Where("id = ? AND status = ? AND capacity_released_at IS NULL", booking.ID, "pending_payment").
				Updates(map[string]any{"status": "expired", "capacity_released_at": now, "updated_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
			booking.Status = "expired"
			businessErr = ErrInvalidBookingState
			return nil
		}
		if booking.Status != "pending_payment" {
			return ErrInvalidBookingState
		}
		var active int64
		if err := tx.Model(&PaymentOrder{}).Where("booking_id = ? AND status IN ?", booking.ID, []string{OrderPending, OrderUnknown}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return ErrPaymentExists
		}
		order = PaymentOrder{RequestNo: input.RequestNo, BookingID: booking.ID, Provider: "mock", AmountCents: booking.AmountCents, Status: OrderPending}
		if err := tx.Create(&order).Error; err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return ErrPaymentExists
			}
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return order, err
	}
	if businessErr != nil {
		return PaymentOrder{}, businessErr
	}
	if !created {
		return order, nil
	}

	result, providerErr := s.provider.CreatePayment(ctx, PayRequest{RequestNo: input.RequestNo, BookingNo: input.BookingNo, Amount: order.AmountCents, SimulationScenario: input.Scenario})
	if providerErr != nil {
		result = Result{Status: StatusUnknown, FailureCode: "PROVIDER_UNAVAILABLE", OccurredAt: s.clock.Now().UTC()}
	}
	if err := s.applyResult(ctx, order.RequestNo, result); err != nil {
		return PaymentOrder{}, err
	}
	if err := s.db.WithContext(ctx).Where("request_no = ?", order.RequestNo).First(&order).Error; err != nil {
		return PaymentOrder{}, err
	}
	return order, nil
}

func (s *Service) applyResult(ctx context.Context, requestNo string, result Result) error {
	if result.Status == StatusSucceeded {
		notification := Notification{NotificationNo: "SYNC-" + requestNo, RequestNo: requestNo, ChannelTradeNo: result.ChannelTradeNo, Amount: 9900, Status: StatusSucceeded, OccurredAt: result.OccurredAt}
		return s.HandleNotification(ctx, notification, []byte("synchronous-provider-result"))
	}
	status := OrderUnknown
	if result.Status == StatusFailed {
		status = OrderFailed
	}
	return s.db.WithContext(ctx).Model(&PaymentOrder{}).Where("request_no = ? AND status IN ?", requestNo, []string{OrderPending, OrderUnknown}).
		Updates(map[string]any{"status": status, "failure_code": nullableString(result.FailureCode), "updated_at": s.clock.Now().UTC()}).Error
}

func (s *Service) HandleNotification(ctx context.Context, notification Notification, rawBody []byte) error {
	if notification.NotificationNo == "" || notification.RequestNo == "" || notification.Amount <= 0 || !validNotificationStatus(notification.Status) || (notification.Status == StatusSucceeded && notification.ChannelTradeNo == "") {
		return ErrInvalidNotification
	}
	receivedAt := s.clock.Now().UTC()
	hash := sha256.Sum256(rawBody)
	var businessErr error
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record := PaymentNotification{Provider: "mock", NotificationNo: notification.NotificationNo, RequestNo: notification.RequestNo, PayloadHash: hex.EncodeToString(hash[:]), ReceivedAt: receivedAt, Result: "received"}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			return nil
		}
		var orderLookup PaymentOrder
		if err := tx.Where("request_no = ?", notification.RequestNo).First(&orderLookup).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				businessErr = ErrPaymentNotFound
				return tx.Model(&record).Update("result", "unknown_request").Error
			}
			return err
		}
		// Use the same lock order as payment initiation: booking first, then
		// payment order. This avoids a deadlock between a provider response and
		// an idempotent concurrent start request.
		var booking paymentBooking
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&booking, orderLookup.BookingID).Error; err != nil {
			return err
		}
		var order PaymentOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, orderLookup.ID).Error; err != nil {
			return err
		}
		if notification.Amount != order.AmountCents {
			if err := tx.Model(&record).Update("result", "amount_mismatch").Error; err != nil {
				return err
			}
			businessErr = ErrAmountMismatch
			return nil
		}
		if order.Status == OrderSucceeded {
			return tx.Model(&record).Update("result", "duplicate_success").Error
		}
		if notification.Status != StatusSucceeded {
			status := OrderUnknown
			if notification.Status == StatusFailed {
				status = OrderFailed
			}
			if err := tx.Model(&order).Updates(map[string]any{"status": status, "failure_code": nullableString(notification.FailureCode), "updated_at": receivedAt}).Error; err != nil {
				return err
			}
			return tx.Model(&record).Update("result", status).Error
		}

		if err := tx.Model(&order).Updates(map[string]any{"status": OrderSucceeded, "channel_trade_no": notification.ChannelTradeNo, "failure_code": nil, "paid_at": receivedAt, "updated_at": receivedAt}).Error; err != nil {
			return err
		}
		if booking.Status == "pending_payment" && receivedAt.Before(booking.HoldExpiresAt) {
			if err := tx.Model(&booking).Updates(map[string]any{"status": "confirmed", "updated_at": receivedAt, "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
			return tx.Model(&record).Update("result", "confirmed").Error
		}
		if booking.Status == "pending_payment" {
			if err := tx.Model(&booking).Where("status = ? AND capacity_released_at IS NULL", "pending_payment").
				Updates(map[string]any{"status": "expired", "capacity_released_at": receivedAt, "updated_at": receivedAt, "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
			booking.Status = "expired"
		}
		var otherSuccessfulPayments int64
		if err := tx.Model(&PaymentOrder{}).Where("booking_id = ? AND id <> ? AND status = ?", booking.ID, order.ID, OrderSucceeded).Count(&otherSuccessfulPayments).Error; err != nil {
			return err
		}
		if otherSuccessfulPayments > 0 {
			return createAnomalyRefund(tx, record, order, booking, receivedAt, "duplicate_payment", "payment.duplicate_success", "duplicate_charge_refund_pending")
		}
		if booking.Status == "expired" || booking.Status == "closed" {
			return createAnomalyRefund(tx, record, order, booking, receivedAt, "late_payment", "payment.late_success", "late_success_refund_pending")
		}
		return tx.Model(&record).Update("result", "success_no_transition").Error
	})
	if err != nil {
		return err
	}
	return businessErr
}

func createAnomalyRefund(tx *gorm.DB, notification PaymentNotification, order PaymentOrder, booking paymentBooking, occurredAt time.Time, reason, action, notificationResult string) error {
	refund := RefundOrder{RefundNo: anomalyRefundNo(reason, order.RequestNo), BookingID: booking.ID, PaymentOrderID: order.ID, AmountCents: order.AmountCents, Status: RefundPending, Reason: reason}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&refund).Error; err != nil {
		return err
	}
	detail := `{"reason":"` + reason + `"}`
	audit := auditLog{Action: action, ResourceType: "booking", ResourceID: booking.BookingNo, Result: "refund_pending", DetailJSON: detail, OccurredAt: occurredAt}
	if err := tx.Create(&audit).Error; err != nil {
		return err
	}
	return tx.Model(&notification).Update("result", notificationResult).Error
}

func anomalyRefundNo(reason, requestNo string) string {
	hash := sha256.Sum256([]byte(reason + ":" + requestNo))
	return "RF" + hex.EncodeToString(hash[:16])
}

func (s *Service) ListOwner(ctx context.Context, ownerID uint64) ([]PaymentView, error) {
	var views []PaymentView
	err := s.db.WithContext(ctx).Table("bookings b").
		Select("b.booking_no, b.status AS booking_status, b.amount_cents, COALESCE(p.request_no, '') AS request_no, COALESCE(p.status, '') AS payment_status, COALESCE(p.failure_code, '') AS failure_code, COALESCE(r.status, '') AS refund_status, COALESCE(r.reason, '') AS refund_reason").
		Joins("LEFT JOIN payment_orders p ON p.booking_id = b.id").
		Joins("LEFT JOIN refund_orders r ON r.payment_order_id = p.id").
		Where("b.owner_id = ?", ownerID).Order("b.created_at DESC, p.created_at DESC").Scan(&views).Error
	return views, err
}

func (s *Service) RequireOwnerPayment(ctx context.Context, ownerID uint64, requestNo string) error {
	if ownerID == 0 || requestNo == "" {
		return ErrInvalidRequest
	}
	var count int64
	err := s.db.WithContext(ctx).Table("payment_orders p").
		Joins("JOIN bookings b ON b.id = p.booking_id").
		Where("p.request_no = ? AND b.owner_id = ?", requestNo, ownerID).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrPaymentNotFound
	}
	return nil
}

func validScenario(value string) bool {
	switch value {
	case "success", "failure", "timeout", "duplicate":
		return true
	default:
		return false
	}
}

func validNotificationStatus(value Status) bool {
	return value == StatusSucceeded || value == StatusFailed || value == StatusUnknown
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
