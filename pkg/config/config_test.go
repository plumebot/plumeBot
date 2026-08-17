package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// 配置文件不存在时：写入嵌入的默认配置，且解析出的值来自默认 yaml。
func TestLoadMissingFileWritesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	// 默认配置已写入磁盘
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("默认配置未写入: %v", err)
	}
	if string(content) != string(defaultConfigYAML) {
		t.Fatal("写入的默认配置与嵌入模板不一致")
	}

	// 解析结果应包含默认值（来自 yaml，而非 Go 侧兜底）
	if cfg.Bot.Name != "PlumeBot" {
		t.Errorf("Bot.Name = %q，期望 %q", cfg.Bot.Name, "PlumeBot")
	}
	if cfg.Onebot.WsURL != "ws://127.0.0.1:3001" {
		t.Errorf("Onebot.WsURL = %q，期望默认地址", cfg.Onebot.WsURL)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q，期望 %q", cfg.Log.Level, "info")
	}
	if cfg.Control.Mode != "mention" {
		t.Errorf("Control.Mode = %q，期望 %q", cfg.Control.Mode, "mention")
	}
	if cfg.Middleware.RateLimit.Rate != 2 || cfg.Middleware.RateLimit.Burst != 20 ||
		cfg.Middleware.RateLimit.MaxWaitSeconds != 10 {
		t.Errorf("限流默认值 = %+v，期望 rate=2/burst=20/max_wait=10",
			cfg.Middleware.RateLimit)
	}
}

// 配置文件已存在时：不做 Go 侧兜底，空值/非法值原样保留（由消费包处理）。
func TestLoadExistingFileNoFallback(t *testing.T) {
	path := writeTempConfig(t,
		"bot:\n  name: \"\"\n"+
			"onebot:\n  ws_url: \"\"\n"+
			"middleware:\n  rate_limit:\n    rate: 0\n    burst: 0\n    max_wait_seconds: 0\n"+
			"llm:\n  timeout_seconds: 0\n  models:\n    - name: \"\"\n      provider: \"\"\n      base_url: \"\"\n      model: \"\"\n      max_tokens: 0\n  chat_model: \"\"\n  vision_model: \"\"\n"+
			"agent:\n  name: \"\"\n  description: \"\"\n  system_prompt: \"\"\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	if cfg.Bot.Name != "" {
		t.Errorf("Bot.Name 空值不应被改写，实际 %q", cfg.Bot.Name)
	}
	if cfg.Onebot.WsURL != "" {
		t.Errorf("Onebot.WsURL 空值不应被改写，实际 %q", cfg.Onebot.WsURL)
	}
	if got := cfg.Middleware.RateLimit.Rate; got != 0 {
		t.Errorf("Rate 零值不应被改写，实际 %v", got)
	}
	if got := cfg.Middleware.RateLimit.Burst; got != 0 {
		t.Errorf("Burst 零值不应被改写，实际 %v", got)
	}
	if got := cfg.Middleware.RateLimit.MaxWaitSeconds; got != 0 {
		t.Errorf("MaxWaitSeconds 零值不应被改写，实际 %v", got)
	}
	// LLM/Tools/Agent 空值同样不做 Go 侧改写
	if cfg.LLM.TimeoutSeconds != 0 {
		t.Errorf("LLM.TimeoutSeconds 零值不应被改写，实际 %v", cfg.LLM.TimeoutSeconds)
	}
	if len(cfg.LLM.Models) != 1 || cfg.LLM.Models[0].BaseURL != "" ||
		cfg.LLM.Models[0].Model != "" || cfg.LLM.Models[0].MaxTokens != 0 {
		t.Errorf("LLM.Models 空值不应被改写，实际 %+v", cfg.LLM.Models)
	}
	if cfg.LLM.ChatModel != "" || cfg.LLM.VisionModel != "" {
		t.Errorf("ChatModel/VisionModel 空值不应被改写，实际 %q/%q", cfg.LLM.ChatModel, cfg.LLM.VisionModel)
	}
	if cfg.Agent.Name != "" || cfg.Agent.Description != "" || cfg.Agent.SystemPrompt != "" {
		t.Errorf("Agent 空值不应被改写，实际 %+v", cfg.Agent)
	}
}

