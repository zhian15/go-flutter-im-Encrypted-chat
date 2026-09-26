-- 027_group_show_members.sql
-- 群级开关「显示群成员与在线人数」（2026-09-22 需求5）：
--   conversation.show_members TINYINT 1=显示（默认，保持历史行为）
--   0=关闭（群聊标题下方不显示「N 成员, M 在线」，聊天信息页不显示群成员卡片）
--
-- 为什么走迁移而不是 AutoMigrate：model.Conversation 虽在 internal/store/store.go
--       的 AutoMigrate 白名单内（加列自动），但按全仓约定仍手写迁移兜底老库
--       （对齐 021_channel_short_id.sql / 024_clear_history.sql 先例），
--       由 store.MigrateMySQL 顺序执行（记录在 schema_migrations，不会重复执行）。
--
-- 幂等：先查 information_schema 判断列是否存在，不存在才 ALTER，可重复执行。

SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'conversation'
    AND COLUMN_NAME = 'show_members'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `conversation` ADD COLUMN `show_members` TINYINT NOT NULL DEFAULT 1 COMMENT ''显示群成员人数/在线人数：1 显示（默认） 0 关闭'' AFTER `qr_join_enabled`',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
