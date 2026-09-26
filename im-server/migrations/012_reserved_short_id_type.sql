-- 012_reserved_short_id_type.sql
-- 给 reserved_short_id 增加 "类型" 字段（1 普通 / 2 豹子号 / 3 顺子号 / 4 VIP），
-- 并为该列建立索引，支持前端按类型展示徽标与统计。
--
-- MySQL 没有原生 ADD COLUMN IF NOT EXISTS / CREATE INDEX IF NOT EXISTS，
-- 因此用 information_schema 判断 + 动态 SQL PREPARE/EXECUTE 实现幂等，
-- 重复执行不会报错，与 011_reserved_short_id.sql 建好的表无冲突，
-- 对已有数据默认值为 1（普通靓号）。

SET @col_exists = (
    SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME   = 'reserved_short_id'
      AND COLUMN_NAME  = 'type'
);
SET @sql_add_col = IF(
    @col_exists = 0,
    'ALTER TABLE reserved_short_id ADD COLUMN type INT NOT NULL DEFAULT 1 COMMENT ''type:1=normal,2=pairs,3=sequence,4=vip'' AFTER source',
    'SELECT 1'
);
PREPARE stmt_add_col FROM @sql_add_col;
EXECUTE stmt_add_col;
DEALLOCATE PREPARE stmt_add_col;

SET @idx_exists = (
    SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.STATISTICS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME   = 'reserved_short_id'
      AND INDEX_NAME   = 'idx_reserved_short_id_type'
);
SET @sql_add_idx = IF(
    @idx_exists = 0,
    'CREATE INDEX idx_reserved_short_id_type ON reserved_short_id (type)',
    'SELECT 1'
);
PREPARE stmt_add_idx FROM @sql_add_idx;
EXECUTE stmt_add_idx;
DEALLOCATE PREPARE stmt_add_idx;
