package entity

// BotState 表示 bot 在某个会话的状态，以 JSON blob 存储。
// 每个会话一条：群聊 = GroupID，私聊 = "private:"+UserID（与 Message.SessionKey 一致，P5-002）。
type BotState struct {
	GroupID string // 会话键（群 ID 或 "private:"+用户 ID）
	State   string // 状态 JSON（精力值、连续回复计数、冷却时间等）
}
