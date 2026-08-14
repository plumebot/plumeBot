package event

import (
	"context"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/service/agent"
	"plumebot/internal/service/memory"
	"plumebot/internal/service/plugin"
	"plumebot/pkg/config"
	"plumebot/pkg/logger"
)

// EventService 是顶层事件调度编排，组合所有子 service。
type EventService struct {
	agent    *agent.AgentService
	memory   *memory.MemoryService
	plugin   *plugin.PluginService
	control  domain.Control
	msgChain Handler
}

// NewEventService 创建 EventService，注入全部子 service 与中间件配置。
// 消息管线顺序固定为：日志 → 限流 → 敏感词 → 末端处理。
func NewEventService(
	agent *agent.AgentService,
	memory *memory.MemoryService,
	plugin *plugin.PluginService,
	control domain.Control,
	mwCfg config.MiddlewareConfig,
) *EventService {
	s := &EventService{
		agent:   agent,
		memory:  memory,
		plugin:  plugin,
		control: control,
	}
	mws := []Middleware{
		logMiddleware,
		rateLimitMiddleware(newRateLimiter(mwCfg.RateLimit)),
		sensitiveWordMiddleware(newSensitiveWordFilter(mwCfg.SensitiveWords)),
	}
	s.msgChain = chain(mws, s.tail)
	return s
}

// tail 是消息管线的末端处理：持久化消息（写入上下文窗口 + SQLite），
// 窗口满时触发 P3-003 异步窗口压缩；随后命令分支（/开头 → 插件分发，P4-002，
// 命令消息短路绕过触发判断，架构 §10.2）；最后对普通消息做 P5-001 触发判断。
// Agent 回复闭环（P6-002）尚未接入，触发判断只标记，不发送。
func (s *EventService) tail(ctx context.Context, msg entity.Message) error {
	full, err := s.memory.PersistMessage(ctx, msg)
	if err != nil {
		return err
	}
	if full {
		s.memory.Compress(ctx, msg)
	}
	handled, err := s.dispatchCommand(ctx, msg)
	if err != nil {
		return err
	}
	if handled {
		return nil // 命令消息已走插件分支，绕过触发判断
	}
	return s.judgeReply(ctx, msg)
}

// judgeReply 是 P5-001 触发判断 + P5-002 状态规则：对非命令普通消息调 control service 判断是否应回复。
// 触发（Reply=true）时追调 OnReplied 更新运行态（P5-002 消耗精力/记冷却/连续计数）；
// 只做判断 + 日志标记，不发送回复（发送归 P6-002 B-003 Sender）。
// 判断失败不阻断管线：记录告警并保守忽略（不触发）。
func (s *EventService) judgeReply(ctx context.Context, msg entity.Message) error {
	d, err := s.control.ShouldReply(ctx, msg)
	if err != nil {
		logger.Warn("触发判断失败",
			logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID), logger.Err(err))
		return nil
	}
	if d.Reply {
		logger.Info("触发回复",
			logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID),
			logger.S("message_id", msg.MessageID), logger.S("reason", string(d.Reason)))
		// P5-002：标记触发即视为 bot 说话，更新运行态（精力/冷却/连续计数）。失败仅告警，不阻断。
		if err := s.control.OnReplied(ctx, msg); err != nil {
			logger.Warn("回复状态记录失败",
				logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID), logger.Err(err))
		}
	} else {
		logger.Debug("未触发回复",
			logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID),
			logger.S("message_id", msg.MessageID), logger.S("reason", string(d.Reason)))
	}
	return nil
}

// HandleMessage 处理消息事件：走「日志 → 限流 → 敏感词 → 末端」管线。
func (s *EventService) HandleMessage(ctx context.Context, msg entity.Message) error {
	return s.msgChain(ctx, msg)
}

// HandleNotice 处理通知事件。第一阶段返回 nil。
func (s *EventService) HandleNotice(_ context.Context, _ entity.Event) error {
	return nil
}
