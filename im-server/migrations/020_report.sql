-- 020 新建 report 投诉表（会话设置「投诉」入口上报 + 管理后台处理）
--
-- 为什么走迁移而不是 AutoMigrate：internal/store/store.go 的 AutoMigrate 白名单不含
--       model.Report，建表必须由 store.MigrateMySQL 顺序执行本文件补齐（记录在
--       schema_migrations，重复启动不会重复执行）。
--
-- 幂等：CREATE TABLE IF NOT EXISTS，可重复执行。

CREATE TABLE IF NOT EXISTS `report` (
  `id`          BIGINT       NOT NULL COMMENT '雪花 ID（应用层生成）',
  `reporter_id` BIGINT       NOT NULL COMMENT '举报人 user.id',
  `peer_id`     BIGINT       DEFAULT 0 COMMENT '被投诉人 user.id（0=未指定，如群聊场景）',
  `conv_id`     BIGINT       DEFAULT 0 COMMENT '关联会话 conversation.id',
  `category`    VARCHAR(64)  DEFAULT '' COMMENT '投诉类型（客户端 l10n key：convSetReportSpam/Fraud/Harass/Impersonate/Other）',
  `note`        TEXT         COMMENT '补充说明（可空）',
  `status`      TINYINT      DEFAULT 0 COMMENT '0 待处理 1 已处理',
  `handled_by`  BIGINT       DEFAULT 0 COMMENT '处理人 admin user.id',
  `handled_at`  DATETIME     NULL COMMENT '处理时间',
  `created_at`  DATETIME     DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_reporter` (`reporter_id`),
  KEY `idx_conv` (`conv_id`),
  KEY `idx_status_created` (`status`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='投诉';
