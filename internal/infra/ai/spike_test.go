package ai

// 本文件是 P2-004 阶段 1 的临时 spike 冒烟测试，用于验证 eino v0.8.13 的实际 API 形态：
//  1. 文本：system + user → 最终 assistant 文本
//  2. 多模态：UserInputMultiContent（图片 URL / base64）原样透传
//  3. tool 自动循环：模型先回 tool_call → 工具执行 → 模型再回最终答案
//  4. eino-ext openai 构造（不发网络）
//  5. 真实 LLM 冒烟（env 门控，可选）
//
// spike 结论已沉淀为阶段 4 正式实现（convert.go/tools.go/agent.go/provider.go）；
// 本文件保留：fakeChatModel/runSpikeAgent 助手被正式单测复用，LiveLLM 冒烟保留（env 门控）。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
)

// fakeChatModel 是脚本化 ChatModel 实现：每次 Generate 按顺序返回脚本中的消息。
// 同时记录每次调用收到的输入（浅拷贝），供断言透传。
type fakeChatModel struct {
	mu       sync.Mutex
	script   []*schema.Message // 按调用顺序返回
	inputs   [][]*schema.Message
	nextResp int
}

var _ model.BaseChatModel = (*fakeChatModel)(nil)

func (f *fakeChatModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cp := make([]*schema.Message, len(input))
	for i, m := range input {
		cm := *m
		cp[i] = &cm
	}
	f.inputs = append(f.inputs, cp)

	if f.nextResp >= len(f.script) {
		return nil, errors.New("fakeChatModel: 脚本用尽")
	}
	resp := f.script[f.nextResp]
	f.nextResp++
	return resp, nil
}

func (f *fakeChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("fakeChatModel: Stream 未实现")
}

// runSpikeAgent 组装 ChatModelAgent 并执行，返回最后一个模型输出消息（最终答案）。
func runSpikeAgent(t *testing.T, cfg *adk.ChatModelAgentConfig, msgs ...*schema.Message) (*schema.Message, error) {
	t.Helper()
	agent, err := adk.NewChatModelAgent(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewChatModelAgent 失败: %v", err)
	}
	iter := agent.Run(context.Background(), &adk.AgentInput{Messages: msgs})

	var last *schema.Message
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			return last, ev.Err
		}
		if ev.Output != nil && ev.Output.MessageOutput != nil && !ev.Output.MessageOutput.IsStreaming {
			last = ev.Output.MessageOutput.Message
		}
	}
	if last == nil {
		return nil, errors.New("未收到任何模型输出消息")
	}
	return last, nil
}

// TestSpikeTextAgent：文本链路（本设计的核心用法：Instruction 留空，system 由消息列表携带）。
func TestSpikeTextAgent(t *testing.T) {
	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("你好，我是 PlumeBot。", nil),
	}}

	got, err := runSpikeAgent(t,
		&adk.ChatModelAgentConfig{Model: fake}, // Instruction 留空：系统提示词来自消息列表
		schema.SystemMessage("你是 PlumeBot，一个赛博群友。"),
		schema.UserMessage("你好"),
	)
	if err != nil {
		t.Fatalf("agent 执行失败: %v", err)
	}
	if got.Content != "你好，我是 PlumeBot。" {
		t.Fatalf("最终文本不符: %q", got.Content)
	}

	in := fake.inputs[0]
	if len(in) != 2 || in[0].Role != schema.System || in[1].Role != schema.User {
		t.Fatalf("模型收到的消息列表不符预期: %+v", in)
	}
}

