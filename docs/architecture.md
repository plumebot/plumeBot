# PlumeBot 架构设计

> 本文档记录项目架构讨论的全部决策，作为后续开发的基准。

---

## 1. 项目概述

基于 OneBot 协议开发的 QQ 机器人，对接 NapCat，使用 Go 语言。核心能力：

- AI 对话（接入 LLM，自主判断是否回复）
- 命令系统（插件分发）
- 群聊记忆（画像 + 上下文窗口）

---

## 2. 技术选型

| 层 | 选型 | 说明 |
|----|------|------|
| OneBot 连接层 | ZeroBot | Go 生态 OneBot v11 框架 |
| Agent 引擎 | eino (CloudWeGo) | 字节跳动开源 AI Agent 框架，支持 ChatModelAgent / DeepAgent / Compose |
| 存储 | SQLite + modernc.org/sqlite | 纯 Go 驱动，无 cgo |
| 日志 | uber/zap | 高性能结构化日志 |
| 配置 | spf13/viper | YAML 配置文件（`config.yaml`）；仅敏感/机器字段支持环境变量覆盖（模型 api_key 经 `PLUMEBOT_APIKEY_<模型名大写>`、self_id 经 `PLUMEBOT_SELFID`，见 CLAUDE.md §6.6） |
| 向量检索 | LLM API embedding（默认关闭） | 可选开启，调 LLM embedding 接口；关闭时走 SQLite 关键词+时间检索 |
| 插件 | 子进程 stdio（HashiCorp go-plugin，net/rpc 变体） | 原 .so 方案因 Windows 不可用废弃；插件按自定义指令集协议返回结构化结果（见 §8.6） |
| 测试 | Go 标准库 testing | 零额外依赖 |

**外部依赖清单（除 NapCat 外无任何额外服务）：**

- NapCatQQ（QQ 登录和 OneBot 消息收发）

---

## 3. 整体架构

```
NapCat (QQ登录)
   │ OneBot WebSocket
   ▼
ZeroBot (连接层，只管收发)
   │ 事件
   ▼
路由层 (命令/普通消息/@消息 分流)
   │                    │
   ▼                    ▼
插件系统               Agent 决策 (eino)
命令分发              是否说话 / 说什么 / 调什么工具
(/xxx)                   │
                    ┌──────┼──────┐
                    ▼              ▼
                 [窗口]         [群画像]
                 内存            SQLite
```

---

## 4. 记忆系统（三层）

> **会话键约定**：群聊 = `GroupID`，私聊 = `private:`+UserID，贯穿上下文窗口、摘要归档
> （`conversation_summary.chat_id`）、`bot_state` 运行态与触发控制状态（§9.2）。
> 统一由 `entity.Message.SessionKey()` 派生（P5-002 消重后单一事实来源）。
>
> **例外（P6-002 修复）**：私聊 bot 自身回复的作者是 botID，按自身 `SessionKey()` 会派生
> `private:`+botID 独立会话，回复将永远进不了用户对话窗；由 event respond 经
> `MemoryService.PersistMessageToSession` 显式并入触发消息（用户）的会话，bot 回复与用户消息同窗
> （SQLite 落库仍记 botID 作者不变）。

### 4.1 上下文窗口（短期）

- 存储：纯内存，ring buffer（消息全量已持久化到 SQLite messages 表）
- 初始容量：20 轮
- 上限：100 轮
- 生命周期：进程期间，重启丢失，正常旁听积累即可
- 重启缺口：bot 重启期间错过的消息，通过 Agent Tool 调用 NapCat `get_group_msg_history` 按 seq 拉取补全
  - bot 运行时持续记录每个群最后收到的 seq
  - 重启后从上次的 seq+1 开始拉，精准补全断档
  - 拉取到的消息写入 SQLite messages 表，确保后续可检索
  - 该 API 不支持关键词搜索，关键词检索走本地 SQLite
- 锁粒度：**每会话一把锁（per-session）**，不同群/私聊的窗口操作互不阻塞，单会话内串行保序
  （`Window` 用 `sync.Map` 注册表 + 每会话 `sync.Mutex`，与 §9.2 service/control 的 per-session 锁同模式）。
  `GetWindow` 深拷贝 Parts（B-040 定案，P6-002）：组装/压缩各持独立快照，无逃逸数组竞态；
  图片描述经 `BackfillParts` 窗口锁内深拷贝回填，无跨 LLM 持锁，同会话组装与压缩互不阻塞。
- 作用：直接拼入 prompt 让 Agent 知道"刚刚在聊什么"

### 4.2 成员记忆（私聊 / 群聊，中期）

- 存储：SQLite，`member_facts` 表，按 `(group_id, user_id, fact)` 唯一
- 粒度：对某个用户的零散事实；`group_id` 区分场景——**空 = 私聊记忆，非空 = 群聊记忆**（同人在不同群可记不同事实）
- 内容：事实性记忆、兴趣话题等原子条目（一条一记，各有独立生命周期：去重 / 删除）
- 写入：Agent 经工具（store_fact / forget_fact，P3-004）实时更新，不并入画像整行重写
- 读取：P6 prompt 组装时按当前会话成员现查注入到 §6 的 ② 会话画像块（「无界写入、有界注入」，每成员各 N 条，上限由组装消费方定——`llm.prompt`，P6-001 落地）
- 召回：`(group_id, user_id)` 精确查询

> 说明：原「群聊个人画像（member_profile，活跃度/亲密度统计）」无写入者、无消费者，
> 已移除（P3-005 数据模型变更）。成员持久上下文唯一机制即本节的 member_facts，
> 私聊与群聊由 `group_id` 区分。

### 4.3 群聊画像（中期）

- 存储：SQLite，每个群一条
- 内容：群文化特征、主流话题、活跃时段、群规、黑话词典、氛围标签
- 更新：黑话/梗持续学习+人工审核；氛围标签周期性 LLM 分析
- 召回：精确查询 group_id

### 4.3.1 画像（聚合）与条目记忆（明细）的分界

（P3-004 讨论定案；member_profile 已于 P3-005 移除，聚合层仅余群画像 group_profile）

