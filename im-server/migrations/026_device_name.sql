-- 026 device 表增加 device_name 列（客户端上报的设备型号）
--
-- 背景：设备页「当前设备/活跃会话」、PC 聊天顶栏「对方在线设备」、后台用户详情
--       都要显示真实设备型号（如 "Xiaomi 2201123G" / "iPhone 15 Pro" / "手机网页"）。
--       客户端在 登录/注册/游客/扫码登录 时随 issueTokens 上报 deviceName 落库。
--
-- 为什么走迁移而不是 AutoMigrate：internal/store/store.go 的 AutoMigrate 白名单
--       不含 model.Device（见 019_device_meta.sql 同款说明），Device 的加列必须
--       由 store.MigrateMySQL 顺序执行本文件补齐（记录在 schema_migrations，不会重复执行）。
--
-- 幂等：先查 information_schema 判断列是否存在，不存在才 ALTER，可重复执行。

SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'device'
    AND COLUMN_NAME = 'device_name'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `device` ADD COLUMN `device_name` VARCHAR(128) NOT NULL DEFAULT '''' COMMENT ''客户端上报的设备型号'' AFTER `last_ip`',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
