package event

import (
	"context"

	"plumebot/internal/domain/entity"
	"plumebot/internal/service/agent"
	"plumebot/internal/service/control"
	"plumebot/internal/service/memory"
	"plumebot/internal/service/plugin"
	"plumebot/pkg/config"
)

// EventService 是顶层事件调度编排，组合所有子 service。
type EventService struct {
	agent    *agent.AgentService
	memory   *memory.MemoryService
	plugin   *plugin.PluginService
	control  *control.ControlService
	msgChain Handler
}

// NewEventService 创建 EventService，注入全部子 service 与中间件配置。
// 消息管线顺序固定为：日志 → 限流 → 敏感词 → 末端处理。
func NewEventService(
	agent *agent.AgentService,
	memory *memory.MemoryService,
	plugin *plugin.PluginService,
	control *control.ControlService,
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
// 窗口满时触发 P3-003 异步窗口压缩；随后进入命令分支（/开头 → 插件分发，P4-002）。
// Agent 回复闭环（P6-002）尚未接入，这里仅完成记忆持久化、压缩触发与插件命令分发。
func (s *EventService) tail(ctx context.Context, msg entity.Message) error {
	full, err := s.memory.PersistMessage(ctx, msg)
	if err != nil {
		return err
	}
	if full {
		s.memory.Compress(ctx, msg)
	}
	return s.dispatchCommand(ctx, msg)
}

// HandleMessage 处理消息事件：走「日志 → 限流 → 敏感词 → 末端」管线。
func (s *EventService) HandleMessage(ctx context.Context, msg entity.Message) error {
	return s.msgChain(ctx, msg)
}

// HandleNotice 处理通知事件。第一阶段返回 nil。
func (s *EventService) HandleNotice(_ context.Context, _ entity.Event) error {
	return nil
}
