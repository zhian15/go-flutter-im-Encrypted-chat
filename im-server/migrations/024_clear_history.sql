-- 024: 「删除聊天记录」软删位点
--
-- conversation_member 加 cleared_msg_id / cleared_seq 两列（单聊任一方触发删除时，
-- 把成员行位点推进到触发时刻的最大 msg_id / max seq；History/ConvList 按 msg_id
-- 位点过滤、Sync 补拉按 seq 位点过滤，消息本体留在 Mongo 不删——后台可查原文）。
-- ConversationMember 在 AutoMigrate 白名单内（internal/store/store.go），全新库由
-- AutoMigrate 按 model tag 建列，这里幂等兜底老库（023 同款手法）。

SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'conversation_member'
    AND COLUMN_NAME = 'cleared_msg_id'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `conversation_member` ADD COLUMN `cleared_msg_id` BIGINT NOT NULL DEFAULT 0 COMMENT ''删除聊天记录软删位点，History/ConvList 只下发 msg_id 大于该值的消息''',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists2 := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'conversation_member'
    AND COLUMN_NAME = 'cleared_seq'
);
SET @ddl2 := IF(@col_exists2 = 0,
  'ALTER TABLE `conversation_member` ADD COLUMN `cleared_seq` BIGINT NOT NULL DEFAULT 0 COMMENT ''删除聊天记录 seq 位点，Sync 补拉只下发 seq 大于该值的消息''',
  'SELECT 1');
PREPARE stmt2 FROM @ddl2;
EXECUTE stmt2;
DEALLOCATE PREPARE stmt2;

