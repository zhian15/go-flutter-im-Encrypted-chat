-- 009 多条置顶消息（JSON 数组，兼容旧单条字段）——幂等：列已存在则跳过
SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation' AND COLUMN_NAME = 'pinned_msg_ids'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `conversation` ADD COLUMN `pinned_msg_ids` TEXT NULL COMMENT ''多条置顶消息ID(JSON数组)'' AFTER `pinned_msg_content`',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
