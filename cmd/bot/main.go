// PlumeBot 唯一入口。第二阶段：接入 ZeroBot 连接 NapCat，接收事件并分发。
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	sdkplugin "github.com/plumebot/plumebot-sdk/plugin"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/handler"
	"plumebot/internal/handler/web"
	"plumebot/internal/infra/ai"
	"plumebot/internal/infra/ai/tools"
	"plumebot/internal/infra/imagecache"
	"plumebot/internal/infra/logfile"
	"plumebot/internal/infra/onebot"
	"plumebot/internal/infra/sqlite"
	"plumebot/internal/service/admin"
	"plumebot/internal/service/agent"
	"plumebot/internal/service/control"
	"plumebot/internal/service/event"
	logsvc "plumebot/internal/service/log"
	"plumebot/internal/service/memory"
	"plumebot/internal/service/plugin"
	"plumebot/pkg/config"
	"plumebot/pkg/jwt"
	"plumebot/pkg/logger"
)

func main() {
	// 初始化日志（先以默认 info 级别）：使配置加载阶段（模板写入、env 覆盖）的日志可见，
	// 且配置加载失败时 Fatal 能落盘/打终端。读到 log.level 后再经 SetLevel 应用（下方）。
	logger.Init(logger.Config{})
	defer logger.Sync()

	// 1. 加载配置
	cfg, err := config.Load("config.yaml")
	if err != nil {
		logger.Fatal("加载配置失败", logger.Err(err))
	}
	logger.SetLevel(cfg.Log.Level)

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
	// B-015 群管理动作工具：能力经 ctx 注入的 GroupManager 执行（per-event），
	// 工具自身无依赖；护栏（per-group 开关 + 管理员校验）在 onebot 实现内集中把关，
	// per-group 开关 group_config.group_mgmt_enabled 默认 1 开（0 = 显式关闭）。
	gmTools := tools.NewGroupTools()
	if err := toolsRegistry.Register("group_mute", gmTools.GroupMute()); err != nil {
		logger.Fatal("注册 group_mute 失败", logger.Err(err))
	}
	if err := toolsRegistry.Register("group_unmute", gmTools.GroupUnmute()); err != nil {
		logger.Fatal("注册 group_unmute 失败", logger.Err(err))
	}
	if err := toolsRegistry.Register("group_kick", gmTools.GroupKick()); err != nil {
		logger.Fatal("注册 group_kick 失败", logger.Err(err))
	}
	if err := toolsRegistry.Register("group_set_card", gmTools.GroupSetCard()); err != nil {
		logger.Fatal("注册 group_set_card 失败", logger.Err(err))
	}
	logger.Info("工具注册完成",
		logger.S("registered", strings.Join(toolsRegistry.Names(), ",")),
		logger.I("enabled", len(cfg.Tools.Enabled)))

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
	if _, err := storageInfra.GetPersonaByAgent(ctx, agentName); errors.Is(err, entity.ErrNotFound) {
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
	chat, _ := cfg.LLM.ChatEntry() // 已由 NewAgent 校验过非空；兜底空条目避免日志 panic
	logger.Info("对话 Agent 已构建", logger.S("agent", agentName), logger.S("model", chat.Model))
	// 窗口压缩摘要器：与对话 Agent 分离（无人设、无工具，单次模型调用）。
	summarizerInfra, err := ai.NewSummarizer(ctx, *cfg)
	if err != nil {
		logger.Fatal("初始化摘要器失败", logger.Err(err))
	}
	logger.Info("摘要器已构建", logger.S("model", chat.Model))
	// 阶段2 图片描述器：vision_model 配置时启用，P6-001 组装经 BuildMessages 惰性调用；
	// vision_model 为空 → 描述关闭（nil），图片只落 [图片] 占位。
	var describerInfra domain.MediaDescriber
	if vision, ok := cfg.LLM.VisionEntry(); ok {
		describerInfra, err = ai.NewMediaDescriber(ctx, *cfg)
		if err != nil {
			logger.Fatal("初始化图片描述器失败", logger.Err(err))
		}
		logger.Info("图片描述器已构建", logger.S("model", vision.Model))
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
		logger.Warn("插件发现失败", logger.Err(err))
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
	// P6-002：注入 bot 自身 QQ 号（BuildMessages 的 assistant 角色映射 + bot 回复 UserID）
	// 与展示名（bot 自身回复的 SenderName，空由 EventService 兜底 DefaultBotName）。
	eventSvc := event.NewEventService(agentSvc, memorySvc, pluginSvc, controlSvc, cfg.Middleware, cfg.Bot.SelfID, cfg.Bot.Name)
	if cfg.Bot.SelfID == "" {
		logger.Warn("bot.self_id 未配置：窗口内 bot 消息将渲染为 user 角色，assistant 角色映射失效")
	}

	// 4. 注入 handler
	msgHandler := handler.NewMessageHandler(eventSvc)
	noticeHandler := handler.NewNoticeHandler(eventSvc)

	// 5. 创建 onebot 连接（ZeroBot 底层自动重连；实际启动见 5.2——goroutine 方式，
	// 主流程让位信号等待以实现优雅关闭）。
	client := onebot.New(cfg.Onebot, cfg.Log.Level, msgHandler, noticeHandler, imgCache, storageInfra, cfg.Bot.SelfID)
	if cfg.Bot.Name == "" {
		cfg.Bot.Name = config.DefaultBotName // 展示用兜底
	}
	if cfg.Onebot.WsURL == "" {
		cfg.Onebot.WsURL = config.DefaultWsURL // 与 onebot.New 内部兜底保持一致，保证日志显示真实连接地址
	}
	logger.Info("PlumeBot 启动，正在连接 NapCat",
		logger.S("name", cfg.Bot.Name),
		logger.S("ws_url", cfg.Onebot.WsURL),
		logger.S("llm_provider", entryProvider(chat)),
		logger.S("llm_model", chat.Model),
		logger.S("llm_base_url", entryBaseURL(chat)),
		logger.S("llm_api_key_set", strconv.FormatBool(chat.APIKey != "")), // 只标记是否配置，绝不打印密钥本身
		logger.S("vision_model", cfg.LLM.VisionModel),
	)
	// 5.1 web 服务（P7-001 升级为配置管理控制台）：
	//   - admin.enabled=true：jwt secret 解析 → admin service → handler/web.NewRouter（/ping + /api/v1 + 前端页）
	//     → 端口扫描监听（admin.port 起，被占用逐次 +1，扫描至 MaxAdminPort；全失败仅告警返回 nil）；
	//   - false：回退仅健康检查 /ping（newWebServer，硬编码 127.0.0.1:8080）。
	// ListenAndServe 放 goroutine，运行错误（非主动关闭）仅告警——web 是辅助服务，不拖垮 bot 核心。
	var web *http.Server
	if cfg.Admin.Enabled {
		web = startAdminWeb(*cfg, dataDir, storageInfra, memorySvc)
	} else {
		web = newWebServer(strconv.Itoa(cfg.Admin.Port))
		go func() {
			logger.Info("web 服务启动", logger.S("addr", strconv.Itoa(cfg.Admin.Port)))
			if err := web.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("web 服务异常退出", logger.Err(err))
			}
		}()
	}

	// 5.2 启动 onebot 连接（goroutine）：ZeroBot 无官方优雅停止 API
	//（RunAndBlock 阻塞在无限重连循环），停止由 5.3 信号路径 return main 后
	// 进程退出终止其内部 goroutine，本进程资源经 defer 链清理。
	go client.Run()

	// 5.3 阻塞等待退出信号（Ctrl+C / SIGTERM），收到后优雅关闭：
	// 先 http.Server.Shutdown 排空 web 在途请求，再 return 触发既有 defer 链
	//（pluginSvc.Close → storageInfra.Close → logger.Sync；注册顺序保证 LIFO 正确，
	// 且 panic 路径同样有清理保障）。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	sig := <-quit
	logger.Info("收到退出信号，开始优雅关闭", logger.S("signal", sig.String()))
	shutdownCtx, cancel := context.WithTimeout(context.Background(), webShutdownTimeout)
	defer cancel()
	if web != nil { // 全端口占用时 startAdminWeb 返回 nil，无可关闭的 web
		if err := web.Shutdown(shutdownCtx); err != nil {
			logger.Warn("web 服务优雅关闭超时/失败", logger.Err(err))
		}
	}
	logger.Info("web 服务已关闭，清理剩余资源（插件 → 存储 → 日志）")
	// 恢复默认信号处理：关闭过程中再次 Ctrl+C 将直接终止进程（防卡死安全网）。
	signal.Stop(quit)

}

// httpAddr 是 web 服务监听地址（admin.enabled=false 时的仅 /ping 回退；硬编码不进配置）。
// webShutdownTimeout 是 web 服务优雅关闭的等待上限（通常瞬时完成）。
const (
	webShutdownTimeout = 5 * time.Second
)

// startAdminWeb 构建并启动管理后端 web 服务（P7-001）：
// JWT secret 解析 → admin service → 日志查询 service（infra/logfile 读 zap JSON 日志文件，
// 架构 §17.6）→ handler/web.NewRouter → 端口扫描监听。
// 返回的 *http.Server 供优雅关闭；全端口占用时返回 nil（打破 shutdown 判空）。
func startAdminWeb(cfg config.Config, dataDir string, store domain.Storage, memSvc *memory.MemoryService) *http.Server {
	secret, err := resolveAdminJWTSecret(dataDir, cfg.Admin.JWTSecret)
	if err != nil {
		logger.Fatal("解析 admin JWT 密钥失败", logger.Err(err))
	}
	ttl := time.Duration(cfg.Admin.TokenTTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = time.Duration(config.DefaultAdminTokenTTLSeconds) * time.Second
	}
	port := cfg.Admin.Port
	if port <= 0 {
		port = config.DefaultAdminPort
	}
	mgr := jwt.NewManager(secret, ttl)
	adminSvc := admin.NewService(store, memSvc, mgr)
	// 日志浏览依赖：读取 pkg/logger 实际生效目录（~/.plumebot/logs）下的 JSON 日志文件。
	logDir := logger.Dir()
	logSvc := logsvc.New(logfile.New(logDir))
	logger.Info("日志浏览已启用", logger.S("logs_dir", logDir))
	return newAdminWebServer(web.NewRouter(adminSvc, logSvc, mgr), port)
}

// resolveAdminJWTSecret 解析管理后端 JWT 密钥：
// 显式配置非空直接使用；否则读 data/admin_jwt_secret（存在即用，跨重启 token 保持有效），
// 不存在则生成 32 字节随机密钥写回（0600）。密钥绝不打印。
func resolveAdminJWTSecret(dataDir, configured string) (string, error) {
	if configured != "" {
		logger.Info("admin JWT 密钥来自配置（jwt_secret）")
		return configured, nil
	}
	path := filepath.Join(dataDir, "admin_jwt_secret")
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		logger.Info("admin JWT 密钥复用已持久化文件", logger.S("path", path))
		return strings.TrimSpace(string(b)), nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(b)
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		return "", err
	}
	// 只记生命周期与路径，密钥本身绝不入日志（架构 §17.5）。
	logger.Info("admin JWT 密钥已生成并持久化（0600）", logger.S("path", path))
	return secret, nil
}

