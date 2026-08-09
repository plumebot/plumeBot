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
| 存储 | SQLite + turso/sqlite | 纯 Go 驱动，无 cgo |
| 日志 | uber/zap | 高性能结构化日志 |
| 配置 | gopkg.in/yaml.v3 | YAML 格式配置文件 |
| 向量检索 | LLM API embedding（默认关闭） | 可选开启，调 LLM embedding 接口；关闭时走 SQLite 关键词+时间检索 |
| 插件 | Go plugin(.so) + exe 子进程 | .so 为主方案，exe 为可添加补充 |
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
                    ┌────┼────┐
                    ▼    ▼    ▼
                 [窗口] [个人画像] [群画像]
                 内存   SQLite   SQLite
```

---

## 4. 记忆系统（三层）

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
- 作用：直接拼入 prompt 让 Agent 知道"刚刚在聊什么"

### 4.2 群聊个人画像（中期）

- 存储：SQLite，按 `(group_id, user_id)` 唯一
- 粒度：某个人在某个群里的表现（同人在不同群表现不同）
- 内容：活跃度统计、亲密度、事实性记忆、兴趣话题
- 更新：渐进式，非每次重算
- 加载策略：
  - 每次构建上下文时，仅加载当前窗口中出现的用户的画像
  - 首次加载后缓存到内存结构体，后续直接从内存读，不重复查 SQL
  - 当用户从窗口消失（消息被压缩淘汰），画像延迟 N 轮后再从内存移出（避免频繁加载/卸载）
- 召回：精确查询 user_id

### 4.3 群聊画像（中期）

- 存储：SQLite，每个群一条
- 内容：群文化特征、主流话题、活跃时段、群规、黑话词典、氛围标签
- 更新：黑话/梗持续学习+人工审核；氛围标签周期性 LLM 分析
- 召回：精确查询 group_id

### 4.3.1 画像（聚合）与条目记忆（明细）的分界

（P3-004 讨论定案，防止实现时跑偏）

- **聚合字段与条目记忆互补不冲突**：member_profile/group_profile 的字段是「聚合摘要」，member_facts/group_jargon 是「原子条目」，是同一问题在聚合层与明细层的两种形态。
  - 聚合字段**有界、整行重写**：一小撮标签/数值，周期分析或规则层整组重写（Upsert 一次覆盖全字段）；
  - 条目记忆**无界、逐条累积**：一条一条追加，各有独立生命周期（去重 / 审核 / 删除），**不并入 profile 整行 upsert**（避免并发整行竞态）。黑话的 pending/confirmed 审核状态机即此生命周期的体现。
- **无界写入、有界注入**：条目记忆无界落库（小行 + 索引，与 messages 全量落库同性质）；真正有界的是注入 prompt 的量——组装时只注窗口内成员各 N 条事实 + confirmed 黑话，上限由 P6-001 消费方决定。
- **读在组装、写在 tool**：事实/黑话的读取在 prompt 组装时现查注入；Agent 执行中经 tool（store_fact / learn_jargon / forget_fact，P3-004）写入。**不做查询型 tool**（数据量小、直接注入更稳，避免模型不可靠地"记得去查"）。

### 4.4 记忆更新闭环

Agent 通过 eino Tool 机制驱动记忆更新，在对话中自行判断何时调用。

- 事实记忆和群黑话由 Agent Tool 实时更新
- 活跃度等统计数据由规则层自动维护
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
│ ① 系统人格            │  固定的，定义"你是谁"
├──────────────────────┤
│ ② 群聊画像            │  群文化/黑话/氛围
├──────────────────────┤
│ ③ 压缩摘要            │  历史上下文（全量拼接）
├──────────────────────┤
│ ④ 当前窗口            │  最近 N 轮原始消息
├──────────────────────┤
│ ⑤ 当前消息            │  正在处理的这条
└──────────────────────┘
```

---

## 7. 人格系统

### 7.1 核心思路

人格由 Agent 自主演化，不是静态配置文件。Agent 在对话中感知环境后自行调整。

### 7.2 存储

