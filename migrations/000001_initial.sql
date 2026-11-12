-- MySQL 8.0 / InnoDB. All timestamps are stored in UTC.
CREATE TABLE stores (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(32) NOT NULL,
  name VARCHAR(100) NOT NULL,
  timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_stores_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE service_items (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(32) NOT NULL,
  name VARCHAR(100) NOT NULL,
  price_cents BIGINT NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_service_items_code (code),
  CONSTRAINT chk_service_items_price CHECK (price_cents >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  external_id VARCHAR(64) NOT NULL,
  display_name VARCHAR(100) NOT NULL,
  role VARCHAR(32) NOT NULL,
  store_id BIGINT UNSIGNED NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_users_external_id (external_id),
  KEY idx_users_store (store_id),
  CONSTRAINT fk_users_store FOREIGN KEY (store_id) REFERENCES stores(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE pets (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  owner_id BIGINT UNSIGNED NOT NULL,
  name VARCHAR(100) NOT NULL,
  breed VARCHAR(100) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_pets_owner (owner_id),
  CONSTRAINT fk_pets_owner FOREIGN KEY (owner_id) REFERENCES users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE service_slots (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id BIGINT UNSIGNED NOT NULL,
  service_item_id BIGINT UNSIGNED NOT NULL,
  starts_at DATETIME(6) NOT NULL,
  ends_at DATETIME(6) NOT NULL,
  capacity INT NOT NULL DEFAULT 3,
  version BIGINT NOT NULL DEFAULT 0,
  created_by BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_slots_store_start (store_id, starts_at),
  KEY idx_slots_item_time (service_item_id, starts_at, ends_at),
  CONSTRAINT fk_slots_store FOREIGN KEY (store_id) REFERENCES stores(id),
  CONSTRAINT fk_slots_item FOREIGN KEY (service_item_id) REFERENCES service_items(id),
  CONSTRAINT fk_slots_creator FOREIGN KEY (created_by) REFERENCES users(id),
  CONSTRAINT chk_slots_time CHECK (ends_at > starts_at),
  CONSTRAINT chk_slots_capacity CHECK (capacity > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE bookings (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  booking_no VARCHAR(32) NOT NULL,
  request_key VARCHAR(64) NOT NULL,
  slot_id BIGINT UNSIGNED NOT NULL,
  owner_id BIGINT UNSIGNED NOT NULL,
  pet_id BIGINT UNSIGNED NOT NULL,
  amount_cents BIGINT NOT NULL,
  status VARCHAR(32) NOT NULL,
  active_guard TINYINT GENERATED ALWAYS AS (
    CASE
      WHEN status IN ('pending_payment', 'confirmed', 'cancel_pending', 'checked_in', 'in_service') THEN 1
      ELSE NULL
    END
  ) STORED,
  hold_expires_at DATETIME(6) NOT NULL,
  capacity_released_at DATETIME(6) NULL,
  version BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_bookings_no (booking_no),
  UNIQUE KEY uk_bookings_owner_request (owner_id, request_key),
  KEY idx_bookings_slot_status (slot_id, status),
  UNIQUE KEY uk_bookings_active_pet_slot (pet_id, slot_id, active_guard),
  KEY idx_bookings_hold_expiry (status, hold_expires_at),
  CONSTRAINT fk_bookings_slot FOREIGN KEY (slot_id) REFERENCES service_slots(id),
  CONSTRAINT fk_bookings_owner FOREIGN KEY (owner_id) REFERENCES users(id),
  CONSTRAINT fk_bookings_pet FOREIGN KEY (pet_id) REFERENCES pets(id),
  CONSTRAINT chk_bookings_amount CHECK (amount_cents = 9900)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE payment_orders (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_no VARCHAR(64) NOT NULL,
  booking_id BIGINT UNSIGNED NOT NULL,
  provider VARCHAR(32) NOT NULL,
  channel_trade_no VARCHAR(128) NULL,
  amount_cents BIGINT NOT NULL,
  status VARCHAR(32) NOT NULL,
  failure_code VARCHAR(64) NULL,
  paid_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_payment_request (request_no),
  UNIQUE KEY uk_payment_channel_trade (provider, channel_trade_no),
  KEY idx_payment_booking (booking_id),
  CONSTRAINT fk_payment_booking FOREIGN KEY (booking_id) REFERENCES bookings(id),
  CONSTRAINT chk_payment_amount CHECK (amount_cents >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE payment_notifications (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  provider VARCHAR(32) NOT NULL,
  notification_no VARCHAR(128) NOT NULL,
  request_no VARCHAR(64) NOT NULL,
  payload_hash CHAR(64) NOT NULL,
  received_at DATETIME(6) NOT NULL,
  result VARCHAR(32) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_payment_notification (provider, notification_no),
  KEY idx_payment_notification_request (request_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE cancellation_requests (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  booking_id BIGINT UNSIGNED NOT NULL,
  requester_id BIGINT UNSIGNED NOT NULL,
  reason VARCHAR(500) NOT NULL,
  status VARCHAR(32) NOT NULL,
  pending_guard TINYINT GENERATED ALWAYS AS (
    CASE WHEN status = 'pending' THEN 1 ELSE NULL END
  ) STORED,
  requested_at DATETIME(6) NOT NULL,
  reviewed_by BIGINT UNSIGNED NULL,
  reviewed_at DATETIME(6) NULL,
  review_reason VARCHAR(500) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_cancel_booking_status (booking_id, status),
  UNIQUE KEY uk_cancel_single_pending (booking_id, pending_guard),
  CONSTRAINT fk_cancel_booking FOREIGN KEY (booking_id) REFERENCES bookings(id),
  CONSTRAINT fk_cancel_requester FOREIGN KEY (requester_id) REFERENCES users(id),
  CONSTRAINT fk_cancel_reviewer FOREIGN KEY (reviewed_by) REFERENCES users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE refund_orders (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  refund_no VARCHAR(64) NOT NULL,
  booking_id BIGINT UNSIGNED NOT NULL,
  payment_order_id BIGINT UNSIGNED NOT NULL,
  amount_cents BIGINT NOT NULL,
  status VARCHAR(32) NOT NULL,
  failure_code VARCHAR(64) NULL,
  refunded_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_refund_no (refund_no),
  KEY idx_refund_booking (booking_id),
  KEY idx_refund_status (status),
  CONSTRAINT fk_refund_booking FOREIGN KEY (booking_id) REFERENCES bookings(id),
  CONSTRAINT fk_refund_payment FOREIGN KEY (payment_order_id) REFERENCES payment_orders(id),
  CONSTRAINT chk_refund_amount CHECK (amount_cents >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE refund_attempts (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  refund_order_id BIGINT UNSIGNED NOT NULL,
  request_no VARCHAR(64) NOT NULL,
  sequence_no INT NOT NULL,
  status VARCHAR(32) NOT NULL,
  failure_code VARCHAR(64) NULL,
  requested_at DATETIME(6) NOT NULL,
  completed_at DATETIME(6) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_refund_attempt_request (request_no),
  UNIQUE KEY uk_refund_attempt_sequence (refund_order_id, sequence_no),
  CONSTRAINT fk_refund_attempt_order FOREIGN KEY (refund_order_id) REFERENCES refund_orders(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE fulfillment_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  booking_id BIGINT UNSIGNED NOT NULL,
  event_type VARCHAR(32) NOT NULL,
  operator_id BIGINT UNSIGNED NOT NULL,
  occurred_at DATETIME(6) NOT NULL,
  idempotency_key VARCHAR(64) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_fulfillment_idempotency (idempotency_key),
  UNIQUE KEY uk_fulfillment_booking_type (booking_id, event_type),
  CONSTRAINT fk_fulfillment_booking FOREIGN KEY (booking_id) REFERENCES bookings(id),
  CONSTRAINT fk_fulfillment_operator FOREIGN KEY (operator_id) REFERENCES users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE no_show_records (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  booking_id BIGINT UNSIGNED NOT NULL,
  marked_by BIGINT UNSIGNED NOT NULL,
  reason VARCHAR(500) NOT NULL,
  marked_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_no_show_booking (booking_id),
  CONSTRAINT fk_no_show_booking FOREIGN KEY (booking_id) REFERENCES bookings(id),
  CONSTRAINT fk_no_show_marker FOREIGN KEY (marked_by) REFERENCES users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE audit_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  actor_id BIGINT UNSIGNED NULL,
  action VARCHAR(64) NOT NULL,
  resource_type VARCHAR(64) NOT NULL,
  resource_id VARCHAR(64) NOT NULL,
  result VARCHAR(32) NOT NULL,
  detail_json JSON NOT NULL,
  occurred_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_audit_resource (resource_type, resource_id, occurred_at),
  KEY idx_audit_actor (actor_id, occurred_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