// newAdminWebServer 在 startPort~MaxAdminPort 内寻找可用端口并启动 gin 服务（回环绑定）。
// 端口被占用逐次 +1；全范围失败仅告警并返回 nil（bot 核心继续运行，沿「web 辅助不拖垮」语义）。
func newAdminWebServer(handler http.Handler, startPort int) *http.Server {
	for port := startPort; port <= config.MaxAdminPort; port++ {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		l, err := net.Listen("tcp", addr)
		if err != nil {
			logger.Warn("admin web 端口被占用，尝试下一个", logger.S("addr", addr), logger.Err(err))
			continue
		}
		srv := &http.Server{Addr: addr, Handler: handler}
		go func() {
			if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("admin web 服务异常退出", logger.Err(err))
			}
		}()
		logger.Info("admin web 服务启动", logger.S("addr", addr))
		return srv
	}
	logger.Warn("admin web 未启动：端口范围被占用",
		logger.I("start_port", startPort), logger.I("max_port", config.MaxAdminPort))
	return nil
}

// newWebServer 构建仅含健康检查 /ping 的 gin web 服务。
// gin.New + 自定义中间件（web 包提供）：访问日志经 logger.GinAccessWriter 落 logs/gin.log、
// panic 经 zap 记 error.log——**不写 stdout/stderr**，健康轮询不再刷终端（架构 §17.1，
// 修正此前「注释称避免刷屏却仍 gin.Logger 打 stdout」的矛盾）。
// 仅作进程存活探针，不暴露任何管理/业务端点。
func newWebServer(addr string) *http.Server {
	r := gin.New()
	r.Use(web.Recovery())
	r.Use(web.AccessLogger())
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})
	return &http.Server{Addr: addr, Handler: r}
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
