-- 025: 单聊会话防重复（同一对人只允许一条单聊）
--
-- 背景：CreateDirect 是「先查后建」，并发竞态（连点发消息入口 / 双端同时打开
-- 资料页发消息 / 转发与聊天同时触发）会给同一对人各建一条单聊会话——
-- 会话列表同一个人显示两条（实测踩坑，2026-09-19）。
-- 方案：conversation 加 direct_key 配对键（"小UID:大UID"，群/频道恒 NULL）
-- + 唯一索引 uk_direct_key；CreateDirect 创建时携带该键，撞索引的请求改为
-- 查回已有会话原样返回（service/conversation.go）。
-- 本迁移同时自愈存量：重复单聊去重（保留一条，其余解散 status=2，消息本体
-- 留在 Mongo 不动，后台仍可查）。
--
-- 注意：唯一索引**必须在去重之后建**（存量重复行会让索引创建失败，服务起不来）；
-- model 的 DirectKey 不写 uniqueIndex tag 也是这个原因（AutoMigrate 先于迁移跑）。

-- A. 加列（幂等，023/024 同款手法）
SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'conversation'
    AND COLUMN_NAME = 'direct_key'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `conversation` ADD COLUMN `direct_key` VARCHAR(64) NULL COMMENT ''单聊配对键 minUID:maxUID，群/频道为 NULL''',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- B. 回填存量单聊的配对键（只回填 status=1 正常会话；已解散的保持 NULL 不占索引）
UPDATE conversation c
JOIN (
  SELECT m.conversation_id AS cid, MIN(m.user_id) AS ua, MAX(m.user_id) AS ub
  FROM conversation_member m
  JOIN conversation cc ON cc.id = m.conversation_id AND cc.type = 1
  GROUP BY m.conversation_id
) t ON t.cid = c.id
SET c.direct_key = CONCAT(t.ua, ':', t.ub)
WHERE c.type = 1 AND c.status = 1 AND c.direct_key IS NULL;

-- C. 存量去重：同一配对键存在多条正常单聊时，保留「实际在用」的那条
--    （成员已读水位最高的；水位并列取 id 最小的最早会话），
--    其余 status=2 解散 + direct_key 置 NULL（列表不再出现；消息本体不动）。
UPDATE conversation c
JOIN (
  SELECT g.direct_key,
         (SELECT c2.id
            FROM conversation c2
            LEFT JOIN (
              SELECT m.conversation_id AS cid, MAX(m.last_read_msg_id) AS mr
              FROM conversation_member m
              GROUP BY m.conversation_id
            ) r ON r.cid = c2.id
           WHERE c2.direct_key = g.direct_key AND c2.status = 1
           ORDER BY r.mr DESC, c2.id ASC
           LIMIT 1) AS keep_id
  FROM (
    SELECT c1.direct_key
    FROM conversation c1
    WHERE c1.type = 1 AND c1.status = 1 AND c1.direct_key IS NOT NULL
    GROUP BY c1.direct_key
    HAVING COUNT(*) > 1
  ) g
) dd ON dd.direct_key = c.direct_key
SET c.status = 2, c.direct_key = NULL
WHERE c.status = 1 AND c.id <> dd.keep_id;

-- D. 建唯一索引（幂等；必须放在 C 去重之后）
SET @idx_exists := (
  SELECT COUNT(DISTINCT INDEX_NAME) FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'conversation'
    AND INDEX_NAME = 'uk_direct_key'
);
SET @ddl2 := IF(@idx_exists = 0,
  'ALTER TABLE `conversation` ADD UNIQUE KEY `uk_direct_key` (`direct_key`)',
  'SELECT 1');
PREPARE stmt2 FROM @ddl2;
EXECUTE stmt2;
DEALLOCATE PREPARE stmt2;
