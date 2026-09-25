# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# PlumeBot — AI 开发执行规范

## 0. 快速参考

| 项 | 值 |
|----|-----|
| Go 版本 | 1.26.4 (go.mod: `go 1.26.4`) |
| 模块名 | `plumebot` |
| 入口 | `cmd/bot/main.go` |
| 当前阶段 | 第七阶段：管理后端（P7-001 管理配置 API、P7-002 网页日志浏览、P7-003 对话历史只读浏览已完结；第六阶段 P6-003 端到端压测待实现） |
| 任务台账 | `docs/roadmap.md`（阶段任务表 + 「待办与遗留事项」B 台账，完成即删行） |

```bash
# 编译
go build -o bot.exe ./cmd/bot/

# 全量编译检查
go build ./...

# 静态分析
go vet ./...

# 测试（已有多包单测：service/event、service/memory、service/agent、service/control、service/plugin、service/admin、service/log、infra/onebot、infra/ai、infra/ai/tools、infra/imagecache、infra/sqlite、infra/logfile、handler/web、pkg/config、pkg/jwt、pkg/logger、pkg/ahocorasick、pkg/base64util、plugin-sdk（独立 module：entity + plugin））
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
第七阶段：管理后端
```

本阶段目标：

```text
完整消息链路跑通，bot 可对话（P6-002：触发判断 → 拼 prompt → Agent 推理 → 回复 → 记忆更新）。
已完成：P6-001 Prompt 组装联调（五段组装 BuildMessages，见下方完成记录）。
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
经 Instruction 注入；默认 agent 无模板则 seed 一条默认模板固化当前生效人设；组装在 main 侧，infra/ai 零改动，
见架构 §7。注：Instruction 注入已由 P6-001 修订为 service 组装注入 system 消息，见 §5.6）。
已完成：P4-002 插件系统（go-plugin 子进程 + 指令集协议：entity.PluginRequest/PluginResult{Reply, Actions}，
plugin-sdk（独立 module，P6-004 抽取）做 go-plugin net/rpc 接线（手写 shim，免 protoc），host 侧 service/plugin 发现路由（plugin.json）+ 命令分发分支；
只定义协议 + 宿主校验（ValidatePluginResult），不执行回复/动作，见架构 §8.6 与 B-017；回复发送已由 P6-002 落地（B-017），群管理动作已由 B-015 落地（插件 Actions 与 AI 工具共用 domain.GroupManager 执行路径，见下方完成记录与架构 §15）。
已完成：P6-004 插件 SDK 抽取（协议 wire 类型 + go-plugin 接线迁至 plugin-sdk/entity + plugin-sdk/plugin，宿主 internal/domain/entity 协议类型改类型别名 + ValidatePluginResult 转发，删除 infra/plugin_exe；cmd/bot 直接用 SDK NewClient；示例插件只依赖 SDK，第三方可独立编写插件）。
已完成：第四阶段（人格与插件）全部完结，进入第六阶段。
已完成：P5-001 触发模式（mention/auto 双模式，per-group group_config 表可独立配置；
domain.Control 两方法 ShouldReply→Decision + OnReplied；service/control 模式判定 + event tail
触发判断接线，只标记不发送，发送归 P6-002（已接线，见 P6-002 完成记录），见架构 §9 与 B-003）。
已完成：P5-002 状态规则（精力/冷却/连续回复/时段/短消息五条纯规则层，不调 LLM；group_config
扩 10 状态参数列 per-group 覆盖；bot_state 运行态 JSON + OnReplied 接线；会话键统一为
entity.Message.SessionKey、覆盖逻辑消重，见架构 §9.2）。
已完成：第六阶段 P6-001 Prompt 组装联调（service/memory BuildMessages 五段组装：
persona(现查 persona 表，改库即时生效) + 会话画像(群画像+成员事实+confirmed 黑话，注入上限
llm.prompt) + 历史摘要 → system，窗口④ + 当前消息⑤；at→text、图片惰性描述(describer 缓存+预算；
缓存键=FileHash→拉取字节 md5→URL，B-049 起「有 http URL 无 FileHash」先拉字节算内容 md5 补键，
同图跨轮/跨 URL 命中同一键，见 infra/ai/mediadescriber.go)
+ 描述写回 UpdateMessageParts；persona 不再经 Instruction 注入，见架构 §6/§7.3 与 §5.6）。
已完成：第六阶段 P6-002 完整消息链路（回复闭环接线，见架构 §9.2/§10.2/§14.4 与 roadmap P6-002）：
触发判断命中 → BuildMessages → Agent GenerateReply（记忆工具经 ctx 注入的 Session 写入 member_facts/
group_jargon）→ 经 ctx 内 domain.Sender 发送（B-003：matcher 闭包注入 per-event Sender + onebot
replyToChain 转 message.ReplyWithMessage/At 后 SendChain）→ 发送成功才窗口追加 bot 回复（合成
`self:` MessageID 规避空串冲突）+ OnReplied 记账（B-038 时序）；插件回复经 Sender 执行（B-017）；
B-040 定案 GetWindow 深拷贝 + BackfillParts 安全回填（组装/压缩无跨 LLM 阻塞、
常规路径零冗余落库）；Agent 回复群聊统一 @ 触发者（mention 与 auto 一致，P6 联调修复 auto 回复
对象不明确）、不引用触发消息（避免引用预览带出触发消息内的 @bot，见 event.go agentReply）；
P6 联调昵称注入：entity.Message.SenderName（convert 从 ev.Sender 提取、群名片 card 优先回落 nickname；
bot 自身回复由 event 填展示名 botName，见 botReplyMessage）→ speakerText/画像成员事实「昵称」优先、
无昵称回落 QQ 号，纯内存不落库（旧消息回落），见 convert.go senderDisplayName 与架构 §6 ④/⑤ 渲染。
已完成：B-015 AI 群管理动作执行（roadmap B-015 与架构 §15）：domain.GroupManager（Execute(ctx,
entity.GroupAction) 统一入口，与 Sender 分离）+ per-event ctx 注入（matcher 闭包构造 botGroupManager，
与 Session/Sender 同构）；4 个 AI 工具 group_mute/group_unmute/group_kick/group_set_card（Desc 写清
触发边界）；插件 Actions 经 dispatchCommand → executeActions 走同一执行路径（B-017 后补）；
三道护栏集中在 botGroupManager.Execute：per-group 开关 group_config.group_mgmt_enabled（002 迁移
默认 1 开，无配置行同样视为开，0 = 显式关闭）+ 触发者/bot 管理员校验（get_group_member_info 查
role，fail-closed）+ mute 时长钳制 30 天；动作经 ctx.CallAction 检查 APIResponse 反馈（SetGroupBan 等封装吞响应不可用）；
migrate() 升级为版本记录式（schema_migrations 表 + 逐文件事务，B-034 版本化迁移基础设施顺带建立）。
已完成：P7-001 管理配置 API（roadmap 第七阶段，设计见 docs/admin-web-api-plan.md）：
gin 管理后端（127.0.0.1 回环绑定；admin.port 默认 9321 起、被占用逐次 +1 至 10024，全占用仅告警）
= pkg/jwt（golang-jwt/v5 HS256 纯封装）+ service/admin（单 service 按配置域分组：group_config /
persona / group_profile / group_jargon / member_facts / bot_state 只读）+ handler/web（单一包
按职责域拆 Handler 对象，dto/request 与 dto/response 分层，middleware/response helper 同包）
+ 简易前端单页（go:embed，原生 fetch）；**契约分层合规**：接口 `domain.Admin`（+ 消费侧
`domain.SessionWindowReader`/`domain.GroupProfileInvalidator`）定义在 domain 层、`service/admin.Service`
实现、handler/web 依赖接口而非具体类型；通信结构体（`entity.AuthResult`/`entity.SessionOverview`/
`entity.SessionMessage`）入 `domain/entity` 保持纯结构（无 json tag），入参/出参 json 由
handler/web/dto（request/response）接管；鉴权：admin_user 表（003 迁移）+ 首次注册门控
（空表可用，其后 4031）+ bcrypt 散列 + 注册/登录每 IP 限流；group_profile 写/删后经
MemoryService.InvalidateGroupProfile 失效内存缓存（D9）；管理面 fail-fast 校验；
config 新增 admin 段（enabled / port / jwt_secret / token_ttl_seconds，双处同步 B-006）。
另：**group_config 自动建行**（P7-001 补充）——群首次被 bot 触达（`service/control.resolveParams`
遇 `GetGroupConfig` 返回 `ErrNotFound`）时，按**当时全局生效值**快照落一行
（`ControlService.defaultGroupConfig`；私聊不建行；`mode` 写归一化枚举、静默时段还原 `"HH:MM"`、
`group_mgmt_enabled` 固定写 1，写库失败仅告警不阻断回复链路）。后果：该群此后**独立于全局**
（`cfg.Control` 变更不再影响它）；管理面 DELETE 删行**非持久**（下条群消息按当时全局值重建）；
`configured:false` 仅对该群**尚未被触达**时可达。见 `docs/admin-web-api-plan.md` §7.2 与架构 §9.2。
已完成：P7-003 对话历史只读浏览（会话窗口查看 + 顶栏折叠 + 移动适配，见 roadmap P7-003）：domain.Memory 加
`ListSessions` + Window 实现（sync.Map 遍历排序）+ MemoryService 转发；service/admin 经消费侧接口
`domain.SessionWindowReader`/`domain.GroupProfileInvalidator`（上移 domain）注入只读方法 `ListSessions`/`GetSessionWindow`（`is_self` 依 `self:` 前缀判断、`sender`
展示名优先回落 QQ 号、`ForLLM` 视图——已回填描述的图片显示「（图片：描述）」、未描述回落 [图片]；
未知会话返空 items 非 404）；handler/web `GET /api/v1/sessions`
与 `GET /api/v1/sessions/:session_key/window`（只读不进审计，会话键 URL 编码）；前端「对话历史」tab
（活跃会话下拉 + 手动输入、bot(self) 居右气泡、首字色块头像、媒体占位标签、空态提示，
展示分页「首屏最新一页 + 加载更早」）+ 顶栏导航折叠
（主行 + 「更多 ▾」收纳人格/运行态/改密）+ 768px 移动适配（汉堡抽屉、.row 单列、表格横滚 tbl-wrap、
#app-msg 收窄）；index.html ~48KB 维持单文件 go:embed。删除语义记 roadmap B-048 待定案。
另：**摘要纪要区块**（P7-003 补充）——`GET /sessions/:key/window` 响应扩展为
`{items, summaries}`（一次拉取一次渲染），新增 `domain.SessionSummaryReader` 消费接口
（`*memory.MemoryService.GetSummaries` → `SummaryStore` 内存热链，会话首次访问惰性回灌归档）
+ `entity.SessionSummary` 展示视图（Text/Keywords/Decisions/CreatedAt，隐去 chat_id/seq）
+ `domain.Admin.GetSessionSummaries` + dto `response.SessionSummary`/`SessionWindow`；
前端在气泡列表**上方**渲染「更早的对话纪要」区块（摘要=窗口之前已压缩的历史，语义连贯），
**不区分一级压缩/二级融合**（管理面只需「这里有一段更早的纪要」），仅显示当前热链
（被二级融合覆盖的一级原件只在 conversation_summary 归档表，本期不展示）。
```

