# 多模态消息模型修复 — 设计计划

> 状态：**阶段 1（数据模型统一）、阶段 2（描述机制）已实现并通过 build/vet/test**；阶段 3 未实现。
> 阶段 1/2 改动见 git diff 与 roadmap B-009。

## 1. 背景与 Bug

`internal/infra/onebot/convert.go` 的 `toMessage` 用 `ev.Message.ExtractPlainText()`
（ZeroBot `message/cqcode.go:56`，只拼接 `Type=="text"` 段）作为 `entity.Message.Content`，
导致 **image / at / face / record / video / file 段在进领域层前全部丢弃**：

- agent 上下文拿不到多模态内容与 @ 目标；
- 窗口 / SQLite / 压缩摘要只能看到纯文本；
- 入站 `Message` 与出站 `ChatMessage` 是两套断裂的模型，无转换桥（`BuildContext` 仍是 stub）。

## 2. 调研结论（eino / 服务商 / NapCat）

### 2.1 eino 层：从不拉取 URL，纯透传

`eino-ext/libs/acl/openai/chat_model.go` 两条转换路径：
- URL 路（:436-443）：`ImageURL.URL = *part.Image.URL`，URL 字符串原样上送；
- base64 路（:444-454）：拼 `data:<mime>;base64,<data>` data URI。

eino 不校验、不拉取、不缓存，`image_url` 是 URL 还是 base64 一律原样进 API 请求。

### 2.2 服务商层：取决于服务端，且要求公网可达

| Provider | URL 透传行为 |
|---|---|
| OpenAI 系（gpt-4o 等） | 服务端会拉公网 URL，但要求公网可达；官方建议优先 base64 |
| DeepSeek（当前默认，`deepseek-chat`） | **纯文本模型，不吃图片输入**（URL/base64 均无效） |
| Ollama（本地视觉，llava 等） | 不拉 URL，必须 base64 |
| qwen-vl / glm-4v / kimi-vision | 支持公网 URL（服务端拉）+ base64 |

### 2.3 NapCat 约束：图片 URL 是本机私网地址

NapCat image 段 `url` 形如 `http://127.0.0.1:<port>/...`，仅 bot 本机可达，
**任何云端模型服务端都够不着**（仓库 `spike_test.go:121` 用的就是公网 URL，作者已知此约束）。

**∴ 结论：NapCat 场景 URL 透传对云端 provider 必然失败，必须 bot 自取图片
（HTTP GET 本地 URL / 读本地文件 / 解码 base64://）→ 转 base64 → 调视觉模型生成描述。**

### 2.4 硬前提

当前默认模型 `deepseek-chat` 不支持视觉。「AI 描述」要跑通，需配置视觉模型
（qwen-vl / glm-4v / gpt-4o 任一，走 OpenAI 兼容端点即可）。

## 3. 已收敛决策

| 项 | 结论 |
|---|---|
| Message 模型 | 去掉 `Content`，改为 `Parts []ContentPart`（与出站同模型）；加只读派生 `PlainText()` / `Render()` |
| 入站转换 | convert.go 段数组 → Parts；image 存 URL（不存 base64）；at → 新增 `PartTypeAt`（Text=`[@qq]`/`[@全体]`）；face/reply/forward/json 等 → **丢弃**；base64-only image → 阶段 2 入链解码落盘 `data/image_cache/<md5>`、URL 存路径闭合（见 §3.1） |
| 派生视图 | `PlainText()` = **仅 text 段**（== 今日 ExtractPlainText 结果，敏感词/命令/短消息**零回归**）；`Render()` = text + `[@qq]` + `[图片]/[语音]/[视频]/[文件]` 标记（日志/压缩摘要用） |
| SQLite | `messages.content` 列 → 单列 `parts`（JSON）；回灌 `sqlGetMessages` 改解析 parts；**直接改 001 迁移文件**（P3-005 先例），已有 dev 库删 `data/plumebot.db` 重建 |
| ContentPart | 加 `Description string`（多模态的 LLM 文本描述；空 = 未生成）；PartType 加 `PartTypeAt`（face 本轮不落） |
| @ 表示 | 文本 `[@qq]` 进 Parts；模型内无 AtMe 字段，回复判定（mention 模式）归 P5-001 |
| 原生多模态模式 | 配置开关 `native_multimodal`，**默认关**；本轮加字段（占位），实现后置 |
| 描述机制 | `domain.MediaDescriber` 接口（infra/ai 实现：拉图→base64→视觉模型→文本），与 Summarizer 同构；失败落 `[图片]` 占位不断链 |
| 描述时机 | 惰性：装配（P6-001）时才描述 + 缓存（机制见 §6，装配接线随 P6 做） |
| 消费者迁移 | 日志/敏感词/命令/短消息忽略/压缩摘要 → `PlainText()`/`Render()` |

