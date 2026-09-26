-- 010 资金包 / 冻结金额（B-22 到期退回 + 防刷钱；B-23 对账）
--
-- 背景：旧模型「发红包即扣款、领取即凭空入账」不守恒 —— 钱能凭空产生也能凭空蒸发，
--       所以没人领就能无限发（刷钱），且 Σ(流水) 与 Σ(余额) 数学上永远对不上。
-- 新模型：balance → frozen → 收款人 balance，任意时刻钱都有唯一归宿。
--
-- 幂等：列/表已存在则跳过，可重复执行。

-- 1) user 增加冻结金额
SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'user' AND COLUMN_NAME = 'frozen'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `user` ADD COLUMN `frozen` DECIMAL(12,2) NOT NULL DEFAULT 0.00 COMMENT ''冻结金额(发出的红包/转账未领取部分)'' AFTER `balance`',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- 2) wallet_transaction 增加冻结变动 / 变动后冻结余额
SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wallet_transaction' AND COLUMN_NAME = 'frozen_delta'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `wallet_transaction` ADD COLUMN `frozen_delta` DECIMAL(12,2) NOT NULL DEFAULT 0.00 COMMENT ''冻结金额变动(正=冻结 负=解冻)'' AFTER `amount`',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wallet_transaction' AND COLUMN_NAME = 'frozen'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `wallet_transaction` ADD COLUMN `frozen` DECIMAL(12,2) NOT NULL DEFAULT 0.00 COMMENT ''变动后冻结金额'' AFTER `balance`',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- 3) 资金包表：一个红包/转账一行，msg_id 唯一（配合发消息前的幂等判断，防止重复冻结）
CREATE TABLE IF NOT EXISTS `money_packet` (
  `id`           BIGINT       NOT NULL AUTO_INCREMENT,
  `msg_id`       BIGINT       NOT NULL COMMENT '关联消息ID(红包8/转账9)',
  `sender_id`    BIGINT       NOT NULL COMMENT '发送者',
  `kind`         INT          NOT NULL DEFAULT 0 COMMENT '8红包 9转账',
  `total`        DECIMAL(12,2) NOT NULL DEFAULT 0.00 COMMENT '总金额',
  `count`        INT          NOT NULL DEFAULT 1 COMMENT '红包个数(转账恒为1)',
  `claimed`      DECIMAL(12,2) NOT NULL DEFAULT 0.00 COMMENT '已领金额',
  `claimed_cnt`  INT          NOT NULL DEFAULT 0 COMMENT '已领人数',
  `status`       TINYINT      NOT NULL DEFAULT 1 COMMENT '1进行中 2已领完 3已过期退回 4已关闭',
  `expire_at`    DATETIME(3)  NULL COMMENT '过期时间(创建+24h)',
  `created_at`   DATETIME(3)  NULL,
  `updated_at`   DATETIME(3)  NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_msg` (`msg_id`),
  KEY `idx_sender` (`sender_id`),
  KEY `idx_status` (`status`),
  KEY `idx_expire` (`expire_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='红包/转账资金包';

-- 4) 兼容：早期由 AutoMigrate 建出的表可能缺唯一索引，补上（已存在会报错，用 IF 判断）
SET @idx_exists := (
  SELECT COUNT(*) FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'money_packet' AND INDEX_NAME = 'uk_msg'
);
SET @ddl := IF(@idx_exists = 0,
  'ALTER TABLE `money_packet` ADD UNIQUE KEY `uk_msg` (`msg_id`)',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
