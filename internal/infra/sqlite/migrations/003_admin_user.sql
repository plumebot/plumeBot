-- Migration: 003_admin_user
-- 描述:     管理后端管理员账号表（P7-001，见 docs/admin-web-api-plan.md §5.3）。
--           凭证唯一事实来源（config 不含任何账号/密码）；存 bcrypt 散列，不存明文。
--           首个账号由页面首次注册创建（admin_user 空表时 /auth/register 放行），
--           创建后 register 端点永久关闭（fail-closed）。
CREATE TABLE IF NOT EXISTS admin_user (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,      -- 登录名
    password_hash TEXT NOT NULL,             -- bcrypt 散列
    created_at    INTEGER NOT NULL DEFAULT 0, -- 创建时间（Unix 秒）
    updated_at    INTEGER NOT NULL DEFAULT 0  -- 最近修改时间（Unix 秒）
);