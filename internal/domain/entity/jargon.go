package entity

// Jargon 群黑话条目（管理面视图，P7-001）。
// 区别于 ListJargon 返回的纯字符串，携带审核状态，供管理端
// 列出 pending / confirmed 并展示或审核。
type Jargon struct {
	GroupID string // 群 ID
	Jargon  string // 黑话/梗文本
	Status  string // 审核状态：pending / confirmed
}