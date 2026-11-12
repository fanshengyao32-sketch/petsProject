package closing

import (
	"context"
	"time"
)

type DailyMetrics struct {
	BookingCount, EffectiveCount, CompletedCount, CancelledCount, NoShowCount, UnresolvedRefundCount                  int
	CollectedCents, RefundedCents, UnrefundedCents, CompletedIncomeCents, NoShowCollectedCents, UnresolvedRefundCents int64
}

type DailySlot struct {
	ID               uint64
	StartsAt, EndsAt time.Time
	Metrics          DailyMetrics `gorm:"-"`
}

type DailyDetail struct {
	BookingNo                     string
	SlotID                        uint64
	StartsAt, EndsAt              time.Time
	OwnerName, PetName, Status    string
	CollectedCents, RefundedCents int64
	UnrefundedCents               int64 `gorm:"-"`
	UnresolvedRefundCents         int64
	UnresolvedRefundCount         int
	RefundStatuses                string
	CompletionFact                bool
	CompletedIncomeCents          int64 `gorm:"-"`
	NoShowReason                  string
}

type DailyReport struct {
	BusinessDate string
	Slots        []DailySlot
	Details      []DailyDetail
	Totals       DailyMetrics
}

func (s *Service) Today(location *time.Location) string {
	return s.clock.Now().In(location).Format("2006-01-02")
}

func (s *Service) BuildDailyReport(ctx context.Context, storeID uint64, businessDate string, startUTC, endUTC time.Time) (DailyReport, error) {
	report := DailyReport{BusinessDate: businessDate}
	if storeID == 0 || businessDate == "" || !startUTC.Before(endUTC) {
		return report, ErrInvalidRequest
	}
	if err := s.db.WithContext(ctx).Table("service_slots").Select("id, starts_at, ends_at").Where("store_id = ? AND starts_at >= ? AND starts_at < ?", storeID, startUTC.UTC(), endUTC.UTC()).Order("starts_at, id").Scan(&report.Slots).Error; err != nil {
		return report, err
	}
	if err := s.db.WithContext(ctx).Table("bookings b").
		Select(`b.booking_no, b.slot_id, s.starts_at, s.ends_at, owner.display_name AS owner_name, p.name AS pet_name, b.status,
		COALESCE((SELECT SUM(po.amount_cents) FROM payment_orders po WHERE po.booking_id = b.id AND po.status = 'succeeded'), 0) AS collected_cents,
		COALESCE((SELECT SUM(ro.amount_cents) FROM refund_orders ro WHERE ro.booking_id = b.id AND ro.status = 'succeeded'), 0) AS refunded_cents,
		COALESCE((SELECT SUM(ro.amount_cents) FROM refund_orders ro WHERE ro.booking_id = b.id AND ro.status IN ('pending','processing','unknown','failed')), 0) AS unresolved_refund_cents,
		COALESCE((SELECT COUNT(*) FROM refund_orders ro WHERE ro.booking_id = b.id AND ro.status IN ('pending','processing','unknown','failed')), 0) AS unresolved_refund_count,
		COALESCE((SELECT GROUP_CONCAT(DISTINCT ro.status ORDER BY ro.status SEPARATOR ',') FROM refund_orders ro WHERE ro.booking_id = b.id), '') AS refund_statuses,
		EXISTS(SELECT 1 FROM fulfillment_events fe WHERE fe.booking_id = b.id AND fe.event_type = 'completed') AS completion_fact,
		COALESCE(n.reason, '') AS no_show_reason`).
		Joins("JOIN service_slots s ON s.id = b.slot_id").Joins("JOIN users owner ON owner.id = b.owner_id").Joins("JOIN pets p ON p.id = b.pet_id").Joins("LEFT JOIN no_show_records n ON n.booking_id = b.id").
		Where("s.store_id = ? AND s.starts_at >= ? AND s.starts_at < ?", storeID, startUTC.UTC(), endUTC.UTC()).Order("s.starts_at, b.id").Scan(&report.Details).Error; err != nil {
		return report, err
	}
	slotIndexes := make(map[uint64]int, len(report.Slots))
	for index := range report.Slots {
		slotIndexes[report.Slots[index].ID] = index
	}
	for index := range report.Details {
		detail := &report.Details[index]
		detail.UnrefundedCents = detail.CollectedCents - detail.RefundedCents
		if detail.UnrefundedCents < 0 {
			detail.UnrefundedCents = 0
		}
		if detail.Status == "completed" && detail.CompletionFact {
			detail.CompletedIncomeCents = 9900
		}
		metrics := DailyMetrics{}
		accumulateDetail(&metrics, *detail)
		report.Totals.add(metrics)
		if slotIndex, ok := slotIndexes[detail.SlotID]; ok {
			report.Slots[slotIndex].Metrics.add(metrics)
		}
	}
	return report, nil
}

func accumulateDetail(metrics *DailyMetrics, detail DailyDetail) {
	metrics.BookingCount++
	if isEffectiveBooking(detail.Status) {
		metrics.EffectiveCount++
	}
	if detail.Status == "completed" && detail.CompletionFact {
		metrics.CompletedCount++
	}
	if detail.Status == "cancelled" || detail.Status == "closed" {
		metrics.CancelledCount++
	}
	if detail.Status == "no_show" {
		metrics.NoShowCount++
		metrics.NoShowCollectedCents += detail.CollectedCents
	}
	metrics.UnresolvedRefundCount += detail.UnresolvedRefundCount
	metrics.CollectedCents += detail.CollectedCents
	metrics.RefundedCents += detail.RefundedCents
	metrics.UnrefundedCents += detail.UnrefundedCents
	metrics.CompletedIncomeCents += detail.CompletedIncomeCents
	metrics.UnresolvedRefundCents += detail.UnresolvedRefundCents
}

func (m *DailyMetrics) add(other DailyMetrics) {
	m.BookingCount += other.BookingCount
	m.EffectiveCount += other.EffectiveCount
	m.CompletedCount += other.CompletedCount
	m.CancelledCount += other.CancelledCount
	m.NoShowCount += other.NoShowCount
	m.UnresolvedRefundCount += other.UnresolvedRefundCount
	m.CollectedCents += other.CollectedCents
	m.RefundedCents += other.RefundedCents
	m.UnrefundedCents += other.UnrefundedCents
	m.CompletedIncomeCents += other.CompletedIncomeCents
	m.NoShowCollectedCents += other.NoShowCollectedCents
	m.UnresolvedRefundCents += other.UnresolvedRefundCents
}

func isEffectiveBooking(status string) bool {
	switch status {
	case "confirmed", "cancel_pending", "checked_in", "in_service", "completed", "no_show":
		return true
	default:
		return false
	}
}
