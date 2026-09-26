-- 008: 用户靓号 ShortID（可通过 ID 添加好友，后台可预留）
-- 幂等修复：GORM AutoMigrate（store.go）启动时已按 model.User 建出 short_id 列，
-- 全新数据库自带该列，这里只能兜底老库，否则全新部署必然报 Error 1060 Duplicate column。
SET @has_short_id := (SELECT COUNT(*) FROM information_schema.COLUMNS
                      WHERE TABLE_SCHEMA = DATABASE()
                        AND TABLE_NAME   = 'user'
                        AND COLUMN_NAME  = 'short_id');
SET @sql := IF(@has_short_id = 0,
  'ALTER TABLE `user` ADD COLUMN `short_id` VARCHAR(32) NULL AFTER `country_code`',
  'SELECT ''short_id column exists, skip''');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- 唯一索引：GORM 生成的叫 idx_user_short_id，这里显式建 uk_user_short_id（不冲突）
CREATE UNIQUE INDEX `uk_user_short_id` ON `user` (`short_id`);