- **聚合字段与条目记忆互补不冲突**：group_profile 的字段是「聚合摘要」，member_facts/group_jargon 是「原子条目」，是同一问题在聚合层与明细层的两种形态。
  - 聚合字段**有界、整行重写**：一小撮标签/数值，周期分析或规则层整组重写（Upsert 一次覆盖全字段）；
  - 条目记忆**无界、逐条累积**：一条一条追加，各有独立生命周期（去重 / 审核 / 删除），**不并入 profile 整行 upsert**（避免并发整行竞态）。黑话的 pending/confirmed 审核状态机即此生命周期的体现。
- **无界写入、有界注入**：条目记忆无界落库（小行 + 索引，与 messages 全量落库同性质）；真正有界的是注入 prompt 的量——组装时只注窗口内成员各 N 条事实 + confirmed 黑话，上限由 P6-001 消费方决定。
- **读在组装、写在 tool**：事实/黑话的读取在 prompt 组装时现查注入；Agent 执行中经 tool（store_fact / learn_jargon / forget_fact，P3-004）写入。**不做查询型 tool**（数据量小、直接注入更稳，避免模型不可靠地"记得去查"）。

### 4.4 记忆更新闭环

Agent 通过 eino Tool 机制驱动记忆更新，在对话中自行判断何时调用。

- 事实记忆和群黑话由 Agent Tool 实时更新
- 群氛围标签由定时任务周期性分析

事实冲突直接覆盖。具体 Tool 实现时再定。

---

## 5. 窗口压缩策略

### 5.1 一级压缩

- 触发：窗口达到 100 轮上限
- 操作：取最早 80 轮 → LLM 压缩为一条摘要
- 输出：摘要文本 + 关键词标签 + 关键决定
- 结果：摘要不计入轮数，窗口继续容纳新消息

### 5.2 二级压缩

- 触发：多条摘要累积到一定程度
- 操作：多条摘要 → LLM 融合为一条综合摘要

### 5.3 摘要淘汰

- 综合摘要也有长度上限，超过后逐步舍弃最旧的摘要（FIFO 淘汰）

### 5.4 摘要参与对话

- 方式：直接拼接在 prompt 中，位于人格设定之后、当前窗口之前

### 5.5 摘要存储（P3-003 定案）

- 热链存内存：当前参与对话的最新摘要，参与二级融合与 FIFO 淘汰，直接拼入 prompt（§5.4）。
- 长程归档存 SQLite（`conversation_summary` 表）：被融合覆盖 / 被淘汰的摘要落库，
  重启后按会话回灌最新若干条作热链底；其余长程历史保留在库中（带 keywords 索引，供后续检索召回，见 §4.1）。

---

## 6. Prompt 组装顺序

```
┌──────────────────────┐
│ ① 系统人格            │  persona 模板的 system_prompt（按 agent 名加载），定义"你是谁"
├──────────────────────┤
│ ② 会话画像            │  群画像（群文化/黑话/氛围 + confirmed 黑话）
│                      │  + 窗口内成员事实（member_facts 各 N 条，见 §4.2）
├──────────────────────┤
│ ③ 压缩摘要            │  历史上下文（全量拼接）
├──────────────────────┤
│ ④ 当前窗口            │  最近 N 轮原始消息
├──────────────────────┤
│ ⑤ 当前消息            │  正在处理的这条
└──────────────────────┘
```

> ② 会话画像 = 群画像（group_profile）+ 窗口内成员事实（member_facts，每成员各 N 条）。
> 成员事实与 confirmed 黑话均「读在组装」现查注入（§4.3.1，上限见 `llm.prompt`，P6-001 落地）；
> 私聊无群画像，② 仅含当前用户成员事实（group_id 空）。
>
> ④/⑤ 渲染（P6-002 扩展；P6 联调昵称注入）：群聊消息带发送者前缀 `[昵称]: 内容`，
> 无昵称回落 `[QQ号]: 内容`（昵称 = convert 从 ev.Sender 提取，群名片 card 优先回落 nickname，
> 见 `infra/onebot convert.go senderDisplayName`；bot 自身回复由 event 填 `SenderName` = bot 展示名
> （`cfg.Bot.Name`/`DefaultBotName`，见 `event.go botReplyMessage`），角色仍为
> assistant）——格式与压缩输入（§5.1 buildLevel1UserPrompt）对齐，agent 可据此区分不同说话人，
> 并按昵称自然称呼/点名；私聊对方唯一、② 已标「用户 ID」，不加前缀。
> ② 会话画像成员事实同样昵称渲染「昵称：事实」（`windowSenderNames` 取窗口内消息 SenderName，
> 无昵称回落 QQ 号）。`SenderName` 纯内存不落 SQLite：旧消息/重灌消息为空 → 组装回落 QQ 号。

---

## 7. 人格系统

### 7.1 核心思路

人格（人设）= AI 自己的性格，用 **DB 人格模板**（persona 表）定义。**人格选择 agent**：
模板行通过 `agent` 字段绑定到某个 agent（按 agent 名），运行时按 `cfg.Agent.Name` 加载对应模板注入；
**不在 agent 配置中写人格 id**（配置只有 agent.name，不引用 persona）。

### 7.2 存储（persona 表）

```sql
persona (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  agent         TEXT NOT NULL DEFAULT '',  -- 绑定的 agent 名（人格选择 agent），UNIQUE
  name          TEXT NOT NULL DEFAULT '',  -- 展示名（给人看），不参与逻辑
  system_prompt TEXT NOT NULL DEFAULT ''   -- 完整人设文本（P6-001 起经 service 组装注入 system 消息）
);
```

- `agent` 是「人格选择 agent」的绑定字段（按 agent 名匹配 `cfg.Agent.Name`），`UNIQUE(agent)` 一人一格；
- 移除原 `userid / groupid / extend / traits`（不做群级区分、不做 extend 继承；人设文本即模板本身）。

### 7.3 生效路径

