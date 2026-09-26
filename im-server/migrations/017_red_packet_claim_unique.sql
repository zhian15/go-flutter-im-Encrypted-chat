-- 017 红包领取记录 (msg_id, user_id) 联合唯一索引（防并发重复领取）
--
-- 背景：red_packet_claim 原来只有 msg_id / user_id 两个独立普通索引，没有联合唯一约束。
--       幂等只靠 wallet.go 里「事务外先 COUNT 再写」，高并发下同一用户可以重复领同一个
--       群红包 —— 先查后写之间存在窗口期，两个请求都能看到 dup=0 然后都写入。
--       对照 transfer_claim：它有 uk_msg(msg_id) 唯一索引，靠数据库兜底。
--
-- 为什么唯一索引只由本迁移建立、不写进 model tag：
--       cmd/api/main.go 先执行 store.InitMySQL（内含 AutoMigrate），之后才执行
--       store.MigrateMySQL。若把 uniqueIndex 写进 RedPacketClaim 的 tag，存量库里
--       一旦存在重复行，AutoMigrate 建唯一索引就会失败 -> 服务启动失败 -> 本迁移永远
--       没机会清理重复行，形成死锁。所以 model 里保持普通索引，唯一约束放这里建。
--
-- 幂等：先清重复行、再判断索引是否已存在，可重复执行。

-- 1) 清理重复领取记录：同一 (msg_id, user_id) 只保留 id 最小的那一条
DELETE c FROM `red_packet_claim` c
INNER JOIN (
  SELECT msg_id, user_id, MIN(id) AS keep_id
  FROM `red_packet_claim`
  GROUP BY msg_id, user_id
  HAVING COUNT(*) > 1
) d ON c.msg_id = d.msg_id AND c.user_id = d.user_id
WHERE c.id > d.keep_id;

-- 2) 建联合唯一索引（已存在则跳过）
SET @idx_exists := (
  SELECT COUNT(*) FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'red_packet_claim' AND INDEX_NAME = 'uk_msg_user'
);
SET @ddl := IF(@idx_exists = 0,
  'ALTER TABLE `red_packet_claim` ADD UNIQUE KEY `uk_msg_user` (`msg_id`, `user_id`)',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
