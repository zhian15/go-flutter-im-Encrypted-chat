-- =============================================
-- 阶段 5（提前）：管理后台（幂等）
-- =============================================

CREATE TABLE IF NOT EXISTS `admin_log` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `admin_id` BIGINT DEFAULT 0,
  `action` VARCHAR(64) DEFAULT '',
  `target` VARCHAR(255) DEFAULT '',
  `detail` JSON NULL,
  `ip` VARCHAR(64) DEFAULT '',
  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_admin` (`admin_id`),
  KEY `idx_created` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='后台操作日志';
