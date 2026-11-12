-- A payment reported failed may later notify success after a retry succeeded.
-- Keep both successful charge facts and refund the extra charge; only one
-- in-flight/unknown payment is forbidden at a time.
ALTER TABLE payment_orders
  MODIFY COLUMN active_guard TINYINT GENERATED ALWAYS AS (
    CASE WHEN status IN ('pending', 'unknown') THEN 1 ELSE NULL END
  ) STORED;
