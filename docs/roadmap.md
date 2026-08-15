# PlumeBot 开发阶段规划

> 单人项目，按架构分层逐步构建。每个阶段完成后进入下一阶段，不跨阶段开发。

---

## 总览

| 阶段 | 内容 | 产出 |
|------|------|------|
| 第一阶段 | 项目骨架 | 可编译、零外部依赖的空框架 | ✅ |
| 第二阶段 | 基础设施接入 | ZeroBot 连通 NapCat + eino 可用 + SQLite 落盘 | ✅ |
| 第三阶段 | 记忆系统 | 上下文窗口 + 画像 + 压缩摘要 | ✅ |
| 第四阶段 | 人格与插件 | 人格模板化 + 子进程插件加载 | ✅ |
| 第五阶段 | 触发控制 | mention/auto 模式 + 状态规则 | ✅ |
| 第六阶段 | 联调验收 | 完整消息链路跑通，bot 可对话 | 🔄（P6-001 ✅，P6-002 待实现） |

---

## 第一阶段｜项目骨架

目标：Go DDD 分层工程骨架，可编译，不接入任何外部服务。

| 任务编号 | 任务名 | 内容 | 优先级 | 涉及模块 | 启动条件 | 验收标准 | 状态 |
|---|---------|------|:---:|------|------|------|:--:|
| P1-001 | Go module 初始化 | `go mod init plumebot`，创建目录骨架（cmd/、internal/domain/、internal/service/、internal/handler/、internal/infra/、pkg/），写入 .gitignore (Go 标准) | P0 | 根工程 | 无 | `go mod tidy` 无报错；目录结构匹配 CLAUDE.md | ✅ |
| P1-002 | 配置加载 (pkg/config) | 定义 Config 结构体（Bot、Log、Control），使用 viper 从 config.yaml 加载，Go 侧仅兜底默认值 | P0 | pkg/config | P1-001 完成 | `go build ./pkg/config/` 通过；加载 config.yaml 不 panic | ✅ |
| P1-003 | domain 接口定义 | 定义 domain.Agent、domain.Memory、domain.Persona、domain.Plugin、domain.Storage、domain.Control 六个核心接口，零外部 import | P0 | internal/domain | P1-001 完成 | 每个接口 1-3 个方法签名，仅依赖标准库和 domain/entity；`go build ./internal/domain/` 通过 | ✅ |
| P1-004 | domain 实体定义 | 定义 entity.Message、entity.Event、entity.MemberProfile、entity.GroupProfile、entity.Persona 等公共结构体 | P0 | internal/domain/entity | P1-001 完成 | 实体字段对齐架构文档 4、7、9 节；纯 struct 无方法限制 | ✅ |
| P1-005 | infra 空壳 | 为每个 domain 接口创建 stub 实现：返回 nil 或 error，放置于 internal/infra/{ai,sqlite,onebot,plugin_so,plugin_exe}/ | P0 | internal/infra | P1-003 完成 | 每个包至少一个文件实现对应接口；`go build ./internal/infra/...` 通过 | ✅ |
| P1-006 | service 编排框架 | 创建各 service 包的构造函数，接收 domain 接口依赖并持有；方法体返回 nil 或 error stub | P0 | internal/service | P1-003、P1-005 完成 | 每个 service 构造函数可注入 stub 实现；`go build ./internal/service/...` 通过 | ✅ |
| P1-007 | handler 空壳 | 创建 message.go 和 notice.go，持有 service 引用，方法 stub | P0 | internal/handler | P1-006 完成 | `go build ./internal/handler/` 通过 | ✅ |
| P1-008 | main.go 组装 | 在 cmd/bot/main.go 中完成：加载配置 → 创建 infra stub → 注入 service → 注入 handler → `select{}` 阻塞（不实际连接任何服务） | P0 | cmd/bot | P1-001～007 全部完成 | `go build ./cmd/bot/` 通过；`go vet ./...` 无警告；可编译出空骨架 | ✅ |

---

## 第二阶段｜基础设施接入

