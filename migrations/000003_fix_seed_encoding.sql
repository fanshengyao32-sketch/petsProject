-- Repair development databases initialized before 000002_seed.sql declared
-- its connection charset. This migration is idempotent and harmless on a
-- fresh database where the values are already correct.
SET NAMES utf8mb4 COLLATE utf8mb4_0900_ai_ci;

UPDATE stores SET name = '茸茸洗护门店', updated_at = UTC_TIMESTAMP(6) WHERE id = 1;
UPDATE stores SET name = '外部测试门店', updated_at = UTC_TIMESTAMP(6) WHERE id = 2;

UPDATE service_items SET name = '基础洗护', updated_at = UTC_TIMESTAMP(6) WHERE id = 1;

UPDATE users SET display_name = '林小满', updated_at = UTC_TIMESTAMP(6) WHERE id = 1;
UPDATE users SET display_name = '许岚', updated_at = UTC_TIMESTAMP(6) WHERE id = 2;
UPDATE users SET display_name = '周宁', updated_at = UTC_TIMESTAMP(6) WHERE id = 3;
UPDATE users SET display_name = '程安', updated_at = UTC_TIMESTAMP(6) WHERE id = 4;
UPDATE users SET display_name = '顾言', updated_at = UTC_TIMESTAMP(6) WHERE id = 5;
UPDATE users SET display_name = '外店前台', updated_at = UTC_TIMESTAMP(6) WHERE id = 6;
UPDATE users SET display_name = '赵晨', updated_at = UTC_TIMESTAMP(6) WHERE id = 7;
UPDATE users SET display_name = '王琪', updated_at = UTC_TIMESTAMP(6) WHERE id = 8;
UPDATE users SET display_name = '陈雨', updated_at = UTC_TIMESTAMP(6) WHERE id = 9;

UPDATE pets SET name = '团子', breed = '比熊', updated_at = UTC_TIMESTAMP(6) WHERE id = 1;
UPDATE pets SET name = '豆包', breed = '柯基', updated_at = UTC_TIMESTAMP(6) WHERE id = 2;
UPDATE pets SET name = '七喜', breed = '金毛', updated_at = UTC_TIMESTAMP(6) WHERE id = 3;
UPDATE pets SET name = '栗子', breed = '泰迪', updated_at = UTC_TIMESTAMP(6) WHERE id = 4;
