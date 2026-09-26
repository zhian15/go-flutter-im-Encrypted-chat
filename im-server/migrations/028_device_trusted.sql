-- 账户安全验证（登录设备批准，2026-09-23）
-- device 表新增 trusted 信任标记：
--   1 = 已通过审批/可信，登录免二次验证
--   0 = 曾被手动注销，下次登录需重新批准
-- 存量设备默认视为已信任（default 1），避免开关开启后全体老设备突然要求审批。
ALTER TABLE `device` ADD COLUMN `trusted` tinyint(1) NOT NULL DEFAULT 1 COMMENT '设备信任标记：1=已信任(免二次验证)，0=曾被手动注销需重新批准';
