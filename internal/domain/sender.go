package domain

import (
	"context"

	"plumebot/internal/domain/entity"
)

// Sender 是事件级回复发送能力（B-003 方案 B）。
// 由连接层（infra/onebot）在 matcher 闭包构造 per-event 实例（持有 *zero.Ctx）并经
// WithSender 注入 ctx；需回复的环节（Agent 回复、插件回复）经 SenderFrom(ctx).Send 直接发送。
// 职责边界：
//   - 只回应当前事件（群回群、私聊回私聊），载荷为结构化 entity.Reply（多模态 + 引用 + @）；
//   - 不含动作能力（禁言/踢人等经 domain.GroupManager 供 agent tool 调用，见 B-015）；
//   - 不含非响应式主动发送（异步插件、定时、P5 auto 后续发言需显式目标，届时另定义 SendTo）。
type Sender interface {
	Send(ctx context.Context, r entity.Reply) error
}

// senderCtxKey 是 Sender 在 context 中的键类型（私有类型，避免与其他包键冲突）。
type senderCtxKey struct{}

// WithSender 把事件级 Sender 注入 context（matcher 闭包调用，Handler 链签名保持 (ctx,msg) error）。
func WithSender(ctx context.Context, s Sender) context.Context {
	return context.WithValue(ctx, senderCtxKey{}, s)
}

// SenderFrom 从 context 取出 Sender。未注入（ok=false）时调用方应告警跳过发送（不报错阻断管线）。
func SenderFrom(ctx context.Context) (Sender, bool) {
	s, ok := ctx.Value(senderCtxKey{}).(Sender)
	return s, ok
}
