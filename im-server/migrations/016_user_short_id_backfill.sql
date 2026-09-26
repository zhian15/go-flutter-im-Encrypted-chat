-- ============================================================
-- 016_user_short_id_backfill.sql
-- 一次性回填 user.short_id 为 NULL 的早期用户
--
-- 背景：model.User.ShortID 改为 *string 之前注册的早期用户，
--       user.short_id 是 ''；014_user_short_id_null_cleanup 把
--       '' 改为 NULL。客户端 profile_page.dart（lib/pages/profile_page.dart）
--       第 145-148 行 shortId 为空时显示 18 位雪花 ID（user.id），
--       与「短 ID 加好友」的设计不符，体验差。
--
-- 策略：按 user.id 升序分配 5 位短号（10000-99999），
--       起点 = 已有短号最大值 + 1（保证不与现有短号冲突）。
--       范围与 service.genShortID 一致（5 位 10000-99999）。
--
-- 跳过 / 兜底：
--   - 已有 short_id（非 NULL）不动 → unique key 不冲突
--   - reserved_short_id 表里 reserved 号段被分配会冲突；
--     本迁移假设 reserved 表无冲突短号（实际为空），如果业务有
--     预留号段在 user.short_id 中已有，先清理 reserved_short_id
--     再跑本迁移。
--
-- 幂等：只更新 short_id IS NULL 的行；第二次执行时所有 NULL 已分配 → 空跑 no-op。
--
-- 执行：mysql -uroot -p im < 016_user_short_id_backfill.sql
--       或 phpMyAdmin 粘贴运行（注意临时表 DROP IF EXISTS 防重复执行报错）。
-- ============================================================

-- 1. 计算起始号（已有 5 位短号最大值 + 1，最大不超过 99999）
SET @start := IFNULL(
  (SELECT MAX(CAST(short_id AS UNSIGNED))
   FROM user
   WHERE short_id IS NOT NULL
     AND short_id REGEXP '^[0-9]+$'
     AND CAST(short_id AS UNSIGNED) BETWEEN 10000 AND 99999),
  9999
);

-- 2. 临时表存 NULL 用户 + 递增编号
--    用临时表 + 用户变量避免 MySQL 5.7 中 JOIN 子查询里 @cnt 同值 bug
DROP TEMPORARY TABLE IF EXISTS tmp_null_backfill;
CREATE TEMPORARY TABLE tmp_null_backfill (
  id  BIGINT PRIMARY KEY,
  rn  INT NOT NULL
);

-- 3. 收集 NULL 用户，按 user.id 升序编号
INSERT INTO tmp_null_backfill(id, rn)
SELECT id, @cnt := @cnt + 1
FROM user, (SELECT @cnt := 0) r
WHERE short_id IS NULL
ORDER BY id;

-- 4. sanity：起始号 + NULL 用户数 不能超过 99999
SET @null_count := (SELECT COUNT(*) FROM tmp_null_backfill);
SELECT
  @start                                AS start_id,
  @null_count                           AS null_users,
  @start + @null_count                  AS end_id,
  IF(@start + @null_count > 99999,
     '警告：5 位号段将耗尽，部分用户分配失败，请手工处理',
     'OK')                              AS sanity_check;

-- 5. 一次性 UPDATE（@start + rn 唯一递增，unique key 不冲突）
UPDATE user u
JOIN tmp_null_backfill t ON u.id = t.id
SET u.short_id = LPAD(@start + t.rn, 5, '0');

-- 6. 验证
SELECT COUNT(*) AS still_null FROM user WHERE short_id IS NULL;

-- 7. 清理
DROP TEMPORARY TABLE tmp_null_backfill;