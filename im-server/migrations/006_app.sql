-- =============================================
-- 阶段 5：小程序管理（H5 容器）（幂等）
-- =============================================

CREATE TABLE IF NOT EXISTS `app_entry` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `name_zh` VARCHAR(64) DEFAULT '',
  `name_en` VARCHAR(64) DEFAULT '',
  `icon` VARCHAR(512) DEFAULT '',
  `url` VARCHAR(1024) DEFAULT '',
  `category` VARCHAR(32) DEFAULT '',
  `sort` INT DEFAULT 0,
  `enabled` TINYINT DEFAULT 1 COMMENT '1 上架 0 下架',
  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_enabled` (`enabled`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='小程序（H5 容器）';

-- 示例数据（测试用，后台可管理）
INSERT INTO `app_entry` (`name_zh`, `name_en`, `url`, `category`, `sort`, `enabled`) VALUES
  ('企业官网', 'Official Site', 'https://www.qq.com', '办公', 1, 1),
  ('帮助中心', 'Help Center', 'https://support.qq.com', '工具', 2, 1)
ON DUPLICATE KEY UPDATE `name_zh`=`name_zh`;
