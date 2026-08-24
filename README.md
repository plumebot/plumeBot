# PlumeBot

**基于 OneBot v11 协议的 QQ「赛博群友」** —— 不是命令机器人，而是一个有记忆、有人格、懂分寸的群成员。对接 NapCat，Go 语言实现，单二进制部署。

## ✨ 项目亮点

### 🧠 三级记忆系统

像人一样记住群里发生过什么：

- **短期**：内存 ring buffer 上下文窗口（20 → 100 轮自动扩容），知道"刚刚在聊什么"；
- **中期**：窗口满后 LLM 压缩为摘要，多摘要再融合、FIFO 淘汰；热链存内存参与对话，归档落库 SQLite，重启自动回灌；
- **长期**：成员事实（`member_facts`）与群黑话词典（`group_jargon`）——Agent 在对话中经 **tool calling 自主读写**，学到什么记什么，黑话还带 pending → confirmed 审核状态机。

Prompt 采用五段组装：人格 → 会话画像 → 历史摘要 → 当前窗口 → 当前消息，每轮现查现拼。

### 🎭 人格即数据，改库即生效

人格不是写在配置文件里的一行 system prompt，而是 SQLite `persona` 表中的模板（按 agent 名绑定）。**改表即时生效，无需重启**。兜底链完整：DB 模板 → 配置项 → 内置默认人设。

### 🌊 像人一样有分寸（触发控制）

不会刷屏、不会半夜吵人：

- **mention / auto 双模式**，每个群可独立配置；
- auto 模式下由 Agent 自主判断是否加入聊天，另有五条纯规则层兜底（不调 LLM）：
  **精力值**（回复消耗、随时间恢复）、**冷却间隔**、**连续回复上限**（超限强制休息）、
  **深夜静默时段**、**短消息忽略**；
- 被规则拦下的消息 ≠ 丢弃——仍持续旁听进入记忆，被 @ 时上下文永远完整。

### 👀 多模态感知

图片惰性描述接入对话：describer 缓存（同图不重复调模型）+ 每轮描述预算控制，描述结果写回存储复用。可选配置视觉模型条目启用。

### 🛡️ AI 群管理，护栏先行

Agent 可调用 `group_mute / group_unmute / group_kick / group_set_card` 四个工具自主管群，三道护栏集中在唯一执行入口：

1. per-group 总开关（默认开，可显式关闭）；
2. 触发者与 bot 都必须是管理员（实时查群成员角色，查询失败一律拒绝，fail-closed）;
3. 禁言时长钳制 30 天，动作结果逐条反馈（不静默失败）。

### 🔌 插件：子进程隔离 + 零权限协议

- 基于 HashiCorp go-plugin 的**独立子进程**通信：插件崩溃不影响主程序，跨平台（Windows 开发友好），真热重载；
- **指令集协议，零权限设计**：插件只声明意图（结构化 `Reply` 文本/图片/引用/@ + `Actions` 群管理动作），宿主是唯一执行者——安全、审计、权限全部收敛在宿主侧，且群管理动作与 AI 工具走**同一套护栏链**；
- **独立 SDK module（`plugin-sdk/`）**：第三方插件只依赖 SDK 即可编写，不 import 宿主 internal 包。一个最小插件只需三步：

```go
type hello struct{}

func (h *hello) Execute(_ context.Context, req plugin.PluginRequest) (plugin.PluginResult, error) {
    return plugin.PluginResult{Reply: &plugin.Reply{
        Segments: []plugin.Segment{{Kind: plugin.SegmentKindText, Text: "hello!"}},
    }}, nil
}

func main() { plugin.Serve(&hello{}) }
```

编译成 exe 放入 `plugins/<name>/` 并配一份 `plugin.json` 即被发现加载。

### 🏗️ 工程质量

- **DDD 分层**：domain（纯接口，零外部依赖）→ service（编排）→ infra（实现），依赖单向，接口驱动，模块可替换；
- 中间件消息管线：日志 → 令牌桶限流 → 敏感词过滤（自研 **Aho-Corasick 自动机**）→ 持久化；
- **纯 Go 无 cgo**：SQLite 用 `modernc.org/sqlite`，交叉编译无障碍；
- **版本化数据库迁移**：逐迁移文件单事务执行 + 版本记录，失败整体回滚下次重试；
- per-session 锁粒度：不同群/私聊互不阻塞，持锁绝不含 LLM/IO；
- zap 结构化日志 + lumberjack 滚动切分。

## 架构一览

```
NapCat (QQ 登录)
   │ OneBot WebSocket
   ▼
ZeroBot 连接层
   │
   ▼
中间件链：日志 → 限流 → 敏感词 → 持久化(窗口+SQLite)
   │                    │
   ▼                    ▼
插件分发(/命令)      触发判断(mention/auto + 状态规则)
                        │
                        ▼
                 Prompt 五段组装 → eino Agent 推理 ⇄ 记忆工具
                        │
                        ▼
                 Sender 发送 → 窗口追加 + OnReplied 记账
```

```
cmd ──→ handler ──→ service ──→ domain（接口）
                      │
                      └──→ infra（编译时注入）

infra ──→ domain（实现接口）
domain 零依赖
```

## 技术栈

| 组件 | 说明 |
|------|------|
| Go 1.21+ | 单二进制，无外部服务依赖（除 NapCat） |
| [ZeroBot](https://github.com/wdvxdr1123/ZeroBot) | OneBot v11 连接层 |
| [eino](https://github.com/cloudwego/eino) (CloudWeGo) | AI Agent 引擎，ChatModelAgent + tool calling |
| modernc.org/sqlite | SQLite 驱动，纯 Go 无 cgo |
| HashiCorp go-plugin | 插件动态加载（net/rpc 变体，免 protoc） |
| uber/zap + lumberjack | 结构化日志 |

## 快速开始

### 1. 启动 NapCat（QQ 登录端）

```bash
cp .env.example .env    # 填入 PLUMEBOT_SELFID（QQ 号）与 WEBUI_TOKEN
docker compose up -d
docker compose logs -f napcat   # 扫码登录
```

登录成功后 OneBot WS 地址为 `ws://127.0.0.1:3001`，与默认配置一致，无需改动。

### 2. 配置 PlumeBot

首次运行自动生成默认 `config.yaml`，填写模型 API Key 即可（支持任意 OpenAI 兼容端点；API Key 也可用环境变量 `PLUMEBOT_APIKEY_<模型名大写>` 注入，密钥不落盘）。

> 人格在 SQLite `persona` 表定义（首次启动自动 seed 默认模板），改表即时生效；`config.yaml` 的 `agent.system_prompt` 仅作兜底。

### 3. 编译运行

```bash
go build -o bot.exe ./cmd/bot/
./bot.exe
```

@ 它即可开始对话。

## 文档

- [架构设计文档](docs/architecture.md) —— 记忆系统、触发控制、插件协议等全部设计决策
- [开发阶段规划](docs/roadmap.md) —— 任务台账与进度
- [开发执行规范](CLAUDE.md) —— AI 辅助开发的执行约束

## 参考项目

- [MumuBot](https://github.com/SugarMGP/MumuBot) · [eino](https://github.com/cloudwego/eino) · [ZeroBot](https://github.com/wdvxdr1123/ZeroBot) · [ZeroBot-Plugin](https://github.com/FloatTech/ZeroBot-Plugin)

## License

[MIT](LICENSE)
