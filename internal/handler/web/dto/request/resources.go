package request

// Persona 人格模板 PUT 请求体（P7-001）。agent 由 path 提供，不进 body。
type Persona struct {
	Name         string `json:"name"`
	SystemPrompt string `json:"system_prompt"`
}

// GroupProfile 群画像 PUT 请求体（P7-001）。group_id 由 path 提供，不进 body。
type GroupProfile struct {
	Culture     string   `json:"culture"`
	Topics      []string `json:"topics"`
	ActiveHours string   `json:"active_hours"`
	Rules       []string `json:"rules"`
	Atmosphere  []string `json:"atmosphere"`
}

// Jargon 黑话条目请求体（P7-001）。添加/删除/确认共用；group_id 由 path 提供。
// 文本一律走 body，避免中文/特殊字符进 URL path 的转义问题（计划书 §7.5 校验注定案）。
type Jargon struct {
	Jargon string `json:"jargon"`
}

// MemberFact 成员事实请求体（P7-001）。增删共用；删除时 group_id/user_id/fact 亦可用 query。
type MemberFact struct {
	GroupID string `json:"group_id"`
	UserID  string `json:"user_id"`
	Fact    string `json:"fact"`
}