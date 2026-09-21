package response

import (
	"encoding/json"

	"plumebot/internal/domain/entity"
)

// Items 列表响应包装：{"items": [...]}（计划书 §6 包络内的 data 形态约定）。
type Items[T any] struct {
	Items []T `json:"items"`
}

// GroupConfig 单群配置响应：嵌入含 json tag 的 entity.GroupConfig，
// 附加 configured（false = 无配置行——该群尚未被 bot 触达，走全局兜底；HTTP 仍 200）。
// 群首次被触达时 service/control 会自动建行（admin-web-api-plan.md §7.2），故 false 已少见。
type GroupConfig struct {
	entity.GroupConfig
	Configured bool `json:"configured"`
}

// Persona 人格模板响应（不含内部 id）。
type Persona struct {
	Agent        string `json:"agent"`
	Name         string `json:"name"`
	SystemPrompt string `json:"system_prompt"`
}

// GroupProfile 群画像响应（configured=false = 无画像，走「无画像」态）。
type GroupProfile struct {
	Culture     string   `json:"culture"`
	Topics      []string `json:"topics"`
	ActiveHours string   `json:"active_hours"`
	Rules       []string `json:"rules"`
	Atmosphere  []string `json:"atmosphere"`
	Configured  bool     `json:"configured"`
}

// Jargon 黑话条目响应（含审核状态）。
type Jargon struct {
	Jargon string `json:"jargon"`
	Status string `json:"status"`
}

// MemberFact 成员事实响应（POST 返回新条目）。
type MemberFact struct {
	Fact string `json:"fact"`
}

// BotState 运行态响应（state 以 JSON 对象而非字符串输出）。
type BotState struct {
	GroupID string          `json:"group_id"`
	State   json.RawMessage `json:"state"`
}

// LogPage 日志浏览响应（架构 §17.6）：items 最新在前，has_more 表示还有更早记录可翻。
type LogPage struct {
	Items   []entity.LogEntry `json:"items"`
	HasMore bool              `json:"has_more"`
}
