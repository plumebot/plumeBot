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

// SessionSummary 会话摘要热链的单条展示视图（「更早的对话纪要」区块）。
// json 形态在本层定义；handler 从 entity.SessionSummary 映射。
type SessionSummary struct {
	Text      string   `json:"text"`
	Keywords  []string `json:"keywords,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
	CreatedAt int64    `json:"created_at"` // Unix 秒
}

// SessionWindow 会话窗口查看响应：窗口内消息 + 摘要热链一并返回（一次拉取一次渲染）。
// 摘要为「窗口之前那段已压缩的历史」，前端渲染在气泡列表上方，故未复用 Items 包络。
type SessionWindow struct {
	Items     []SessionMessage `json:"items"`
	Summaries []SessionSummary `json:"summaries"`
}