禁止提前实现（跨阶段禁令，后续阶段能力勿提前实装）：

- 端到端压测（P6-003）

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
- HashiCorp go-plugin（插件动态加载，P4-002 起，net/rpc 变体；插件按自定义指令集协议返回结构化结果，见架构 §8.6；原 Go `plugin` .so 因 Windows 不可用废弃）；
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
│   ├── domain/                     # 领域层：纯接口 + 实体 + 哨兵错误（entity 子包承载），零外部依赖
│   │   ├── entity/                 #   公共实体（Message, Event, Profile...）
│   │   │   └── errors.go           #     哨兵错误 + 参数化错误（SensitiveWordError / ValidationError）
│   │   ├── agent.go                #   Agent 接口（P2-004 由 infra/ai 实现）
│   │   ├── memory.go               #   Memory 接口（P3-001 由 service/memory 实现）
│   │   ├── plugin.go               #   Plugin 接口（stub）
│   │   ├── storage.go              #   Storage 接口（P2-001 已实现）
│   │   ├── control.go              #   Control 接口（P5-001/002 由 service/control 实现）
│   │   ├── sender.go               #   Sender 接口 + WithSender/SenderFrom（B-003，由 infra/onebot 实现）
│   │   └── log.go                  #   LogReader 接口（P7-002 由 infra/logfile 实现；查询条件/结果为 entity.LogQuery/LogPage）
│   ├── service/                    # 业务编排层，依赖 domain 接口
│   │   ├── event/                  #   中间件链：日志 → 限流 → 敏感词 → 持久化(tail)
│   │   │   ├── event.go            #     EventService，HandleMessage 走链
│   │   │   ├── middleware.go       #     Handler/Middleware 类型 + logMiddleware
│   │   │   ├── ratelimit.go        #     令牌桶限流（按群/私聊按用户）
│   │   │   ├── sensitive.go        #     敏感词中间件（AC 自动机）
│   │   │   └── *_test.go           #     核心单测
│   │   ├── agent/                  #   薄透传：GenerateReply → domain.Agent
│   │   ├── memory/                 #   上下文窗口 + 群画像缓存 + 窗口压缩（P3-001/002/003 已实现；成员画像已移除）
│   │   ├── plugin/                 #   插件发现/加载/路由分发（P4-002）
│   │   ├── control/                #   触发判断 + 状态规则（P5-001/002，判定/记账，不发送——发送归 event respond）
│   │   └── log/                    #   日志查询服务 logsvc（P7-002：参数归一后转调 domain.LogReader；包名 logsvc 避免与 stdlib log 混淆）
│   ├── handler/                    # 事件处理入口（薄胶水，无业务逻辑）
│   │   ├── message.go              #   消息事件 → event service
│   │   └── notice.go               #   通知事件 → 规则处理（stub）
│   └── infra/                      # 基础设施，实现 domain 接口
│       ├── onebot/                 #   ZeroBot 封装（已接入：matcher 分发 + 固定文案回复 + Sender 实现 B-003）
│       ├── ai/                     #   eino Agent + 摘要器实现（P2-004 provider 注册中心 + 多模态转换 + tool 机制；P3-003 Summarizer 裸模型单次调用；P3-004 tools/ 记忆更新工具）
│       ├── sqlite/                 #   SQLite 存储实现（已接入：P2-001，8 张表 + migrations；member_profile/plugin_config 已移除，persona 为 agent 绑定人格模板）
│       │   └── migrations/        #     DDL 迁移文件（001 单文件，migrate 全量执行）
│       └── logfile/                #   日志文件读取（P7-002：domain.LogReader 实现，纯标准库读 zap JSON 日志 + 滚动备份，目录经构造注入）
├── plugin-sdk/                     #   独立 SDK module（P6-004）：entity 协议 wire 类型 + plugin go-plugin 接线（宿主 replace 本地）
├── pkg/                            # 可复用工具
│   ├── config/                     #   配置加载（嵌入默认模板 config.default.yaml）
│   ├── logger/                     #   全局 zap 日志（另提供 Context/From 的 ctx 注入派生 logger = trace_id 机制，见架构 §17.6）
│   └── ahocorasick/                #   Aho-Corasick 多模式匹配（敏感词）
├── plugins/                        # 插件目录（运行时，插件子进程可执行文件）
├── data/                           # SQLite 自动生成（运行时创建）
├── config.yaml                     # 本地配置文件（不入库，见 .gitignore；模型 api_key 可用环境变量 PLUMEBOT_APIKEY_<模型名大写> 覆盖，self_id 可用 PLUMEBOT_SELFID 覆盖）
├── docker-compose.yml              # NapCat 启动（入库；ACCOUNT 优先取 PLUMEBOT_SELFID、WEBUI_TOKEN 经 .env 插值，.env 不入库）
├── .env.example                    # .env 模板（入库）
├── docs/
│   ├── architecture.md             # 架构设计文档
│   ├── roadmap.md                  # 任务台账（阶段表 + 遗留事项 B 台账）
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
- 定义哨兵错误（entity/errors.go）。

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
- **发送能力在 infra/onebot**：ZeroBot 的 `ctx` 只在连接层可见（无 CtxFromEvent，`zero.GetBot` 拿到的 Ctx 的 Event 为 nil，只能用于 SendGroupMessage/SendPrivateMessage/CallAction）。限流/敏感词命中由 matcher 内 `ctx.Send` 固定文案回复（`rateLimitedReply`/`sensitiveWordReply` 常量）。Agent 回复采用**方案 B：Sender 注入 ctx**——matcher 闭包构造 per-event 的 `domain.Sender`（持有 `*zero.Ctx`）经 `domain.WithSender` 注入，Handler 链签名保持 `(ctx,msg) error`，需回复的环节 `domain.SenderFrom(ctx).Send(entity.Reply)` 直接发送，载荷结构化（多模态 + 引用回复 + @，见架构 §9.2/§10.2 与 roadmap P6-002 行）。`domain.Sender` 不含动作能力（禁言/踢人等经 `domain.GroupManager` 供 agent tool 调用，已由 B-015 实现，见下条）；非响应式主动发送（异步插件、定时、P5 auto 后续发言）需显式目标，届时另定义 `SendTo` 形态并 main 注入。
- **连接层无事件级 context**：onebot 适配层传 `context.Background()`；service Handler 已预留 ctx 参数（B-007，未来仅改 onebot 一处）。
- **LLM provider 注册中心为注入式实例**（P2-004）：`ai.Registry` 经 main 组装注入，非包级全局单例；`Factory func(ctx, config.Config) (domain.Agent, error)` 接收**完整 Config**（LLM 段 + Tools 段在工厂内组装）；工具表经 `NewOpenAIFactory(tr *ToolsRegistry)` 闭包注入（Factory 签名固定）。
- **Agent 实现细节**（P2-004，`internal/infra/ai`）：`EinoAgent` 封装 eino v0.8.13 的 `adk.ChatModelAgent` —— `Instruction` 通常为空（P6-001 起人格由 service 组装注入 system 消息，见下条）、`Name`/`Description` 填 adk 元数据（为将来多 agent `NewAgentTool` 做准备）、`MaxIterations=20`（tool 循环上限）、仅当启用工具时才注入 `ToolsConfig`。工具挂在注入式 `ToolsRegistry` 上（无全局状态），`Register` 拒绝重复名。
- **人格=DB 人格模板、人格选择 agent，P6-001 起由 service 组装注入 system 消息，不进 infra/agent 层**（P2-004 方案 A' + P4-001 定案 → P6-001 修订，原 Instruction 注入因「改库需重启」不能满足运行时生效而废弃）：persona 表（agent 绑定字段 + name + system_prompt，无 userid/groupid/extend，见架构 §7）按 `cfg.Agent.Name` 由 `service/memory BuildMessages` 每次组装现查（`GetPersonaByAgent`）注入首条 system 消息，**改 persona 表即时生效**；main 侧 `cfg.Agent.SystemPrompt` 置空使 `Instruction` 为空、provider 工厂不再兜底 `DefaultSystemPrompt`；兜底链：模板 `system_prompt` → `config.agent.system_prompt`（`defaultPersona`，main 传入组装器）→ `DefaultSystemPrompt`。不在 agent 配置中写人格 id（配置只有 agent.name）。
- **agent 三要素配置化，多 agent 平滑演进**（P2-004）：`agent.name/description`（空值兜底 `config.DefaultAgentName/DefaultAgentDescription`）；`system_prompt` 空值时由组装器兜底 `defaultPersona`（`config.agent.system_prompt`）→ `DefaultSystemPrompt`（见上条）；`agent.name` 是 adk 元数据标识（multi-agent 路由），与 `bot.name`（QQ 展示名）语义独立不耦合；未来多 agent 演进为 `agents.list[]` + `active` 选择（每项一份三要素，LLM 保持全局 provider 注册中心），本期不实现。
- **模板不替代 memory**（P2-004 评审）：eino ChatTemplate/StateModifier **不引入**（service 层不能 import infra 类型；组装逻辑不进 infra）；memory 存结构化数据不做渲染（同一数据源多渲染目标）；prompt 组装在 service 层（P6-001 完整五段），摘要压缩 prompt 归 service/memory（P3-003）。
- **窗口/状态按会话分锁，跨会话互不阻塞**（P6-001 审查定案）：`Window`（service/memory）与 `ControlService`（service/control）对**同一会话**的读-改写以 per-session 锁串行化（`sync.Map` 注册表 + 每会话 `sync.Mutex`），**不同群/私聊互不阻塞**、跨会话天然并行（持锁绝不含 LLM/IO）。**同会话组装/压缩并发竞态已由 B-040 定案解决**（P6-002）：`GetWindow` 深拷贝 Parts（组装/压缩各持独立快照，无逃逸数组竞态）+ `Window.BackfillParts` 在窗口锁内安全回填图片描述——无跨 LLM 持锁（同会话组装与压缩互不阻塞）、常规路径零冗余落库（跳过条件 `Description!=""` 靠回填喂，见 roadmap P6-002）。
- **插件形态 = 子进程 stdio，.so 废弃，go-plugin 定案**（P4-002 前置定案）：Go `plugin` 包（`-buildmode=plugin` / `plugin.Open()`）仅支持 Linux/FreeBSD/macOS——Windows 开发机不可用、禁交叉编译；且无卸载 API（非真热，只能热添加）、与主程序同进程（panic 拖垮整个 bot）、插件需与主程序完全同版本编译。插件统一为**独立进程 + stdio 通信**（HashiCorp go-plugin，net/rpc 变体，免 protoc）：跨平台、进程隔离、真热重载（重启子进程）。插件遵循**指令集协议**（§8.6）——`entity.PluginRequest{Command, Args, Session}` → `entity.PluginResult{Reply, Actions}`，插件零权限只声明意图、宿主唯一执行者；`domain.Plugin` 签名为 `Execute(ctx, entity.PluginRequest) (entity.PluginResult, error)`。**回复执行已由 P6-002 落地**（校验通过后经 B-003 Sender 发送 `Reply`）；群管理动作执行已由 B-015 落地（dispatchCommand → executeActions 经 `domain.GroupManager` 执行，与 AI 工具共用执行路径，见 §15 决策）。**协议与接线抽为独立 SDK module（P6-004）**：`plugin-sdk/entity`（wire 类型）+ `plugin-sdk/plugin`（`Serve`/`NewClient`），宿主 `internal/domain/entity` 协议类型改类型别名、`ValidatePluginResult` 转发，`cmd/bot` 直接用 SDK `NewClient`；第三方插件只依赖 plugin-sdk 即可独立编写（不 import 宿主 internal），gob 类型名因同 module 路径保持一致。
- **群管理执行器 = per-event ctx 注入，护栏集中在 GroupManager.Execute，工具/插件共用单一执行路径**（B-015 定案，架构 §15）：`domain.GroupManager` 仅一个方法 `Execute(ctx, entity.GroupAction) error`（统一入口，AI 工具与插件 Actions 走同一护栏链，护栏不重复、不旁路）；onebot 层 matcher 闭包构造 per-event `botGroupManager`（持 `*zero.Ctx` + `domain.Storage` + botID）经 `WithGroupManager` 注入 ctx，与 Session/Sender 同构（工具为共享单例，经 `GroupManagerFrom(ctx)` 取执行器）。三道护栏集中在 `Execute`：① per-group 开关 `group_config.group_mgmt_enabled`（002 迁移，默认 1 开；`GetGroupConfig` 无配置行 = 默认开，0 = 显式关闭）；② 触发者与 bot 都须为群主/管理员（`get_group_member_info` 查 role，查询失败/无 role 一律拒绝，fail-closed）；③ mute 时长钳制 30 天（超限平台拒绝）。动作经 `ctx.CallAction` 检查 `APIResponse.Status/RetCode` 反馈成功与否——`SetGroupBan/SetGroupKick/SetGroupCard` 封装吞掉响应（void），动作失败无法反馈，高危能力静默失败不可接受。
- **SQLite 迁移 = 版本记录式（B-015 起，B-034 基础设施顺带建立）**：`migrate()` 建 `schema_migrations` 表（version PK + applied_at），每迁移文件在**单事务内**执行并记录，失败整体回滚不记版本（下次启动重试，避免「ALTER 成功但未记录 → 重启 duplicate column」）；旧库 001 重放靠 IF NOT EXISTS 幂等。001 不再改动，新增列一律新建 `00N_*.sql`（SQLite 无 `ADD COLUMN IF NOT EXISTS`，幂等依赖版本记录）。B-034 完整任务（校验等）仍后置。
- **管理后端契约分层 = 接口在 domain / 通信结构体在 entity / json 归 web dto**（P7 层引用合规定案）：`domain.Admin` 接口（方法签名以 entity 承载）+ 消费侧接口 `domain.SessionWindowReader`/`domain.GroupProfileInvalidator`/`domain.SessionSummaryReader` 全部定义在 domain 层，`service/admin.Service` 实现接口，`handler/web` 依赖 `domain.Admin` 而非具体类型（与其他 service → `domain.Control`/`domain.Memory`/`domain.LogReader` 一致）；通信/出参结构体（`entity.AuthResult`/`entity.SessionOverview`/`entity.SessionMessage`/`entity.SessionSummary`）入 `domain/entity` 保持纯结构（不带 json tag，同 `entity.LogQuery` 先例）；HTTP 入参/出参的 json 序列化由 `handler/web/dto`（request/response）独家接管——entity 不背 json 责任，handler 在 web 边界做 entity↔dto 转换。
- **entity 无 json tag（唯一例外 = `entity.ContentPart` 的持久化形态）**（json 职责收口定案）：`entity.GroupConfig` / `entity.LogEntry` / `entity.LogPage` 的 json tag 已移除，出参形态改由 `response.GroupConfig` / `response.LogEntry` / `response.LogPage` 自持（handler 做 entity→dto 映射，wire 键名/顺序零变化）；`entity.ContentPart` 保留 tag 是因为 `messages.parts` 列为 JSON 文本、由 `infra/sqlite` 直接 `json.Marshal/Unmarshal`——tag 是**持久化形态约定**（列内键名），非 web 出参形态，属有意豁免（`entity.Message`/`SavedMessage` 之类从未带 tag，无需处理）。判据：有无非 web 的序列化消费方。

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
- 哨兵错误定义（entity/errors.go）。

