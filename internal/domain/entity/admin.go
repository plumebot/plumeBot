package entity

// 管理后端通信结构体（P7-001/003）：service ↔ handler/web 之间交换的业务数据载体。
// 入参/出参的 json 序列化由 handler/web/dto（request / response）接管，本层保持纯结构
//（不带 json tag），与 entity 其余业务实体一致。

// AuthResult 注册/登录成功签发的认证结果（token 由 pkg/jwt.Manager 签发）。
type AuthResult struct {
	Token     string // 登录 token
	TokenType string // 令牌类型（"Bearer"）
	Username  string // 登录用户名
	ExpiresAt int64  // 过期时间（Unix 秒）
}

// SessionOverview 单个活跃会话的概览（管理前端会话下拉展示）。
type SessionOverview struct {
	Key        string // 会话键：群号 或 "private:"+QQ
	Count      int    // 窗口内消息条数
	LastTS     int64  // 窗口内最后一条消息的时间戳（秒）；空窗口为 0
	LastRender string // 最后一条消息的展示文本（ForLLM：含图片描述）
}

// SessionMessage 窗口内单条消息的展示视图（前端聊天气泡）。
type SessionMessage struct {
	MessageID string // 消息 ID（bot 转发为 "self:" 前缀，见 Self）
	Sender    string // 发送者展示名（SenderName 优先，空回落 QQ 号）
	SendAt    int64  // 消息时间戳（秒）
	Self      bool   // 是否 bot 自身回复（识别见 service/admin 的 isBotReply）
	Render    string // 展示文本（ForLLM 视图：text + 已描述图片「（图片：描述）」+ 占位标记）
}

// SessionSummary 会话摘要热链的单条展示视图（管理前端「更早的对话纪要」区块）。
// 摘要文本、关键词与关键决定直接取自 entity.Summary；会话键/序号为链路内部字段，不进展示视图。
type SessionSummary struct {
	Text      string   // 摘要文本
	Keywords  []string // 关键词标签
	Decisions []string // 关键决定/共识
	CreatedAt int64    // 生成时间（Unix 秒）
}