目标：ZeroBot 连通 NapCat 可收发消息，eino Agent 可调用 LLM，SQLite 可读写。

| 任务编号 | 任务名 | 内容 | 优先级 | 涉及模块 | 启动条件 | 验收标准 | 状态 |
|---|---------|------|:---:|------|------|------|:--:|
| P2-001 | SQLite 存储层 | 实现 domain.Storage 接口：建表（8 张表：messages、group_profile、group_jargon、member_profile、member_facts、persona、bot_state、plugin_config）、CRUD；启动时自动建表 + 插入默认人格 | P0 | infra/sqlite | P1 全部完成 | 可写入/查询 messages；默认人格 groupid=0 自动插入 | ✅ |
| P2-002 | ZeroBot 连接层 | 实现 OneBot WebSocket 客户端，连接 NapCat，接收原始事件 → 转为 domain.Event → 交给 handler | P0 | infra/onebot | P1 全部完成 | bot 启动后连上 NapCat，能收到群消息事件并打印日志 | ✅ |
| P2-003 | 消息管线中间件 | 实现中间件链（日志 → 限流 → 敏感词过滤），在 event service 中编织 | P0 | service/event | P2-002 完成 | 每条消息有日志输出；限流超限丢弃；敏感词命中拦截且记录 | ✅ |
| P2-004 | eino Agent 接入 | 实现 domain.Agent 接口，封装 eino ChatModelAgent，支持 tool calling | P0 | infra/ai | P1 全部完成 | 传入简单 prompt 可收到 LLM 文本回复 | ✅ |

> 附加交付（P2-003 里程碑内）：敏感词实装为 Aho-Corasick 自动机（`pkg/ahocorasick`，大小写不敏感、返回最早命中词且保留配置原大小写）；敏感词经 `middleware.sensitive_words` 配置；命中返回 `domain.ErrSensitiveWord`（携带命中词），连接层回复「我拒绝回答」。

---

## 第三阶段｜记忆系统

目标：上下文窗口滚动、画像按需加载缓存、摘要压缩流水线。

| 任务编号 | 任务名 | 内容 | 优先级 | 涉及模块 | 启动条件 | 验收标准 | 状态 |
|---|---------|------|:---:|------|------|------|:--:|
| P3-001 | 上下文窗口 | 实现 ring buffer 窗口：初始 20 轮、上限 100 轮；消息自动追加；窗口满触发压缩信号 | P0 | service/memory | P2-001、P2-002 | 群聊消息持续追加窗口；达到 100 轮时返回压缩触发信号 | ✅ |
| P3-002 | 画像加载与缓存 | 实现群聊个人画像和群画像的按需加载 + 内存缓存 + 延迟淘汰（N 轮后移除）；非窗口人物不加载 | P0 | service/memory + infra/sqlite | P3-001、P2-001 | 窗口中人物画像在首次出现时加载到缓存；消失后延迟 N 轮淘汰 | ✅ |
| P3-003 | 窗口压缩策略 | 实现一级压缩（LLM 摘要 + 关键词提取）、二级压缩（多摘要融合）、摘要淘汰（FIFO）；摘要热链存内存，被淘汰/被融合覆盖的摘要落库归档（conversation_summary 表），重启回灌热链底 | P0 | service/memory + infra/sqlite | P3-001、P2-004 | 100 轮触发摘要生成；多摘要达到上限后二次融合；摘要总数有上限；重启后回灌长程摘要 | ✅ |
| P3-004 | 记忆更新闭环 | 实现 eino Tool：store_fact、learn_jargon 等，Agent 对话中自行调用更新 SQLite | P1 | infra/ai/tools | P3-002、P2-004 | Agent 在对话中能通过 tool calling 存储事实；黑话学习标记待确认状态 | ✅ |
| P3-005 | 数据模型变更（P4-001 前置） | 移除 member_profile 表（无写入者/消费者的空壳画像）；member_facts 承担私聊+群聊成员记忆（group_id 空=私聊）；persona 表重构为「人格模板」：删 userid/groupid/extend，加 agent（人格选择 agent，UNIQUE(agent)）/name/system_prompt；开发期直接改 001 schema + 删本地 dev 库（不追加 drop 迁移）；联动 Storage/entity/ProfileCache/seed/测试 | P0 | infra/sqlite + service/memory + domain | P2-001、P3-002、P3-004 | 无 member_profile 残留；persona 新 schema 正确；store_fact 私聊/群聊均正确落库 | ✅ |

