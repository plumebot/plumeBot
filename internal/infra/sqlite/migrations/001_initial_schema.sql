-- =============================================================================
-- Migration: 001_initial_schema
-- 描述:      PlumeBot 数据库全部表结构（单文件；conversation_summary 与
--           group_jargon.status 已并入，原 002/003 迁移文件删除）：
--           共 8 张表 + 3 个索引。
-- 创建时间:  2026-07（002/003 于 P3-005 合并）
-- =============================================================================

-- -----------------------------------------------------------------------------
-- 1. messages — 聊天消息（全量持久化）
-- 作用:   长期存储所有群聊/私聊消息，供记忆检索和上下文补全。
-- 索引:   idx_messages_group_time — 按群 + 时间排序查询最近消息。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id  TEXT    NOT NULL,                -- OneBot 消息唯一 ID
    group_id    TEXT    NOT NULL DEFAULT '',     -- 群 ID，私聊时为空
    user_id     TEXT    NOT NULL,                -- 发送者 QQ 号
    parts       TEXT    NOT NULL DEFAULT '[]',   -- 消息内容段（JSON：text/at/image/audio/video/file）
    timestamp   INTEGER NOT NULL DEFAULT 0,     -- Unix 时间戳（秒）
    message_type TEXT   NOT NULL DEFAULT 'group' -- group / private
);

CREATE INDEX IF NOT EXISTS idx_messages_group_time
    ON messages(group_id, timestamp);

-- -----------------------------------------------------------------------------
-- 2. group_profile — 群聊画像（每个群一条）
-- 作用:   群文化特征、主流话题、活跃时段、群规、氛围标签。
-- 注意:   黑话词典走 group_jargon 表，不在此处冗余。
-- 召回:   group_id 精确查询。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS group_profile (
    group_id     TEXT PRIMARY KEY,               -- 群 ID
    culture      TEXT NOT NULL DEFAULT '',       -- 群文化特征描述
    topics       TEXT NOT NULL DEFAULT '[]',     -- 主流话题标签 (JSON array)
    active_hours TEXT NOT NULL DEFAULT '',       -- 活跃时段描述
    rules        TEXT NOT NULL DEFAULT '[]',     -- 群规 (JSON array)
    atmosphere   TEXT NOT NULL DEFAULT '[]'      -- 氛围标签 (JSON array)
);

-- -----------------------------------------------------------------------------
-- 3. group_jargon — 群黑话词典
-- 作用:   存储群内特有词汇/梗，供 Agent 理解上下文时参考。
-- 状态:   status=pending 待人工审核；confirmed 才进 prompt（P3-004 learn_jargon）。
-- 约束:   (group_id, jargon) 唯一。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS group_jargon (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id TEXT    NOT NULL,                   -- 群 ID
    jargon   TEXT    NOT NULL,                   -- 黑话/梗文本
    status   TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed')),
    UNIQUE(group_id, jargon)
);

-- -----------------------------------------------------------------------------
-- 4. member_facts — 成员事实记忆（私聊 / 群聊）
-- 作用:   存储对某个用户的零散事实（如"喜欢猫"、"是程序员"）。
--         group_id 空 = 私聊记忆，非空 = 群聊记忆（成员持久上下文唯一机制）。
-- 约束:   (group_id, user_id, fact) 唯一。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS member_facts (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id TEXT    NOT NULL,                   -- 群 ID（私聊时为空）
    user_id  TEXT    NOT NULL,                   -- 用户 QQ 号
    fact     TEXT    NOT NULL,                   -- 事实描述
    UNIQUE(group_id, user_id, fact)
);

-- -----------------------------------------------------------------------------
-- 5. persona — 人格模板
-- 作用:   定义 bot 人设。「人格选择 agent」：agent 字段绑定到某个 agent（按名）。
-- 无 userid/groupid/extend（不做群级区分/继承）；默认模板 seed 由 P4-001 提供。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS persona (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    agent         TEXT NOT NULL DEFAULT '',      -- 绑定的 agent 名（人格选择 agent）
    name          TEXT NOT NULL DEFAULT '',      -- 展示名（给人看）
    system_prompt TEXT NOT NULL DEFAULT ''       -- 完整人设文本（经 Instruction 注入）
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_persona_agent ON persona(agent);

-- -----------------------------------------------------------------------------
-- 6. bot_state — Bot 群内状态
-- 作用:   每个群一条，存储 bot 在本群的运行时状态
--         （精力值、连续回复计数、冷却时间等），JSON blob。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS bot_state (
    group_id TEXT PRIMARY KEY,                   -- 群 ID
    state    TEXT NOT NULL DEFAULT '{}'          -- 状态 JSON
);

-- -----------------------------------------------------------------------------
-- 7. plugin_config — 插件配置
-- 作用:   按 (group_id, plugin_name) 唯一，存储某插件在某群的配置。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS plugin_config (
    group_id    TEXT NOT NULL,                   -- 群 ID
    plugin_name TEXT NOT NULL,                   -- 插件名称
    config      TEXT NOT NULL DEFAULT '{}',      -- 配置 JSON
    PRIMARY KEY (group_id, plugin_name)
);

-- -----------------------------------------------------------------------------
-- 8. conversation_summary — 摘要归档（1 会话 N 条）
-- 作用:   存储被淘汰/被融合覆盖的窗口摘要（长程记忆，P3-003）。
-- 约束:   (chat_id, seq) 唯一 —— seq 为会话内递增序号，upsert 幂等，
--         回灌的旧摘要再次被覆盖时重复落库不产生冗余。
-- 索引:   idx_conversation_summary_chat — 按会话 + seq 排序读取最新 N 条回灌。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS conversation_summary (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id    TEXT    NOT NULL,                -- 会话键：群聊=GroupID，私聊="private:"+UserID
    seq        INTEGER NOT NULL,                -- 会话内递增序号（排序 + 幂等键）
    text       TEXT    NOT NULL DEFAULT '',     -- 摘要文本
    keywords   TEXT    NOT NULL DEFAULT '[]',   -- 关键词标签 (JSON array)
    decisions  TEXT    NOT NULL DEFAULT '[]',   -- 关键决定 (JSON array)
    created_at INTEGER NOT NULL DEFAULT 0,      -- 生成时间（Unix 秒）
    UNIQUE(chat_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_conversation_summary_chat
    ON conversation_summary(chat_id, seq);
