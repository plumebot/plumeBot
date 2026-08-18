package entity

import (
	"context"

	sdkentity "github.com/plumebot/plumebot-sdk/plugin"
)

// Session 是一次 Agent 推理/插件调用的会话身份：当前群 + 当前说话人。
// 协议单一事实来源已迁至 plugin-sdk/entity（插件协议 PluginRequest 复用同一类型，
// 见架构 §8.6），此处为类型别名。
// 记忆工具（store_fact/learn_jargon/forget_fact，P3-004）经 ctx 读取它确定记忆归属；
// 由消息管线（P6-002）在调用 Agent 前注入。
type Session = sdkentity.Session

// sessionCtxKey 是 Session 在 context 中的键类型（私有类型，避免与其他包键冲突）。
type sessionCtxKey struct{}

// WithSession 把会话身份注入 context，供记忆工具经 ctx 读取。
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, s)
}

// SessionFrom 从 context 取出会话身份。未注入（ok=false）时，工具应报明确错误，
// 而不是写入错误的记忆归属（见 infra/ai/tools）。
func SessionFrom(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(sessionCtxKey{}).(Session)
	return s, ok
}