P6-001 起：`service/memory BuildMessages` 每次组装按 `cfg.Agent.Name` 现查 persona 模板（`GetPersonaByAgent`），
其 `system_prompt` 作为 ① 段拼入首条 system 消息（与 ② 会话画像、③ 历史摘要 同消息）。
**改 persona 表即时生效，无需重启**。兜底链：模板 `system_prompt` → `config.agent.system_prompt`
（`defaultPersona`，main 传入组装器）→ `DefaultSystemPrompt`。`ChatModelAgentConfig.Instruction`
自 P6-001 起不再承载人格（`cfg.Agent.SystemPrompt` 置空，provider 工厂不再兜底）。

### 7.4 更新与初始化

直接改 persona 表（人工/管理端）即时生效（P6-001 起 BuildMessages 每次组装现查，无需重启，见 §7.3）。
「Agent 在对话中感知环境后自行调整人格」（原 update_persona tool 思路）暂缓，列为未来方向，见 roadmap B-016。
启动时默认 agent 无模板则 seed 一条默认模板（P4-001）。

---

## 8. 插件系统

> 设计变更（P4-002 前置，Windows 约束）：原「.so 动态加载」主方案**废弃**。Go 官方 `plugin` 包
> （`-buildmode=plugin` / `plugin.Open()`）仅支持 Linux/FreeBSD/macOS——Windows 开发机不可用、禁交叉编译；
> 且插件无卸载 API（非真热，只能热添加）、与主程序同进程运行（panic 拖垮整个 bot）、
> 插件需与主程序完全同 Go 版本编译。插件统一改为**子进程 stdio 通信**（原 §8.3「exe 子进程」提升为主方案）。

### 8.1 插件形态（单一形态：子进程）

| 形态 | 说明 | 优先级 |
|------|------|:---:|
| 子进程 stdio | 插件为独立进程，主程序经 stdio 通信；跨平台、进程隔离、真热重载（重启子进程） | 主方案 |

> 实现选型（P4-002 定案，P6-004 更新）：**HashiCorp go-plugin，net/rpc 变体**——握手/版本协商/崩溃检测/热重载齐全，
> 纯 Go 无 cgo、免 protoc 代码生成（stdlib net/rpc + gob 序列化结构化结果）。**插件协议与接线抽为独立
> SDK module `github.com/plumebot/plugin-sdk`（P6-004，方案 A）**：`plugin-sdk/entity`（协议 wire 类型）+ `plugin-sdk/plugin`
> （`Serve`/`NewClient` 接线）；宿主与第三方插件共用，第三方无需 import 宿主 internal。
> gRPC 变体留作将来支持非 Go 语言插件的升级路径。

### 8.2 子进程 stdio 通信

- 传输：stdio（非网络），主进程 ↔ 插件子进程
- 格式：go-plugin 协议（net/rpc + gob）；插件 = 依赖 `plugin-sdk` 编译出的独立 exe（仅需 plugin-sdk，不 import 宿主 internal）
- 热重载：插件代码变更 → 重启该插件子进程，bot 本体不动、无需重编译主程序
- 隔离：插件崩溃不影响主进程；宿主经 go-plugin 检测进程退出（自动重启属 B 类遗留）

### 8.3 .so 动态加载（已废弃）

原主方案，因 Windows 不可用 + 非真热 + 同进程无隔离而废弃。本节仅作决策记录，不再实现。

### 8.4 插件发现

扫描 `plugins/` 目录，优先读 `plugin.json` 元数据，无则用默认规则。

### 8.5 插件与 Agent Tool 的关系

- 插件 = 命令分发（`/天气 北京` → 精确匹配 → 直接返回）
- Tool = Agent 能力（"今天好热" → Agent 判断 → 调 weatherTool → 自然语言回复）
- 两者并存，互不冲突

### 8.6 插件协议（自定义规则，P4-002 定案）

插件遵循统一的**指令集协议**：**插件零权限、只声明意图，宿主是唯一执行者**——插件进程碰不到 OneBot API，
安全/审计/权限收敛在宿主侧（与 B-015 护栏精神一致）。

- **请求**（宿主 → 插件）：`PluginRequest{Command, Args, Session{GroupID, UserID, MessageID}}`，
  `group_id` 空 = 私聊（沿用 member_facts 约定）；`proto` 版本字段预留演进。
- **结果**（插件 → 宿主）：`PluginResult{Reply, Actions}`——
  - `Reply`：单条回复，多段混排 `Segments`（`text` / `image` / `face`），消息级 `quote`（引用触发消息）
    与 `at`（`""` / `"sender"` / 具体 user_id）；
  - `Actions`：附加动作，强类型枚举 `GroupOp`（`mute` / `unmute` / `kick` / `set_card`），
    对齐 B-015 `domain.GroupManager` 能力集。
- **执行范围（P4-002 定案，P6-002/B-015 更新）**：协议完整定义 + 宿主校验（`Validate()` 校验枚举/必填字段）。
  插件零权限只声明意图，宿主唯一执行者。**回复执行已落地**：校验通过的 `Reply` 由 event 命令分支经
  B-003 `domain.Sender` 发送（P6-002，文本/图片/引用/@，见 §10.2）；**群管理动作 `Actions` 已执行**
  （B-015）：dispatchCommand → executeActions 经 ctx 内 `domain.GroupManager` 执行，与 AI 工具共用
  同一执行路径与护栏链（per-group 开关 + 管理员校验 + 时长钳制，见 §15）。
- **协议类型落点（P6-004 更新）**：`plugin-sdk/entity`（协议 wire 类型，单一事实来源）；`plugin-sdk/plugin` 内含
  go-plugin 的 RPC 接线（宿主/插件共用）。宿主 `internal/domain/entity` 对协议类型做**类型别名**
  （`type X = sdkentity.X`）+ `ValidatePluginResult` 转发，宿主业务代码继续经 entity 引用，
  gob 类型名与插件侧一致（同一 SDK module 路径，宿主 `replace` 本地）。

---

## 9. 触发控制

### 9.1 触发模式

| 模式 | 说明 |
|------|------|
| mention | 仅被 @ 或私聊时回复；所有消息仍流入窗口和 SQLite，被 @ 时上下文完整 |
| auto | @ 和私聊一定回复；普通消息由 Agent 自主判断是否加入 |

触发模式只控制「是否回复」，不影响「是否收消息」——消息始终旁听并缓存。