// TestSpikeMultimodalInput：多模态消息（图片 URL 与 base64 两路）原样透传。
func TestSpikeMultimodalInput(t *testing.T) {
	imgURL := "https://example.com/cat.png"
	imgBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

	userURL := &schema.Message{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{
		{Type: schema.ChatMessagePartTypeText, Text: "这张图里有什么？"},
		{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{
			MessagePartCommon: schema.MessagePartCommon{URL: &imgURL},
			Detail:            schema.ImageURLDetailAuto,
		}},
	}}
	userB64 := &schema.Message{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{
		{Type: schema.ChatMessagePartTypeText, Text: "这张图里有什么？"},
		{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{
			MessagePartCommon: schema.MessagePartCommon{Base64Data: &imgBase64, MIMEType: "image/png"},
			Detail:            schema.ImageURLDetailAuto,
		}},
	}}

	fakeURL := &fakeChatModel{script: []*schema.Message{schema.AssistantMessage("是一只猫。", nil)}}
	if _, err := runSpikeAgent(t, &adk.ChatModelAgentConfig{Model: fakeURL}, userURL); err != nil {
		t.Fatalf("URL 图片链路失败: %v", err)
	}
	fakeB64 := &fakeChatModel{script: []*schema.Message{schema.AssistantMessage("是一只猫。", nil)}}
	if _, err := runSpikeAgent(t, &adk.ChatModelAgentConfig{Model: fakeB64}, userB64); err != nil {
		t.Fatalf("base64 图片链路失败: %v", err)
	}

	// URL 路透传断言
	urlParts := fakeURL.inputs[0][0].UserInputMultiContent
	if len(urlParts) != 2 || urlParts[1].Image == nil || urlParts[1].Image.URL == nil || *urlParts[1].Image.URL != imgURL {
		t.Fatalf("URL 图片 part 透传不符: %+v", urlParts)
	}
	// base64 路透传断言
	b64Parts := fakeB64.inputs[0][0].UserInputMultiContent
	if len(b64Parts) != 2 || b64Parts[1].Image == nil || b64Parts[1].Image.Base64Data == nil || *b64Parts[1].Image.Base64Data != imgBase64 {
		t.Fatalf("base64 图片 part 透传不符: %+v", b64Parts)
	}
}

type spikeEchoArgs struct {
	Text string `json:"text" jsonschema:"description=要回显的文本"`
}

// TestSpikeToolCallLoop：tool 自动循环（模型回 tool_call → 工具执行 → 模型回最终答案）。
func TestSpikeToolCallLoop(t *testing.T) {
	var echoCalls []string
	echoParams, err := toolutils.GoStruct2ParamsOneOf[spikeEchoArgs]()
	if err != nil {
		t.Fatalf("GoStruct2ParamsOneOf 失败: %v", err)
	}
	echoTool := toolutils.NewTool(&schema.ToolInfo{Name: "echo", Desc: "回显用户文本", ParamsOneOf: echoParams},
		func(_ context.Context, args spikeEchoArgs) (string, error) {
			echoCalls = append(echoCalls, args.Text)
			return "已回显：" + args.Text, nil
		})

	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:       "call_1",
			Function: schema.FunctionCall{Name: "echo", Arguments: `{"text":"你好"}`},
		}}),
		schema.AssistantMessage("已回显：你好", nil),
	}}

	got, err := runSpikeAgent(t, &adk.ChatModelAgentConfig{
		Model: fake,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{echoTool}},
		},
	}, schema.UserMessage("帮我 echo 你好"))
	if err != nil {
		t.Fatalf("tool 循环失败: %v", err)
	}
	if len(echoCalls) != 1 || echoCalls[0] != "你好" {
		t.Fatalf("echo 工具调用不符: %v", echoCalls)
	}
	if got.Content != "已回显：你好" {
		t.Fatalf("最终文本不符: %q", got.Content)
	}

	// 第二次模型调用应包含 tool 角色消息（工具结果回填）
	in := fake.inputs[1]
	hasTool := false
	for _, m := range in {
		if m.Role == schema.Tool {
			hasTool = true
		}
	}
	if !hasTool {
		t.Fatalf("tool 结果未回填为 tool 消息: %+v", in)
	}
}

