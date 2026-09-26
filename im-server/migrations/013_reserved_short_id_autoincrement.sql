-- 013_reserved_short_id_autoincrement.sql
-- Bug 修复：011_reserved_short_id.sql 建表时忘记给 id 列加 AUTO_INCREMENT，
-- 导致批量生成靓号时 tx.Create 每次都尝试写 id=0，与已有 0 值主键冲突，
-- service 层又把 Create error 当"跳过"处理，结果 added=0 看起来成功但实际 1 条都没入库。
--
-- 本迁移把 reserved_short_id.id 列 MODIFY 为 BIGINT NOT NULL AUTO_INCREMENT，
-- 与 user.id、wallet_transaction.id 等已有列保持一致。
-- 用 information_schema + PREPARE 实现幂等：
--   - 若该列尚未带 AUTO_INCREMENT 属性，则 ALTER；
--   - 若已经是 AUTO_INCREMENT，则跳过 SELECT 1；
--   - 若当前表里已有 id=0 的脏数据（由之前的 bug 插入），先置为 NULL 让 MODIFY 时重算成 1+（NULL 在 AUTO_INCREMENT 会被分配下一个 ID）。

-- 先清理 bug 造成的 id=0 脏行（若存在）：把 id 置 NULL，触发 AUTO_INCREMENT 重新分配
SET @dirty_exists = (
    SELECT COUNT(*) FROM reserved_short_id WHERE id = 0
);
SET @sql_fix_dirty = IF(
    @dirty_exists > 0,
    'UPDATE reserved_short_id SET id = NULL WHERE id = 0',
    'SELECT 1'
);
PREPARE stmt_fix_dirty FROM @sql_fix_dirty;
EXECUTE stmt_fix_dirty;
DEALLOCATE PREPARE stmt_fix_dirty;

-- 检查 id 列是否已经带 AUTO_INCREMENT（EXTRA 里有 auto_increment 标记）
SET @ai_exists = (
    SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME   = 'reserved_short_id'
      AND COLUMN_NAME  = 'id'
      AND EXTRA LIKE '%auto_increment%'
);
SET @sql_modify = IF(
    @ai_exists = 0,
    'ALTER TABLE reserved_short_id MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT',
    'SELECT 1'
);
PREPARE stmt_modify FROM @sql_modify;
EXECUTE stmt_modify;
DEALLOCATE PREPARE stmt_modify;
