-- 019 device 表增加 last_ip 列（最后活跃出口 IP，设备页「活跃会话」展示用）
--
-- 背景：设备页「活跃会话」要显示每台已登录设备的出口 IP（参考截图 47.123.117.33）。
--       原 IP 只存在于 Redis `online:{uid}:ip:{平台名}`（TTL 90s，且粒度是平台名不是设备），
--       无法持久化、也无法对应到具体设备。现在 WS 建连时随 last_active_at 一起落库
--       （service/ws.go handleWS → UPDATE device SET last_active_at/last_ip）。
--
-- 为什么走迁移而不是 AutoMigrate：internal/store/store.go 的 AutoMigrate 白名单**不含
--       model.Device**（只有 Wallet/Moments/RedPacket/Transfer/MoneyPacket/User/
--       InviteFriendCode/Conversation/ConversationMember 10 个），Device 的任何加列都必须
--       由 store.MigrateMySQL 顺序执行本文件补齐（记录在 schema_migrations，不会重复执行）。
--
-- 幂等：先查 information_schema 判断列是否存在，不存在才 ALTER，可重复执行
--       （与 018_friend_request_source.sql 同一手法）。

SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'device'
    AND COLUMN_NAME = 'last_ip'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `device` ADD COLUMN `last_ip` VARCHAR(64) NOT NULL DEFAULT '''' COMMENT ''最后活跃出口 IP（WS 建连时捕获）'' AFTER `last_active_at`',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