---

## 第四阶段｜人格与插件

目标：人格模板化（DB persona 表）、子进程插件动态加载运行。

> 设计变更（P4-001 定案，迭代）：人格改为 **DB 人格模板、人格选择 agent**（persona 表加 agent 绑定字段，
> 移除 userid/groupid/extend，见架构 §7）；member_profile 表移除（成员上下文由 member_facts 承担，
> 私聊/群聊经 group_id 区分）。开发期直接改 001 schema（原 002/003 迁移已并入 001）+ 删本地 dev 库，
> 不追加 drop 迁移；变更由第四阶段前的 **P3-005** 处理，再进入 P4-001。
>
> 设计变更（P4-002 前置，Windows 约束）：原「.so 插件」主方案**废弃**（Go `plugin` 包仅支持
> Linux/FreeBSD/macOS——Windows 不可用、禁交叉编译；且无卸载 API 非真热、同进程无隔离），
> 插件统一为**子进程 stdio 通信**，原 P4-003「exe 子进程」机制提升为主方案，**P4-002/P4-003 合并**
> （见架构 §8）。选型**定案：HashiCorp go-plugin（net/rpc 变体）**。插件遵循自定义**指令集协议**
> （返回结构化 Reply + Actions，见架构 §8.6）；**本期只定义协议 + 宿主校验，不执行**——回复发送归
> P6-002（B-003），群管理动作执行归 B-015（护栏）。`infra/plugin_so` stub 随本任务删除。

| 任务编号 | 任务名 | 内容 | 优先级 | 涉及模块 | 启动条件 | 验收标准 | 状态 |
|---|---------|------|:---:|------|------|------|:--:|
| P4-001 | 人格系统 | 按 cfg.Agent.Name 从 persona 表加载人格模板（GetPersonaByAgent），其 system_prompt 经 Instruction 注入 Agent；默认 agent 无模板则 seed 默认模板；兜底链：模板 → config.agent.system_prompt → DefaultSystemPrompt（注：Instruction 注入已由 P6-001 修订为组装注入，见 CLAUDE.md §5.6） | P0 | infra/ai + infra/sqlite | P3-005、P2-004 | 启动按 agent 名加载模板生效；无模板兜底默认人设 | ✅ |
| P4-002 | 子进程插件加载（吸收原 P4-003） | 定义插件**指令集协议**（entity.PluginRequest/PluginResult + infra/plugin_exe go-plugin RPC 接线，见架构 §8.6）+ 插件发现/命令路由（service/plugin，plugin.json）+ event service 命令分支（/开头 → 分发 → 校验 Result 记录，不发送） | P0 | domain/entity + infra/plugin_exe + service/plugin + service/event + cmd/bot | P1 全部 | 协议类型 + Validate 单测全绿；示例插件经 plugin.json 发现并拉起，命令匹配返回结构化 Result，宿主校验通过并记录（不执行回复/动作） | ✅ |
| P4-003 | ~~exe 插件加载（补充）~~ | 已并入 P4-002（原「启动 exe → stdin JSON → 读 stdout 响应」机制提升为主方案，见上方设计变更） | - | infra/plugin_exe | - | - | - |

---

## 第五阶段｜触发控制

目标：mention/auto 双模式切换，精力/冷却/连续回复规则生效。

| 任务编号 | 任务名 | 内容 | 优先级 | 涉及模块 | 启动条件 | 验收标准 | 状态 |
|---|---------|------|:---:|------|------|------|:--:|
| P5-001 | 触发模式 | 实现 mention（仅 @/私聊回复）和 auto（AI 自主判断）模式，每个群可独立配置 | P0 | service/control | P2-002、P2-004 | mention 模式只有被 @ 才回复；auto 模式可通过预检后自主回复 | ✅ |
| P5-002 | 状态规则 | 实现精力值（消耗/恢复）、冷却时间、连续回复上限、时段控制、短消息忽略；纯规则层，不调 LLM | P0 | service/control | P5-001 完成 | 精力低于阈值不主动说话；连续 N 句后强制冷却；深夜静默 | ✅ |

