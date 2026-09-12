package request

// GroupConfig 群配置 PUT 请求体（P7-001）。group_id 由 path 提供，不进 body；
// 字段语义与 DB/entity 对齐：0/空 = 走全局，group_mgmt_enabled 为显式开关。
type GroupConfig struct {
	Mode               string `json:"mode"`
	EnergyMax          int    `json:"energy_max"`
	EnergyCost         int    `json:"energy_cost"`
	EnergyRecover      int    `json:"energy_recover"`
	EnergyThreshold    int    `json:"energy_threshold"`
	CooldownSeconds    int    `json:"cooldown_seconds"`
	ConsecutiveLimit   int    `json:"consecutive_limit"`
	RestSeconds        int    `json:"rest_seconds"`
	QuietHoursStart    string `json:"quiet_hours_start"`
	QuietHoursEnd      string `json:"quiet_hours_end"`
	ShortMessageChars  int    `json:"short_message_chars"`
	GroupMgmtEnabled   int    `json:"group_mgmt_enabled"`
}