// 配置文件已存在时：合法值原样保留。
func TestLoadExistingFilePreservesValues(t *testing.T) {
	path := writeTempConfig(t,
		"bot:\n  name: TestBot\n"+
			"middleware:\n  rate_limit:\n    rate: 5\n    burst: 30\n    max_wait_seconds: 15\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	if cfg.Bot.Name != "TestBot" {
		t.Errorf("Bot.Name = %q，期望 %q", cfg.Bot.Name, "TestBot")
	}
	if got := cfg.Middleware.RateLimit.Rate; got != 5 {
		t.Errorf("Rate = %v，期望 5", got)
	}
	if got := cfg.Middleware.RateLimit.Burst; got != 30 {
		t.Errorf("Burst = %v，期望 30", got)
	}
	if got := cfg.Middleware.RateLimit.MaxWaitSeconds; got != 15 {
		t.Errorf("MaxWaitSeconds = %v，期望 15", got)
	}
}

// LLM/Tools/Agent 各字段的合法值解析，temperature 以指针形式保留。
func TestLoadLLMToolsAgentValues(t *testing.T) {
	// 隔离宿主环境：本用例断言文件中的 api_key 原样保留，不受用户环境变量影响。
	t.Setenv(ModelAPIKeyEnvVar("chat"), "")
	t.Setenv(ModelAPIKeyEnvVar("vision"), "")
	t.Setenv(EnvSelfID, "")

	path := writeTempConfig(t,
		"llm:\n"+
			"  timeout_seconds: 30\n"+
			"  models:\n"+
			"    - name: chat\n"+
			"      provider: openai\n"+
			"      base_url: https://api.deepseek.com/v1\n"+
			"      api_key: sk-test-123\n"+
			"      model: deepseek-chat\n"+
			"      temperature: 0.7\n"+
			"      max_tokens: 512\n"+
			"    - name: vision\n"+
			"      provider: openai\n"+
			"      base_url: https://api.vision.com/v1\n"+
			"      api_key: sk-vision\n"+
			"      model: qwen-vl-plus\n"+
			"  chat_model: chat\n"+
			"  vision_model: vision\n"+
			"tools:\n"+
			"  enabled:\n"+
			"    - tool_a\n"+
			"    - tool_b\n"+
			"agent:\n"+
			"  name: TestAgent\n"+
			"  description: 测试描述\n"+
			"  system_prompt: 测试人设\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	if cfg.LLM.TimeoutSeconds != 30 {
		t.Errorf("LLM.TimeoutSeconds = %v，期望 30", cfg.LLM.TimeoutSeconds)
	}
	chat, ok := cfg.LLM.ChatEntry()
	if !ok || chat.BaseURL != "https://api.deepseek.com/v1" || chat.APIKey != "sk-test-123" ||
		chat.Model != "deepseek-chat" {
		t.Fatalf("chat 条目解析不符: %+v ok=%v", chat, ok)
	}
	if chat.Temperature == nil {
		t.Fatal("chat.Temperature 应为指针（已配置 0.7）")
	}
	if *chat.Temperature != 0.7 {
		t.Errorf("chat.Temperature = %v，期望 0.7", *chat.Temperature)
	}
	if chat.MaxTokens != 512 {
		t.Errorf("chat.MaxTokens = %v，期望 512", chat.MaxTokens)
	}
	vision, ok := cfg.LLM.VisionEntry()
	if !ok || vision.APIKey != "sk-vision" || vision.Model != "qwen-vl-plus" {
		t.Errorf("vision 条目解析不符: %+v ok=%v", vision, ok)
	}
	if len(cfg.Tools.Enabled) != 2 || cfg.Tools.Enabled[0] != "tool_a" || cfg.Tools.Enabled[1] != "tool_b" {
		t.Errorf("Tools.Enabled = %v，期望 [tool_a tool_b]", cfg.Tools.Enabled)
	}
	if cfg.Agent.Name != "TestAgent" {
		t.Errorf("Agent.Name = %q，期望 TestAgent", cfg.Agent.Name)
	}
	if cfg.Agent.Description != "测试描述" {
		t.Errorf("Agent.Description = %q，期望 测试描述", cfg.Agent.Description)
	}
	if cfg.Agent.SystemPrompt != "测试人设" {
		t.Errorf("Agent.SystemPrompt = %q，期望 测试人设", cfg.Agent.SystemPrompt)
	}
}

