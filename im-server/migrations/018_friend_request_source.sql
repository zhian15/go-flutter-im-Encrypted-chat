-- 018 friend_request 增加 source 列（好友申请来源：1 搜索添加 2 扫码添加 3 名片）
--
-- 背景：好友申请原来不记录来源渠道，客户端名片加好友、扫码加好友无法在申请列表中区分展示。
--       跨端契约（与 Flutter 同期实现，不可变）：source 为 int 可选项，默认 1；
--       FriendRequestAdd 会对 1/2/3 之外的值净化归 1。
--
-- 为什么走迁移而不是 AutoMigrate：internal/store/store.go 的 AutoMigrate 白名单不含
--       FriendRequest，新列必须由 store.MigrateMySQL 顺序执行本文件补齐（记录在
--       schema_migrations，重复启动不会重复执行）。
--
-- 幂等：先查 information_schema 判断列是否存在，不存在才 ALTER，可重复执行。

SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'friend_request'
    AND COLUMN_NAME = 'source'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `friend_request` ADD COLUMN `source` INT NOT NULL DEFAULT 1 COMMENT ''来源 1搜索 2扫码 3名片'' AFTER `status`',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
