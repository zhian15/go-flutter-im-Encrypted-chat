-- =============================================
-- 企业 IM 初始化表结构（幂等：IF NOT EXISTS）
-- MySQL 8.x, utf8mb4
-- =============================================

CREATE TABLE IF NOT EXISTS `user` (
  `id` BIGINT NOT NULL,
  `account` VARCHAR(64) NOT NULL COMMENT '登录账号（手机号或邮箱）',
  `password_hash` VARCHAR(255) NOT NULL,
  `nickname` VARCHAR(64) DEFAULT '',
  `avatar` VARCHAR(512) DEFAULT '',
  `phone` VARCHAR(32) DEFAULT '',
  `email` VARCHAR(128) DEFAULT '',
  `country_code` VARCHAR(8) DEFAULT '+86',
  `department_id` BIGINT DEFAULT 0,
  `status` TINYINT DEFAULT 1 COMMENT '1正常 2禁用',
  `role` TINYINT DEFAULT 1 COMMENT '1普通 2管理员',
  `last_login_at` DATETIME NULL,
  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_account` (`account`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户';

CREATE TABLE IF NOT EXISTS `department` (
  `id` BIGINT NOT NULL,
  `name_zh` VARCHAR(64) NOT NULL,
  `name_en` VARCHAR(64) NOT NULL,
  `parent_id` BIGINT DEFAULT 0,
  `sort` INT DEFAULT 0,
  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_parent` (`parent_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='部门（双语）';

CREATE TABLE IF NOT EXISTS `sys_config` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `config_key` VARCHAR(64) NOT NULL,
  `config_value` JSON NULL,
  `updated_at` DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_key` (`config_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='系统配置KV';

-- 默认配置
INSERT INTO `sys_config` (`config_key`, `config_value`) VALUES
  ('register_enabled', '{"value": true}'),
  ('auth_mode', '{"value": "none"}'),
  ('invite_code_enabled', '{"value": false}'),
  ('e2e_enabled', '{"value": false}'),
  ('announcement', '{"zh": "", "en": ""}')
ON DUPLICATE KEY UPDATE `config_key`=`config_key`;

-- 默认部门（双语）
INSERT INTO `department` (`id`, `name_zh`, `name_en`, `parent_id`, `sort`) VALUES
  (1, '产品部', 'Product', 0, 1),
  (2, '研发部', 'Engineering', 0, 2),
  (3, '市场部', 'Marketing', 0, 3),
  (4, '行政部', 'Administration', 0, 4),
  (5, '其他', 'Others', 0, 5)
ON DUPLICATE KEY UPDATE `name_zh`=`name_zh`;
