// PlumeBot 唯一入口。第二阶段：接入 ZeroBot 连接 NapCat，接收事件并分发。
package main

import (
	"context"
	"errors"
	"strconv"

	sdkplugin "github.com/plumebot/plumebot-sdk/plugin"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/handler"
	"plumebot/internal/infra/ai"
	"plumebot/internal/infra/ai/tools"
	"plumebot/internal/infra/imagecache"
	"plumebot/internal/infra/onebot"
	"plumebot/internal/infra/sqlite"
	"plumebot/internal/service/agent"
	"plumebot/internal/service/control"
	"plumebot/internal/service/event"
	"plumebot/internal/service/memory"
	"plumebot/internal/service/plugin"
	"plumebot/pkg/config"
	"plumebot/pkg/logger"
)

func main() {
	// 1. 加载配置
	cfg, err := config.Load("config.yaml")
	if err != nil {
		logger.Fatal("加载配置失败", logger.Err(err))
	}

	// 初始化日志
	logger.Init(logger.Config{Level: cfg.Log.Level})
	defer logger.Sync()

	// 2. 创建 infra 实现
	ctx := context.Background()
	// LLM 注册中心：本期仅注册 openai（OpenAI 兼容接口）provider；
	// 工具注册表：P3-004 起注册记忆更新工具（store_fact/learn_jargon/forget_fact），
	// 经 cfg.Tools.Enabled 决定是否注入 Agent。
	toolsRegistry := ai.NewToolsRegistry()
	llmRegistry := ai.NewRegistry()
	if err := llmRegistry.Register(config.DefaultLLMProvider, ai.NewOpenAIFactory(toolsRegistry)); err != nil {
		logger.Fatal("注册 LLM provider 失败", logger.Err(err))
	}
	// 数据根目录：SQLite 库 + base64:// 图片缓存（data/image_cache/）共用。
	dataDir := "data"
	storageInfra, err := sqlite.Open(dataDir)
	if err != nil {
		logger.Fatal("打开 SQLite 失败", logger.Err(err))
	}
	defer storageInfra.Close()
	// base64:// 入链图片缓存：内容级去重落盘，URL 存路径闭合（base64 不入库原则）。
	imgCache := imagecache.New(dataDir)

	// P3-004 记忆更新工具：Agent 对话中经 tool calling 写 SQLite（事实/黑话）。
	// 会话身份（群/用户）由 P6-002 消息管线经 ctx 注入，工具经 entity.SessionFrom 读取。
	memTools := tools.NewMemoryTools(storageInfra)
	if err := toolsRegistry.Register("store_fact", memTools.StoreFact()); err != nil {
		logger.Fatal("注册 store_fact 失败", logger.Err(err))
	}
	if err := toolsRegistry.Register("learn_jargon", memTools.LearnJargon()); err != nil {
		logger.Fatal("注册 learn_jargon 失败", logger.Err(err))
	}
	if err := toolsRegistry.Register("forget_fact", memTools.ForgetFact()); err != nil {
		logger.Fatal("注册 forget_fact 失败", logger.Err(err))
	}

	// P4-001 人格模板 + P6-001 运行时生效：
	// persona 模板按 cfg.Agent.Name 由 service/memory BuildMessages 每次组装现查注入 system 消息
	//（改 persona 表即时生效，无需重启）。此处仅启动 seed（默认 agent 无模板则固化当前生效人设）
	// 与计算 defaultPersona（组装兜底）；cfg.Agent.SystemPrompt 置空使 Instruction 为空，
	// 人格不再经 infra 注入（见 provider 工厂注释）。
	agentName := cfg.Agent.Name
	if agentName == "" {
		agentName = config.DefaultAgentName
	}
	personaFound := true
	if _, err := storageInfra.GetPersonaByAgent(ctx, agentName); errors.Is(err, domain.ErrNotFound) {
		personaFound = false
		prompt := cfg.Agent.SystemPrompt
		if prompt == "" {
			prompt = config.DefaultSystemPrompt
		}
		if _, err := storageInfra.InsertPersona(ctx, entity.Persona{Agent: agentName, Name: agentName, SystemPrompt: prompt}); err != nil {
			logger.Fatal("seed 默认人格模板失败", logger.Err(err))
		}
	} else if err != nil {
		logger.Fatal("加载人格模板失败", logger.Err(err))
	}
	// P6-001 审查 BUG-4：persona 表是人格唯一生效来源，config.agent.system_prompt 仅作兜底。
	// 启动时提示，避免用户改 config 后困惑「改了不生效」。
	if personaFound {
		logger.Info("人格来自 DB persona 表，由组装现查（改 persona 表即时生效；config.agent.system_prompt 仅作兜底）",
			logger.S("agent", agentName))
	} else {
		logger.Info("已 seed 默认人格模板，由组装现查（改 persona 表即时生效）", logger.S("agent", agentName))
	}

	// P6-001：defaultPersona 作为组装兜底传给 NewMemoryService；Instruction 置空（人格走组装）。
	defaultPersona := cfg.Agent.SystemPrompt
	if defaultPersona == "" {
		defaultPersona = config.DefaultSystemPrompt
	}
	cfg.Agent.SystemPrompt = ""

	agentInfra, err := llmRegistry.NewAgent(ctx, *cfg)
	if err != nil {
		logger.Fatal("初始化 LLM Agent 失败", logger.Err(err))
	}
	// 窗口压缩摘要器：与对话 Agent 分离（无人设、无工具，单次模型调用）。
	summarizerInfra, err := ai.NewSummarizer(ctx, *cfg)
	if err != nil {
		logger.Fatal("初始化摘要器失败", logger.Err(err))
	}
	// 阶段2 图片描述器：vision_model 配置时启用，P6-001 组装经 BuildMessages 惰性调用；
	// vision_model 为空 → 描述关闭（nil），图片只落 [图片] 占位。
	var describerInfra domain.MediaDescriber
	if _, ok := cfg.LLM.VisionEntry(); ok {
		describerInfra, err = ai.NewMediaDescriber(ctx, *cfg)
		if err != nil {
			logger.Fatal("初始化图片描述器失败", logger.Err(err))
		}
	} else if cfg.LLM.VisionModel != "" {
		// P6-001 审查 BUG-5：vision_model 填了但未命中 models 条目 → 描述静默关闭，显式告警。
		logger.Warn("llm.vision_model 已配置但未指向任何 models 条目，图片描述关闭（检查 models 列表的 name）",
			logger.S("vision_model", cfg.LLM.VisionModel))
	}
	// P4-002 插件系统：go-plugin 子进程（stdio）。service/plugin 经注入的工厂拉起插件进程，
	// 避免 service 直接依赖 SDK；接线来自 plugin-sdk（方案 A：宿主与插件共用独立 SDK module）。
	// 协议见架构 §8.6：宿主只校验指令集，不执行动作（回复发送见 P6-002）。
	pluginSvc := plugin.NewPluginService(func(exePath string) (plugin.PluginClient, error) {
		return sdkplugin.NewClient(exePath)
	})
	if err := pluginSvc.Discover("./plugins"); err != nil {
		logger.Fatal("插件发现失败", logger.Err(err))
	}
	defer pluginSvc.Close()

	// 3. 注入 service
	agentSvc := agent.NewAgentService(agentInfra)
	// P6-001：组装器注入（builder 携带组装预算 llm.prompt + persona 查询键 + 兜底人设）。
	memorySvc := memory.NewMemoryService(
		memory.NewWindow(), storageInfra, summarizerInfra,
		memory.BuilderConfig{
			Prompt:         cfg.LLM.Prompt,
			AgentName:      agentName,
			DefaultPersona: defaultPersona,
		},
		describerInfra,
	)
	// P5-001 触发控制：注入全局 cfg.Control.Mode（空 → mention 兜底）与存储（读 per-group group_config）。
	controlSvc := control.NewControlService(cfg.Control, storageInfra)
	// P6-002：注入 bot 自身 QQ 号（BuildMessages 的 assistant 角色映射 + bot 回复 UserID）。
	eventSvc := event.NewEventService(agentSvc, memorySvc, pluginSvc, controlSvc, cfg.Middleware, cfg.Bot.SelfID)
	if cfg.Bot.SelfID == "" {
		logger.Warn("bot.self_id 未配置：窗口内 bot 消息将渲染为 user 角色，assistant 角色映射失效")
	}

	// 4. 注入 handler
	msgHandler := handler.NewMessageHandler(eventSvc)
	noticeHandler := handler.NewNoticeHandler(eventSvc)

	// 5. 启动 onebot 连接（阻塞，ZeroBot 底层自动重连）
	client := onebot.New(cfg.Onebot, cfg.Log.Level, msgHandler, noticeHandler, imgCache)
	if cfg.Bot.Name == "" {
		cfg.Bot.Name = config.DefaultBotName // 展示用兜底
	}
	if cfg.Onebot.WsURL == "" {
		cfg.Onebot.WsURL = config.DefaultWsURL // 与 onebot.New 内部兜底保持一致，保证日志显示真实连接地址
	}
	chat, _ := cfg.LLM.ChatEntry() // 已由 NewAgent 校验过非空；兜底空条目避免日志 panic
	logger.Info("PlumeBot 启动，正在连接 NapCat",
		logger.S("name", cfg.Bot.Name),
		logger.S("ws_url", cfg.Onebot.WsURL),
		logger.S("llm_provider", entryProvider(chat)),
		logger.S("llm_model", chat.Model),
		logger.S("llm_base_url", entryBaseURL(chat)),
		logger.S("llm_api_key_set", strconv.FormatBool(chat.APIKey != "")), // 只标记是否配置，绝不打印密钥本身
		logger.S("vision_model", cfg.LLM.VisionModel),
	)
	client.Run()
}

// entryProvider 返回条目实际生效的 provider 名（与 Registry.NewAgent 的兜底一致）。
func entryProvider(e config.LLMModelConfig) string {
	if e.Provider == "" {
		return config.DefaultLLMProvider
	}
	return e.Provider
}

// entryBaseURL 返回条目实际生效的 base_url（与 openai 工厂的兜底一致，保证日志显示真实端点）。
func entryBaseURL(e config.LLMModelConfig) string {
	if e.BaseURL == "" {
		return config.DefaultOpenAIBaseURL
	}
	return e.BaseURL
}
