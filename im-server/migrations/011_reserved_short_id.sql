-- 预留靓号池：后台分配用户 short_id 前可先批量生成/冻结，分配后自动置为 used。
-- 注意：表名必须与 model.ReservedShortID.TableName() 一致 = reserved_short_id

CREATE TABLE IF NOT EXISTS reserved_short_id (
    id          BIGINT        NOT NULL PRIMARY KEY,
    short_id    VARCHAR(32)   NOT NULL,
    source      INT           NOT NULL DEFAULT 1 COMMENT '1手动 2范围 3规则',
    status      INT           NOT NULL DEFAULT 1 COMMENT '1未分配 2冻结 3已用',
    remark      VARCHAR(255)  NOT NULL DEFAULT '',
    price       DECIMAL(12,2) NOT NULL DEFAULT 0,
    used_by     BIGINT        NOT NULL DEFAULT 0,
    used_at     DATETIME      NULL,
    created_at  DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_short_id (short_id),
    KEY idx_status (status),
    KEY idx_source (source),
    KEY idx_used_by (used_by)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='预留靓号池';
