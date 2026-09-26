-- 021: 频道自定义唯一 ID short_id（类公众号微信号：3-20 位字母/数字/下划线，
--       全表唯一，凭 ID 可搜索/打开频道，详见 doc/API.md 频道节）。
-- Conversation 其实在 AutoMigrate 白名单内（internal/store/store.go），本文件是幂等兜底
-- （对齐 008_short_id.sql 手法）：全新库由 AutoMigrate 按 model tag 建列建索引，这里全部跳过；
-- 老库 / 白名单将来被移除的场景由这里补齐，保证两条路径都拿到列与唯一索引。
SET @has_short_id := (SELECT COUNT(*) FROM information_schema.COLUMNS
                      WHERE TABLE_SCHEMA = DATABASE()
                        AND TABLE_NAME   = 'conversation'
                        AND COLUMN_NAME  = 'short_id');
SET @sql := IF(@has_short_id = 0,
  'ALTER TABLE `conversation` ADD COLUMN `short_id` VARCHAR(32) NULL',
  'SELECT ''short_id column exists, skip''');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- 唯一索引：列存在且索引不存在才建（AutoMigrate 已按 tag uk_conv_short_id 建出则跳过）。
-- 新列存量行全为 NULL，MySQL 唯一索引允许多个 NULL，不会冲突。
SET @has_idx := (SELECT COUNT(*) FROM information_schema.STATISTICS
                 WHERE TABLE_SCHEMA = DATABASE()
                   AND TABLE_NAME   = 'conversation'
                   AND INDEX_NAME   = 'uk_conv_short_id');
SET @sql := IF(@has_short_id > 0 AND @has_idx = 0,
  'CREATE UNIQUE INDEX `uk_conv_short_id` ON `conversation` (`short_id`)',
  'SELECT ''uk_conv_short_id exists or column missing, skip''');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
