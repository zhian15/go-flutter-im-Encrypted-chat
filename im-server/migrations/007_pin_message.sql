-- 007 群置顶消息（幂等：列已存在则跳过）
-- 注意：MySQL 没有 ADD COLUMN IF NOT EXISTS（那是 MariaDB 方言，MySQL 直接报
-- 1064 语法错误，每次重启迁移都卡死在这里），因此用 information_schema 判断 +
-- PREPARE/EXECUTE 动态 SQL 实现幂等（同 009/012 惯例）。
SET @col_id_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation' AND COLUMN_NAME = 'pinned_msg_id'
);
SET @ddl := IF(@col_id_exists = 0,
  'ALTER TABLE `conversation` ADD COLUMN `pinned_msg_id` BIGINT DEFAULT 0 COMMENT ''置顶消息ID''',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_content_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation' AND COLUMN_NAME = 'pinned_msg_content'
);
SET @ddl := IF(@col_content_exists = 0,
  'ALTER TABLE `conversation` ADD COLUMN `pinned_msg_content` VARCHAR(512) DEFAULT '''' COMMENT ''置顶消息内容快照''',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;


