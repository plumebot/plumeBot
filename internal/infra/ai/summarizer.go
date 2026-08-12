package ai

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"plumebot/internal/domain"
	"plumebot/pkg/config"
)

// 编译期校验：EinoSummarizer 实现 domain.Summarizer。
var _ domain.Summarizer = (*EinoSummarizer)(nil)

// EinoSummarizer 是基于原始 ChatModel 的 domain.Summarizer 实现。
// 与 EinoAgent 刻意分离：不注入人设 Instruction、不注册工具、无 ReAct 循环，
// 是单次"system + user → 文本"的原始模型调用（窗口压缩专用，行为可预测）。
type EinoSummarizer struct {
	cm model.BaseChatModel
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
	return &EinoSummarizer{cm: cm}, nil
}

// Summarize 执行一次摘要推理：system 为摘要指令，user 为待压缩文本，返回模型输出文本。
func (s *EinoSummarizer) Summarize(ctx context.Context, system, user string) (string, error) {
	msg, err := s.cm.Generate(ctx, []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(user),
	})
	if err != nil {
		return "", fmt.Errorf("摘要推理失败: %w", err)
	}
	return msg.Content, nil
}
