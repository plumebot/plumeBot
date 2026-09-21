package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	"plumebot/internal/domain"
	"plumebot/pkg/config"
	"plumebot/pkg/logger"
)

// 编译期校验：EinoSummarizer 实现 domain.Summarizer。
var _ domain.Summarizer = (*EinoSummarizer)(nil)

// EinoSummarizer 是基于原始 ChatModel 的 domain.Summarizer 实现。
// 与 EinoAgent 刻意分离：不注入人设 Instruction、不注册工具、无 ReAct 循环，
// 是单次"system + user → 文本"的原始模型调用（窗口压缩专用，行为可预测）。
// model 为对话模型名（调用度量日志用，复用对话模型条目）。
type EinoSummarizer struct {
	cm    model.BaseChatModel
	model string
}

// NewSummarizer 依据对话模型条目构建摘要器（与对话 Agent 复用同一 buildModelFromEntry 构造）。
func NewSummarizer(ctx context.Context, cfg config.Config) (domain.Summarizer, error) {
	entry, err := chatEntry(cfg)
	if err != nil {
		return nil, err
	}
	cm, err := buildModelFromEntry(ctx, entry, cfg.LLM.TimeoutSeconds)
	if err != nil {
		return nil, err
	}
	return &EinoSummarizer{cm: cm, model: entry.Model}, nil
}

// Summarize 执行一次摘要推理：system 为摘要指令，user 为待压缩文本，返回模型输出文本。
// 记模型调用度量（架构 §17.2 infra/ai）：model、latency_ms、prompt/completion tokens。
func (s *EinoSummarizer) Summarize(ctx context.Context, system, user string) (string, error) {
	start := time.Now()
	msg, err := s.cm.Generate(ctx, []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(user),
	})
	fields := []zap.Field{
		logger.S("model", s.model),
		logger.I64("latency_ms", time.Since(start).Milliseconds()),
	}
	if err != nil {
		fields = append(fields, logger.Err(err))
		logger.Warn("模型调用失败", fields...)
		return "", fmt.Errorf("摘要推理失败: %w", err)
	}
	if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
		u := msg.ResponseMeta.Usage
		fields = append(fields,
			logger.I("prompt_tokens", u.PromptTokens),
			logger.I("completion_tokens", u.CompletionTokens),
			logger.I("total_tokens", u.TotalTokens))
	}
	logger.Info("模型调用", fields...)
	return msg.Content, nil
}
