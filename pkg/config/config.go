// Package config 负责加载和解析 config.yaml 配置文件。
package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// defaultConfigYAML 是嵌入的默认配置模板，配置文件缺失时写入磁盘。
// 注意：模板与仓库根目录 config.yaml 各自独立，新增配置字段时需同步两处；
// config.yaml 已 .gitignore（含用户真实密钥，不入库）。
//
//go:embed config.default.yaml
var defaultConfigYAML []byte

// EnvLLMAPIKey 是 LLM API key 的环境变量名。
// 优先级高于 config.yaml 中 chat_model 条目的 api_key：环境变量非空时覆盖文件值，
// 便于将密钥放入启动脚本/系统环境而非配置文件，避免误提交。vision 条目密钥由文件提供。
const EnvLLMAPIKey = "PLUMEBOT_API_KEY"

// 默认值常量：供各消费包在字段为空时兜底，避免默认值字符串在多处漂移。
const (
	DefaultBotName = "PlumeBot"
	DefaultWsURL   = "ws://127.0.0.1:3001"

	// Agent 默认值（消费方兜底，P2-004 阶段 5 起由 infra/ai 消费）。
	DefaultAgentName        = "PlumeBot"                      // agent.name 空 → 默认标识
	DefaultAgentDescription = "PlumeBot：QQ 群聊 AI 机器人，负责对话回复。" // agent.description 空 → 默认描述
	// DefaultSystemPrompt 是机器人系统提示词的占位文案（人设占位，P4 人格系统前使用）。
	DefaultSystemPrompt = "你是 PlumeBot，一个活跃在 QQ 群聊中的 AI 赛博群友。你语气自然友好，用简体中文回复，内容简洁，贴合聊天语境。"

	// LLM 接入默认值（阶段 4 起由 infra/ai 消费，空值/非法值在消费方兜底）。
	DefaultLLMProvider       = "openai"                    // provider 空 → openai
	DefaultOpenAIBaseURL     = "https://api.openai.com/v1" // base_url 空 → 默认端点
	DefaultLLMTimeoutSeconds = 60                          // timeout_seconds ≤0 → 60
)

// Config 是应用程序的根配置结构体。
type Config struct {
	Bot        BotConfig        `mapstructure:"bot"`
	Onebot     OnebotConfig     `mapstructure:"onebot"`
	Log        LogConfig        `mapstructure:"log"`
	Control    ControlConfig    `mapstructure:"control"`
	Middleware MiddlewareConfig `mapstructure:"middleware"`
	LLM        LLMConfig        `mapstructure:"llm"`
	Tools      ToolsConfig      `mapstructure:"tools"`
	Agent      AgentConfig      `mapstructure:"agent"`
}

// BotConfig 包含 bot 基础信息。
type BotConfig struct {
	Name   string `mapstructure:"name"`
	SelfID string `mapstructure:"self_id"`
}

// OnebotConfig 包含 OneBot 连接配置（正向 WebSocket）。
type OnebotConfig struct {
	WsURL       string `mapstructure:"ws_url"`       // NapCat WebSocket 服务器地址
	AccessToken string `mapstructure:"access_token"` // 访问令牌，NapCat 未设置时为空
}

// LogConfig 包含日志配置。
type LogConfig struct {
	Level string `mapstructure:"level"` // debug, info, warn, error
}

// ControlConfig 包含触发控制配置。
type ControlConfig struct {
	Mode string `mapstructure:"mode"` // mention, auto
}

// MiddlewareConfig 包含消息管线中间件配置。
type MiddlewareConfig struct {
	RateLimit      RateLimitConfig `mapstructure:"rate_limit"`
	SensitiveWords []string        `mapstructure:"sensitive_words"` // 敏感词表；命中回复「我拒绝回答」，空数组 = 不过滤
}

// RateLimitConfig 包含消息限流（令牌桶）配置。
type RateLimitConfig struct {
	Rate           float64 `mapstructure:"rate"`             // 令牌补充速率（个/秒）
	Burst          int     `mapstructure:"burst"`            // 桶容量，允许的突发消息数
	MaxWaitSeconds int     `mapstructure:"max_wait_seconds"` // 等待令牌的上限秒数，超时后回复固定消息并丢弃
}

// LLMConfig 包含 LLM 接入配置（阶段 4 由 infra/ai 消费）。
// 空值/非法值不做 Go 侧改写，由消费方按 §阶段 2.3 语义兜底。
type LLMConfig struct {
	// TimeoutSeconds 单次 LLM 调用超时（秒）；≤0 → DefaultLLMTimeoutSeconds。
	TimeoutSeconds int `mapstructure:"timeout_seconds"`
	// NativeMultimodal 原生多模态模式（阶段 3 占位，默认关，本轮无消费者）。
	NativeMultimodal bool `mapstructure:"native_multimodal"`
	// Models 模型条目数组，条目内 Provider 为工厂选择键（当前仅 openai 兼容工厂）。
	Models []LLMModelConfig `mapstructure:"models"`
	// ChatModel 引用 Models[].name 指定对话模型；空 → models[0]（消费方兜底）。
	ChatModel string `mapstructure:"chat_model"`
	// VisionModel 引用 Models[].name 指定图片描述模型；空 = 描述关闭。
	VisionModel string `mapstructure:"vision_model"`
}

