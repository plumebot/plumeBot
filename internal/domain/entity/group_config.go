package entity

// GroupConfig 表示某个群的静态配置（P5-001 起：触发模式 mode）。
// 与 bot_state（运行态 JSON）职责分离：本表存管理员/持久配置，bot_state 存 bot 运行时状态。
// 未配置行（group_id 不存在或 mode 为空）= 走全局 cfg.Control.Mode 兜底。
type GroupConfig struct {
	GroupID string // 群 ID（PRIMARY KEY）
	Mode    string // 触发模式：mention / auto；空 = 未配置（走全局 cfg.Control.Mode 兜底）
}
