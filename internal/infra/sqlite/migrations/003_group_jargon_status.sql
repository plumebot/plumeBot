-- =============================================================================
-- Migration: 003_group_jargon_status
-- 描述:      群黑话待确认状态（P3-004 扩展）。
--           learn_jargon 工具写入的黑话默认 pending，经 ConfirmJargon 转为 confirmed；
--           P6 prompt 组装只暴露 confirmed 黑话（pending 待人工审核，不进上下文）。
-- 创建时间:  2026-08
-- =============================================================================

ALTER TABLE group_jargon ADD COLUMN status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'confirmed'));
