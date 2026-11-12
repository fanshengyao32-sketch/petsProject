-- The mysql CLI may default to latin1 even when the database is utf8mb4.
-- Declare the client/connection charset before importing Chinese seed data.
SET NAMES utf8mb4 COLLATE utf8mb4_0900_ai_ci;

INSERT INTO stores (id, code, name, timezone, created_at, updated_at) VALUES
  (1, 'ST001', '茸茸洗护门店', 'Asia/Shanghai', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (2, 'ST999', '外部测试门店', 'Asia/Shanghai', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6));

INSERT INTO service_items (id, code, name, price_cents, created_at, updated_at) VALUES
  (1, 'BASIC_WASH', '基础洗护', 9900, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6));

INSERT INTO users (id, external_id, display_name, role, store_id, created_at, updated_at) VALUES
  (1, 'owner-lin', '林小满', 'owner', NULL, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (2, 'manager-xu', '许岚', 'manager', 1, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (3, 'front-zhou', '周宁', 'front_desk', 1, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (4, 'service-cheng', '程安', 'service_staff', 1, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (5, 'support-gu', '顾言', 'support', NULL, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (6, 'front-other', '外店前台', 'front_desk', 2, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (7, 'owner-zhao', '赵晨', 'owner', NULL, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (8, 'owner-wang', '王琪', 'owner', NULL, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (9, 'owner-chen', '陈雨', 'owner', NULL, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6));

INSERT INTO pets (id, owner_id, name, breed, created_at, updated_at) VALUES
  (1, 1, '团子', '比熊', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (2, 7, '豆包', '柯基', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (3, 8, '七喜', '金毛', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (4, 9, '栗子', '泰迪', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6));

-- UTC values correspond to 2026-11-15 afternoon in Asia/Shanghai.
INSERT INTO service_slots (id, store_id, service_item_id, starts_at, ends_at, capacity, version, created_by, created_at, updated_at) VALUES
  (1, 1, 1, '2026-11-15 05:00:00', '2026-11-15 06:00:00', 3, 0, 2, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (2, 1, 1, '2026-11-15 06:30:00', '2026-11-15 07:30:00', 3, 0, 2, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)),
  (3, 1, 1, '2026-11-15 08:00:00', '2026-11-15 09:00:00', 3, 0, 2, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6));

