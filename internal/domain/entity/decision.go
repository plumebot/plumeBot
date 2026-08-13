package entity

// Decision 表示触发判断（P5-001）的决策结果：是否应回复 + 决策原因。
// 消费方（event 管线）据 Reply 决定消息是否进入回复路径；Reason 供日志标记，
// 亦为将来（P5-002 状态规则 / P6-002 回复行为）按 reason 定制行为的稳定标识。
type Decision struct {
	Reply  bool           // 是否应回复
	Reason DecisionReason // 决策原因
}

// DecisionReason 触发决策原因。值为稳定字符串，用于日志与跨层传输，不做展示。
type DecisionReason string

const (
	// ── P5-001 产出 ──

	// DecisionReasonMentionForced mention 模式：被 @ 或私聊 → 强制回复。
	DecisionReasonMentionForced DecisionReason = "mention_forced"
	// DecisionReasonAutoPass auto 模式：预检通过 → 自主回复（是否真正回复由后续 Agent 判断）。
	DecisionReasonAutoPass DecisionReason = "auto_pass"
	// DecisionReasonNotTriggered mention 模式：群聊未被 @ → 不触发。
	DecisionReasonNotTriggered DecisionReason = "not_triggered"

	// ── P5-002 预留（B-023 定名；本期不产出、禁止返回）──

	// DecisionReasonLowEnergy 精力不足（P5-002）。
	DecisionReasonLowEnergy DecisionReason = "low_energy"
	// DecisionReasonCooldown 冷却中（P5-002）。
	DecisionReasonCooldown DecisionReason = "cooldown"
	// DecisionReasonQuietHours 深夜静默（P5-002）。
	DecisionReasonQuietHours DecisionReason = "quiet_hours"
	// DecisionReasonShortMessage 短消息忽略（P5-002）。
	DecisionReasonShortMessage DecisionReason = "short_message"
)
