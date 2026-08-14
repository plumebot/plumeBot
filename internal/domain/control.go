package domain

import (
	"context"

	"plumebot/internal/domain/entity"
)

// Control 判断 bot 在当前消息下是否应该回复，以及回复后的回调（P5-001 触发模式 + P5-002 状态规则）。
// 入参为消息管线流转的 entity.Message（含 Mentioned/MessageType/GroupID/UserID），
// 而非 entity.Event——Event 无 Parts/提及/MessageID，无法承载触发判断所需字段。
// 实现放 service/control（P5-001 仅模式判定；P5-002 加状态规则：精力/冷却/连续/时段/短消息）。
type Control interface {
	// ShouldReply 判断当前消息是否应回复，返回决策（是否回复 + 原因）。
	// 评估序：强制回复（@/私聊）→ 模式解析（per-group → 全局 → "mention"）→
	// auto 模式状态规则（short_message → quiet_hours → energy → cooldown/连续休息 → auto_pass）。
	ShouldReply(ctx context.Context, msg entity.Message) (entity.Decision, error)

	// OnReplied 在 bot 决定回复后回调，维护 bot_state 状态规则（P5-002：消耗精力、记冷却、
	// 连续计数）。P5-002 起由 event 管线在 judge 时接线（标记触发即视为 bot 说话）；
	// P6-002 回复真实发出后若需按实际发送结果调整，再在发送环节接线。
	OnReplied(ctx context.Context, msg entity.Message) error
}
