-- 015_recharge_withdraw.sql
-- 充值/提现系统：用户侧充值（提交订单+支付凭证 → 后台审核入账）、提现（绑定方式 → 申请冻结 → 后台审核打款）。

-- 1) 充值订单
CREATE TABLE IF NOT EXISTS recharge_order (
    id            BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id       BIGINT       NOT NULL,
    amount        DECIMAL(12,2) NOT NULL COMMENT '申请充值金额',
    pay_method    TINYINT      NOT NULL DEFAULT 1 COMMENT '1微信 2支付宝 3银行卡',
    receive_qrcode_url VARCHAR(512) NOT NULL DEFAULT '' COMMENT '后台收款码在下单时的快照',
    proof_image   VARCHAR(512) NOT NULL DEFAULT '' COMMENT '用户上传的支付凭证截图',
    pay_tx_no     VARCHAR(128) NOT NULL DEFAULT '' COMMENT '用户填写的交易单号（可选）',
    status        TINYINT      NOT NULL DEFAULT 1 COMMENT '1待审核 2已通过 3已拒绝',
    reject_reason VARCHAR(255) NOT NULL DEFAULT '',
    reviewer_id   BIGINT       NOT NULL DEFAULT 0,
    reviewed_at   DATETIME     NULL,
    remark        VARCHAR(255) NOT NULL DEFAULT '',
    created_at    DATETIME     NOT NULL,
    INDEX idx_user (user_id),
    INDEX idx_status (status),
    INDEX idx_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 2) 用户提现绑定信息（每个用户最多每种类型一条）
CREATE TABLE IF NOT EXISTS withdraw_account (
    id            BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id       BIGINT       NOT NULL UNIQUE COMMENT '一个用户只保留一条最新绑定',
    -- 公共
    account_type  TINYINT      NOT NULL DEFAULT 1 COMMENT '1微信 2支付宝 3银行卡',
    -- 微信
    wechat_qrcode_url VARCHAR(512) NOT NULL DEFAULT '',
    wechat_name        VARCHAR(64)  NOT NULL DEFAULT '',
    -- 支付宝
    alipay_qrcode_url  VARCHAR(512) NOT NULL DEFAULT '',
    alipay_account     VARCHAR(128) NOT NULL DEFAULT '',
    alipay_name        VARCHAR(64)  NOT NULL DEFAULT '',
    -- 银行卡
    bank_card_no       VARCHAR(64)  NOT NULL DEFAULT '',
    bank_name          VARCHAR(128) NOT NULL DEFAULT '',
    bank_account_name  VARCHAR(64)  NOT NULL DEFAULT '',
    updated_at    DATETIME NOT NULL,
    created_at    DATETIME NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 3) 提现订单
CREATE TABLE IF NOT EXISTS withdraw_order (
    id              BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id         BIGINT       NOT NULL,
    amount          DECIMAL(12,2) NOT NULL COMMENT '申请金额',
    fee             DECIMAL(12,2) NOT NULL DEFAULT 0 COMMENT '手续费',
    actual_amount   DECIMAL(12,2) NOT NULL COMMENT '实际到账 = amount - fee',
    withdraw_type   TINYINT      NOT NULL DEFAULT 1 COMMENT '1微信 2支付宝 3银行卡',
    account_snapshot JSON         NOT NULL COMMENT '下单时的提现账户信息快照（防止用户事后改绑定）',
    status          TINYINT      NOT NULL DEFAULT 1 COMMENT '1待审核 2已通过 3已拒绝',
    reject_reason   VARCHAR(255) NOT NULL DEFAULT '',
    reviewer_id     BIGINT       NOT NULL DEFAULT 0,
    reviewed_at     DATETIME     NULL,
    remark          VARCHAR(255) NOT NULL DEFAULT '',
    created_at      DATETIME     NOT NULL,
    INDEX idx_user (user_id),
    INDEX idx_status (status),
    INDEX idx_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 4) sys_config: 支付收款码 + 提现参数默认值（后台支付配置页可改，JSON）
--    pay_config = {
--      enabled: true,
--      receiveWechatQrcodeUrl: string,
--      receiveAlipayQrcodeUrl: string,
--      receiveBankQrcodeUrl: string,
--      receiveBankInfo: { bankName, cardNo, accountName },
--      rechargeTips: string,
--      withdrawEnabled: true,
--      withdrawMin: 10,
--      withdrawMax: 50000,
--      withdrawFeeRate: 0.006,
--      withdrawFeeMin: 1
--    }
SET @cnt := (SELECT COUNT(*) FROM sys_config WHERE config_key = 'pay_config');
SET @sql := IF(@cnt = 0,
  'INSERT INTO sys_config(config_key, config_value) VALUES (''pay_config'', ''{"value":{"enabled":true,"receiveWechatQrcodeUrl":"","receiveAlipayQrcodeUrl":"","receiveBankQrcodeUrl":"","receiveBankInfo":{"bankName":"","cardNo":"","accountName":""},"rechargeTips":"请扫码向平台支付对应金额，并上传支付凭证，审核通过后余额会自动到账。","withdrawEnabled":true,"withdrawMin":10,"withdrawMax":50000,"withdrawFeeRate":0,"withdrawFeeMin":0}}'')',
  'SELECT ''pay_config key exists, skip''');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