// TestSpikeOpenAIConstruct：eino-ext openai ChatModel 构造（不发网络，仅验证配置字段映射）。
func TestSpikeOpenAIConstruct(t *testing.T) {
	m, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: "https://api.deepseek.com/v1",
		APIKey:  "sk-test",
		Model:   "deepseek-chat",
	})
	if err != nil {
		t.Fatalf("openai.NewChatModel 失败: %v", err)
	}
	if m == nil {
		t.Fatal("openai.NewChatModel 返回 nil")
	}
}

// TestSpikeLiveLLM：真实 LLM 端到端冒烟（可选）。设置 PLUMEBOT_TEST_LLM=1 时执行。
// 走完整生产链路：config.Load（根 config.yaml，含用户真实 api_key）→ Registry →
// NewOpenAIFactory → NewEinoAgent → Generate（entity.ChatMessage 业务消息）。
// 配置覆盖：PLUMEBOT_TEST_LLM_{CONFIG,BASE_URL,API_KEY,MODEL,IMAGE_URL}（可选）。
// 注意：本测试只打印 LLM 回复文本，不打印 api_key。
func TestSpikeLiveLLM(t *testing.T) {
	if os.Getenv("PLUMEBOT_TEST_LLM") != "1" {
		t.Skip("PLUMEBOT_TEST_LLM=1 时执行真实 LLM 冒烟")
	}

	// 配置源：默认根 config.yaml，可用环境变量覆盖敏感/易变字段。
	cfgPath := os.Getenv("PLUMEBOT_TEST_LLM_CONFIG")
	if cfgPath == "" {
		cfgPath = filepath.Join("..", "..", "..", "config.yaml")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	// 对话模型条目：chat_model 引用 name，空 → models[0]。
	chatIdx := -1
	if cfg.LLM.ChatModel == "" {
		chatIdx = 0
	} else {
		for i := range cfg.LLM.Models {
			if cfg.LLM.Models[i].Name == cfg.LLM.ChatModel {
				chatIdx = i
				break
			}
		}
	}
	if chatIdx < 0 {
		t.Fatal("未配置对话模型：llm.chat_model 未指向任何 models 条目")
	}
	entry := &cfg.LLM.Models[chatIdx]
	if v := os.Getenv("PLUMEBOT_TEST_LLM_BASE_URL"); v != "" {
		entry.BaseURL = v
	}
	if v := os.Getenv("PLUMEBOT_TEST_LLM_API_KEY"); v != "" {
		entry.APIKey = v
	}
	if v := os.Getenv("PLUMEBOT_TEST_LLM_MODEL"); v != "" {
		entry.Model = v
	}
	if entry.Model == "" {
		t.Fatal("未配置模型：config.yaml 的 llm.models[chat].model 或环境变量 PLUMEBOT_TEST_LLM_MODEL")
	}

	// 生产链路组装（与 cmd/bot/main.go 一致）
	reg := NewRegistry()
	if err := reg.Register(config.DefaultLLMProvider, NewOpenAIFactory(NewToolsRegistry())); err != nil {
		t.Fatalf("注册 provider 失败: %v", err)
	}
	agentInfra, err := reg.NewAgent(context.Background(), *cfg)
	if err != nil {
		t.Fatalf("组装 Agent 失败: %v", err)
	}

	// 系统提示词由工厂经 Instruction 注入（cfg.Agent.SystemPrompt），此处只传业务消息。
	msgs := []entity.ChatMessage{
		{Role: entity.RoleUser, Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: "你好，用一句话介绍你自己。"}}},
	}
	if imgURL := os.Getenv("PLUMEBOT_TEST_LLM_IMAGE_URL"); imgURL != "" {
		msgs = append(msgs, entity.ChatMessage{Role: entity.RoleUser, Parts: []entity.ContentPart{
			{Type: entity.PartTypeText, Text: "这张图里有什么？"},
			{Type: entity.PartTypeImage, URL: imgURL},
		}})
	}

	got, err := agentInfra.Generate(context.Background(), msgs)
	if err != nil {
		t.Fatalf("真实 LLM 调用失败: %v", err)
	}
	if got == "" {
		t.Fatal("真实 LLM 返回空文本")
	}
	t.Logf("LLM 回复: %s", got)
}
