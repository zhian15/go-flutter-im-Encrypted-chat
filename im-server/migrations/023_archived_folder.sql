-- 023: 归档 + 会话文件夹（PC 端需求：归档/文件夹从纯本地 localStorage 升级为服务端同步）
--
-- A. conversation_member 加 archived 列（个人归档开关，PUT /conversation/:id/archive）。
--    ConversationMember 在 AutoMigrate 白名单内（internal/store/store.go），全新库由
--    AutoMigrate 按 model tag 建列，这里幂等兜底老库（021_channel_short_id.sql 同款手法）。
-- B. conversation_folder 表：用户自定义文件夹（名称/颜色/包含的会话 ID JSON）。
--    模型不在 AutoMigrate 白名单（对齐 020_report.sql 先例），建表必须走迁移，
--    由 store.MigrateMySQL 顺序执行（记录在 schema_migrations，不会重复执行）。

-- ---- A. conversation_member.archived ----
SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'conversation_member'
    AND COLUMN_NAME = 'archived'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `conversation_member` ADD COLUMN `archived` INT NOT NULL DEFAULT 0 COMMENT ''个人归档开关 1=已归档''',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- ---- B. conversation_folder ----
CREATE TABLE IF NOT EXISTS `conversation_folder` (
  `id`         BIGINT       NOT NULL AUTO_INCREMENT,
  `user_id`    BIGINT       NOT NULL COMMENT '所属用户 user.id',
  `name`       VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '文件夹名称',
  `color`      VARCHAR(16)  NOT NULL DEFAULT '' COMMENT '图标颜色（#RRGGBB）',
  `chat_ids`   TEXT         COMMENT '包含的会话 ID JSON 数组（字符串化雪花 ID）',
  `sort`       INT          NOT NULL DEFAULT 0 COMMENT '预留排序位',
  `created_at` DATETIME     NULL,
  `updated_at` DATETIME     NULL,
  PRIMARY KEY (`id`),
  KEY `idx_folder_user` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='会话文件夹（个人视图分组）';