// LLMModelConfig 是单个模型条目的端点配置（OpenAI 兼容接口）。
type LLMModelConfig struct {
	// Name 用户取名，供 chat_model / vision_model 引用（非硬编码角色）。
	Name string `mapstructure:"name"`
	// Provider 供应商名（工厂选择键），本期仅支持 openai（任意 OpenAI 兼容接口）；空 → DefaultLLMProvider。
	Provider string `mapstructure:"provider"`
	// BaseURL 兼容端点（如 https://api.deepseek.com/v1）；空 → DefaultOpenAIBaseURL。
	BaseURL string `mapstructure:"base_url"`
	// APIKey 密钥；本地模型（如 Ollama）可留空，透传不设。
	APIKey string `mapstructure:"api_key"`
	// Model 模型名；空 → 构造期报错（不可猜测，禁止兜底）。
	Model string `mapstructure:"model"`
	// Temperature 采样温度；nil → 不传该参数（使用模型默认）。
	Temperature *float64 `mapstructure:"temperature"`
	// MaxTokens 最大输出 token 数；≤0 → 不传该参数。
	MaxTokens int `mapstructure:"max_tokens"`
}

// ByName 按 name 查找模型条目；name 为空返回 models[0]。
// 未命中返回 ok=false。只读解析助手，不改写字段（「chat_model 空→models[0]」策略唯一实现点）。
func (c LLMConfig) ByName(name string) (LLMModelConfig, bool) {
	if name == "" {
		if len(c.Models) == 0 {
			return LLMModelConfig{}, false
		}
		return c.Models[0], true
	}
	for _, m := range c.Models {
		if m.Name == name {
			return m, true
		}
	}
	return LLMModelConfig{}, false
}

// ChatEntry 返回对话模型条目：ChatModel 引用 name，空 → models[0]。
func (c LLMConfig) ChatEntry() (LLMModelConfig, bool) {
	return c.ByName(c.ChatModel)
}

// VisionEntry 返回视觉模型条目：VisionModel 引用 name；空 → (零值, false)（描述关闭）。
func (c LLMConfig) VisionEntry() (LLMModelConfig, bool) {
	if c.VisionModel == "" {
		return LLMModelConfig{}, false
	}
	return c.ByName(c.VisionModel)
}

// ToolsConfig 包含工具（function calling）配置。
type ToolsConfig struct {
	// Enabled 启用的工具名列表；空 = 不注册任何工具（仅机制，P3-004 起注册具体工具）。
	Enabled []string `mapstructure:"enabled"`
}

// AgentConfig 包含 agent 元数据与人设配置（P2-004 阶段 5 起由 infra/ai 消费）。
// 未来多 agent：演进为 agents 列表 + active 选择（每个 agent 一份三要素），
// 本段为当前唯一启用 agent 的配置；空值/非法值由消费方按默认常量兜底。
type AgentConfig struct {
	// Name agent 标识（adk 元数据，multi-agent 路由用，与 bot.name 展示名语义不同）；空 → DefaultAgentName。
	Name string `mapstructure:"name"`
	// Description 能力描述（adk 元数据，multi-agent 场景供其他 agent 判断是否转移任务）；空 → DefaultAgentDescription。
	Description string `mapstructure:"description"`
	// SystemPrompt 系统提示词（经 ChatModelAgentConfig.Instruction 注入，固定人设）；空 → DefaultSystemPrompt。
	SystemPrompt string `mapstructure:"system_prompt"`
}

// Load 从 path 加载 YAML 配置文件。
// 配置文件不存在时，将嵌入的默认配置（config.default.yaml）写入 path 后再加载。
// 字段空值/缺省不做 Go 侧兜底，由各消费包自行处理默认值。
// 唯一例外：chat_model 条目的 api_key 支持环境变量 EnvLLMAPIKey 覆盖（非空时优先于文件值，
// 敏感密钥不入配置文件；vision 条目密钥由文件提供）。
func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeDefault(path); err != nil {
			return nil, err
		}
	}

	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	// 敏感字段环境变量覆盖：仅当环境变量非空时生效，空值保留文件配置。
	// 数组化后限定作用于 chat_model 条目（vision 条目密钥由文件提供）。
	if key := os.Getenv(EnvLLMAPIKey); key != "" {
		if entry, ok := cfg.LLM.ChatEntry(); ok {
			for i := range cfg.LLM.Models {
				if cfg.LLM.Models[i].Name == entry.Name {
					cfg.LLM.Models[i].APIKey = key
					break
				}
			}
		}
	}

	return &cfg, nil
}

// writeDefault 将嵌入的默认配置写入 path（自动创建父目录）。
func writeDefault(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建配置目录失败: %w", err)
		}
	}
	if err := os.WriteFile(path, defaultConfigYAML, 0o644); err != nil {
		return fmt.Errorf("写入默认配置文件 %s 失败: %w", path, err)
	}
	return nil
}