### 3.1 已知边界（入站裁剪，roadmap B-009/B-021 记录）

- **base64:// 收图已闭合（阶段 2）**：无 `url` 时入链解码写 `data/image_cache/<md5>` 缓存文件、URL 存本地路径
  （`internal/infra/imagecache`，内容级去重、绝对路径；base64 不入库原则不变）；解码/落盘失败回退占位不断链。
- **reply（引用回复）被丢弃**：强上下文信号，支持需解析引用 `message_id` 从窗口/SQLite 找回原文，P6 级能力，未排期（roadmap B-021）。
- **face/forward/json/xml/music 永久简化**：无消费者，不排期。

## 4. 配置设计（已定案：引用式，阶段 2 已实现）

`llm` 段数组化，**provider 保留**作为工厂选择键，`name` 为用户取名，用途映射新增配置：

```yaml
llm:
  timeout_seconds: 60
  native_multimodal: false   # 原生多模态模式，默认关（本轮加字段，占位）
  models:                    # 模型条目（可多个）
    - name: chat             # 用户取名
      provider: openai       # 选哪个工厂构建（当前仅 openai 兼容工厂）
      base_url: https://api.deepseek.com/v1
      api_key: ""
      model: deepseek-chat
    - name: vision           # 用户取名
      provider: openai
      base_url: ...
      api_key: ""
      model: qwen-vl-plus
  chat_model: chat           # 对话 agent 用哪个条目（name 引用；新增）
  vision_model: ""           # 图片描述用哪个条目（name 引用；空 = 描述能力关闭；新增）
```

- `provider` = 工厂选择键（保留，用户已确认）；`name` = 用户标签，非硬编码角色；
- 「具体 LLM 用于什么」由新增 `chat_model` / `vision_model` 引用 `name` 指定；
- 待讨论：`chat_model`/`vision_model` 引用式 vs 每个条目内 `use: chat|vision` 字段式；
- 环境变量 `PLUMEBOT_LLM_OPENAI_API_KEY`（config.go:23,156）数组化后失去归属，
  需限定作用于 `chat_model` 条目，实现时处理。

## 5. 待讨论点

1. ~~惰性的本质~~ → **已定**：限制轮数（见 §6），惰性 ≠ 缓存，缓存只去重。
2. ~~描述持久化~~ → **已定**：阶段 2 只做纯内存缓存；回写 SQLite parts JSON 后置（限制轮数下重描述量小，不值得现在引 UPDATE 路径）。
3. ~~窗口图片预算~~ → **已定**：装配只给窗口**最近 N 轮**图片做描述，超过 N 轮只落 `[图片]`；N 值实现时定。
4. ~~**配置用途映射形态**~~ → **已定案**：引用式（§4 示例，chat_model/vision_model 引用 name，阶段 2 已实现）。
5. **`native_multimodal` 开启后的语义**：直接喂 image part 给视觉模型；「读取后转描述省上下文」为后续增强（已默认关、后置，本点仅记录不阻塞）。

## 6. 惰性/缓存机制（已定：限制轮数）

```text
装配上下文时遍历窗口【最近 N 轮】消息 Parts：
  text part          → 直接进文本
  image part         → 超过 N 轮 → 文本 [图片]（不描述）
                       N 轮内 → 查描述缓存（map[图片指纹]string）
                       命中 → 用缓存
                       未命中 → describer.Describe(ctx, part)   ← 惰性点
                                 成功 → 写缓存 + 文本 [图片：<描述>]
                                 失败 → 文本 [图片]（负缓存，防重复打）
```

- 缓存：`map[指纹]string` + mutex，**不用 redis**（CLAUDE.md 明确不用），自己实现，
  成本低；需上限淘汰（ratelimit map 已有先例，见 roadmap B-004）。
