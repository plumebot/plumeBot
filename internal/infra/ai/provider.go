package ai

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"plumebot/internal/domain"
	"plumebot/pkg/config"
)

// Factory 根据完整配置构建 domain.Agent（LLM 映射 + 工具组装 + Agent 封装）。
// 接收完整 config.Config：LLM 配置在 LLM 段，工具启用列表在 Tools 段，二者组装点在此。
type Factory func(ctx context.Context, cfg config.Config) (domain.Agent, error)

// Registry 是 LLM provider 注册中心（注入式实例，无全局状态）。
type Registry struct {
	factories map[string]Factory
}

// NewRegistry 创建空注册中心。
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register 注册 provider 工厂；重名返回错误（防止静默覆盖）。
func (r *Registry) Register(name string, f Factory) error {
	if _, ok := r.factories[name]; ok {
		return fmt.Errorf("provider %q 已注册", name)
	}
	r.factories[name] = f
	return nil
}

// NewAgent 按对话模型条目（cfg.LLM.ChatModel 引用 name，空 → models[0]）的 provider 分发构建 Agent。
// provider 空 → DefaultLLMProvider；未注册 → 错误（错误信息含已注册列表）。
func (r *Registry) NewAgent(ctx context.Context, cfg config.Config) (domain.Agent, error) {
	entry, err := chatEntry(cfg)
	if err != nil {
		return nil, err
	}
	name := entry.Provider
	if name == "" {
		name = config.DefaultLLMProvider
	}

	f, ok := r.factories[name]
	if !ok {
		return nil, fmt.Errorf("未知 LLM provider %q（已注册：%s）", name, registeredProviders(r.factories))
	}
	return f(ctx, cfg)
}

// registeredProviders 返回已注册 provider 名的排序、逗号分隔列表（用于错误提示）。
func registeredProviders(m map[string]Factory) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// NewOpenAIFactory 创建 openai（OpenAI 兼容接口）provider 工厂。
// 依赖工具注册表，通过闭包注入：按 cfg.Tools.Enabled 过滤启用工具。
// 兜底语义（消费方）：base_url 空 → DefaultOpenAIBaseURL；model 空 → 构造期报错（不可猜测）；
// timeout_seconds ≤0 → DefaultLLMTimeoutSeconds；api_key 空透传；Temperature 转 *float32；
// MaxTokens >0 → MaxCompletionTokens（*int，不用已废弃的 MaxTokens 字段）；
// agent 三要素兜底：name 空 → DefaultAgentName，description 空 → DefaultAgentDescription，
// system_prompt 空 → DefaultSystemPrompt（均经 Instruction/元数据注入 EinoAgent）。
func NewOpenAIFactory(tr *ToolsRegistry) Factory {
	return func(ctx context.Context, cfg config.Config) (domain.Agent, error) {
		entry, err := chatEntry(cfg)
		if err != nil {
			return nil, err
		}
		cm, err := buildModelFromEntry(ctx, entry, cfg.LLM.TimeoutSeconds)
		if err != nil {
			return nil, err
		}

		tools, err := tr.EnabledTools(cfg.Tools.Enabled)
		if err != nil {
			return nil, err
		}
		// agent 三要素兜底（消费方兜底原则）：空值 → 默认常量，避免默认值漂移。
		acfg := cfg.Agent
		if acfg.Name == "" {
			acfg.Name = config.DefaultAgentName
		}
		if acfg.Description == "" {
			acfg.Description = config.DefaultAgentDescription
		}
		if acfg.SystemPrompt == "" {
			acfg.SystemPrompt = config.DefaultSystemPrompt
		}
		return NewEinoAgent(ctx, cm, tools, acfg)
	}
}

// chatEntry 解析对话模型条目：cfg.LLM.ChatModel 引用 name，空 → models[0]。
// 空列表 / 引用未命中统一报错，供 NewAgent、工厂与 NewSummarizer 复用。
func chatEntry(cfg config.Config) (config.LLMModelConfig, error) {
	entry, ok := cfg.LLM.ChatEntry()
	if !ok {
		return config.LLMModelConfig{}, errors.New("llm.chat_model 未指向任何 models 条目（需至少配置一个模型条目）")
	}
	return entry, nil
}

// buildModelFromEntry 依据单个模型条目构造 OpenAI 兼容 ChatModel
// （供对话 Agent、摘要器与图片描述器复用）。
// 兜底语义：base_url 空 → DefaultOpenAIBaseURL；model 空 → 报错（空值不可猜测）；
// timeout_seconds ≤0 → DefaultLLMTimeoutSeconds；api_key 空透传；
// Temperature 转 *float32；MaxTokens >0 → MaxCompletionTokens。
func buildModelFromEntry(ctx context.Context, e config.LLMModelConfig, timeoutSeconds int) (model.BaseChatModel, error) {
	baseURL := e.BaseURL
	if baseURL == "" {
		baseURL = config.DefaultOpenAIBaseURL
	}
	if e.Model == "" {
		return nil, fmt.Errorf("llm.models[%s].model 未配置（空值不可猜测，请在 config.yaml 中填写模型名）", e.Name)
	}
	timeout := timeoutSeconds
	if timeout <= 0 {
		timeout = config.DefaultLLMTimeoutSeconds
	}

	var temperature *float32
	if e.Temperature != nil {
		t := float32(*e.Temperature)
		temperature = &t
	}
	var maxTokens *int
	if e.MaxTokens > 0 {
		mt := e.MaxTokens
		maxTokens = &mt
	}

	cm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:             baseURL,
		APIKey:              e.APIKey,
		Model:               e.Model,
		Timeout:             time.Duration(timeout) * time.Second,
		Temperature:         temperature,
		MaxCompletionTokens: maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("构造 openai ChatModel 失败: %w", err)
	}
	return cm, nil
}
