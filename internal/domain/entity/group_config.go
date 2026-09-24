package entity

// GroupConfig 表示某个群的静态配置（P5-001 起：触发模式 mode；P5-002 起：状态规则参数覆盖）。
// 与 bot_state（运行态 JSON）职责分离：本表存管理员/持久配置，bot_state 存 bot 运行时状态。
// 未配置列（0 或空串）= 不覆盖，走全局 cfg.Control.state 兜底；mode 空 = 走全局 cfg.Control.Mode 兜底。
// 纯结构（无 json tag）——SQLite 为列式存取，前端出参的 json 形态定义在 handler/web/dto/response
//（response.GroupConfig），入参形态定义在 handler/web/dto/request。
type GroupConfig struct {
	GroupID string // 群 ID（PRIMARY KEY）
	Mode    string // 触发模式：mention / auto；空 = 未配置（走全局 cfg.Control.Mode 兜底）

	// ── P5-002 状态规则参数覆盖（0/空 = 未配置，走全局）──
	EnergyMax         int    // 精力上限
	EnergyCost        int    // 每次回复消耗
	EnergyRecover     int    // 精力恢复（点/分钟）
	EnergyThreshold   int    // 低于此值不主动说话（@ 除外）
	CooldownSeconds   int    // 两次主动回复最小间隔（秒）
	ConsecutiveLimit  int    // 连续回复上限（达此值强制休息）
	RestSeconds       int    // 连续达上限后的强制休息时长（秒）
	QuietHoursStart   string // 深夜静默起 "HH:MM"
	QuietHoursEnd     string // 深夜静默止 "HH:MM"
	ShortMessageChars int    // 短消息忽略阈值（<N 字不触发）

	// ── B-015 群管理开关（per-group 单一开关，默认开）──
	// 与上面「0 = 走全局兜底」语义不同：此为显式开关，0=关 1=开（默认）；
	// 无配置行（ErrNotFound）同样视为开，botGroupManager.Execute 放行（需管理员校验）。
	GroupMgmtEnabled int
}
