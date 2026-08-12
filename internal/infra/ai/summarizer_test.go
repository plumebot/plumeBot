package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"plumebot/pkg/config"
)

// EinoSummarizer 直接调用底层 ChatModel：system+user 原样透传，无人设、无工具。
func TestEinoSummarizerSummarizeRoundtrip(t *testing.T) {
	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage(`{"summary":"本周热点","keywords":["游戏"],"decisions":[]}`, nil),
	}}
	s := &EinoSummarizer{cm: fake}

	got, err := s.Summarize(context.Background(), "摘要指令", "待压缩的对话文本")
	if err != nil {
		t.Fatalf("Summarize 失败: %v", err)
	}
	if got != `{"summary":"本周热点","keywords":["游戏"],"decisions":[]}` {
		t.Errorf("返回值 = %q", got)
	}

	in := fake.inputs[0]
	if len(in) != 2 || in[0].Role != schema.System || in[1].Role != schema.User {
		t.Fatalf("模型应收到 system+user 两条消息, 实际 %+v", in)
	}
	if in[0].Content != "摘要指令" || in[1].Content != "待压缩的对话文本" {
		t.Errorf("system/user 未原样透传: %q / %q", in[0].Content, in[1].Content)
	}
}

// 模型错误应透传，不吞错。
func TestEinoSummarizerPropagatesError(t *testing.T) {
	s := &EinoSummarizer{cm: &fakeChatModel{}} // 空脚本 → 首次调用即报错
	_, err := s.Summarize(context.Background(), "指令", "内容")
	if err == nil {
		t.Fatal("模型报错应透传，实际无错误")
	}
	if !strings.Contains(err.Error(), "脚本用尽") {
		t.Errorf("错误应包含模型原始信息, 实际 %q", err.Error())
	}
}

// NewSummarizer 构造：chat 条目缺失 / model 空 → 报错；model 配置 → 返回可用摘要器。
func TestNewSummarizerConfig(t *testing.T) {
	if _, err := NewSummarizer(context.Background(), config.Config{}); err == nil {
		t.Error("models 为空时应报错")
	}

	cfg := config.Config{
		LLM: config.LLMConfig{
			Models:    []config.LLMModelConfig{{Name: "chat", Model: "test-model"}},
			ChatModel: "chat",
		},
	}
	sum, err := NewSummarizer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("model 配置时构造失败: %v", err)
	}
	if sum == nil {
		t.Error("构造结果不应为 nil")
	}
}
