# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# PlumeBot — AI 开发执行规范

## 0. 快速参考

| 项 | 值 |
|----|-----|
| Go 版本 | 1.26.4 (go.mod: `go 1.26.4`) |
| 模块名 | `plumebot` |
| 入口 | `cmd/bot/main.go` |
| 当前阶段 | 第四阶段：人格与插件（P4-002 .so 插件加载待做） |
| 任务台账 | `docs/roadmap.md`（阶段任务表 + 「待办与遗留事项」B-003~B-016） |

```bash
# 编译
go build -o bot.exe ./cmd/bot/

# 全量编译检查
go build ./...

# 静态分析
go vet ./...

# 测试（已有多包单测：service/event、service/memory、service/agent、infra/onebot、infra/ai、infra/ai/tools、infra/sqlite、pkg/config、pkg/ahocorasick）
go test ./...

# 运行（连接 NapCat，需先配置 config.yaml 的 onebot.ws_url；缺失配置会自动写入默认模板）
./bot.exe
```

## 1. 文档用途

本文件是本仓库中所有 AI 开发任务的长期执行规范。

AI 助手在开始任何任务前，必须先阅读本文件，并严格按照本文件中的：

- 项目目标；
- 技术栈；
- 模块边界；
- 目录规范；
- 代码限制；
- 分层规则；
- 测试要求；
- 任务验收标准；

执行工作。

用户每次只会下达一个小任务，例如：

```text
完成 domain 层 storage 接口定义
```

AI 助手必须根据 `docs/roadmap.md` 找到对应任务，只完成该任务，不顺带开发后续任务，也不实现任何未明确要求的业务功能。

## 2. 项目基本信息

项目名称：

```text
PlumeBot
```

定位：

```text
基于 OneBot 协议的 QQ 机器人，对接 NapCat，AI 驱动的赛博群友。
```

架构设计详见：`docs/architecture.md`；任务进度详见：`docs/roadmap.md`。

当前阶段：

```text
第四阶段：人格与插件
```

本阶段目标：

```text
人格模板化（DB persona 表）、.so 插件动态加载运行。
已完成：第一阶段（项目骨架）、第二阶段（基础设施接入）——
P2-001 SQLite 存储层、P2-002 ZeroBot 连接层、P2-003 消息中间件链
（日志 → 限流 → 敏感词过滤，敏感词为 Aho-Corasick 实装）、
P2-004 eino Agent 接入（eino v0.8.13 + eino-ext openai，多模态消息、tool 机制、
provider 注册中心；真实 LLM 冒烟见 internal/infra/ai/spike_test.go 的 TestSpikeLiveLLM，
PLUMEBOT_TEST_LLM=1 门控）。
已完成：P3-001 上下文窗口（ring buffer，20→100 轮 + 压缩触发信号 + 管线持久化接线）；
已完成：P3-002 画像加载与缓存（群画像按需加载 + 内存缓存；窗口内成员画像 member_profile 已移除，见架构 §4.2）。
已完成：P3-003 窗口压缩策略（一级压缩 LLM 摘要 + 二级融合/淘汰 + 摘要热链内存 + 长程归档 SQLite 重启回灌）。
已完成：P3-004 记忆更新闭环（store_fact/learn_jargon/forget_fact 三工具，Agent 经 tool calling 写
member_facts/group_jargon；黑话 pending/confirmed 状态机；会话身份经 entity.Session 注入 ctx，
读在组装、写在 tool，见架构 §4.3.1）。
已完成：P3-005 数据模型变更（移除 member_profile；member_facts 承担私聊+群聊成员记忆（group_id 空=私聊）；
persona 重构为 agent 绑定人格模板，见架构 §7）。
已完成：P4-001 人格系统（按 cfg.Agent.Name 从 persona 表加载人格模板 GetPersonaByAgent，其 system_prompt
经覆盖 config.agent.system_prompt 后交 provider 工厂兜底（模板 → config.agent.system_prompt → DefaultSystemPrompt），
经 Instruction 注入；默认 agent 无模板则 seed 一条默认模板固化当前生效人设；组装在 main 侧，infra/ai 零改动，见架构 §7）。
```

禁止提前实现（跨阶段禁令，第三阶段禁令继续有效）：

