package entity

// Role 表示对话消息的发送者角色。
type Role string

const (
	RoleSystem    Role = "system"    // 系统提示词
	RoleUser      Role = "user"      // 用户输入
	RoleAssistant Role = "assistant" // 模型回复（历史对话回传时使用）
)

// PartType 表示消息内容片段的类型。
type PartType string

const (
	PartTypeText  PartType = "text"
	PartTypeImage PartType = "image"
	PartTypeAudio PartType = "audio"
	PartTypeVideo PartType = "video"
	PartTypeFile  PartType = "file"
	// PartTypeAt 是 @ 标记（入站专用：@某人/@全体）。不直接进 LLM，组装时转文本（见 service 层）。
	PartTypeAt PartType = "at"
)

// ContentPart 是一个内容片段（入站 Message 段与出站 ChatMessage 通用）。
// URL 与 Base64 二选一（按 Type 决定语义）；MIMEType 用于图片等二进制片段（如 image/png）。
// Description 是多模态片段的 LLM 文本描述：空 = 未生成（阶段 2 惰性填充）。
// 持久化规则：base64 不落库，仅 URL / 文本 / 描述落库。
//
// 本结构是 entity 中唯一带 json tag 的类型：**tag 是其持久化形态约定**——
// messages.parts 列为 JSON 文本，由 infra/sqlite 直接 json.Marshal/Unmarshal（键名即列内键名，
// 改名必须是有意为之，避免旧行读不出）。web 出参不经本类型，json 序列化仍由 dto 层负责。
type ContentPart struct {
	Type        PartType `json:"type"`
	Text        string   `json:"text,omitempty"`        // Type==text 时的文本；Type==at 时为 "[@qq]"/"[@全体]"
	URL         string   `json:"url,omitempty"`         // 远程资源地址（如图片 URL）；可空（占位）
	Base64      string   `json:"base64,omitempty"`      // 二进制内容（base64 编码）；瞬时传递，不持久化
	MIMEType    string   `json:"mime_type,omitempty"`   // 二进制片段媒体类型（如 image/png）
	Description string   `json:"description,omitempty"` // 多模态的 LLM 文本描述；空 = 未生成
	// FileHash 图片内容寻址键（一图一值）：OneBot image 段 file/file_md5 的 32 位 hex
	//（NapCat 收图 file 即内容 md5，可能带 .image 后缀），或 base64:// 解码字节 md5。
	// 跨 URL/跨来源共享；空 = 来源不可得（回落 URL 字符串键）。
	// 不带 omitempty：持久化键显式出现（键稳定，便于排查），与其他文本字段的体积优化语义区分。
	FileHash string `json:"file_hash"`
}

// ChatMessage 是传给 LLM 的一条会话消息（多模态：Parts 可含文本、图片等片段）。
// 完整消息列表（含 system role）由调用方（service 层）组装，infra 层只执行。
type ChatMessage struct {
	Role  Role
	Parts []ContentPart
}