---

## 第六阶段｜联调验收

目标：完整消息链路跑通，bot 能在群里正常对话。

| 任务编号 | 任务名 | 内容 | 优先级 | 涉及模块 | 启动条件 | 验收标准 | 状态 |
|---|---------|------|:---:|------|------|------|:--:|
| P6-001 | Prompt 组装联调 | 确认人格（persona 模板）→ 会话画像（群画像 + 窗口内成员事实 member_facts 各 N 条）→ 压缩摘要 → 窗口 → 当前消息的 prompt 顺序正确。落地能力：注入上限（`llm.prompt` config：每成员事实条数/黑话条数/描述预算）、图片描述写回（`Storage.UpdateMessageParts`）、`Message.ForLLM()` 文本视图（压缩共用）、describer 缓存 + 失败冷却、at→text 组装转文本；persona 并入组装（BuildMessages 现查 persona 表，改库即时生效，取代 Instruction 注入）；事件管线接线归 P6-002 | P0 | service/memory + infra/ai + infra/sqlite + pkg/config + entity | P3-003、P4-001 | 生成 prompt 格式符合架构文档第 6 节 | ✅ |
| P6-002 | 完整消息链路 | 端到端：收到群消息 → 中间件 → 触发判断 → 拼 prompt → Agent 推理 → 回复（agent service 收 `Generate` 返回值经 ctx 内 `domain.Sender` 直接发送，机制见 B-003）→ 记忆更新 → 窗口追加 | P0 | 全部 | 前五阶段全部完成 | bot 在群聊中被 @ 能正常回复；记忆正常更新；摘要正常生成 |  |
| P6-003 | 稳定性验证 | 连续运行数小时，检查内存泄漏、goroutine 泄漏、SQLite 文件增长、API 调用频率 | P1 | 全部 | P6-002 完成 | 内存不持续增长；goroutine 不泄漏；API 调用不超过限制 |  |

---

## 禁止提前实现清单

以下功能在对应阶段到达前明确不得实现：

- **第一阶段禁**: ZeroBot 连接、eino 调用、SQLite 读写、业务逻辑、中间件
- **第二阶段禁**: 窗口管理、画像缓存、压缩摘要、记忆 Tool、人格系统、插件系统、触发控制
- **第三阶段禁**: 人格系统、插件系统、触发模式
- **第四阶段禁**: 触发模式、完整联调
- **第五阶段禁**: 端到端压测

---

## 待办与遗留事项

> 各阶段交付中明确暂缓/遗留的事项，防止遗忘。完成一项即删除对应行。
>
> 「所属阶段」列标注每个遗留事项的落地阶段：`P5-001` / `P6-002` 为有明确承接任务；
> `P6` = 第六阶段内有需要时；`阶段3` = 原生多模态阶段；
> `后置` = 无固定阶段，触发条件（见「处理时机」列）满足后再排期；
> `持续` = 新增配置字段时持续履行的义务。

