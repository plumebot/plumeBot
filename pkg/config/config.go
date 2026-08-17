// Package config 负责加载和解析 config.yaml 配置文件。
package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// defaultConfigYAML 是嵌入的默认配置模板，配置文件缺失时写入磁盘。
// 注意：模板与仓库根目录 config.yaml 各自独立，新增配置字段时需同步两处；
// config.yaml 已 .gitignore（含用户真实密钥，不入库）。
//
//go:embed config.default.yaml
var defaultConfigYAML []byte

// EnvSelfID 是 bot.self_id 的环境变量名：非空时覆盖 config.yaml 的 bot.self_id。
// 与 NapCat 登录的 QQ 号一致时，可经 docker-compose 的 ACCOUNT 共用同一来源（见 .env.example）。
const EnvSelfID = "PLUMEBOT_SELFID"

// EnvModelAPIKeyPrefix 是模型条目 api_key 的环境变量名前缀。
// 完整变量名 = EnvModelAPIKeyPrefix + 模型 name 大写，如 name=chat → PLUMEBOT_APIKEY_CHAT。
// 非空时覆盖该模型条目在 config.yaml 的 api_key（敏感密钥不入配置文件）；每个模型条目
// 独立读取自己的变量（vision 等同样支持）；变量名未设置或模型未命名（name 空）则不覆盖。
const EnvModelAPIKeyPrefix = "PLUMEBOT_APIKEY_"

// ModelAPIKeyEnvVar 返回指定模型条目 api_key 对应的环境变量名（模型名统一大写）。
// 例：name=chat → PLUMEBOT_APIKEY_CHAT。
func ModelAPIKeyEnvVar(name string) string {
	return EnvModelAPIKeyPrefix + strings.ToUpper(name)
}

