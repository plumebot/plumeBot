package entity

// GroupProfile 表示一个群的群聊画像，每个群一条。
// 黑话词典通过 group_jargon 表查询，不在此处冗余存储。
type GroupProfile struct {
	GroupID     string   // 群 ID
	Culture     string   // 群文化特征描述
	Topics      []string // 主流话题标签
	ActiveHours string   // 活跃时段描述（如"晚上8-11点"）
	Rules       []string // 群规摘要
	Atmosphere  []string // 氛围标签（如"轻松"、"技术向"）
}

// Persona 表示一条人格模板，「人格选择 agent」：通过 Agent 字段绑定到某个 agent（按名）。
// Agent 字段 UNIQUE，一人一格；system_prompt 为完整人设文本，P6-001 起由 service 组装注入 system 消息。
type Persona struct {
	ID           int64  // 人格模板 ID
	Agent        string // 绑定的 agent 名（人格选择 agent）
	Name         string // 展示名（给人看），不参与逻辑
	SystemPrompt string // 完整人设文本
}
