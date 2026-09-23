package response

// 会话窗口域响应（P7-003，管理前端「对话历史」栏目）：活跃会话概览 + 窗口内消息展示视图，均只读。

// SessionOverview 活跃会话概览（会话下拉展示：键 / 条数 / 最近消息）。
type SessionOverview struct {
	Key        string `json:"key"`
	Count      int    `json:"count"`
	LastTS     int64  `json:"last_ts"`
	LastRender string `json:"last_render"`
}

// SessionMessage 窗口内单条消息的展示视图（聊天气泡）。
type SessionMessage struct {
	MessageID string `json:"message_id"`
	Sender    string `json:"sender"`  // 展示名（SenderName 优先，空回落 QQ 号）
	SendAt    int64  `json:"send_at"` // Unix 秒
	Self      bool   `json:"is_self"` // bot 自身回复（右侧气泡）
	Render    string `json:"render"`  // 展示文本（含媒体占位标记）
}