-- T17 payment invariants for both existing and freshly initialized databases.
ALTER TABLE payment_orders
  ADD COLUMN active_guard TINYINT GENERATED ALWAYS AS (
    CASE WHEN status IN ('pending', 'unknown') THEN 1 ELSE NULL END
  ) STORED AFTER status,
  ADD UNIQUE KEY uk_payment_single_active (booking_id, active_guard);

ALTER TABLE refund_orders
  ADD COLUMN reason VARCHAR(64) NOT NULL DEFAULT 'approved_cancellation' AFTER status,
  ADD UNIQUE KEY uk_refund_payment (payment_order_id);
