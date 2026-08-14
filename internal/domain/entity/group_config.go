package entity

// GroupConfig 表示某个群的静态配置（P5-001 起：触发模式 mode；P5-002 起：状态规则参数覆盖）。
// 与 bot_state（运行态 JSON）职责分离：本表存管理员/持久配置，bot_state 存 bot 运行时状态。
// 未配置列（0 或空串）= 不覆盖，走全局 cfg.Control.state 兜底；mode 空 = 走全局 cfg.Control.Mode 兜底。
type GroupConfig struct {
	GroupID string `json:"group_id"` // 群 ID（PRIMARY KEY）
	Mode    string `json:"mode"`     // 触发模式：mention / auto；空 = 未配置（走全局 cfg.Control.Mode 兜底）

	// ── P5-002 状态规则参数覆盖（0/空 = 未配置，走全局）──
	EnergyMax         int    `json:"energy_max"`          // 精力上限
	EnergyCost        int    `json:"energy_cost"`         // 每次回复消耗
	EnergyRecover     int    `json:"energy_recover"`      // 精力恢复（点/分钟）
	EnergyThreshold   int    `json:"energy_threshold"`    // 低于此值不主动说话（@ 除外）
	CooldownSeconds   int    `json:"cooldown_seconds"`    // 两次主动回复最小间隔（秒）
	ConsecutiveLimit  int    `json:"consecutive_limit"`   // 连续回复上限（达此值强制休息）
	RestSeconds       int    `json:"rest_seconds"`        // 连续达上限后的强制休息时长（秒）
	QuietHoursStart   string `json:"quiet_hours_start"`   // 深夜静默起 "HH:MM"
	QuietHoursEnd     string `json:"quiet_hours_end"`     // 深夜静默止 "HH:MM"
	ShortMessageChars int    `json:"short_message_chars"` // 短消息忽略阈值（<N 字不触发）
}
