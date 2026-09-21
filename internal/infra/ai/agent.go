package ai

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
	"plumebot/pkg/logger"
)

// defaultMaxIterations 是 tool 自动循环上限（adk 默认同为 20，显式设置防止未来默认值变化）。
const defaultMaxIterations = 20

// errNoModelOutput 表示推理结束但未收到任何模型输出消息。
var errNoModelOutput = errors.New("模型未返回任何消息")

// 编译期校验：EinoAgent 实现 domain.Agent。
var _ domain.Agent = (*EinoAgent)(nil)

// EinoAgent 是基于 eino ChatModelAgent 的 domain.Agent 实现。
// P6-001 起人格不再经 Instruction 注入（通常为空）：①系统人格由 service/memory BuildMessages
// 组装注入首条 system 消息（改 persona 表即时生效），本层只执行推理；工具按需注入。
// model 为对话模型名（调用度量日志用，架构 §17.2 infra/ai），由 provider 工厂注入。
type EinoAgent struct {
	agent *adk.ChatModelAgent
	model string
}

// NewEinoAgent 组装 eino ChatModelAgent。
// acfg 为 agent 元数据：Name/Description 是 adk 元数据标识
// （adk 要求 Name/Description 非空才能被 NewAgentTool 包装为子 agent 工具，
// 当前单 agent 场景不依赖，但规范填写为将来 multi-agent 铺路；由工厂兜底默认值）；
// SystemPrompt 保留字段但 P6-001 起通常为空（人格由 service 组装注入，见 EinoAgent 注释；
// 非空时仍会经 Instruction 前置，用于兜底/测试）；
// tools 为空时不注入 ToolsConfig（空 ToolsNodeConfig 行为未验证，不冒险）；
// MaxIterations 显式兜底 defaultMaxIterations。
func NewEinoAgent(ctx context.Context, cm model.BaseChatModel, tools []tool.BaseTool, acfg config.AgentConfig) (*EinoAgent, error) {
	cfg := &adk.ChatModelAgentConfig{
		Name:          acfg.Name,
		Description:   acfg.Description,
		Instruction:   acfg.SystemPrompt,
		Model:         cm,
		MaxIterations: defaultMaxIterations,
	}
	if len(tools) > 0 {
		cfg.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
		}
	}

	agent, err := adk.NewChatModelAgent(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("组装 ChatModelAgent 失败: %w", err)
	}
	return &EinoAgent{agent: agent}, nil
}

// Generate 将业务消息转换为 eino 消息并执行推理，返回最终文本回复。
// 事件迭代中第一个错误（含超时、tool 循环超限、工具执行失败）直接透传；
// 无任何模型输出消息时报错；有输出但 Content 为空 → 返回空串（P6 再议）。
// 每次调用记模型调用度量（架构 §17.2）：model、latency_ms、prompt/completion tokens
//（eino ResponseMeta.Usage，模型实现未返回时缺省）；失败也记 Warn（排障 AI 回复有依据）。
func (e *EinoAgent) Generate(ctx context.Context, msgs []entity.ChatMessage) (string, error) {
	// ctx 内派生 logger 携带 trace_id（会话键，架构 §17.6）：模型调用可在日志浏览页
	// 与同会话的消息入口/结局行串联；未注入时 From 回退全局。
	l := logger.From(ctx)
	start := time.Now()

	schemaMsgs, err := ToSchema(msgs)
	if err != nil {
		l.Warn("模型调用失败",
			logger.S("model", e.model),
			logger.I64("latency_ms", time.Since(start).Milliseconds()), logger.Err(err))
		return "", err
	}

	iter := e.agent.Run(ctx, &adk.AgentInput{Messages: schemaMsgs, EnableStreaming: false})

	var last *schema.Message
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			l.Warn("模型调用失败",
				logger.S("model", e.model),
				logger.I64("latency_ms", time.Since(start).Milliseconds()), logger.Err(ev.Err))
			return "", ev.Err
		}
		if ev.Output != nil && ev.Output.MessageOutput != nil && !ev.Output.MessageOutput.IsStreaming {
			last = ev.Output.MessageOutput.Message
		}
	}
	if last == nil {
		l.Warn("模型调用失败",
			logger.S("model", e.model),
			logger.I64("latency_ms", time.Since(start).Milliseconds()), logger.Err(errNoModelOutput))
		return "", errNoModelOutput
	}
	fields := []zap.Field{
		logger.S("model", e.model),
		logger.I64("latency_ms", time.Since(start).Milliseconds()),
	}
	if last.ResponseMeta != nil && last.ResponseMeta.Usage != nil {
		u := last.ResponseMeta.Usage
		fields = append(fields,
			logger.I("prompt_tokens", u.PromptTokens),
			logger.I("completion_tokens", u.CompletionTokens),
			logger.I("total_tokens", u.TotalTokens))
	}
	l.Info("模型调用", fields...)
	return FromSchema(last), nil
}
