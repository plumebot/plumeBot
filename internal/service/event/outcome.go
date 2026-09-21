package event

// 消息结局账本（架构 §17.4）。需求：默认 log.level=info 下，任何消息在「收到消息」
// 之后必须还能对账到最终结局——「这条消息最终被回复了 / 被拦截了 / 被忽略了」。
//
// 约定：每条入站消息恰好「1 条入口行 + 1 条结局行」。入口行由 logMiddleware 输出
// （Info("收到消息", …)）；结局行由各终局分叉调用 logOutcome 输出：
//   - 限流/敏感词在中间件拦截（不进入 respond）；
//   - 命令分支 handled 短路（不透传 respond）；
//   - 普通消息唯一走 respond。
//
// 正常结局 Info、异常结局 Warn；结局细节（命中词、错误对象）作为结局行自带字段，
// 不另起语义重复的日志。

import (
	"go.uber.org/zap"

	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// Outcome 是消息结局枚举（架构 §17.4）。值稳定，用于日志检索（grep "消息结局"）。
type Outcome string

const (
	OutcomeAgentReplied    Outcome = "agent_replied"     // 触发并发送成功（带 reason）
	OutcomeCommand         Outcome = "command"           // 插件命令分支（含 /help）已消费
	OutcomeCommandNotFound Outcome = "command_not_found" // 命令未匹配任何插件（已消费）
	OutcomeNotTriggered    Outcome = "not_triggered"     // 触发判断不回复（带 reason）
	OutcomeEmptyReply      Outcome = "empty_reply"       // Agent 返回空回复过滤
	OutcomeRateLimited     Outcome = "rate_limited"      // 限流等待超时丢弃
	OutcomeSensitive       Outcome = "sensitive"         // 敏感词命中拦截
	OutcomeControlError    Outcome = "control_error"     // 触发判断失败
	OutcomePromptFail      Outcome = "prompt_fail"       // Prompt 组装失败
	OutcomeAgentFail       Outcome = "agent_fail"        // Agent 推理失败
	OutcomeSendFail        Outcome = "send_fail"         // 回复发送失败
	OutcomeNoSender        Outcome = "no_sender"         // 缺少 Sender 注入，跳过回复
	OutcomeCommandError    Outcome = "command_error"     // 插件命令执行失败（非 NotFound）
)

// isWarnOutcome 返回结局是否为异常（Warn）级别；正常结局（回复成功/已消费）为 Info。
func isWarnOutcome(oc Outcome) bool {
	switch oc {
	case OutcomeAgentReplied, OutcomeCommand, OutcomeCommandNotFound,
		OutcomeNotTriggered, OutcomeEmptyReply:
		return false
	default:
		return true
	}
}

// logOutcome 统一打一条「消息结局」结局行（架构 §17.4）：message_id/group_id/user_id/outcome
// 必带，extra 追加结局细节（命中词、错误对象、reason 等）。每条消息至多一个结局行。
func logOutcome(msg entity.Message, oc Outcome, extra ...zap.Field) {
	fields := []zap.Field{
		logger.S("message_id", msg.MessageID),
		logger.S("group_id", msg.GroupID),
		logger.S("user_id", msg.UserID),
		logger.S("outcome", string(oc)),
	}
	fields = append(fields, extra...)
	if isWarnOutcome(oc) {
		logger.Warn("消息结局", fields...)
		return
	}
	logger.Info("消息结局", fields...)
}