# eino v0.8.13 API 速查笔记（P2-004 阶段 1 spike 产出）

> 全部结论来自对 `github.com/cloudwego/eino@v0.8.13` 与
> `github.com/cloudwego/eino-ext/components/model/openai@v0.1.13` 模块缓存的**源码级核实**，
> 并已通过 `internal/infra/ai/spike_test.go` 冒烟验证（文本 / 图片 URL / 图片 base64 / tool 自动循环）。
> 参考：`$(go env GOMODCACHE)/github.com/cloudwego/eino@v0.8.13/{adk,schema,components}`。

## 1. 重要事实：v0.8.13 中 ChatModelAgent 在 adk 包

计划早期预期 `flow/agent.ChatModelAgent`（v0.8 形态），**实际核实**：

- v0.8.13 的 `flow/agent/` 只有 `agent_option.go`、`multiagent/`、`react/`、`utils.go`——**没有 ChatModelAgent**。
- `ChatModelAgent` 位于 **`adk` 包**（`adk/chatmodel.go`），调用形态为
  `Runner / AsyncIterator[*AgentEvent]` 事件流（与 v0.9 adk 同构，本版本已具备）。
- 本项目仍锁定 v0.8.13（eino-ext 兼容、多模态字段可用、冒烟通过），只是包路径与调用形态按实际修正。

## 2. 组装与调用（最小代码）

```go
import (
    "github.com/cloudwego/eino/adk"
    "github.com/cloudwego/eino/components/model"
    "github.com/cloudwego/eino/schema"
)

// 1. 组装：Instruction 留空 = 不注入系统提示词（本项目 system 由消息列表携带）
agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
    Name:        "plumebot",
    Description: "PlumeBot 对话 Agent",
    Model:       cm, // model.BaseChatModel（eino-ext openai.ChatModel 即实现它）
    // ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{...}}},
    // MaxIterations: 20, // tool 循环上限，默认 20，超限报错
})

// 2. 执行：事件迭代，最后一个 Output 消息即最终答案
iter := agent.Run(ctx, &adk.AgentInput{Messages: msgs}) // msgs []*schema.Message
var last *schema.Message
for {
    ev, ok := iter.Next() // ev *adk.AgentEvent
    if !ok { break }
    if ev.Err != nil { /* 节点错误，含 tool 执行失败、超限 */ }
    if ev.Output != nil && ev.Output.MessageOutput != nil && !ev.Output.MessageOutput.IsStreaming {
        last = ev.Output.MessageOutput.Message
    }
}
// last.Content 即最终回复文本（tool 循环结束后为最终 assistant 消息）
```

关键类型：

- `adk.AgentInput{Messages []Message, EnableStreaming bool}`（`Message = *schema.Message`）
- `adk.AgentEvent{AgentName, RunPath, Output *AgentOutput, Action *AgentAction, Err error}`
- `adk.AgentOutput{MessageOutput *MessageVariant, CustomizedOutput any}`
- `adk.MessageVariant{IsStreaming bool, Message, MessageStream, Role, ToolName}`；
  流式时用 `mv.GetMessage()` 聚合。
- 非流式事件直接取 `ev.Output.MessageOutput.Message`。

## 3. ChatModelAgentConfig 字段（v0.8.13）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `Name` / `Description` | string | 子 agent 场景必填，独立运行可空 |
| `Instruction` | string | 系统提示词；**本项目留空**（system 在消息列表里） |
| `Model` | `model.BaseChatModel` | Generate + Stream；配工具时需支持 `model.WithTools` option |
| `ToolsConfig` | `adk.ToolsConfig` | 内嵌 `compose.ToolsNodeConfig{Tools []tool.BaseTool}`；`ReturnDirectly map[string]bool`、`EmitInternalEvents` |
| `GenModelInput` | func | 自定义"指令+输入→模型消息"；默认把 Instruction 前置为 system |
| `Exit` | `tool.BaseTool` | 终止 agent 的工具；nil = 不产生 Exit Action（默认即可） |
| `MaxIterations` | int | tool 循环上限，默认 20，超限报错 |
| `Middlewares` / `Handlers` | — | 扩展钩子（本项目不用） |

## 4. ChatModel 接口（fake 需实现）

```go
// components/model/interface.go
type BaseChatModel interface {
    Generate(ctx context.Context, input []*schema.Message, opts ...Option) (*schema.Message, error)
    Stream(ctx context.Context, input []*schema.Message, opts ...Option) (*schema.StreamReader[*schema.Message], error)
}
// ToolCallingChatModel = BaseChatModel + WithTools(tools []*schema.ToolInfo) (ToolCallingChatModel, error)
// （adk 走 model.WithTools option，配工具的模型只要 BaseChatModel 能接受该 option 即可；
//   spike 的 fakeChatModel 仅实现 BaseChatModel 即通过 tool 循环测试）
```

## 5. 多模态输入（v0.8.13，非废弃）