### 9.2 状态规则（规则层，不调 LLM）

bot 在每个群维护独立状态，纯规则驱动：

| 规则 | 说明 |
|------|------|
| 精力值 | 每次回复消耗，随时间恢复。低于阈值不主动说话（@ 除外） |
| 连续回复上限 | 连续说了 N 句后强制休息 |
| 冷却时间 | 两次主动回复之间最小间隔 |
| 时段控制 | 深夜/凌晨不参与对话（除非 @） |
| 短消息忽略 | <4 字的消息不触发 Agent 判断 |

**规则边界**（P5-002 定案）：五条规则只约束「主动发言」——auto 模式下的普通消息。
被 @ / 私聊 = 强制回复，绕过全部状态规则（架构 §9.1 的「触发只控制回复」）；mention 模式下规则不生效。
**拦截 ≠ 丢弃**：被规则拦截的消息已走完 日志→限流→敏感词→持久化（窗口+SQLite）链，只是不进回复路径，仍旁听并缓存（§9.1）。

**参数来源**（优先级）：
per-group `group_config` 列（非 0/非空）→ 全局 `cfg.Control.state` → 代码默认常量（service/control）。

**自动建行**：群首次被 bot 触达（`ControlService.resolveParams` 遇 `GetGroupConfig` 返回 `ErrNotFound`）时，
按**当前全局生效值**快照落一行（`defaultGroupConfig`；私聊不建行，写库失败仅告警不阻断回复链路）。
落库三处刻意处理：`mode` 写归一化后的枚举（管理面校验只接受 `mention`/`auto`）、静默时段由内部分钟数还原
为 `"HH:MM"`、`group_mgmt_enabled` 固定写 1（显式开关，留零值会静默关掉该群群管理）。
后果：快照值与「走全局」等价（读取行为不变），但该行此后**独立于全局**——`cfg.Control` 变更不再影响该群；
管理面 DELETE 删行后，该群下一条消息会按当时的全局值重新建行（非持久）。

| 参数 | 全局默认 | 说明 |
|------|---------|------|
| `energy_max` | 100 | 精力上限 |
| `energy_cost` | 10 | 每次回复消耗 |
| `energy_recover` | 5/分钟 | 精力恢复（惰性计算，读时按整分钟增益，不跑定时器） |
| `energy_threshold` | 20 | 低于此值不主动说话 |
| `cooldown_seconds` | 60 | 两次主动回复最小间隔（兼连续计数窗口） |
| `consecutive_limit` | 5 | 连续回复上限，达此值进入强制休息 |
| `rest_seconds` | 300 | 连续达上限后的强制休息时长；休息期内主动发言拦（Reason=cooldown），休息内 @ 不延长休息 |
| `quiet_hours_start/end` | 23:00/07:00 | "HH:MM" 跨午夜约定（end<start 取并集）；start==end = 空段禁用 |
| `short_message_chars` | 4 | 短消息忽略阈值（按字符数，`PlainText()`） |

**评估序**（auto 模式普通消息）：`short_message` → `quiet_hours`（两者不读 bot_state，短路省查询）→
`energy` → `cooldown`/连续休息 → `auto_pass`。命中即返回对应 `DecisionReason`
（`short_message`/`quiet_hours`/`low_energy`/`cooldown`，B-023 定名，已完结）。

**运行态**：精力/冷却/连续计数存 `bot_state.state` JSON（`group_state` 结构），每群一条；
私聊独立一行（key=`"private:"+UserID`，与 §4 会话键一致）。`OnReplied` 在 **发送成功环节**回调
（P6-002 定案，B-038：event 管线 respond 在 `domain.Sender.Send` 成功后才调用，移除 judge 触发点，
防同一消息重复记账），消耗精力、记冷却、递增连续计数。主动发言 DB 故障 fail-closed（不主动刷屏）；
强制回复不读状态、故障必回。发送失败 / Agent 推理失败 = bot 未说话，不记账、不追加窗口。

---

## 10. 事件处理管线

### 10.1 事件分类

| 事件类型 | 处理策略 | 进入 Agent 上下文 |
|------|------|:---:|
| 消息事件 (group/private) | 核心管线（命令分发 / Agent 决策） | ✅ |
| 通知事件 (群增减/戳一戳/禁言) | 规则引擎处理，插件可订阅扩展 | ❌ |
| 请求事件 (加好友/加群) | 规则引擎（自动通过/拒绝策略） | ❌ |
| 元事件 (心跳/生命周期) | 仅状态更新 | ❌ |

Agent 上下文窗口仅保留消息事件，通知/请求/元事件不污染对话流。

### 10.2 消息管线

```
消息进入
  │
  ├── [1. 日志]         所有消息先落日志，包括后续被拦截的
  ├── [2. 限流]          短时间大量消息则降级
  ├── [3. 敏感词过滤]    命中则拦截
  ├── [4. 持久化]        写入窗口（内存 ring buffer）+ SQLite messages 表
  │
  ├── 是命令 (/开头)？  → 插件分发 → 回复
  │
  └── 是普通消息？
        ├── 私聊 → 走 Agent
        └── 群聊 → 按触发模式走 Agent / 忽略
```

> 注（P6-002/B-015 已实现）：插件分支校验指令集后，回复（`Reply`）经 ctx 内 `domain.Sender` 执行发送
> （§8.6 / B-017），群管理动作（`Actions`）经 ctx 内 `domain.GroupManager` 执行（B-015，先发回复后
> 执行动作，失败仅告警日志）；普通消息触发判断命中后走
> 完整回复闭环：拼 prompt（§6）→ Agent 推理（记忆工具经 ctx 会话身份写入）→ 经 `domain.Sender`
> 发送（§9.2 OnReplied 在发送成功环节记账）。上图为已实现链路。

### 10.3 通知管线（规则处理）

```
通知进入
  ├── 群成员增加 → 更新群信息 + 发规则欢迎语（不走 Agent）
  ├── 群成员减少 → 更新群信息
  ├── 戳一戳      → 规则回应（"？" / "别戳了"）
  ├── 禁言/解禁   → 记录
  └── 其他        → 忽略（插件可订阅扩展）
```

