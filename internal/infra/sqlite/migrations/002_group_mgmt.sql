-- Migration: 002_group_mgmt
-- 描述:     B-015 群管理 per-group 开关列。单一开关，默认开（0=关 1=开，默认 1）。
--           001 已建表的旧库升级执行一次；新库在 001 建表后同样执行
--           （版本记录机制保证每文件只执行一次）。
-- 注意:     SQLite 不支持 ADD COLUMN IF NOT EXISTS，幂等依赖 schema_migrations
--           版本记录，勿手动重复执行。
ALTER TABLE group_config ADD COLUMN group_mgmt_enabled INTEGER NOT NULL DEFAULT 1;
