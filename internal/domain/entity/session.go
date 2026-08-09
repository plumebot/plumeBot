package entity

import "context"

// Session 是一次 Agent 推理的会话身份：当前群 + 当前说话人。
// 记忆工具（store_fact/learn_jargon/forget_fact，P3-004）经 ctx 读取它确定记忆归属；
// 由消息管线（P6-002）在调用 Agent 前注入。entity 包零依赖，仅标准库。
type Session struct {
	GroupID string // 群 ID（私聊时为空）
	UserID  string // 当前说话人 QQ 号
}

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
