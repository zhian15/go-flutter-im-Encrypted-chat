-- =============================================
-- 阶段 3：会话与群组（幂等）
-- =============================================

CREATE TABLE IF NOT EXISTS `conversation` (
  `id` BIGINT NOT NULL,
  `type` TINYINT DEFAULT 1 COMMENT '1 单聊 2 群聊',
  `name_zh` VARCHAR(64) DEFAULT '',
  `name_en` VARCHAR(64) DEFAULT '',
  `avatar` VARCHAR(512) DEFAULT '',
  `owner_id` BIGINT DEFAULT 0,
  `announcement_zh` TEXT,
  `announcement_en` TEXT,
  `max_members` INT DEFAULT 500,
  `status` TINYINT DEFAULT 1 COMMENT '1 正常 2 已解散',
  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_owner` (`owner_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='会话（单聊/群聊）';

CREATE TABLE IF NOT EXISTS `conversation_member` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `conversation_id` BIGINT NOT NULL,
  `user_id` BIGINT NOT NULL,
  `role` TINYINT DEFAULT 3 COMMENT '1 群主 2 管理员 3 普通',
  `nickname` VARCHAR(64) DEFAULT '',
  `mute` TINYINT DEFAULT 0 COMMENT '0 正常 1 免打扰',
  `pinned` TINYINT DEFAULT 0 COMMENT '0 否 1 置顶',
  `last_read_msg_id` BIGINT DEFAULT 0,
  `joined_at` DATETIME DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_conv_user` (`conversation_id`, `user_id`),
  KEY `idx_user` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='会话成员';