```sql
persona (
  id       INTEGER PK,
  userid   INTEGER,     -- 用户 ID
  groupid  INTEGER,     -- 群 ID，父级时为 0
  extend   INTEGER,     -- 继承父级 persona.id，一父多子
  traits   TEXT         -- 性格标签 JSON
);
```

- `groupid = 0` → 全局默认人格（父级）
- `groupid != 0` → 某群专属人格（子级，extend 指向父）
- 查询：先按 `userid + groupid` 精确匹配，再通过 extend 递归向上合并父级人格

### 7.3 更新流程（Agent 驱动）

```
Agent 感知到需要调整人格
  → 调 update_persona tool
  → 合并 extend 链生成完整人格
  → 更新内存缓存，立即生效
  → 异步写入 SQLite，持久化
```

### 7.4 初始化

纯 SQL 初始化，不依赖种子文件。启动时 SQLite 中无数据则插入默认人格（groupid=0）。

---

## 8. 插件系统

### 8.1 两种形态

| 形态 | 说明 | 优先级 |
|------|------|:---:|
| 动态加载 | Go package 编译为 `.so`，运行时 `plugin.Open()` 动态加载，实现统一接口 | 主方案 |
| exe 子进程 | fork 独立进程，stdio JSON 通信，任意语言实现 | 可添加方案 |

### 8.2 Go 动态加载

- 插件包实现统一接口，编译为 `-buildmode=plugin` 输出 `.so`
- 主程序运行时扫描目录 `plugin.Open()` 加载
- 支持热加载/卸载，无需重编译主程序

### 8.3 exe 插件（补充）

- 传输：stdio（非网络）
- 格式：一行 JSON（主进程→stdin），一行 JSON（stdout→主进程）
- 任意语言实现，读 stdin 写 stdout 即可

### 8.4 插件发现

扫描 `plugins/` 目录，优先读 `plugin.json` 元数据，无则用默认规则。

### 8.5 插件与 Agent Tool 的关系

- 插件 = 命令分发（`/天气 北京` → 精确匹配 → 直接返回）
- Tool = Agent 能力（"今天好热" → Agent 判断 → 调 weatherTool → 自然语言回复）
- 两者并存，互不冲突

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
| group_profile | 群画像（1群1条） |
| group_jargon | 群黑话（1群N条） |
| member_profile | 群内个人画像（1人1群1条） |
| member_facts | 个人事实记忆（1人N条） |
| persona | 人格模板（默认 + 群级），支持 extends 继承 |
| bot_state | bot 在各群的状态 |
| plugin_config | 插件在各群的配置 |

---

## 12. 待讨论方向

- [x] 人格系统（多群人设隔离、模板热更新）
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
    persona.go                      #   Persona 接口
    plugin.go                       #   Plugin 接口
    storage.go                      #   Storage 接口
    control.go                      #   Control 接口
  service/                          # 业务编排层，依赖 domain 接口
    agent/                          #   prompt 组装 → Agent 推理
    memory/                         #   窗口 + 画像缓存 + 摘要流程
    persona/                        #   extend 链 + 缓存
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
    plugin_so/                      #   plugin.Open() 实现
    plugin_exe/                     #   exec 子进程实现
pkg/                                # 可复用工具
plugins/                            # .so 文件目录
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

### 14.3 启动流程

```
main()
  ├── 1. load config.yaml
  ├── 2. infra/sqlite.Init()         → 建表 + 默认人格
  ├── 3. service/persona.Init()      → 加载 extend 链
  ├── 4. service/plugin.Discover()   → 扫描 .so 加载
  ├── 5. service/agent.Init()        → 构建 eino Agent + 注册 Tool
  ├── 6. handler/message.Init()      → 组装中间件链 + 分流
  ├── 7. handler/notice.Init()       → 通知规则
  └── 8. infra/onebot.Run()          → ZeroBot 连接 NapCat，接收事件
```

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
              context.Build (人格+画像+摘要+窗口)
              → agent service → eino 推理 → Tool 调用
              → 记忆更新 → 状态更新
              → 回复
```