- 人格系统（P4-001，下一任务，勿在后续阶段提前实现）；
- 插件系统（P4-002/003）；
- 触发模式与状态规则（P5-001/002）；
- 完整 Agent 对话闭环（群聊回复闭环属 P6-002；当前管线末端 `tailHandler` 仅做持久化（窗口+SQLite），Agent 能力由 eino 直调验证）

## 3. 技术栈

已使用 / 必须使用：

- Go 1.21+（当前环境 go 1.26.4）；
- ZeroBot v1.8.2（OneBot v11 连接层，已接入 NapCat）；
- `golang.org/x/time/rate`（限流令牌桶，已接入）；
- `modernc.org/sqlite`（SQLite 驱动，纯 Go 无 cgo，已接入）；
- `spf13/viper`（配置文件解析）；
- `uber/zap` + `natefinch/lumberjack`（`pkg/logger` 全局结构化日志）；
- `sirupsen/logrus`（infra/onebot 内部日志）；
- eino / CloudWeGo（AI Agent 引擎，P2-004 接入）；
- Go `plugin` 包（插件动态加载，P4 使用）；
- Go 标准库 `testing`（测试）。

当前明确不使用：

- MySQL / PostgreSQL；
- Redis；
- 消息队列；
- gRPC / 微服务；
- 任何外部向量数据库（Milvus / Qdrant）；
- `stretchr/testify`（用标准库 testing）；
- cgo 依赖的库。

可选能力（默认关闭）：

- LLM API embedding（向量检索开关，需用户自行配置 API）。

## 4. 工程目录

```text
plumebot/
├── cmd/
│   └── bot/
│       └── main.go                 # 唯一入口，组装依赖注入
├── internal/
│   ├── domain/                     # 领域层：纯接口 + 实体 + 哨兵错误，零外部依赖
│   │   ├── entity/                 #   公共实体（Message, Event, Profile...）
│   │   ├── agent.go                #   Agent 接口（P2-004 由 infra/ai 实现）
│   │   ├── memory.go               #   Memory 接口（P3-001 由 service/memory 实现）
│   │   ├── plugin.go               #   Plugin 接口（stub）
│   │   ├── storage.go              #   Storage 接口（P2-001 已实现）
│   │   ├── control.go              #   Control 接口（stub）
│   │   └── errors.go               #   哨兵错误 + 参数化错误（SensitiveWordError）
│   ├── service/                    # 业务编排层，依赖 domain 接口
│   │   ├── event/                  #   中间件链：日志 → 限流 → 敏感词 → 持久化(tail)
│   │   │   ├── event.go            #     EventService，HandleMessage 走链
│   │   │   ├── middleware.go       #     Handler/Middleware 类型 + logMiddleware
│   │   │   ├── ratelimit.go        #     令牌桶限流（按群/私聊按用户）
│   │   │   ├── sensitive.go        #     敏感词中间件（AC 自动机）
│   │   │   └── *_test.go           #     核心单测
│   │   ├── agent/                  #   薄透传：GenerateReply → domain.Agent
│   │   ├── memory/                 #   上下文窗口 + 群画像缓存 + 窗口压缩（P3-001/002/003 已实现；成员画像已移除）
│   │   ├── plugin/                 #   stub
│   │   └── control/                #   stub
│   ├── handler/                    # 事件处理入口（薄胶水，无业务逻辑）
│   │   ├── message.go              #   消息事件 → event service
│   │   └── notice.go               #   通知事件 → 规则处理（stub）
│   └── infra/                      # 基础设施，实现 domain 接口
│       ├── onebot/                 #   ZeroBot 封装（已接入：matcher 分发 + 固定文案回复）
│       ├── ai/                     #   eino Agent + 摘要器实现（P2-004 provider 注册中心 + 多模态转换 + tool 机制；P3-003 Summarizer 裸模型单次调用；P3-004 tools/ 记忆更新工具）
│       ├── sqlite/                 #   SQLite 存储实现（已接入：P2-001，8 张表 + migrations；member_profile 已移除，persona 为 agent 绑定人格模板）
│       │   └── migrations/        #     DDL 迁移文件（001 单文件，migrate 全量执行）
│       ├── plugin_so/              #   plugin.Open() 实现（stub）
│       └── plugin_exe/             #   exec 子进程实现（stub）
├── pkg/                            # 可复用工具
│   ├── config/                     #   配置加载（嵌入默认模板 config.default.yaml）
│   ├── logger/                     #   全局 zap 日志
│   └── ahocorasick/                #   Aho-Corasick 多模式匹配（敏感词）
├── plugins/                        # .so 文件目录（运行时）
├── data/                           # SQLite 自动生成（运行时创建）
├── config.yaml                     # 本地配置文件（不入库，见 .gitignore；api_key 可用环境变量 PLUMEBOT_LLM_OPENAI_API_KEY 覆盖）
├── docs/
│   ├── architecture.md             # 架构设计文档
│   ├── roadmap.md                  # 任务台账（阶段表 + 遗留事项 B-003~B-016）
│   └── eino-notes.md               # eino v0.8.13 API 速查（P2-004 spike 产出，升级评估时对照）
├── CLAUDE.md                       # 本文件
├── README.md
├── go.mod
└── go.sum
```

