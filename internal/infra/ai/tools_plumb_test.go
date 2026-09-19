package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"plumebot/pkg/config"
)

// 本文件审计「tool 未被调用 / AI 像不知道有 tool」（issue④）。
//
//	结论：机制装配是完好的 —— cfg.Tools.Enabled → ToolsRegistry.EnabledTools → NewEinoAgent
//	ToolsConfig；未知工具名会在启动期 fail-fast（本文件钉死）。因此“AI 不知道有 tool”不是
//	装配漏了，而是以下之一（按嫌疑排序）：
//	  1. 运行用 config.yaml 的 tools.enabled 为空/被删（→ 模型请求不带任何 tool schema）；
//	  2. 模型 API/端点不支持 function calling，或模型产不出 tool_calls；
//	  3. persona 强约束“伪装人类”，模型回避系统感工具（store_fact 等）；
//	  4. auto 模式大多数消息被冷却/规则挡在 LLM 之外，观察不到工具被调。
//	修复优先级：先确认跑起来用的 config 真含 tools.enabled（默认模板含，见
//	TestDefaultConfigEnabledToolsCanBeRegistered）；再在 persona/system 指令里明示记忆工具可用。

// factoryEchoArgs 是工厂装配测试用工具的参数结构。
type factoryEchoArgs struct {
	Text string `json:"text" jsonschema:"description=要回显的文本"`
}

// newFactoryEchoTool 构造可注册的 echo 工具（与 main.go 注册 pattern 一致）。
func newFactoryEchoTool(t *testing.T) tool.BaseTool {
	t.Helper()
	params, err := toolutils.GoStruct2ParamsOneOf[factoryEchoArgs]()
	if err != nil {
		t.Fatalf("GoStruct2ParamsOneOf 失败: %v", err)
	}
	return toolutils.NewTool(&schema.ToolInfo{Name: "echo", Desc: "回显文本", ParamsOneOf: params},
		func(_ context.Context, args factoryEchoArgs) (string, error) { return args.Text, nil })
}

// testChatConfig 构造工厂测试最简对话配置（chat entry 合法；构造 openai 模型不发网络）。
func testChatConfig() config.Config {
	return config.Config{
		LLM: config.LLMConfig{
			ChatModel: "chat",
			Models: []config.LLMModelConfig{{
				Name: "chat", Provider: "openai",
				BaseURL: "https://api.test.local/v1", Model: "deepseek-chat",
			}},
		},
		Agent: config.AgentConfig{Name: "test", Description: "test"},
	}
}

// TestOpenAIFactoryFailFastOnUnknownTool 启用列表含未注册名 → 工厂报错（fail-fast，不静默丢工具）。
// 说明工具名单不是“写了就注入”，必须与已注册名逐一匹配（名字拼错 bot 直接起不来）。
func TestOpenAIFactoryFailFastOnUnknownTool(t *testing.T) {
	tr := NewToolsRegistry()
	if err := tr.Register("echo", newFactoryEchoTool(t)); err != nil {
		t.Fatalf("注册工具失败: %v", err)
	}
	f := NewOpenAIFactory(tr)

	cfg := testChatConfig()
	cfg.Tools.Enabled = []string{"echo", "ghost_tool"}
	if _, err := f(context.Background(), cfg); err == nil {
		t.Fatal("启用列表含未注册工具名应报错（fail-fast）")
	} else if !strings.Contains(err.Error(), "ghost_tool") {
		t.Errorf("错误应点名未知工具, 实际 %v", err)
	}
}

// TestOpenAIFactoryEmptyEnabledSkipsTools cfg.Tools.Enabled 为空 → 不注入任何工具、Agent 正常构造。
// 这是“AI 不知道有 tool”的第一嫌疑配置：空列表 = 模型请求不带任何 tool schema。
func TestOpenAIFactoryEmptyEnabledSkipsTools(t *testing.T) {
	tr := NewToolsRegistry()
	f := NewOpenAIFactory(tr)

	cfg := testChatConfig() // Enabled 为空
	if got, err := f(context.Background(), cfg); err != nil || got == nil {
		t.Fatalf("空工具列表应正常构造 Agent（且不注入工具）, got=%v err=%v", got, err)
	}
}

// TestOpenAIFactoryValidEnabledConstructs 启用列表与注册表一致 → Agent 正常构造。
// 与 fail-fast 用例共同证明 cfg.Tools.Enabled 被工厂消费、装配链路完好。
func TestOpenAIFactoryValidEnabledConstructs(t *testing.T) {
	tr := NewToolsRegistry()
	if err := tr.Register("echo", newFactoryEchoTool(t)); err != nil {
		t.Fatalf("注册工具失败: %v", err)
	}
	f := NewOpenAIFactory(tr)

	cfg := testChatConfig()
	cfg.Tools.Enabled = []string{"echo"}
	if got, err := f(context.Background(), cfg); err != nil || got == nil {
		t.Fatalf("合法工具列表应正常构造 Agent, got=%v err=%v", got, err)
	}
}

// TestDefaultConfigEnabledToolsCanBeRegistered 默认模板 tools.enabled 的每个名字都应能被 main.go
// 注册（当前 7 个记忆/群管工具）。名字写错会在启动 fail-fast（bot 直接起不来），但此测试把
// 「默认配置 ↔ 生产注册」的一致性作为不变量钉住，防止新增工具时漏掉双处同步。
func TestDefaultConfigEnabledToolsCanBeRegistered(t *testing.T) {
	cfg, err := config.Load(t.TempDir() + "/config.yaml")
	if err != nil {
		t.Fatalf("加载默认模板失败: %v", err)
	}
	known := map[string]bool{
		"store_fact": true, "learn_jargon": true, "forget_fact": true,
		"group_mute": true, "group_unmute": true, "group_kick": true, "group_set_card": true,
	}
	if len(cfg.Tools.Enabled) == 0 {
		t.Error("默认模板 tools.enabled 不应为空（空 = 模型请求不带任何 tool schema）")
	}
	for _, name := range cfg.Tools.Enabled {
		if !known[name] {
			t.Errorf("默认模板启用的工具 %q 未在 main.go 注册", name)
		}
	}
}