-- =============================================
-- 阶段 2：好友与通讯录（幂等）
-- =============================================

CREATE TABLE IF NOT EXISTS `friend_relation` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `user_id` BIGINT NOT NULL,
  `friend_id` BIGINT NOT NULL,
  `remark` VARCHAR(64) DEFAULT '',
  `source` TINYINT DEFAULT 1 COMMENT '1 搜索添加 2 通讯录添加',
  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user_friend` (`user_id`, `friend_id`),
  KEY `idx_friend` (`friend_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='好友关系';

CREATE TABLE IF NOT EXISTS `friend_request` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `from_user` BIGINT NOT NULL,
  `to_user` BIGINT NOT NULL,
  `message` VARCHAR(255) DEFAULT '',
  `status` TINYINT DEFAULT 0 COMMENT '0 待处理 1 同意 2 拒绝',
  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_to` (`to_user`, `status`),
  KEY `idx_from` (`from_user`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='好友申请';

CREATE TABLE IF NOT EXISTS `blacklist` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `user_id` BIGINT NOT NULL,
  `block_user_id` BIGINT NOT NULL,
  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user_block` (`user_id`, `block_user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='黑名单';
