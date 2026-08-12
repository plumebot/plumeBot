// Package entity 定义领域层公共实体，所有结构体均为纯数据结构，
// 不包含任何业务逻辑或外部依赖；仅允许只读的结构校验函数（见 plugin_validate.go）。
package entity

// Message 表示一条聊天消息的核心字段。
type Message struct {
	MessageID   string // 消息唯一 ID
	GroupID     string // 群 ID（私聊时为空）
	UserID      string // 发送者 QQ 号
	Content     string // 消息文本内容
	Timestamp   int64  // Unix 时间戳（秒）
	MessageType string // 消息类型：group / private
}
