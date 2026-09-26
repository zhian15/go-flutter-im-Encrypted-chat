-- 022: 存量用户默认头像回填
-- 背景：注册/游客注册时若后台配置了 default_avatar 会写入 user.avatar
--       （internal/service/auth.go 注册/游客链路），但在后台配置 default_avatar
--       之前注册的存量用户 avatar 为空，App 端只能显示字母占位
--       （后台管理端自己渲染了默认图兜底，所以「看起来人人都有头像」）。
-- 取值口径：sys_config.config_value 为 JSON，SysConfigSet 统一存 {"value":"..."}，
--       优先取 $.value，兼容历史裸 JSON 字符串写法（'"url"'）。
-- 幂等性：UPDATE 只命中 avatar='' / avatar IS NULL 的行，回填后重复执行 0 行受影响；
--       未配置 default_avatar 时子查询为 NULL 且 EXISTS 不成立，同样 0 行受影响。
--       单条 UPDATE 语句，不依赖用户变量（迁移框架逐语句执行可能换连接，SET @var 不可靠）。
UPDATE `user` AS u
SET u.avatar = (
  SELECT COALESCE(
    JSON_UNQUOTE(JSON_EXTRACT(sc.config_value, '$.value')),
    JSON_UNQUOTE(sc.config_value)
  )
  FROM sys_config sc
  WHERE sc.config_key = 'default_avatar'
  LIMIT 1
)
WHERE (u.avatar IS NULL OR u.avatar = '')
  AND EXISTS (
    SELECT 1 FROM sys_config sc
    WHERE sc.config_key = 'default_avatar'
      AND COALESCE(
            JSON_UNQUOTE(JSON_EXTRACT(sc.config_value, '$.value')),
            JSON_UNQUOTE(sc.config_value)
          ) IS NOT NULL
      AND COALESCE(
            JSON_UNQUOTE(JSON_EXTRACT(sc.config_value, '$.value')),
            JSON_UNQUOTE(sc.config_value)
          ) <> ''
  );
