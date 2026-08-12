// PlumeBot 唯一入口。第二阶段：接入 ZeroBot 连接 NapCat，接收事件并分发。
package main

import (
	"context"
	"errors"
	"strconv"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/handler"
	"plumebot/internal/infra/ai"
	"plumebot/internal/infra/ai/tools"
	"plumebot/internal/infra/onebot"
	"plumebot/internal/infra/plugin_exe"
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
	storageInfra, err := sqlite.Open("data")
	if err != nil {
		logger.Fatal("打开 SQLite 失败", logger.Err(err))
	}
	defer storageInfra.Close()

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

	// P4-001 人格模板：按 cfg.Agent.Name 加载 DB 人格模板，其 system_prompt 覆盖
	// config.agent.system_prompt 后交给 provider 工厂（工厂对空值兜底 DefaultSystemPrompt）。
	// 默认 agent 无模板则 seed 一条默认模板（固化当前生效人设，之后改 DB 重启生效）。
	agentName := cfg.Agent.Name
	if agentName == "" {
		agentName = config.DefaultAgentName
	}
	persona, err := storageInfra.GetPersonaByAgent(ctx, agentName)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		prompt := cfg.Agent.SystemPrompt
		if prompt == "" {
			prompt = config.DefaultSystemPrompt
		}
		if _, err := storageInfra.InsertPersona(ctx, entity.Persona{Agent: agentName, Name: agentName, SystemPrompt: prompt}); err != nil {
			logger.Fatal("seed 默认人格模板失败", logger.Err(err))
		}
		cfg.Agent.SystemPrompt = prompt
	case err != nil:
		logger.Fatal("加载人格模板失败", logger.Err(err))
	case persona.SystemPrompt != "":
		// 模板非空才覆盖；空模板（如人工清空）保留 config 值，兜底链仍成立。
		cfg.Agent.SystemPrompt = persona.SystemPrompt
	}

	agentInfra, err := llmRegistry.NewAgent(ctx, *cfg)
	if err != nil {
		logger.Fatal("初始化 LLM Agent 失败", logger.Err(err))
	}
	// 窗口压缩摘要器：与对话 Agent 分离（无人设、无工具，单次模型调用）。
	summarizerInfra, err := ai.NewSummarizer(ctx, *cfg)
	if err != nil {
		logger.Fatal("初始化摘要器失败", logger.Err(err))
	}
	// P4-002 插件系统：go-plugin 子进程（stdio）。service/plugin 经注入的工厂拉起插件进程，
	// 避免 service 直接依赖 infra。协议见架构 §8.6：只定义协议 + 宿主校验，不执行回复/动作。
	pluginSvc := plugin.NewPluginService(func(exePath string) (plugin.PluginClient, error) {
		return plugin_exe.NewClient(exePath)
	})
	if err := pluginSvc.Discover("./plugins"); err != nil {
		logger.Fatal("插件发现失败", logger.Err(err))
	}
	defer pluginSvc.Close()

	// 3. 注入 service
	agentSvc := agent.NewAgentService(agentInfra)
	memorySvc := memory.NewMemoryService(memory.NewWindow(), storageInfra, summarizerInfra)
	controlSvc := control.NewControlService(control.Nop())
	eventSvc := event.NewEventService(agentSvc, memorySvc, pluginSvc, controlSvc, cfg.Middleware)

	// 4. 注入 handler
	msgHandler := handler.NewMessageHandler(eventSvc)
	noticeHandler := handler.NewNoticeHandler(eventSvc)

	// 5. 启动 onebot 连接（阻塞，ZeroBot 底层自动重连）
	client := onebot.New(cfg.Onebot, cfg.Log.Level, msgHandler, noticeHandler)
	if cfg.Bot.Name == "" {
		cfg.Bot.Name = config.DefaultBotName // 展示用兜底
	}
	if cfg.Onebot.WsURL == "" {
		cfg.Onebot.WsURL = config.DefaultWsURL // 与 onebot.New 内部兜底保持一致，保证日志显示真实连接地址
	}
	logger.Info("PlumeBot 启动，正在连接 NapCat",
		logger.S("name", cfg.Bot.Name),
		logger.S("ws_url", cfg.Onebot.WsURL),
		logger.S("llm_provider", llmProviderName(cfg)),
		logger.S("llm_model", cfg.LLM.OpenAI.Model),
		logger.S("llm_base_url", llmBaseURL(cfg)),
		logger.S("llm_api_key_set", strconv.FormatBool(cfg.LLM.OpenAI.APIKey != "")), // 只标记是否配置，绝不打印密钥本身
	)
	client.Run()
}

// llmProviderName 返回实际生效的 provider 名（与 Registry.NewAgent 的兜底一致）。
func llmProviderName(cfg *config.Config) string {
	if cfg.LLM.Provider == "" {
		return config.DefaultLLMProvider
	}
	return cfg.LLM.Provider
}

// llmBaseURL 返回实际生效的 base_url（与 openai 工厂的兜底一致，保证日志显示真实端点）。
func llmBaseURL(cfg *config.Config) string {
	if cfg.LLM.OpenAI.BaseURL == "" {
		return config.DefaultOpenAIBaseURL
	}
	return cfg.LLM.OpenAI.BaseURL
}