禁止：

- 任何外部依赖 import。

### 6.3 internal/service/event/

职责：

- 消息中间件链编排：`日志 → 限流 → 敏感词 → tail`，tail 持久化消息到窗口 + SQLite；
- `HandleMessage` 走链，`HandleNotice` 暂为 stub。

关键约定：

- `HandleMessage(ctx, msg) error`，中间件命中返回哨兵错误（`entity.ErrRateLimited` / `entity.SensitiveWordError`）；
- 限流：`golang.org/x/time/rate` 令牌桶，按群（私聊按用户）独立，超时返回 `ErrRateLimited`；
- 敏感词：`pkg/ahocorasick` 匹配，空词表 = 不过滤；
- 日志规范（完整版见架构 §17 日志规范）：消息日志为**两条锚点**——入口行 `Info("收到消息", message_id, group_id, user_id, message_type, content, mentioned)` + 结局行 `logOutcome(ctx, msg, outcome…)`（`Info|Warn("消息结局", …, outcome)`，每条消息**恰好**一入口一结局）；日志不重复（infra/onebot 不再打消息 Info；群管理审计只落 `GroupManager.Execute`；发送成功只 Debug；详情作结局行字段）；结局 outcome 枚举与级别见架构 §17.4；等级语义见 §17.3；输出形态见 §17.1（按精确级别分文件 debug/info/warn/error + fatal.log + gin.log，仅文件不写终端，lumberjack 10MB 滚动）；审计与敏感信息（密钥/token/密码绝不入日志）见 §17.5；**trace_id 经 `logger.Context/From` 注入 ctx**（管理端=IP、QQ=group:/private: 会话键，见 §17.6），入口行/结局行/审计/模型度量经 `logger.From(ctx)` 输出以便按会话检索；代码落地状态见 roadmap B-046 与 P7-002（均已落地）；
- 末端 tail 持久化消息（写入窗口 + SQLite），窗口满时触发 P3-003 异步窗口压缩（经 memory.Compress，防重入 + 失败冷却）；随后进入 P4-002 命令分支（/开头 → service/plugin 分发 → 校验指令集并记录；校验通过且含 `Reply` 时经 ctx 内 domain.Sender 发送，B-017，见架构 §10.2）；最后对普通消息走 P6-002 回复闭环 respond（P5-001/002 触发判断 + 状态规则命中 → BuildMessages 拼 prompt → Agent GenerateReply（记忆工具经 ctx Session 写入）→ 经 domain.Sender 发送 → 发送成功才窗口追加 bot 回复 + OnReplied 记账，B-038，见架构 §14.4）。

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
- `sqlite/`：已接入（P2-001，8 张表 + migrations；member_profile/plugin_config 已移除，persona 为 agent 绑定人格模板）。
- `ai/`：已接入（P2-004：Registry provider 注册中心 + openai 兼容工厂 + EinoAgent + 多模态转换，正式单测全绿；P3-004：`ai/tools` 记忆更新工具 store_fact/learn_jargon/forget_fact，会话身份经 `entity.Session` 注入 ctx）。
- `logfile/`：已接入（P7-002：`domain.LogReader` 实现，纯标准库读 `~/.plumebot/logs` 下的 zap JSON 日志与 lumberjack 滚动备份，支持等级 × 时间段 × trace_id + 分页；gin.log 文本不纳入，见架构 §17.6）。
- `plugin-sdk/`（根目录，独立 module，P6-004）：插件协议 wire 类型（`plugin-sdk/entity`）+ go-plugin net/rpc 接线（`plugin-sdk/plugin`：插件侧 `Serve` + 宿主侧 `NewClient`，宿主侧 `Client` 结构化满足 `domain.Plugin`）。宿主 `internal/domain/entity` 协议类型为 SDK 类型别名；第三方插件只依赖 SDK。

