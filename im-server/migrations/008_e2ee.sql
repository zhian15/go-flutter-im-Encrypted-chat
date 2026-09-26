-- 008 user_keys 表增加 e2ee_on 列（E2EE 用户级开关，2026-09-18 定稿 §36）
--
-- 背景：真端到端加密（X25519 身份密钥 + 每单聊 AES-256-GCM 会话密钥）落地。
--       后台「加密方式」三选（off/server/e2ee，sys_config 键 e2ee_mode）决定总开关；
--       本列是**用户级**开关：端到端模式下用户可自行关闭，默认开启（拍板③）。
--       身份密钥对与开关解耦：keys 行存在即有公钥，开关只影响会话是否走端到端。
--
-- 为什么走迁移而不是 AutoMigrate：internal/store/store.go 的 AutoMigrate 白名单
--       **不含 model.UserKey**（预留表），加列必须由 store.MigrateMySQL 顺序执行本文件。
--
-- 幂等：先查 information_schema 判断列是否存在（与 019_device_meta.sql 同一手法）。

SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'user_keys'
    AND COLUMN_NAME = 'e2ee_on'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `user_keys` ADD COLUMN `e2ee_on` TINYINT NOT NULL DEFAULT 1 COMMENT ''用户级 E2EE 开关：1 开（默认） / 0 关'' AFTER `key_version`',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