// 模型条目 api_key 环境变量（PLUMEBOT_APIKEY_<模型名大写>）非空时覆盖文件值；
// 每个模型条目独立读取自己的变量（chat/vision 各读各的），未被覆盖的条目保留文件值。
func TestLoadEnvModelAPIKeyOverrides(t *testing.T) {
	path := writeTempConfig(t,
		"llm:\n  models:\n    - name: chat\n      api_key: sk-file\n      model: some-model\n    - name: vision\n      api_key: sk-vision\n      model: qwen-vl-plus\n  chat_model: chat\n  vision_model: vision\n")
	t.Setenv(ModelAPIKeyEnvVar("chat"), "sk-env-chat")
	t.Setenv(ModelAPIKeyEnvVar("vision"), "sk-env-vision")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	chat, _ := cfg.LLM.ChatEntry()
	if chat.APIKey != "sk-env-chat" {
		t.Errorf("chat 条目 APIKey = %q，期望环境变量值 sk-env-chat", chat.APIKey)
	}
	vision, _ := cfg.LLM.VisionEntry()
	if vision.APIKey != "sk-env-vision" {
		t.Errorf("vision 条目 APIKey = %q，期望环境变量值 sk-env-vision", vision.APIKey)
	}
}

// 环境变量名为 PLUMEBOT_APIKEY_<模型名大写>：变量名只与模型 name 相关，与 chat_model 引用无关。
func TestModelAPIKeyEnvVarName(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"chat", "PLUMEBOT_APIKEY_CHAT"},
		{"vision", "PLUMEBOT_APIKEY_VISION"},
		{"deepseek-chat", "PLUMEBOT_APIKEY_DEEPSEEK-CHAT"},
	} {
		if got := ModelAPIKeyEnvVar(tc.name); got != tc.want {
			t.Errorf("ModelAPIKeyEnvVar(%q) = %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

// 环境变量为空时视为未设置，保留文件中的 api_key。
func TestLoadEnvModelAPIKeyEmptyKeepsFile(t *testing.T) {
	path := writeTempConfig(t,
		"llm:\n  models:\n    - name: chat\n      api_key: sk-file\n      model: some-model\n  chat_model: chat\n")
	t.Setenv(ModelAPIKeyEnvVar("chat"), "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	chat, _ := cfg.LLM.ChatEntry()
	if chat.APIKey != "sk-file" {
		t.Errorf("chat 条目 APIKey = %q，期望保留文件值 sk-file", chat.APIKey)
	}
}

// bot.self_id 环境变量 PLUMEBOT_SELFID 非空时覆盖文件值（与 NapCat 登录 QQ 号共用同一来源）。
func TestLoadEnvSelfIDOverrides(t *testing.T) {
	path := writeTempConfig(t, "bot:\n  self_id: \"1527301260\"\n")
	t.Setenv(EnvSelfID, "10001")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if cfg.Bot.SelfID != "10001" {
		t.Errorf("Bot.SelfID = %q，期望环境变量值 10001", cfg.Bot.SelfID)
	}
}

// PLUMEBOT_SELFID 为空时视为未设置，保留文件中的 self_id。
func TestLoadEnvSelfIDEmptyKeepsFile(t *testing.T) {
	path := writeTempConfig(t, "bot:\n  self_id: \"1527301260\"\n")
	t.Setenv(EnvSelfID, "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if cfg.Bot.SelfID != "1527301260" {
		t.Errorf("Bot.SelfID = %q，期望保留文件值 1527301260", cfg.Bot.SelfID)
	}
}

// temperature 缺省时指针应为 nil（消费方据此不传该参数）。
func TestLoadTemperatureNilWhenAbsent(t *testing.T) {
	path := writeTempConfig(t,
		"llm:\n  models:\n    - name: chat\n      model: some-model\n  chat_model: chat\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	chat, _ := cfg.LLM.ChatEntry()
	if chat.Temperature != nil {
		t.Errorf("Temperature 缺省应为 nil，实际 %v", *chat.Temperature)
	}
}

// 未知字段（顶层或嵌套）应被忽略，不影响解析。
func TestLoadUnknownFieldsIgnored(t *testing.T) {
	path := writeTempConfig(t,
		"unknown_top: 1\n"+
			"llm:\n  chat_model: chat\n  unknown_nested: hello\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("未知字段不应导致加载失败: %v", err)
	}
	if cfg.LLM.ChatModel != "chat" {
		t.Errorf("LLM.ChatModel = %q，期望 chat", cfg.LLM.ChatModel)
	}
}

// 路径父目录不存在时，写入默认配置应自动创建目录。
func TestLoadMissingFileCreatesParentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "nested")
	path := filepath.Join(dir, "config.yaml")

	if _, err := Load(path); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("默认配置应写入嵌套目录: %v", err)
	}
}

// 嵌入的默认配置应包含全部配置节（与 Config 结构对应）。
func TestDefaultYAMLHasAllSections(t *testing.T) {
	content := string(defaultConfigYAML)
	for _, section := range []string{"bot:", "onebot:", "log:", "control:", "middleware:", "rate_limit:", "llm:", "models:", "chat_model:", "vision_model:", "prompt:", "native_multimodal:", "tools:", "agent:"} {
		if !strings.Contains(content, section) {
			t.Errorf("默认配置缺少 %q 节", section)
		}
	}
}

// LLMConfig 只读解析助手：ChatEntry 空 → models[0]；VisionEntry 空 → false；ByName 未命中 → false。
func TestLLMEntryHelpers(t *testing.T) {
	t.Run("ChatEntry 空引用取 models[0]", func(t *testing.T) {
		cfg := LLMConfig{
			Models:    []LLMModelConfig{{Name: "chat", Model: "a"}, {Name: "vision", Model: "b"}},
			ChatModel: "",
		}
		entry, ok := cfg.ChatEntry()
		if !ok || entry.Model != "a" {
			t.Errorf("ChatEntry 空引用应取 models[0]，实际 %+v ok=%v", entry, ok)
		}
	})
	t.Run("ChatEntry 按 name 命中", func(t *testing.T) {
		cfg := LLMConfig{
			Models:    []LLMModelConfig{{Name: "chat", Model: "a"}, {Name: "vision", Model: "b"}},
			ChatModel: "vision",
		}
		entry, _ := cfg.ChatEntry()
		if entry.Model != "b" {
			t.Errorf("ChatEntry 应按 name 命中，实际 %+v", entry)
		}
	})
	t.Run("ChatEntry 未命中报 false", func(t *testing.T) {
		cfg := LLMConfig{Models: []LLMModelConfig{{Name: "chat"}}, ChatModel: "nope"}
		if _, ok := cfg.ChatEntry(); ok {
			t.Error("ChatEntry 未命中应返回 false")
		}
	})
	t.Run("VisionEntry 空引用返回 false（描述关闭）", func(t *testing.T) {
		cfg := LLMConfig{Models: []LLMModelConfig{{Name: "chat"}}, VisionModel: ""}
		if _, ok := cfg.VisionEntry(); ok {
			t.Error("VisionEntry 空引用应返回 false")
		}
	})
	t.Run("VisionEntry 按 name 命中", func(t *testing.T) {
		cfg := LLMConfig{
			Models:      []LLMModelConfig{{Name: "chat"}, {Name: "vision", Model: "vl"}},
			VisionModel: "vision",
		}
		entry, ok := cfg.VisionEntry()
		if !ok || entry.Model != "vl" {
			t.Errorf("VisionEntry 应按 name 命中，实际 %+v ok=%v", entry, ok)
		}
	})
	t.Run("Models 空时任何引用返回 false", func(t *testing.T) {
		cfg := LLMConfig{}
		if _, ok := cfg.ChatEntry(); ok {
			t.Error("Models 空时 ChatEntry 应返回 false")
		}
		if _, ok := cfg.VisionEntry(); ok {
			t.Error("Models 空时 VisionEntry 应返回 false")
		}
	})
}