通知事件不进入 Agent 上下文窗口，保证窗口内只有对话内容。

---

## 11. 存储方案

### 11.1 数据库表概要

| 表 | 说明 |
|----|------|
| messages | 全量群聊消息存储，按 chat_id+时间索引，供关键词检索（后续可扩展向量检索） |
| conversation_summary | 归档摘要（会话键 chat_id + seq 唯一，重启回灌热链底，见 §4.1） |
| group_profile | 群画像（1群1条） |
| group_jargon | 群黑话（1群N条） |
| member_facts | 成员事实记忆（1人N条；group_id 空=私聊，非空=群聊） |
| persona | 人格模板（agent 绑定，见 §7） |
| bot_state | bot 在各会话的运行态（群=group_id，私聊=`private:`+user_id，P5-002） |
| group_config | 群静态配置（mode + 状态规则参数 10 列 + 群管理开关，P5-001/002/B-015；群首次被触达自动建行，见 §9.2） |
| schema_migrations | 迁移版本记录（version PK，B-015 起 migrate 为版本记录式） |

> 说明：共 9 张表（7 张业务表 + conversation_summary 归档摘要表 + schema_migrations 迁移版本表）。
> member_profile（个人画像统计）已于 P3-005 移除——成员上下文由 member_facts 承担；人格为 DB 人格模板（见 §7）。
> plugin_config（插件群配置）已移除——插件配置不落库，由插件自持（见 §8.6）。
> group_config 为 P5-001 新增（per-group 静态配置，与 bot_state 运行态职责分离），P5-002 扩 10
> 状态参数列（energy_*/cooldown_*/consecutive_*/rest_*/quiet_hours_*/short_message_chars），
> 0/空 = 走全局 cfg.Control.state 兜底；B-015 扩 `group_mgmt_enabled`（群管理单一开关，
> 默认 1 开；无配置行同样视为开，0 = 显式关闭，与「0=走全局」语义不同，为显式开关）。
> P7-001 补充：群首次被 bot 触达时按全局生效值**自动建行**（快照），故实际行多为非 0/非空值，
> 「0/空 = 走全局」仍成立但不再常见（建行细节与管理面语义见 §9.2 与 admin-web-api-plan.md §7.2）。
> schema_migrations 自 B-015 起：migrate() 每迁移文件在单事务内执行一次并记录版本，
> 失败整体回滚不记版本（下次启动重试）——新增列一律新建 `00N_*.sql`，不再改动 001
> （SQLite 无 `ADD COLUMN IF NOT EXISTS`，幂等依赖版本记录，见 §15）。

---

## 12. 待讨论方向

- [x] 人格系统（DB 人格模板，人格选择 agent）
- [x] 事件处理管线（完整 OneBot 事件覆盖清单）
- [x] Agent 记忆更新闭环
- [x] 情绪/状态系统
- [x] 安全与风控（限流 + 敏感词过滤，已在消息管线中间件）

---

## 13. 参考项目