// 默认值常量：供各消费包在字段为空时兜底，避免默认值字符串在多处漂移。
const (
	DefaultBotName = "PlumeBot"
	DefaultWsURL   = "ws://127.0.0.1:3001"

	// Agent 默认值（消费方兜底，P2-004 阶段 5 起由 infra/ai 消费）。
	DefaultAgentName        = "PlumeBot"                      // agent.name 空 → 默认标识
	DefaultAgentDescription = "PlumeBot：QQ 群聊 AI 机器人，负责对话回复。" // agent.description 空 → 默认描述
	// DefaultSystemPrompt 是机器人系统提示词的占位文案（P6-001 起作为 persona 兜底人设：
	// persona 模板 → config.agent.system_prompt（defaultPersona）→ 本默认值，由 service/memory
	// 组装 personaText 与 main 启动 seed 消费，勿删）。
	DefaultSystemPrompt = "你是 PlumeBot，一个活跃在 QQ 群聊中的 AI 赛博群友。你语气自然友好，用简体中文回复，内容简洁，贴合聊天语境。"

	// LLM 接入默认值（阶段 4 起由 infra/ai 消费，空值/非法值在消费方兜底）。
	DefaultLLMProvider       = "openai"                    // provider 空 → openai
	DefaultOpenAIBaseURL     = "https://api.openai.com/v1" // base_url 空 → 默认端点
	DefaultLLMTimeoutSeconds = 60                          // timeout_seconds ≤0 → 60

	// 触发控制状态规则默认值（P5-002 起由 service/control 消费，字段 0/空 时兜底；
	// 与 config.default.yaml control.state 的推荐值一致，改动需双处同步）。
	DefaultEnergyMax         = 100     // energy_max 空 → 100
	DefaultEnergyCost        = 10      // energy_cost 空 → 10
	DefaultEnergyRecover     = 5       // energy_recover 空 → 5（点/分钟）
	DefaultEnergyThreshold   = 20      // energy_threshold 空 → 20
	DefaultCooldownSeconds   = 60      // cooldown_seconds 空 → 60
	DefaultConsecutiveLimit  = 5       // consecutive_limit 空 → 5
	DefaultRestSeconds       = 300     // rest_seconds 空 → 300
	DefaultQuietHoursStart   = "23:00" // quiet_hours_start 空 → "23:00"
	DefaultQuietHoursEnd     = "07:00" // quiet_hours_end 空 → "07:00"
	DefaultShortMessageChars = 4       // short_message_chars 空 → 4

	// Prompt 组装预算/上限默认值（P6-001，架构 §6，B-014/B-027；字段 ≤0 时消费方兜底；
	// 与 config.default.yaml llm.prompt 的推荐值一致，改动需双处同步）。
	DefaultFactsPerMember       = 3  // facts_per_member ≤0 → 3（窗口内每成员事实条数上限）
	DefaultJargonCap            = 20 // jargon_cap ≤0 → 20（confirmed 黑话条数上限）
	DefaultDescribeRecentRounds = 5  // describe_recent_rounds ≤0 → 5（只描述窗口最近 N 轮消息的图片）
	DefaultDescribePerTurnCap   = 10 // describe_per_turn_cap ≤0 → 10（每条消息图片描述条数上限）
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

// ControlConfig 包含触发控制配置（P5-001 mode；P5-002 状态规则参数）。
type ControlConfig struct {
	// Mode 触发模式：mention | auto；空 → mention 兜底（消费方）。
	Mode string `mapstructure:"mode"`
	// State 状态规则参数全局默认值（P5-002）。0/空 → 代码默认常量兜底（消费方）；
	// per-group group_config 列非 0/非空 → 覆盖此处。
	State ControlStateConfig `mapstructure:"state"`
}

// ControlStateConfig 是状态规则参数（架构 §9.2）的全局默认值。
// 各字段 0/空 = 未配置，由 service/control 引用下方 DefaultEnergyMax 等默认常量兜底；
// 每个参数可被 group_config 对应列 per-group 覆盖。
type ControlStateConfig struct {
	EnergyMax         int    `mapstructure:"energy_max"`          // 精力上限；0 → DefaultEnergyMax
	EnergyCost        int    `mapstructure:"energy_cost"`         // 每次回复消耗；0 → DefaultEnergyCost
	EnergyRecover     int    `mapstructure:"energy_recover"`      // 精力恢复（点/分钟）；0 → DefaultEnergyRecover
	EnergyThreshold   int    `mapstructure:"energy_threshold"`    // 低于此值不主动说话（@ 除外）；0 → DefaultEnergyThreshold
	CooldownSeconds   int    `mapstructure:"cooldown_seconds"`    // 两次主动回复最小间隔（秒）；0 → DefaultCooldownSeconds
	ConsecutiveLimit  int    `mapstructure:"consecutive_limit"`   // 连续回复上限；0 → DefaultConsecutiveLimit
	RestSeconds       int    `mapstructure:"rest_seconds"`        // 连续达上限强制休息时长（秒）；0 → DefaultRestSeconds
	QuietHoursStart   string `mapstructure:"quiet_hours_start"`   // 深夜静默起 "HH:MM"；空 → DefaultQuietHoursStart
	QuietHoursEnd     string `mapstructure:"quiet_hours_end"`     // 深夜静默止 "HH:MM"；空 → DefaultQuietHoursEnd
	ShortMessageChars int    `mapstructure:"short_message_chars"` // 短消息忽略阈值（<N 字）；0 → DefaultShortMessageChars
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
	// Prompt 是 prompt 组装（BuildMessages，P6-001）的上限/预算参数。
	Prompt PromptConfig `mapstructure:"prompt"`
}

// PromptConfig 是 prompt 组装（P6-001，架构 §6）的注入上限/预算参数。
// 字段 ≤0 = 未配置，由 service/memory 按上方 DefaultFactsPerMember 等默认常量兜底（消费方兜底纪律）。
type PromptConfig struct {
	FactsPerMember       int `mapstructure:"facts_per_member"`       // 窗口内每成员事实条数上限；≤0 → DefaultFactsPerMember（B-014）
	JargonCap            int `mapstructure:"jargon_cap"`             // confirmed 黑话条数上限；≤0 → DefaultJargonCap（B-014）
	DescribeRecentRounds int `mapstructure:"describe_recent_rounds"` // 只描述窗口最近 N 轮消息的图片；≤0 → DefaultDescribeRecentRounds（B-027）
	DescribePerTurnCap   int `mapstructure:"describe_per_turn_cap"`  // 每条消息图片描述条数上限；≤0 → DefaultDescribePerTurnCap（B-027）
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
	// SystemPrompt 系统提示词（P6-001 起不再经 Instruction 注入，作为组装兜底 defaultPersona，
	// 由 service/memory BuildMessages 在 persona 模板未命中/空时使用）；空 → DefaultSystemPrompt。
	SystemPrompt string `mapstructure:"system_prompt"`
}

// Load 从 path 加载 YAML 配置文件。
// 配置文件不存在时，将嵌入的默认配置（config.default.yaml）写入 path 后再加载。
// 字段空值/缺省不做 Go 侧兜底，由各消费包自行处理默认值。
// 唯一例外（仅敏感/机器相关字段的环境变量覆盖，非通用 env 读取）：
//   - 每个模型条目的 api_key 支持环境变量 PLUMEBOT_APIKEY_<模型名大写> 覆盖（如 PLUMEBOT_APIKEY_CHAT，
//     非空时优先于文件值，敏感密钥不入配置文件；name 空的条目不参与）；
//   - bot.self_id 支持环境变量 EnvSelfID 覆盖（NapCat 登录 QQ 号与 self_id 共用同一来源时配合 docker-compose）。
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

	// 敏感字段环境变量覆盖：仅当对应环境变量非空时生效，空值保留文件配置。
	// - 模型条目 api_key：每个条目独立读取 PLUMEBOT_APIKEY_<模型名大写>（name 空的条目不参与，
	//   无法推导稳定变量名）。
	// - bot.self_id：读取 EnvSelfID，非空时覆盖（与 NapCat 登录 QQ 号共用同一来源）。
	for i := range cfg.LLM.Models {
		m := &cfg.LLM.Models[i]
		if m.Name == "" {
			continue
		}
		if key := os.Getenv(ModelAPIKeyEnvVar(m.Name)); key != "" {
			m.APIKey = key
		}
	}
	if selfID := os.Getenv(EnvSelfID); selfID != "" {
		cfg.Bot.SelfID = selfID
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
