package ai

import (
	"context"
	"strings"
	"testing"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
)

// recordFactory 记录收到的配置并返回预设 agent，用于验证分发逻辑。
func recordFactory(agentName string, gotCfg *config.Config) Factory {
	return func(_ context.Context, cfg config.Config) (domain.Agent, error) {
		*gotCfg = cfg
		return &stubAgent{name: agentName}, nil
	}
}

// stubAgent 是最小 domain.Agent 实现（测试替身）。
type stubAgent struct {
	name string
}

func (s *stubAgent) Name() string { return s.name }

func (s *stubAgent) Generate(_ context.Context, _ []entity.ChatMessage) (string, error) {
	return s.name, nil
}

func TestRegistryDispatch(t *testing.T) {
	r := NewRegistry()
	var got config.Config
	if err := r.Register("fake_a", recordFactory("A", &got)); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	if err := r.Register("fake_b", recordFactory("B", &got)); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	cfg := config.Config{LLM: config.LLMConfig{
		Models:    []config.LLMModelConfig{{Name: "chat", Provider: "fake_b"}},
		ChatModel: "chat",
	}}
	agent, err := r.NewAgent(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewAgent 失败: %v", err)
	}
	if agent.(*stubAgent).Name() != "B" {
		t.Errorf("分发的 agent = %q，期望 fake_b 的工厂产物", agent.(*stubAgent).Name())
	}
	entry, _ := got.LLM.ChatEntry()
	if entry.Provider != "fake_b" {
		t.Errorf("工厂收到的 chat 条目 provider = %q，期望原样透传", entry.Provider)
	}
}

func TestRegistryUnknownProvider(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("fake", recordFactory("F", &config.Config{})); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	_, err := r.NewAgent(context.Background(), config.Config{LLM: config.LLMConfig{
		Models:    []config.LLMModelConfig{{Name: "chat", Provider: "nope"}},
		ChatModel: "chat",
	}})
	if err == nil {
		t.Fatal("未知 provider 应报错")
	}
	if !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "已注册：fake") {
		t.Errorf("错误应含 provider 名与已注册列表，实际 %q", err.Error())
	}
}

// provider 为空 → 兜底 DefaultLLMProvider（openai）。
func TestRegistryEmptyProviderDefaults(t *testing.T) {
	r := NewRegistry()
	var got config.Config
	if err := r.Register(config.DefaultLLMProvider, recordFactory("O", &got)); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	cfg := config.Config{LLM: config.LLMConfig{
		Models:    []config.LLMModelConfig{{Name: "chat"}}, // provider 空
		ChatModel: "chat",
	}}
	agent, err := r.NewAgent(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewAgent 失败: %v", err)
	}
	if agent.(*stubAgent).Name() != "O" {
		t.Errorf("空 provider 应分发到 openai 工厂，实际 %q", agent.(*stubAgent).Name())
	}
}

// chat_model 未指向任何条目 → 构造报错。
func TestRegistryEmptyModels(t *testing.T) {
	r := NewRegistry()
	_, err := r.NewAgent(context.Background(), config.Config{}) // Models 为空
	if err == nil || !strings.Contains(err.Error(), "llm.chat_model") {
		t.Errorf("Models 为空应报错，实际 %v", err)
	}
}

func TestRegistryDuplicateRegister(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("fake", recordFactory("F", &config.Config{})); err != nil {
		t.Fatalf("首次注册失败: %v", err)
	}
	err := r.Register("fake", recordFactory("F2", &config.Config{}))
	if err == nil || !strings.Contains(err.Error(), "已注册") {
		t.Errorf("重名注册应报错，实际 %v", err)
	}
}

func fullLLMCfg() config.Config {
	temp := 0.7
	return config.Config{
		LLM: config.LLMConfig{
			TimeoutSeconds: 30,
			Models: []config.LLMModelConfig{{
				Name:        "chat",
				Provider:    "openai",
				BaseURL:     "https://api.deepseek.com/v1",
				APIKey:      "sk-test",
				Model:       "deepseek-chat",
				Temperature: &temp,
				MaxTokens:   512,
			}},
			ChatModel: "chat",
		},
	}
}

// openai 工厂构造：完整配置 → NewChatModel + NewEinoAgent（不发网络）。
func TestOpenAIFactoryConstruct(t *testing.T) {
	f := NewOpenAIFactory(NewToolsRegistry())

	agent, err := f(context.Background(), fullLLMCfg())
	if err != nil {
		t.Fatalf("工厂执行失败: %v", err)
	}
	if _, ok := agent.(*EinoAgent); !ok {
		t.Errorf("工厂应产出 *EinoAgent，实际 %T", agent)
	}
}

// agent.system_prompt 为空 → 兜底 DefaultSystemPrompt 注入 Instruction（构造成功即证明走了兜底路径）。
func TestOpenAIFactoryPromptFallback(t *testing.T) {
	f := NewOpenAIFactory(NewToolsRegistry())
	cfg := fullLLMCfg()
	cfg.Agent = config.AgentConfig{}

	if _, err := f(context.Background(), cfg); err != nil {
		t.Fatalf("agent.system_prompt 空应兜底默认人设，实际报错: %v", err)
	}
}

// agent.name/description 为空 → 兜底默认常量（构造成功即证明元数据被接受）。
func TestOpenAIFactoryAgentMetaFallback(t *testing.T) {
	f := NewOpenAIFactory(NewToolsRegistry())
	cfg := fullLLMCfg()
	cfg.Agent = config.AgentConfig{}

	if _, err := f(context.Background(), cfg); err != nil {
		t.Fatalf("agent.name/description 空应兜底默认值，实际报错: %v", err)
	}
}

// model 为空 → 构造期报错（不可猜测）。
func TestOpenAIFactoryModelEmpty(t *testing.T) {
	f := NewOpenAIFactory(NewToolsRegistry())
	cfg := fullLLMCfg()
	cfg.LLM.Models[0].Model = ""

	_, err := f(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "llm.models[chat].model") {
		t.Errorf("model 空应报错并提示配置项，实际 %v", err)
	}
}

// base_url 为空 → 兜底默认端点（构造成功即证明走了默认值路径）。
func TestOpenAIFactoryBaseURLFallback(t *testing.T) {
	f := NewOpenAIFactory(NewToolsRegistry())
	cfg := fullLLMCfg()
	cfg.LLM.Models[0].BaseURL = ""

	if _, err := f(context.Background(), cfg); err != nil {
		t.Fatalf("base_url 空应兜底默认值，实际报错: %v", err)
	}
}

// tools.enabled 含未注册工具 → 构造期报错（含已注册列表）。
func TestOpenAIFactoryUnknownTool(t *testing.T) {
	f := NewOpenAIFactory(NewToolsRegistry())
	cfg := fullLLMCfg()
	cfg.Tools = config.ToolsConfig{Enabled: []string{"nope"}}

	_, err := f(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "未注册") {
		t.Errorf("未注册工具应报错，实际 %v", err)
	}
}