| 编号 | 所属阶段 | 事项 | 来源 | 处理时机 | 说明 |
|------|:---:|------|------|----------|------|
| B-003 | P6-002 | 回复发送机制（已定方案 B：Sender 注入 ctx） | P6-002 设计决策 | P6-002 实现时 | 选定「方案 B：Sender 注入 ctx」（替代原方案 A 回复上抛）：matcher 闭包构造 per-event 的 `domain.Sender`（实现内持有 `*zero.Ctx`）经 `domain.WithSender(ctx, s)` 注入，Handler 链签名保持 `(ctx,msg) error` 不变；需回复的环节（agent service 收 `Generate` 返回值、插件等）`domain.SenderFrom(ctx).Send(reply)` 直接发送，载荷为结构化 `entity.Reply{ReplyID, AtTarget, Parts}`（多模态 + 引用回复 + @，onebot 实现转 `message.ReplyWithMessage`/`message.At` 段后 `ctx.SendChain`）。限流/敏感词固定文案维持现状（闭包 `decideReply(err)` 处理哨兵错误，见 CLAUDE.md §5.6）。`domain.Sender` 不含动作能力（与回复正交，见 B-015），也不含非响应式主动发送——后者（异步插件、定时、P5 auto 后续发言）需显式目标，届时另定义 `SendTo` 形态并 main 注入 |
| B-004 | P6-003 | 无界 map 内存治理（限流/窗口/画像/摘要） | P2-003 实现注释 + 设计评审 L1 | P6-003 | ratelimit `map[string]*rate.Limiter`、`Window.sessions`、`ProfileCache.groups`、`SummaryStore.chains/nextSeq` 四个按群/用户键的 map 只增不删，需按空闲时长统一淘汰（LRU 或 TTL） |
| B-006 | 持续 | 配置模板双份同步 | config 重构 | 新增配置字段时 | pkg/config/config.default.yaml 与根 config.yaml 需手动同步（config.go 注释已标明）。阶段 2 llm 数组化后守卫测试 `TestRootConfigYAMLSyncedWithDefault` 已随 `models[]` 更新，api_key 按条目逐项排除比较 |
| B-007 | 后置 | 连接级 context 传播 | P2-003 设计讨论 | ZeroBot 支持或自研连接时 | ZeroBot 无事件级 ctx，连接层传 context.Background()；service Handler 已预留 ctx 参数，未来仅需改 onebot 一处 |
| B-008 | 后置 | eino 版本升级观察 | P2-004 评审决策 | eino-ext 跟进 v0.9 后 | 当前锁定 eino v0.8.13 + eino-ext openai v0.1.13。**不升 v0.9 的理由**（原 P2-004 计划文档 §3 决策，已归档进本行）：① eino-ext 生态滞后——eino-ext main 仍 require `eino v0.7.13`，与 v0.9 schema 大改（ToolInfo 移除 Bound/InvokableRun、agent 迁 adk）组合存在编译/行为不兼容风险；② v0.9 取最终文本需 Runner + AsyncIterator 事件循环，v0.8 为薄门面（消息进、文本出），本项目不需要事件流复杂度；③ v0.9 发布太新，文档与示例几乎全是旧 API，踩坑成本高；④ 本项目需要的多模态字段（UserInputMultiContent/MessageInputImage）、tool 自动循环在 v0.8.13 全部可用且非废弃；⑤ 迁移成本可控——domain.Agent 是门面，infra/ai 内部换实现不影响上层。API 速查见 docs/eino-notes.md；待 eino-ext 跟进 v0.9 且 adk API 稳定后评估迁移 |
| B-009 | 后置 | 多模态消息模型 / 描述机制 | P2-004 范围边界 | 有需要时 | **阶段 1/2 已完成（P5 多模态修复 + 描述机制）**；**惰性装配接线已于 P6-001 完成**（BuildMessages 经 describer 惰性描述最近 N 轮 + 当前消息的图片，预算 llm.prompt，describer 内缓存 contentHash→desc）。剩余：① NapCat 图片 URL 可达性已加门控 spike（`TestSpikeNapCatImageReachable`，需真实 NapCat 复验）；② 阶段 3 原生多模态（`native_multimodal` 配置开关，默认关，字段已占位，见 B-035） |
| B-012 | 后置 | 摘要关键词召回未规划 | P3-003 验收边界 | 检索式长程记忆需要时 | 归档摘要（conversation_summary）带 keywords 字段，但当前只用于回灌热链底，无「按关键词召回相关摘要」的消费路径；届时实现关键词索引/检索（架构 §4.1「关键词检索走本地 SQLite」） |
| B-013 | 后置 | 氛围标签写入者归属 | P3-004 讨论 | 群氛围周期分析任务排期时 | `group_profile.atmosphere` 当前无写入者（P3-002 仅加载/缓存）；「氛围标签周期性 LLM 分析」（架构 §4.3）暂无任务承接。P3-004 确认黑话走 group_jargon 独立表 + 状态机，氛围标签仍留 profile（整组重写、非逐条生命周期，见架构 §4.3.1） |
| B-015 | 后置 | AI 群管理动作（设计已定） | 设计讨论（B-003 关联） | 有实际需求时 | AI 自主群管理（禁言/踢人/改名片等）经 agent tool 触发，不走回复通道：新建 `domain.GroupManager` 接口（Ban/Kick/...），与 `domain.Sender` 分离——动作权限不注入整条消息链（禁言等为高危能力），仅暴露给工具层；实现 per-event 经 ctx 注入（OneBot 动作绑定当次事件 `*zero.Ctx`，与 Session/Sender 同构，工具为共享单例）；护栏：per-group 配置开关（默认关）+ bot 需管理员权限 + 工具 Desc 写清触发边界 |
| B-016 | 后置 | Agent 动态人格演化暂缓 | P4-001 设计决策 | 有需要时 | 人格为 DB 人格模板、人格选择 agent（persona.agent 绑定，见架构 §7），无 update_persona tool / 无运行时演化；如需 per-group 人设或 Agent 在对话中自主调整人格，届时再加工具与维度 |
| B-017 | P6-002 | 插件回复/动作**执行** | P4-002 协议先行决策 | P6-002 / B-015 | 插件返回 `entity.PluginResult{Reply, Actions}` 仅协议定义 + 宿主校验记录（架构 §8.6），不执行。回复发送（文本/图片/引用/@，`Reply.Segments`/`Quote`/`At`）归 P6-002（B-003 Sender）；群管理动作（`Actions`，mute/unmute/kick/set_card）归 B-015（GroupManager + per-group 开关 + 管理员校验） |
| B-018 | 后置 | 插件热重载 | P4-002 范围外 | 有需要时 | plugin.json / 插件 exe 变更（mtime）→ 自动重启该插件进程（go-plugin 原生支持重启，mtime 轮询零新依赖） |
| B-019 | 后置 | 插件崩溃自动重启 + 超时策略细化 | P4-002 范围外 | 有需要时 | go-plugin 已能检测进程退出；崩溃自动重启与单次调用超时策略（当前 `infra/plugin_exe` 固定 5s）细化后置 |
| B-020 | 后置 | `plugin_config` 表使用 + 插件自持状态 | P4-002 范围外 | 有需要时 | 按群插件配置（§11 `plugin_config` 表）与插件自持状态读写，当前 `plugin.json` 仅承载命令表元数据 |
| B-021 | P6 | 引用回复（reply 段）解析 | 多模态修复阶段 1 裁剪 | P6 有需要时 | 入站 `reply` 段当前被丢弃（face/forward/json/xml/music 同为永久简化，但 reply 不同）。引用回复是强上下文信号：支持需解析引用 `message_id`，从窗口 / SQLite 找回原文并入上下文。P6 级能力，未排期 |
| B-022 | 后置 | `data/image_cache/` 文件只增不清 | 阶段 2 base64 入链闭合 + 设计评审 E7 | 有需要时 | `internal/infra/imagecache` 按内容 md5 去重落盘（同图幂等），但无引用计数/淘汰，长期运行会累积缓存文件；届时按体积/时间做清理策略。另：`Save` 的 `Stat+WriteFile` 有 TOCTOU，改 `O_CREATE|O_EXCL` 原子去重（随本项处理） |
| B-033 | P6 | `GetMessages` 私聊维度 | 设计评审 S3 | P6 | 接口加 user_id，私聊按用户区分（当前 `group_id=""` 会混） |
| B-034 | 后置 | 版本化迁移 | 设计评审 S1 + P5-002 审查 | 生产前 | `schema_migrations` 版本化，替代全量重跑 001（解决加列不生效）。**P5-002 触发案例**：group_config 扩 10 列时原地改 001，对已存在的旧库（P5-001 时代 2 列）升级会静默失效（migrate 只 `CREATE TABLE IF NOT EXISTS` 不加列，12 列 SELECT 报错 → auto 模式对所有非 @ 群消息静默）。开发期 data/ 为空无影响；生产前必须版本化，并新增独立迁移文件用 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 补列 |
| B-035 | 阶段3 | 阶段3 原生多模态（`native_multimodal`）+ 本地路径转 base64 | 设计评审 M4 + 多模态阶段 3 设计 | 阶段3 | `native_multimodal: true` 时 image part 直接进 ChatMessage 喂视觉模型（可选增强：读取后转描述省上下文；可能的对象存储升级后续）；`loadImageBytes` 下沉共享 helper，`ToSchema`/`partCommon` 对本地路径走 base64（默认关，不阻塞；配置字段已占位） |
| B-036 | P6-003 | per-group 配置内存缓存 | P5-001 设计 + P5-002 评估 | P6-003 map 治理时 | 热路径每条非命令消息一次 `GetGroupConfig` 单 PK 查询。**P5-002 评估结论**：加 10 列后仍为单 PK 查询（本地 SQLite 纯 Go 驱动，微秒级，秒级流量可忽略）；缓存收益 < 引入的一致性窗口 + 无界 map 淘汰复杂度，维持不缓存。如优化，per-group 静态配置（groupID→mode+rules，变更低频）加短 TTL 内存缓存，与 B-004（ratelimit/Window/ProfileCache/SummaryStore 四个 map）统一在 P6-003 做 LRU/TTL 淘汰 |
| B-038 | P6-002 | OnReplied 接线时序（防重复记账） | P5-002 审查 M2 | P6-002 接发送时 | P5-002 约定「judge 触发即 OnReplied 记账」（标记触发=视为说话，架构 §9.2）。P6-002 真实发送后，OnReplied 应在**发送成功环节**调用并移除 judge 处调用，否则同一消息记两次（能量/冷却/连续重复扣） |
| B-039 | P6 | P5-002 已知边界 | P5-002 审查 L2/L3/L4 | P6 有需要时 | ①纯图片/媒体消息 auto 模式永不触发（`PlainText()` 空 → short_message 短路，多模态启用后需豁免非 text 段）；②静默时段按服务器本地时区（群成员时区不同时窗口偏移）；③per-group 只配 `quiet_hours_start` 且等于全局 end 时静默被禁用（start==end 空段语义，建议配置侧 warn） |
| B-040 | P6-002 | 组装/压缩同会话并发约束落实 | P6-001 审查 BUG-1 | P6-002 接线时 | `BuildMessages` 的惰性描述回填依赖 `GetWindow` 浅拷贝（Parts 共享底层数组**逃逸出窗口锁**），**同会话 BuildMessages 与窗口压缩（Compressor）必须串行**，否则并发读写共享数组产生数据竞态（ZeroBot 每条消息一个 goroutine）。跨会话已由「窗口/状态 per-session 锁」保证互不阻塞（P6-001 审查后落实，见 CLAUDE.md §5.6 与架构 §4.1）；**剩余的是同会话数组竞态**：P6-002 接线时二选一：① 事件管线对同会话消息处理加锁（注意窗口 per-session 锁不覆盖逃逸数组，需组装/压缩共享同一把会话锁）；② `GetWindow` 改深拷贝（放弃窗口内存回填，靠 describer 缓存 + `UpdateMessageParts` 持久化，代价是每次组装重查重写） |
| B-041 | P6 | 空 MessageID 根修（OneBot message_id=0） | P6-001 审查 BUG-2/6 | P6 有需要时 | 入站 `message_id=0` 时 `formatID` 返回 `""`，导致：SQLite `message_id=''` 行被多消息共享（描述写回错位）、窗口内多副本。P6-001 已在组装层防御（空 ID 不持久化描述 + 按位置去重）；根修在 onebot convert 对空 ID 合成唯一 ID（如时间+内容哈希），消除 `''` 行共享与窗口多副本 |

---

> 每个任务完成后应执行 `go build ./... && go vet ./...` 验证。