## 5. 分层规则

### 5.1 domain — 领域层

职责：

- 定义所有业务接口；
- 定义公共实体（Entity）；
- 定义哨兵错误（errors.go）。

禁止：

- 包含任何 `import` 第三方库；
- 包含接口实现；
- 包含数据库操作；
- 包含网络调用。

### 5.2 service — 业务编排层

职责：

- 实现业务流程编排；
- 依赖 domain 接口（不依赖 infra 实现）；
- 通过构造函数注入依赖。

禁止：

- 直接 import infra 包；
- 直接操作数据库；
- 直接发起网络请求。

### 5.3 infra — 基础设施层

职责：

- 实现 domain 层定义的接口；
- 封装第三方库（ZeroBot、eino、SQLite 驱动等）。

依赖方向：

```text
infra → domain（实现接口）
```

### 5.4 handler — 事件处理入口

职责：

- 接收外部事件；
- 调用 service 层；
- 不包含业务逻辑。

### 5.5 依赖方向总图

```text
cmd ──→ handler ──→ service ──→ domain（接口）
                      │
                      └──→ infra（编译时注入）

infra ──→ domain（实现接口）
domain 零依赖
```

上层依赖接口，底层实现接口。编译时通过 main.go 组装依赖关系。

### 5.6 已确立的架构决策

- **中间件链在 service/event**：ZeroBot 无中间件机制（只有 Rule/Matcher/Engine.midHandler），业务管线属编排层，放 service 可单测。
- **发送能力在 infra/onebot**：ZeroBot 的 `ctx` 只在连接层可见（无 CtxFromEvent，`zero.GetBot` 拿到的 Ctx 的 Event 为 nil，只能用于 SendGroupMessage/SendPrivateMessage/CallAction）。限流/敏感词命中由 matcher 内 `ctx.Send` 固定文案回复（`rateLimitedReply`/`sensitiveWordReply` 常量）。Agent 回复采用**方案 B：Sender 注入 ctx**——matcher 闭包构造 per-event 的 `domain.Sender`（持有 `*zero.Ctx`）经 `domain.WithSender` 注入，Handler 链签名保持 `(ctx,msg) error`，需回复的环节 `domain.SenderFrom(ctx).Send(entity.Reply)` 直接发送，载荷结构化（多模态 + 引用回复 + @，见 roadmap B-003）。`domain.Sender` 不含动作能力（禁言/踢人等经 `domain.GroupManager` 供 agent tool 调用，见 B-015）；非响应式主动发送（异步插件、定时、P5 auto 后续发言）需显式目标，届时另定义 `SendTo` 形态并 main 注入。
- **连接层无事件级 context**：onebot 适配层传 `context.Background()`；service Handler 已预留 ctx 参数（B-007，未来仅改 onebot 一处）。
- **LLM provider 注册中心为注入式实例**（P2-004）：`ai.Registry` 经 main 组装注入，非包级全局单例；`Factory func(ctx, config.Config) (domain.Agent, error)` 接收**完整 Config**（LLM 段 + Tools 段在工厂内组装）；工具表经 `NewOpenAIFactory(tr *ToolsRegistry)` 闭包注入（Factory 签名固定）。
- **Agent 实现细节**（P2-004，`internal/infra/ai`）：`EinoAgent` 封装 eino v0.8.13 的 `adk.ChatModelAgent` —— `Instruction` 注入固定人设、`Name`/`Description` 填 adk 元数据（为将来多 agent `NewAgentTool` 做准备）、`MaxIterations=20`（tool 循环上限）、仅当启用工具时才注入 `ToolsConfig`。工具挂在注入式 `ToolsRegistry` 上（无全局状态），`Register` 拒绝重复名。
- **人格=DB 人格模板、人格选择 agent，经 Instruction 注入，不进 agent 层**（P2-004 方案 A' + P4-001 定案）：persona 表（agent 绑定字段 + name + system_prompt，无 userid/groupid/extend，见架构 §7）按 `cfg.Agent.Name` 加载模板，其 system_prompt 覆盖 `config.agent.system_prompt` 后由 provider 工厂兜底（空 → `config.DefaultSystemPrompt`）经 `ChatModelAgentConfig.Instruction` 注入；`domain.Agent.Generate` 收完整消息列表，infra 零改动。不在 agent 配置中写人格 id（配置只有 agent.name）。
- **agent 三要素配置化，多 agent 平滑演进**（P2-004）：`agent.name/description/system_prompt`（空值兜底 `config.DefaultAgentName/DefaultAgentDescription/DefaultSystemPrompt`；persona 模板可覆盖 system_prompt，见上条）；`agent.name` 是 adk 元数据标识（multi-agent 路由），与 `bot.name`（QQ 展示名）语义独立不耦合；未来多 agent 演进为 `agents.list[]` + `active` 选择（每项一份三要素，LLM 保持全局 provider 注册中心），本期不实现。
- **模板不替代 memory**（P2-004 评审）：eino ChatTemplate/StateModifier **不引入**（service 层不能 import infra 类型；组装逻辑不进 infra）；memory 存结构化数据不做渲染（同一数据源多渲染目标）；prompt 组装在 service 层（P6-001 完整五段），摘要压缩 prompt 归 service/memory（P3-003）。