每个 infra 包必须：

- 若包含 SQL 操作：`queries.go` — 所有 DML 语句以包级 `const` 存放，方法体内不内嵌 SQL 字面量；DDL 语句存放于 `migrations/*.sql` 通过 `//go:embed` 加载。

哨兵错误定义在 `internal/domain/entity/errors.go`，infra 层引用 `entity.ErrXxx` 返回，供上层 service 通过 `errors.Is` 判断，避免上层耦合数据库驱动。

### 6.6 pkg/config/

职责：

- 配置文件加载与解析；
- `//go:embed config.default.yaml` 嵌入默认模板：`Load()` 时配置文件不存在则写入模板再加载；
- **空值兜底由消费方负责**（如限流 rate≤0→2、burst≤0→20、max_wait≤0→10s；WsURL 空→`ws://127.0.0.1:3001`；Bot.Name 空→`PlumeBot`）；config 层不改写字段；
- **仅敏感/机器相关字段支持环境变量覆盖**（非通用 env 读取）：每个模型条目的 `api_key` 支持 `PLUMEBOT_APIKEY_<模型名大写>` 覆盖（如 `PLUMEBOT_APIKEY_CHAT`，非空时优先于文件值，敏感密钥不入配置文件；`name` 空的条目不参与）；`bot.self_id` 支持 `PLUMEBOT_SELFID` 覆盖（与 NapCat 登录 QQ 号一致时，docker-compose 的 ACCOUNT 可与 self_id 共用同一来源）；
- 新增配置字段必须**双处同步**：`pkg/config/config.default.yaml` + 根 `config.yaml`（config.go 注释已标明）。根 `config.yaml` 已被 `.gitignore`（本地文件，含用户真实密钥，不入库）；配置双处同步靠人工维护，不再有守卫测试强制（原 `TestRootConfigYAMLSyncedWithDefault` 已移除）。

## 7. 代码规则

1. domain 层不 import 任何第三方库（标准库除外）。
2. service 层不 import infra 包。
3. 接口在 domain 定义，实现在 infra。
4. 依赖通过构造函数注入，不使用全局变量。
5. 未实现模块保持 stub（返回 nil 或 error），保持可编译。
6. 不提前实现任何业务逻辑（对照 roadmap 阶段禁令）。
7. 不接入阶段外外部服务。
8. 不为了"架构完整"创建大量空类和方法，只创建架构文档中明确列出的模块。
9. 哨兵错误定义在 `internal/domain/entity/errors.go`（运行期哨兵与管理面校验错误 `ValidationError`/`ErrAlreadyRegistered` 等集中于此）：`ErrNotFound`、`ErrConflict`、`ErrClosed`、`ErrRateLimited`、`ErrSensitiveWord`。参数化错误（如 `SensitiveWordError{Word}`）实现 `Unwrap()` 指向哨兵，上层用 `errors.Is` 判断，禁止在 infra 包内自定义哨兵。
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