| 项目 | Stars | 定位 | 可参考点 |
|------|-------|------|---------|
| [MumuBot](https://github.com/SugarMGP/MumuBot) | 20 | Go + eino + NapCat 的赛博群友 | 架构设计、eino 集成、记忆/情绪/画像系统 |
| [ZeroBot](https://github.com/wdvxdr1123/ZeroBot) | 398 | Go OneBot v11 框架 | OneBot 连接层实现 |
| [ZeroBot-Plugin](https://github.com/FloatTech/ZeroBot-Plugin) | 2.6k | ZeroBot 插件合集 | 插件参考、OneBot API 使用方式 |
| [eino](https://github.com/cloudwego/eino) | 12.5k | Go AI Agent 框架 | Agent 构建、Tool 注册、Compose 编排 |

---

## 14. 项目架构

### 14.1 分层

参照 DDD 分层，接口驱动，模块可独立开发与替换。

```
cmd/                                # 入口，组装依赖注入
internal/
  domain/                           # 领域层：纯接口 + 实体，零外部依赖
    entity/                         #   公共实体 (Message, Event, Profile...)
    agent.go                        #   Agent 接口
    memory.go                       #   Memory 接口
    plugin.go                       #   Plugin 接口
    storage.go                      #   Storage 接口
    control.go                      #   Control 接口
    group_manager.go                #   GroupManager 接口（B-015 群管理动作，见 §15）
  service/                          # 业务编排层，依赖 domain 接口
    agent/                          #   prompt 组装 → Agent 推理
    memory/                         #   窗口 + 群画像缓存 + 摘要流程
    plugin/                         #   发现、加载、路由
    control/                        #   触发判断 + 状态规则
    event/                          #   中间件链 + 分流编排
  handler/                          # 事件处理入口
    message.go                      #   消息事件 → event service
    notice.go                       #   通知事件 → 规则处理
  infra/                            # 基础设施，实现 domain 接口
    onebot/                         #   ZeroBot 封装
    ai/                             #   eino Agent 实现
    sqlite/                         #   SQLite 存储实现
plugin-sdk/                        #   独立 SDK module（P6-004）：entity 协议 wire 类型 + plugin go-plugin 接线（宿主 replace 本地）
pkg/                                # 可复用工具
plugins/                            # 插件目录（运行时，插件子进程可执行文件）
data/                               # SQLite 自动生成
```

### 14.2 依赖方向

```
cmd ──→ handler ──→ service ──→ domain (接口)
                      │
                      └──→ infra (编译时注入)

infra ──→ domain (实现接口)
domain 零依赖
```

上层依赖接口，底层实现接口，模块可独立开发替换。

---

## 15. 群管理动作执行（B-015）

AI 自主群管理（禁言/踢人/改名片等）经 agent tool 触发，不走回复通道。与插件 `Actions`
共用同一执行路径与护栏链——工具层与插件分支都不重复校验，护栏唯一落在
`domain.GroupManager` 实现内。

### 15.1 接口与注入

- `domain.GroupManager`：仅一个方法 `Execute(ctx, entity.GroupAction) error`（统一入口；
  `entity.GroupAction{Op, Target, Duration, Card}` 与插件 Actions 协议同类型，SDK 类型别名）。
  与 `domain.Sender` 分离——动作权限不注入整条消息链，仅暴露给工具层与插件执行路径。
- **per-event ctx 注入**（与 Session/Sender 同构）：onebot matcher 闭包构造
  `botGroupManager`（持当次事件 `*zero.Ctx` + `domain.Storage` + botID）经
  `domain.WithGroupManager` 注入 ctx；工具为共享单例（`NewGroupTools()` 无依赖），
  eino 把 ctx 透传到工具 `InvokableRun`，工具经 `domain.GroupManagerFrom(ctx)` 取执行器。

### 15.2 护栏链（集中在 `botGroupManager.Execute`）

1. **群聊守卫**：`Event.GroupID == 0`（私聊/非群事件）拒绝；
2. **per-group 开关**：`group_config.group_mgmt_enabled`（002 迁移，默认 1 开；
   `GetGroupConfig` 无配置行返回 `ErrNotFound` = 默认开，0 = 显式关闭）；
3. **管理员校验**（fail-closed）：触发者与 bot 都须为群主/管理员——经
   `get_group_member_info`（noCache）查 `role`，`owner`/`admin` 通过；
   查询失败/缺 role 一律拒绝；
4. **动作映射**：mute → `set_group_ban`（duration 钳制 30 天）、unmute →
   `set_group_ban`（duration=0，OneBot v11 规范 0=取消禁言）、kick → `set_group_kick`、
   set_card → `set_group_card`；未知 Op / 非法 Target / duration≤0 拒绝；
5. **API 响应检查**：经 `ctx.CallAction` 调用并检查 `APIResponse.Status/RetCode`——
   `SetGroupBan` 等封装方法吞掉响应（void），动作失败无法反馈，高危能力静默失败不可接受。

### 15.3 工具（internal/infra/ai/tools/group_tools.go）

4 个工具：`group_mute`（user_id + duration）、`group_unmute`、`group_kick`、
`group_set_card`（user_id + card）。Desc 写清触发边界（高危动作、仅群聊、开关与
管理员前置条件）。工具只做：群聊守卫 → 取执行器 → 构造 `GroupAction` → Execute
→ 中文反馈；不持 store、不重复校验。

### 15.4 插件 Actions 接线

`dispatchCommand` 发送 `Reply` 后执行 `res.Actions`（`executeActions` 逐条
`gm.Execute`，失败即停返回首个错误，仅告警日志——命令分支吞错误语义维持现状）。
先发回复后执行动作，用户能立刻看到插件反馈。

### 15.5 迁移机制（B-015 起版本记录式）

`migrate()` 建 `schema_migrations` 表（version PK + applied_at），每迁移文件在
**单事务内**执行并记录版本；任一语句失败整体回滚、不记版本（下次启动重试）。
旧库 001 重放靠 `CREATE TABLE IF NOT EXISTS` 幂等。新增列一律新建 `00N_*.sql`
（SQLite 无 `ADD COLUMN IF NOT EXISTS`）。B-034 完整任务（校验等）仍后置。

### 14.3 启动流程

```
main()
  ├── 1. load config.yaml
  ├── 2. infra/sqlite.Init()         → 建表 + 默认人格模板 seed
  ├── 3. service/plugin.Discover()   → 扫描 plugins/ 拉起子进程插件
  ├── 4. service/agent.Init()        → 构建 eino Agent + 注册 Tool（persona 由 P6-001 组装时现查注入 system 消息，启动仅 seed 默认模板）
  ├── 5. handler/message.Init()      → 组装中间件链 + 分流
  ├── 6. handler/notice.Init()       → 通知规则
  └── 7. infra/onebot.Run()          → ZeroBot 连接 NapCat，接收事件
```

> 注（web/优雅关闭已实现，见 §16）：启动不再是「7 步最后一步阻塞 Run」——onebot `Run`
> 改 goroutine，辅助 web（仅 /ping，127.0.0.1:8080）并行启动，主流程 `signal.Notify`
> 等待退出信号，收到后优雅关闭（web Shutdown → defer 链清理 → 进程退出）。

### 14.4 消息链路

```
NapCat → OneBot WS → ZeroBot
  │
  ├── middleware: 日志 → 限流 → 敏感词
  │
  ├── 持久化: 写入窗口（内存 ring buffer）+ SQLite messages 表
  │
  ├── 命令消息 → plugin service → 插件执行 → 回复
  │
  └── 普通消息 → control service 判断
        ├── 不满足 → 忽略
        └── 满足 →
              context.Build (人格+群画像+摘要+窗口)
              → agent service → eino 推理 → Tool 调用
              → 记忆更新 → 状态更新
              → 回复
```

> 注（P6-002 已实现）：链路已完整——普通消息触发命中后走 context.Build → agent service →
> eino 推理（Tool 调用写记忆）→ 经 `domain.Sender` 发送回复（B-003，发送成功才 OnReplied 记账，
> 见 §9.2 / B-038）→ 窗口追加 bot 回复；命令消息插件回复同样经 Sender 发送（B-017）。

---

## 16. 运行与优雅关闭（web 健康检查）

`cmd/bot/main.go` 的运行时骨架：bot 核心（OneBot 事件管线）与辅助 web 服务并存，
主流程以信号等待方式阻塞，收到退出信号后按序优雅关闭自有资源（B-044）。

### 16.1 web 服务（仅健康检查）

- 定位：进程存活探针，非管理用途——仅 `GET /ping`（返回 `{"message":"pong"}`），
  不暴露任何管理/业务端点；管理型 web（状态查看/手动发消息等）为后置需求（见 roadmap B 台账）。
- 形态：`newWebServer` 用 gin（`gin.New` + `gin.Recovery`，避免 Default 每请求日志刷屏）
  注册路由后包成 `http.Server`；`ListenAndServe` 放 goroutine。
- 监听地址硬编码 `127.0.0.1:8080`（本期不进 config；仅本机回环，不做远程暴露）。
- 失败语义：启动/运行错误（非 `http.ErrServerClosed`）仅 `logger.Error` 告警并继续运行——
  web 是辅助服务，不因端口占用拖垮 bot 核心。

### 16.2 优雅关闭流程（信号驱动）

实际启动顺序（取代 §14.3 旧 7 步末尾的「阻塞 Run」表述）：

1. 创建 onebot client 后 `go client.Run()`（goroutine；ZeroBot 底层自动重连）；
2. `signal.Notify(os.Interrupt, SIGTERM)`，主 goroutine 阻塞等待退出信号；
3. 收到信号：日志记录 → `http.Server.Shutdown`（5s 超时，排空 web 在途请求）→
   return main 触发既有 defer 链（`pluginSvc.Close` → `storageInfra.Close` →
   `logger.Sync`；注册顺序保证 LIFO 正确）→ 进程退出。

约束（接受现实）：ZeroBot v1.8.2 无官方优雅停止 API（`RunAndBlock` 阻塞在 driver 无限重连
循环，WSClient 无 Close），故 OneBot 连接自身不排空——进程退出即终止其内部 goroutine，
QQ 在途事件不做等待。二次信号安全网：`signal.Stop` 恢复默认信号处理，关闭过程中再次
Ctrl+C 直接终止进程（防卡死）。

---

## 17. 日志规范

日志的目标：**任何一条消息 / 一次操作都能从日志还原真相**（是否被消费、结局如何、谁干了什么、
花了多少成本），同时**同一事实只记一次**，避免层层重复刷屏。

> 落地状态：本规范**已落地**（roadmap B-046 已完成），代码以本规范为唯一准绳；
> 日志去向矩阵同步在 README「工程质量」段。

### 17.1 输出形态与文件布局

- 技术栈：`pkg/logger`（uber/zap 结构化 JSON + lumberjack 按**大小**滚动）。全局单例，**只写文件，不写终端**。
- 目录：`~/.plumebot/logs/`（`os.UserHomeDir()/.plumebot/logs`，可由 `logger.Config.Dir` 覆盖；
  实际生效路径经 `logger.Dir()` 取用，管理后端的日志浏览据此构造读取器，见 §17.6）。
- 滚动策略统一：单文件 10MB 切分 / 保留 5 备份 / 30 天（lumberjack `MaxSize/MaxBackups/MaxAge`）。
- 按**精确级别分文件**（`levelGate` 只收该精确级别，不是 ≥ 聚合）：

| 文件 | 记录 | 备注 |
|---|---|---|
| `debug.log` | 仅 Debug | 高频细节（窗口/画像/压缩跳过/发送成功/元事件） |
| `info.log` | 仅 Info | 消息入口 + 结局账本 + 模型调用 + 写操作审计 + 启动里程碑 |
| `warn.log` | 仅 Warn | 被拦截 / 各环节失败 / 护栏拒绝 / 429 / 401 |
| `error.log` | 仅 Error | 服务级故障（自动带 stacktrace） |
| `fatal.log` | 仅 Fatal | 启动致命错误（记账后 `os.Exit(1)`）；**Init 前**（`zl==nil`）改打 stderr 保证可见 |
| `gin.log` | gin 每请求访问 | 经 `pkg/logger.GinAccessWriter()`（同滚动策略）；`/ping` 健康轮询也在其中，不再刷 stdout |

- 全局门控 `log.level`（`config.yaml`，`debug|info|warn|error`）决定各级别是否输出；`fatal.log` 恒开。
- ZeroBot/logrus 内部日志**保持 stderr**（不并入 zap 文件体系）；ZeroBot 侧细节（在线/重连）看终端或重定向。

### 17.2 分层记录与防重复

| 层 | 记录什么 | 不记录什么 |
|---|---|---|
| pkg/logger | 唯一输出形态 + gin.log / fatal.log 写入器 | 业务细节 |
| infra/onebot | 事件转换失败、通知事件、连接状态、消息处理异常、固定文案发送结果 | 消息 Info（由 service/event 统一，连接层不重复） |
| handler | 空（纯胶水透传） | 一切 |
| service/event | 消息入口 + 结局账本 + 中间件拦截 | 结局细节不另打语义重复行 |
| service/memory·control·agent | 内部细节 Debug、关键成功 Info、失败 Warn | — |
| service/admin + handler/web | 管理审计 + 429/401 | 密钥/token/密码明文 |
| infra/ai | 模型调用度量 + 记忆工具写库审计 | 群管理动作审计（收敛到执行器，见下） |
| infra/sqlite | Open/迁移成功、迁移失败 Error | SQL 级慢查询（本期不做） |
| pkg/config | 模板写入、env 覆盖变量名、加载成功 | env 值/密钥 |
| cmd/bot main | 启动各步成功/失败、jwt secret 生命周期 | 密钥本身 |

防重复五条：

1. 一条入站消息恰好 **1 条入口行 + 1 条结局行**（见 §17.4）。
2. 连接层（onebot）不重打消息 Info（现状约定保留）。
3. 群管理动作审计**只**落在 `GroupManager.Execute`，工具层（`group_tools.go`）与插件执行层（`command.go executeActions`）不重复。
4. 发送成功防重复：`sender.Send` 成功只打 Debug，结局行（`agent_replied`）才是 Info 锚点。
5. 拦截/失败详情（命中词、错误对象）作为结局行自带字段，不另起语义相同的日志。

### 17.3 等级语义

| 级别 | 语义 | 典型位置 |
|---|---|---|
| Debug | 高频细节 | 窗口操作、画像缓存命中/失效、压缩跳过/冷却、发送成功、元事件 |
| Info | 正常业务里程碑 | 消息入口、结局（正常）、命令成功、模型调用、压缩成功、sqlite 打开/迁移、config 模板写入/env 覆盖、jwt 生成、写操作审计 |
| Warn | 可恢复问题 / 被拦截 | 限流丢弃、敏感词拦截、各环节失败、群管理护栏拒绝/API 失败、429/401、改密失败 |
| Error | 服务级故障 | web 异常退出、admin API 内部错误、迁移失败 |
| Fatal | 启动致命错误 | 初始化失败（落 fatal.log） |

### 17.4 消息结局账本（核心）

**需求**：默认 `log.level=info` 下，任何消息在「收到消息」之后必须还能对账到最终结局，
即「这条消息最终被回复了 / 被拦截了 / 被忽略了」。

- `service/event` 日志中间件（`logMiddleware`）打**入口行**：
  `Info("收到消息", message_id, group_id, user_id, message_type, content, mentioned)`。
- 管线的**每个终局分叉**打一条**结局行**，统一签名 `logOutcome(ctx, msg, outcome, …)`：
  `Info|Warn("消息结局", message_id, group_id, user_id, outcome, …)`——正常结局 Info、异常结局 Warn。
  经 `logger.From(ctx)` 输出，故结局行自带 trace_id（§17.6，会话键）。
- 结局 `outcome` 枚举：

| outcome | 级别 | 说明 |
|---|---|---|
| `agent_replied` | Info | 触发并发送成功（带 reason：auto / mention_forced） |
| `command` / `command_not_found` | Info | 插件命令分支（含 /help）；未找到也算已消费 |
| `not_triggered` | Info | 触发判断不回复（带 reason） |
| `empty_reply` | Info | Agent 返回空回复过滤 |
| `rate_limited` / `sensitive` | Warn | 中间件拦截丢弃（带 word / max_wait） |
| `control_error` / `prompt_fail` / `agent_fail` / `send_fail` / `no_sender` / `command_error` | Warn | 各环节失败（带 err） |

- **每条消息恰好一个结局行**：限流/敏感词在中间件拦截（不进入 respond）；命令分支 handled 短路
  （不透传 respond）；普通消息唯一走 respond。发送**成功之后**的持久化/记账失败仍单独 Warn
  （主结局 `agent_replied` 保留一行 Info）。
- 连接层对 `Handle` 返回的非拦截错误打 `Warn("消息处理失败")`，不补结局行。

### 17.5 审计与敏感信息

- **管理后端**（service/admin + handler/web）：注册/登录/改密记录带来源 IP；写操作统一
  「admin config changed」审计（identity/resource/target/IP）；429（每 IP 限流命中）与
  401（JWT 验签失败）打 Warn 带 IP。
- **群管理动作**：`GroupManager.Execute` 统一审计（group_id、actor、op、target、结果）；护栏拒绝
  （开关关 / 非管理员 / 时长钳制）Warn。
- **记忆工具写库**（store_fact / learn_jargon / forget_fact）：成功 Info / 失败 Warn，带会话归属。
- **绝不入日志**：API key、JWT 密钥、密码、token——只标 `api_key_set: true/false`，只记录环境变量**名**不记录值。

### 17.6 日志浏览（管理后端）

管理控制台内置日志浏览页，直接读 §17.1 的 JSON 日志文件，免登服务器查日志。

**trace_id 注入机制**（`pkg/logger` 提供，任何层可用，不依赖 service/admin）：

| 来源 | trace_id 取值 | 注入点 |
|---|---|---|
| 管理端 HTTP 请求 | 客户端 IP（原值） | `handler/web.withClientIP`：`logger.Context(ctx, S("trace_id", ip))` |
| QQ 群消息 | `group:<群号>` | `infra/onebot` matcher 闭包构造事件 ctx 时注入 |
| QQ 私聊消息 | `private:<对方QQ号>` | 同上 |

- `logger.Context(ctx, fields...)` 注入派生 logger（`zl.With(fields...)`）；`logger.From(ctx)` 取出，
  未注入时**回退全局 logger**（故存量调用无需一次改完，可渐进接入）。
- 已接入 trace_id 的行（关键路径）：消息入口行与结局行（`logOutcome` 带 ctx）、命令/限流/敏感词拦截、
  插件回复失败、固定文案发送、群管理动作审计、记忆工具写库、三类模型调用度量（agent/摘要/图片描述）、
  admin 审计与登录注册改密。**其余人工 Debug 细节行不带 trace_id**（覆盖范围以本节为准）。
- 一次会话的全部动态因此可在浏览页按 trace_id 一键串联（入口行 → 结局行 → 模型调用 → 群管理动作）。

**读取实现**（`internal/infra/logfile`，纯标准库零依赖）：

- 目录经构造函数注入（`logfile.New(dir)`，main 传 `logger.Dir()`）；只匹配 `<level>.log` 与
  lumberjack 备份名 `<level>-<时间戳>.log`，**`gin.log`（文本格式）不纳入浏览**。
- 策略：按文件 mtime 降序扫描 → 文件内从末行向前 → 行 `ts < begin` 停止读该文件 → 收集
  `offset+limit+1` 条即止 → 按 ts 稳定排序切页（不依赖文件系统时间戳精度）。单文件 ≤10MB。
- 损坏/非 JSON 行静默跳过；目录不存在返回空页（首次运行不报错）。

**接口契约**（`GET /api/v1/logs`，已鉴权 + 每 IP 限流）：

| 参数 | 说明 |
|---|---|
| `levels` | csv：`info,warn,error,debug`；缺省 = 全部；**`error` 语义含 `fatal.log`**（fatal 并入错误展示） |
| `trace_id` | 精确匹配 |
| `begin` / `end` | RFC3339，可缺省 |
| `limit` / `offset` | 缺省 200 / 上限 1000，offset ≥ 0 |

- 返回：`{code:0, data:{items:[{ts,level,message,trace_id,fields}], has_more}}`（items 最新在前）。
- 参数非法一律 400（不静默忽略，避免前端以为筛选生效却看到全量）。
- 分层：`domain.LogReader`（接口，domain 层只放接口）← `infra/logfile`（实现）；查询条件与结果类型
  `entity.LogQuery`/`LogEntry`/`LogPage` 归 entity；`service/log`（logsvc，参数归一）←
  `handler/web.LogHandler`（独立于 admin.Service）。
- 全接口限流：`verifyAuth` 之后统一挂 `apiLimiter`（10/s、burst 30），注册/登录沿用更紧的
  `authLimiter`（2/s、burst 10）。

**前端交互**（`static/index.html` 日志 tab）：四个等级开关**只切换渲染**（各等级结果前端缓存，
不发请求），trace_id/时间范围为「待应用条件」，「刷新」才按当前开启等级统一重查，逐等级「加载更多」
按 offset 追加——避免开关抖动打爆后端（日志读取为多文件扫描，代价高于普通配置读写）。
