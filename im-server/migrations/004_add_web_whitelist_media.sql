-- 004 web_whitelist 增加 logo / cover 列（白名单自定义封面与小程序图标）
--
-- 背景：白名单站点希望后台能自定义链接卡片的封面大图与小程序图标，
--       而不是只能依赖抓取到的 og:image / 站点 favicon。
--       命中白名单时，若配置了 cover/logo，/api/v1/link/meta 会用它们覆盖
--       image/icon；未配置则维持原有 og:image / favicon 回退逻辑（前后向兼容）。
--
-- 为什么走迁移而不是 AutoMigrate：internal/store/store.go 的 AutoMigrate 白名单不含
--       WebWhitelist，新列必须由 store.MigrateMySQL 顺序执行本文件补齐（记录在
--       schema_migrations，重复启动不会重复执行）。
--
-- 为什么不动 003_add_web_whitelist.sql：003 已在线上执行过（记录在 schema_migrations），
--       改它不会重新执行，必须新增本文件。
--
-- 幂等：先查 information_schema 判断列是否存在，不存在才 ALTER，可重复执行。

-- logo：小程序图标（覆盖 /link/meta 的 icon，原为站点 favicon）
SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'web_whitelist'
    AND COLUMN_NAME = 'logo'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `web_whitelist` ADD COLUMN `logo` VARCHAR(512) NOT NULL DEFAULT '''' COMMENT ''小程序图标URL，非空时覆盖 link/meta 的 icon'' AFTER `native_bridge`',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- cover：封面大图（覆盖 /link/meta 的 image，原为 og:image）
SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'web_whitelist'
    AND COLUMN_NAME = 'cover'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `web_whitelist` ADD COLUMN `cover` VARCHAR(512) NOT NULL DEFAULT '''' COMMENT ''封面大图URL，非空时覆盖 link/meta 的 image'' AFTER `logo`',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
