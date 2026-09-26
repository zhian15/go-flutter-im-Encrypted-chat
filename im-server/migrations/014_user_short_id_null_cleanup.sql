-- 014_user_short_id_null_cleanup.sql
-- 修复：model.User.ShortID 改为 *string 后，新用户会写 NULL；
-- 历史上由于 GORM zero-string 默认写 ''，UNIQUE 索引 user.idx_user_short_id 会把多个 '' 视为重复。
-- 本迁移将 user.short_id = '' 全部置为 NULL（UNIQUE 对多 NULL 不冲突），与 008 定义的 "VARCHAR(32) NULL" 对齐。
SET @exist := (SELECT COUNT(*) FROM information_schema.COLUMNS
               WHERE TABLE_SCHEMA = DATABASE()
                 AND TABLE_NAME   = 'user'
                 AND COLUMN_NAME  = 'short_id');
SET @sql := IF(@exist > 0,
  'UPDATE `user` SET short_id = NULL WHERE short_id = ''''',
  'SELECT ''short_id column not exists, skip''');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