## 6. 模块说明

### 6.1 cmd/bot/main.go

职责：

- 唯一 main 入口；
- 加载配置（缺失时自动写入嵌入默认模板）；
- 手动依赖注入（组装 service + infra + handler）；
- 启动 onebot 客户端并阻塞。

### 6.2 internal/domain/

职责：

- 全部业务接口定义；
- 公共实体定义；
- 哨兵错误定义。

禁止：

- 任何外部依赖 import。

### 6.3 internal/service/event/

职责：

- 消息中间件链编排：`日志 → 限流 → 敏感词 → tail`，tail 持久化消息到窗口 + SQLite；
- `HandleMessage` 走链，`HandleNotice` 暂为 stub。

关键约定：

- `HandleMessage(ctx, msg) error`，中间件命中返回哨兵错误（`domain.ErrRateLimited` / `domain.SensitiveWordError`）；
- 限流：`golang.org/x/time/rate` 令牌桶，按群（私聊按用户）独立，超时返回 `ErrRateLimited`；
- 敏感词：`pkg/ahocorasick` 匹配，空词表 = 不过滤；
- 日志中间件记录 message_id/group_id/user_id/message_type/content，日志不重复（infra/onebot 不再打消息 Info）；
- 末端 tail 持久化消息（写入窗口 + SQLite），窗口满时触发 P3-003 异步窗口压缩（经 memory.Compress，防重入 + 失败冷却）。

### 6.4 internal/handler/

职责：

- 消息事件、通知事件的处理入口；
- 调用 event service 进行分发。

### 6.5 internal/infra/

职责：

- domain 接口的具体实现；
- 封装 ZeroBot、SQLite 等第三方库。

现状：

- `onebot/`：已接入（matcher 注册 + 事件转换 + 固定文案回复）；已实现、有单测。
- `sqlite/`：已接入（P2-001，8 张表 + migrations；member_profile 已移除，persona 为 agent 绑定人格模板）。
- `ai/`：已接入（P2-004：Registry provider 注册中心 + openai 兼容工厂 + EinoAgent + 多模态转换，正式单测全绿；P3-004：`ai/tools` 记忆更新工具 store_fact/learn_jargon/forget_fact，会话身份经 `entity.Session` 注入 ctx）。
- `plugin_so/`、`plugin_exe/`：stub（返回 nil 或 error），待对应阶段实现。

每个 infra 包必须：

- 若包含 SQL 操作：`queries.go` — 所有 DML 语句以包级 `const` 存放，方法体内不内嵌 SQL 字面量；DDL 语句存放于 `migrations/*.sql` 通过 `//go:embed` 加载。

