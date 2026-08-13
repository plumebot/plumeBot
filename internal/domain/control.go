package domain

import (
	"context"

	"plumebot/internal/domain/entity"
)

// Control 判断 bot 在当前消息下是否应该回复，以及回复后的回调（P5-001 触发模式）。
// 入参为消息管线流转的 entity.Message（含 Mentioned/MessageType/GroupID/UserID），
// 而非 entity.Event——Event 无 Parts/提及/MessageID，无法承载触发判断所需字段。
// 实现放 service/control（P5-001：仅模式判定，无状态规则）。
type Control interface {
	// ShouldReply 判断当前消息是否应回复，返回决策（是否回复 + 原因）。
	// 模式解析优先级：per-group group_config.mode → 全局 cfg.Control.Mode → "mention"。
	ShouldReply(ctx context.Context, msg entity.Message) (entity.Decision, error)

	// OnReplied 在 bot 实际回复后回调，供状态规则更新（P5-002 精力/冷却；P6-002 回复发出后接线调用）。
	OnReplied(ctx context.Context, msg entity.Message) error
}
