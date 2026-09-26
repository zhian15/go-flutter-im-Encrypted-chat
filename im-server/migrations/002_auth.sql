-- =============================================
-- 阶段 1：认证与用户（幂等）
-- =============================================

CREATE TABLE IF NOT EXISTS `device` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `user_id` BIGINT NOT NULL,
  `device_type` TINYINT DEFAULT 1 COMMENT '1 Android 2 iOS 3 Web 4 Windows 5 macOS',
  `device_id` VARCHAR(128) NOT NULL,
  `push_token` VARCHAR(512) DEFAULT '',
  `status` TINYINT DEFAULT 1 COMMENT '1 在线 0 离线',
  `last_active_at` DATETIME NULL,
  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user_device` (`user_id`, `device_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='设备';

CREATE TABLE IF NOT EXISTS `login_log` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `user_id` BIGINT DEFAULT 0,
  `ip` VARCHAR(64) DEFAULT '',
  `device` VARCHAR(255) DEFAULT '',
  `result` TINYINT DEFAULT 1 COMMENT '1 成功 0 失败',
  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_user` (`user_id`),
  KEY `idx_created` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='登录日志';

CREATE TABLE IF NOT EXISTS `invite_code` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `code` VARCHAR(32) NOT NULL,
  `batch` VARCHAR(64) DEFAULT '',
  `used_by` BIGINT DEFAULT NULL,
  `used_at` DATETIME DEFAULT NULL,
  `expires_at` DATETIME DEFAULT NULL,
  `enabled` TINYINT DEFAULT 1,
  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_code` (`code`),
  KEY `idx_used` (`used_by`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='邀请码';

CREATE TABLE IF NOT EXISTS `user_keys` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `user_id` BIGINT NOT NULL,
  `public_key` TEXT,
  `encrypted_private_key` TEXT,
  `key_version` INT DEFAULT 1,
  `updated_at` DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='E2E 用户密钥（预留）';