```go
msg := &schema.Message{
    Role: schema.User,
    UserInputMultiContent: []schema.MessageInputPart{
        {Type: schema.ChatMessagePartTypeText, Text: "看图说话"},
        {Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{
            MessagePartCommon: schema.MessagePartCommon{URL: &imgURL}, // 或 Base64Data: &b64str, MIMEType: "image/png"
            Detail: schema.ImageURLDetailAuto,                        // auto / low / high
        }},
    },
}
```

- `MessageInputPart{Type, Text, Image *MessageInputImage, Audio, Video, File}`
- Type 枚举：`ChatMessagePartTypeText / ImageURL / AudioURL / VideoURL / FileURL`
- 旧 `MultiContent []ChatMessagePart` 已废弃，不用。
- eino-ext openai 会自动把 Image part 转成 OpenAI `image_url`（URL 直传或 base64 data: URL）。

## 6. Tool（v0.8.13 形态：描述与执行分离）

```go
import (
    toolutils "github.com/cloudwego/eino/components/tool/utils"
    "github.com/cloudwego/eino/components/tool"
    "github.com/cloudwego/eino/schema"
)

type EchoArgs struct {
    Text string `json:"text" jsonschema:"description=要回显的文本"`
}

// 方式 A：struct 推断参数 schema（spike 采用，推荐）
params, err := toolutils.GoStruct2ParamsOneOf[EchoArgs]()
echoTool := toolutils.NewTool(&schema.ToolInfo{Name: "echo", Desc: "回显文本", ParamsOneOf: params},
    func(ctx context.Context, args EchoArgs) (string, error) { ... })

// 方式 B：一步到位（内部完成推断）
echoTool2, err := toolutils.InferTool[EchoArgs, string]("echo", "回显文本", fn)
```

- `tool.BaseTool{ Info(ctx) (*schema.ToolInfo, error) }`——模型侧只需要描述；
  执行接口为 `tool.InvokableTool{ BaseTool; InvokableRun(ctx, argumentsInJSON string, opts ...Option) (string, error) }`。
- `schema.ToolInfo{Name, Desc, Extra, ParamsOneOf}`——**只有描述，没有执行字段**
  （v0.9 移除的 `Bound/InvokableRun` 在 v0.8 的 schema.ToolInfo 上同样不存在，勿沿用旧资料）。
- `ParamsOneOf` 构造：`schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo)`
  或 `schema.NewParamsOneOfByJSONSchema(*jsonschema.Schema)` 或 `toolutils.GoStruct2ParamsOneOf[T]()`。
- jsonschema tag 风格：`jsonschema:"description=xxx"`、`jsonschema:"required,description=xxx"`。

## 7. eino-ext openai（v0.1.13）

```go
m, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
    BaseURL: "https://api.deepseek.com/v1", // 任意 OpenAI 兼容端点
    APIKey:  "sk-xxx",
    Model:   "deepseek-chat",
    Timeout: 60 * time.Second,
    // Temperature *float32 / MaxCompletionTokens *int（MaxTokens 已废弃）
    // 无 Headers 字段；自定义 header 用 openai.WithExtraHeader(map[string]string) option
})
```

- go.mod require `eino v0.7.13`，但本仓库以 `eino v0.8.13` 编译运行，冒烟通过。
- `openai.ChatModel` 实现 `model.BaseChatModel`（含 tool option 支持）。

## 8. 冒烟结论（spike_test.go）

| 用例 | 结果 |
| --- | --- |
| 文本：system+user → 最终文本；模型收到的消息 system 在前 | PASS |
| 图片 URL part 透传 | PASS |
| 图片 base64 part 透传 | PASS |
| tool 自动循环：tool_call → InvokableRun 执行 → 结果回填 tool 消息 → 最终答案 | PASS |
| openai.NewChatModel 构造（无网络） | PASS |
| 真实 LLM（`PLUMEBOT_TEST_LLM=1` 门控，需配置环境变量） | SKIP（可选） |

## 8.5 实战补充（阶段 5 端到端发现）

- **`UserInputMultiContent` 有角色限制**：eino-ext openai 转换器仅允许 user/tool 角色携带多模态内容，system 消息带 `UserInputMultiContent` 会报 `user input multi content only support user&tool role`。因此纯文本消息一律走 `Message.Content`（任何角色皆可），多模态（含图片等）才走 `UserInputMultiContent` 且限定 user 角色——`convert.go` 已按此实现（含非文本 part 非 user 角色时显式报错）。
- 真实 LLM 端到端命令：`PLUMEBOT_TEST_LLM=1 go test ./internal/infra/ai/ -run TestSpikeLiveLLM -v`（读根 config.yaml 真实配置，全链路：Registry → openai 工厂 → EinoAgent）。

## 9. 参考 URL

- eino 仓库：[https://github.com/cloudwego/eino](https://github.com/cloudwego/eino) （tag v0.8.13）
- eino-ext openai 示例（generate_with_image）：[https://github.com/cloudwego/eino-ext/tree/main/components/model/openai/examples](https://github.com/cloudwego/eino-ext/tree/main/components/model/openai/examples)
- eino-contrib/jsonschema：[https://github.com/eino-contrib/jsonschema](https://github.com/eino-contrib/jsonschema) （v1.0.3）