哨兵错误定义在 `internal/domain/errors.go`，infra 层引用 `domain.ErrXxx` 返回，供上层 service 通过 `errors.Is` 判断，避免上层耦合数据库驱动。

### 6.6 pkg/config/

职责：

- 配置文件加载与解析；
- `//go:embed config.default.yaml` 嵌入默认模板：`Load()` 时配置文件不存在则写入模板再加载；
- **空值兜底由消费方负责**（如限流 rate≤0→2、burst≤0→20、max_wait≤0→10s；WsURL 空→`ws://127.0.0.1:3001`；Bot.Name 空→`PlumeBot`）；config 层不改写字段；
- **唯一例外：`llm.openai.api_key`** 支持环境变量 `PLUMEBOT_LLM_OPENAI_API_KEY` 覆盖（非空时优先于文件值，敏感密钥不入配置文件）；
- 新增配置字段必须**双处同步**：`pkg/config/config.default.yaml` + 根 `config.yaml`（config.go 注释已标明）。根 `config.yaml` 已被 `.gitignore`（本地文件，含用户真实密钥，不入库）；守卫测试 `TestRootConfigYAMLSyncedWithDefault` 比较时排除 `api_key` 字段。

## 7. 代码规则

1. domain 层不 import 任何第三方库（标准库除外）。
2. service 层不 import infra 包。
3. 接口在 domain 定义，实现在 infra。
4. 依赖通过构造函数注入，不使用全局变量。
5. 未实现模块保持 stub（返回 nil 或 error），保持可编译。
6. 不提前实现任何业务逻辑（对照 roadmap 阶段禁令）。
7. 不接入阶段外外部服务。
8. 不为了"架构完整"创建大量空类和方法，只创建架构文档中明确列出的模块。
9. 哨兵错误定义在 `internal/domain/errors.go`：`ErrNotFound`、`ErrConflict`、`ErrClosed`、`ErrRateLimited`、`ErrSensitiveWord`。参数化错误（如 `SensitiveWordError{Word}`）实现 `Unwrap()` 指向哨兵，上层用 `errors.Is` 判断，禁止在 infra 包内自定义哨兵。
10. infra 包中 SQL DML 语句不得内嵌在方法体内，必须提取到 `queries.go` 作为包级 `const`；DDL 语句存放于 `migrations/*.sql` 通过 `//go:embed` 加载。
11. 代码保持直接、易读，不引入不必要的抽象层。
12. 配置空值兜底放在消费方（默认值集中下沉），pkg/config 不改写字段。

## 8. AI 助手每次任务的执行流程

接到任务后必须执行以下步骤。

### 第一步：阅读

阅读：

1. 本文件 `CLAUDE.md`；
2. `docs/architecture.md`；
3. `docs/roadmap.md`（确认任务编号、状态、遗留事项）；
4. 已有代码和目录结构。

### 第二步：确认范围

在修改前输出：

- 本次任务目标；
- 计划修改/创建的文件；
- 明确不修改的内容；
- 遵守的分层规则。

不得把多个任务合并执行。

### 第三步：实现

要求：

- 优先最小改动；
- 不提前实现后续任务；
- 不接入外部服务；
- 不加入未要求的框架或库；
- 代码保持直接、易读。

### 第四步：验证

至少执行：

```bash
go build ./...
go vet ./...
go test ./...
```

如果环境无法执行，必须如实说明实际错误。

不得伪造测试通过。

### 第五步：交付

完成后必须输出：

- 修改文件列表；
- 关键实现说明；
- 实际执行命令；
- 编译/测试结果；
- 未完成事项（写入 roadmap「待办与遗留事项」）。

完成后停止，不自动进入下一个任务。

## 9. 最重要的执行原则

1. 一次只完成一个任务。
2. 不提前开发下一任务。
3. 不扩大范围。
4. 不实现业务逻辑。
5. 不接入外部服务。
6. 不引入未声明的依赖。
7. 不在 domain 层 import 第三方库。
8. 不在 service 层 import infra 包。
9. 不把代码塞进一个文件。
10. 不为了"架构完整"过度设计。
11. 不伪造编译结果。
12. 代码优先简单、直接、易读。
13. 完成任务后停止，等待审核。