- 指纹：图片 URL 或拉取字节 md5。
- 说明：这是**去重**（同图多次装配只描述一次），不是**范围控制**。
- 范围控制：靠「最近 N 轮」预算 + 压缩释放（压缩归档为文本摘要，图片随压缩消失不被描述）。

## 7. 阶段划分（初稿，待讨论定稿）

### 阶段 1：数据模型统一（已实现）

#### 改动清单（范围确认稿）

**A. domain/entity**
- `message.go`：Message 去 `Content`，加 `Parts []ContentPart`；加只读派生 `PlainText()` / `Render()`（对齐 plugin_validate.go 只读函数先例）
- `content.go`：ContentPart 加 `Description string`；PartType 加 `PartTypeAt`

**B. infra/onebot**
- `convert.go`：`toMessage` 的 `Content: ExtractPlainText()` → `Parts: toParts(ev.Message)`；新增段→Parts 映射：
  - text → `{text, Text}`；at → `{at, Text:"[@qq]"/"[@全体]"}`（at-self 已被 ZeroBot 裁，只剩 @他人/@全体）
  - image → `{image, URL:data.url}`；无 url 或仅 base64 → 空 image part（占位 `[图片]`，base64 不入库）
  - record/video/file → `{audio/video/file, URL:data.url}`（空则占位）
  - face/reply/forward/json/xml/music 等 → 丢弃
- `convert_test.go`：改 `Content` 断言；加 image/at/face 段映射单测

**C. infra/sqlite**
- `migrations/001_initial_schema.sql`：`content TEXT DEFAULT ''` → `parts TEXT DEFAULT '[]'`（JSON）
- `queries.go`：`sqlSaveMessage`/`sqlGetMessages` 的 content ↔ parts
- `storage.go`：`SaveMessage` 序列化 `msg.Parts` 为 JSON；`GetMessages` 反序列化回 Parts（回灌即恢复多模态）

**D. service 消费者（零回归：PlainText() == 今日 ExtractPlainText）**
- `event/middleware.go`：日志 `msg.Content` → `msg.Render()`
- `event/sensitive.go`：`Find(msg.Content)` → `Find(msg.PlainText())`
- `event/command.go`：`parseCommand(msg.Content)` → `parseCommand(msg.PlainText())`
- `memory/compress.go`：摘要渲染 `m.Content` → `m.Render()`

**E. 单测迁移（构造/断言 `entity.Message.Content` 处）**
- `infra/onebot/convert_test.go`
- `service/event/sensitive_test.go`
- `service/memory/{memory,window,profile}_test.go`
- `service/control/control_test.go`（若引用 Content）

**F. 明确不做（阶段外）**
- 配置改造（llm 数组化 + `native_multimodal` + `chat_model`/`vision_model`）→ 独立**配置阶段**（④ 已定拆开）
- `ContentPart.Description` 的惰性生成（`domain.MediaDescriber`）→ 阶段 2
- 原生多模态模式（`native_multimodal: true` 直喂 image part）→ 阶段 3

### 阶段 2：描述机制（已实现：接口 + infra/ai + 配置前置 + base64 闭合；装配接线待 P6-001）
- ✅ domain.MediaDescriber 接口 + infra/ai EinoMediaDescriber（拉图→base64→视觉模型，复用 ToSchema）
- ✅ 配置前置：llm 数组化（models[] + chat_model/vision_model 引用 name，env 限定 chat 条目）
- ✅ base64:// 入链闭合（internal/infra/imagecache，data/image_cache/<md5> 路径）
- ⬜ 惰性装配接线（P6-001 拼 ChatMessage 时调用）+ 最近 N 轮预算 + 内存缓存（随 P6 装配做）
- ✅ vision_model 未配置 → 描述关闭（main 侧不建描述器，image 只落 `[图片]` 占位）

### 阶段 3：原生多模态模式（后置，默认关）
- `native_multimodal: true` 时 image part 直接进 ChatMessage 喂视觉模型
- 可选增强：读取后转描述省上下文
- 可能的对象存储升级（后续）

## 8. 待你拍板的问题（下轮讨论）

1. 惰性/缓存的边界你认同吗？窗口子集预算（§5.3）怎么定？
2. 描述持久化：纯内存缓存 vs 回写 SQLite？
3. 配置用途映射：引用式 vs use 字段式？
4. 阶段划分（§7）是否认可，阶段 1 可否作为本轮任务范围？
