# PlumeBot 管理后端（gin + JWT + 简易前端）配置管理 API 计划书

> 状态：**计划书（已评审定案，未实现）**
> 日期：2026-09-12（首版）；2026-09-12（评审修订：K2 定案一次）；2026-09-13（补充修订：`group_config` 自动建行，见 §7.2）
> 关联文档：architecture.md §16（web 健康检查 /ping）、roadmap.md（B 台账）、CLAUDE.md 分层规则
> 本文档只做**接口与架构设计**，不含代码实现。遗留小决策见 §12「待讨论问题」。

---

## 1. 背景与目标

现有 web 服务只有 `GET /ping` 存活探针（`cmd/bot/main.go newWebServer`，硬编码 `127.0.0.1:8080`）。
本任务把 gin 服务升级为**配置管理控制台**：管理员经浏览器访问简易前端页，对 **SQLite 中与 bot 行为相关的配置**
做读取/修改，并落地鉴权（JWT）、每配置独立接口、返回值/入参设计、读写校验、DB 管理员账号。

设计目标：

1. **每个可配置项一份独立接口**（资源化，REST 风格）；
2. **改库即时生效**的配置直接落库即可；需失效内存缓存的（`group_profile`）走缓存失效出口；
3. **简易前端页**：单页 HTML（无构建链），gin 直接服务静态页 + API；**gin 监听端口进 config.yaml**（默认 9321，扫描范围 9321~10024，被占用逐次 +1）；
4. 管理员账号（用户名+密码）存 **DB 新表**，**首个账号由首次注册创建**，登录后可在页面改密；
5. 严格遵循既有分层：`cmd → handler → service → domain(接口)`，`infra` 实现接口，`pkg` 放可复用 JWT 工具。

---

## 2. 配置盘点与可改性

### 2.1 SQLite DB 配置清单（可管理对象）

| 表 | 字段摘要 | 运行时消费方 | 生效机制 | 是否管理面可改 |
|----|----------|--------------|----------|:---:|
| `group_config` | `mode` + 10 状态规则参数 + `group_mgmt_enabled` | `service/control.ShouldReply` 每条非 @ 群消息现查 `GetGroupConfig`（**无缓存**，B-036 定案）；群首次被触达时由 `resolveParams` 自动落一行默认配置（快照全局生效值，见 §7.2） | **改库即时生效** | ✅ 核心 |
| `persona` | `agent`(UNIQUE) + `name` + `system_prompt` | `service/memory.BuildMessages` 每次组装现查 `GetPersonaByAgent` | **改库即时生效**（P6-001） | ✅ 核心 |
| `group_profile` | `culture/topics/active_hours/rules/atmosphere` | `BuildMessages` 经 `memory.GetGroupProfile` | 走 `ProfileCache` 内存缓存，**无淘汰/失效出口**（`service/memory/profile.go` 只增不删，B-004） | ✅ 但需**缓存失效** |
| `group_jargon` | `group_id + jargon + status(pending/confirmed)` | `BuildMessages` 现查 `ListConfirmedJargon` | confirmed 变更**即时生效**；pending 不注入 prompt | ✅ 需审核/删除 |
| `member_facts` | `group_id + user_id + fact` | `BuildMessages` 现查 `ListMemberFacts` | **改库即时生效** | ✅ 纠错用 |
| `bot_state` | `group_id + state(JSON)` 运行态 | `service/control` 精力/冷却/连续计数，`OnReplied` 维护 | —（**运行态，绝非配置**） | ⭕ **只读查看**（已定案纳入） |
| `admin_user` | `username(UNIQUE) + password_hash` | 登录鉴权（**本期新增**，见 §8） | 新增账号/改密即时生效 | ✅（仅改密码端点，增删列后续） |
| `messages` / `conversation_summary` | 纯数据 | — | — | ❌ 不属配置 |

### 2.2 `config.yaml` 全局配置（**今年期不改，改后需重启**）

`cfg` 在启动时装载并按值注入各构造函数：`control.state` 全局默认（`NewControlService`）、中间件敏感词 AC 表
（`NewEventService`）、`llm.prompt` 组装预算（`BuilderConfig`）、LLM 模型条目（`NewAgent/NewSummarizer`）、
`agent` 三段等。**运行时没有热更新机制**——任何这类字段变更都需要：
- 改 `config.yaml` + 重启（现状，最简单），或
- 后续引入「动态配置源」机制（大改，不在本期）。

> **边界（定案 G）**：管理 API **只覆盖 §2.1 的 SQLite 配置**（含新增 `admin_user`）。`config.yaml`
> 全局参数如需热改，列为后续方向，本文档不为此做设计占位。

### 2.3 技术要点提示（对接口设计有约束）

- **`group_config` 无缓存** → PUT/DELETE 后即时生效，无需额外动作；
- **`persona` 无缓存** → PUT 后即时生效；
- **`group_profile` 有缓存且无失效出口** → 管理面修改/删除后**必须**调 `MemoryService.InvalidateGroupProfile(groupID)`
  新方法删除 `ProfileCache.groups` 条目，否则 prompt 仍走旧画像（详见 §7.4）；
- `group_config` 语义：列值 **0/空 = 不覆盖、走全局**（`mergeOverrides`）；`group_mgmt_enabled` 为**显式开关**（默认 1 开，0=关）。
  **自动建行**：群首次被 bot 触达时自动落一行「全局生效值快照」，故实际存在的行绝大多数列值非 0/非空
  ——「0/空 = 走全局」仍成立，只是自动建行后不再常见（详见 §7.2 说明）。

---

## 3. 范围边界

**本期包含：**

- `group_config`（查询 / 全量更新 / 删除单群配置 / 配置列表）；
- `persona`（列表 / 按 agent 查询 / upsert）；
- `group_profile`（查询 / 更新 / **删除** + 缓存失效）；
- `group_jargon`（列表带 status、添加（**默认 confirmed**）、删除、审核确认）；
- `member_facts`（按群+用户查询、添加纠错、删除单条）；
- `bot_state` **只读查看**（无写入端点）；
- `admin_user` **首次注册** + 登录 + 改密码（DB 表 + bcrypt）；
- 鉴权：登录签发 JWT + 中间件校验（Bearer）；
- **简易前端页**：登录、群配置、人格、群画像、黑话审核、成员事实、运行态查看、改密码（单页 HTML，gin 服务）；
- **config.yaml 新增 `admin` 段**：`enabled / port / jwt_secret / token_ttl_seconds`；
- 审计：全部写操作结构化日志（谁/何时/改了哪个配置目标）。

**本期不包含（明确排除）：**

- `bot_state` 运行态**写入**；`admin_user` 的增删列（本期仅改密码）；
- 手动发消息 / 群管理等「行动型」端点（architecture §16.1 已声明为后置需求；行为面走既有 `GroupManager` 而非配置）；
- `config.yaml` 全局配置热更新（改后重启）。

---

## 4. 总体架构

### 4.1 分层与新增包

```
cmd/bot/main.go
  │             组装注入 + port 探测递增
  ▼
internal/handler/web/        (新增) gin 路由注册 + auth 中间件 + 前端静态页服务
  │   NewRouter(svc domain.Admin, logSvc, mgr) *gin.Engine   // 依赖接口，不依赖具体实现
  │   authMiddleware(manager *jwt.Manager)
  ├── static/index.html     (新增) 简易前端单页（go:embed，无构建链）
  ▼
internal/domain/admin.go     (新增) Admin 接口（方法签名以 entity 承载）+ 消费侧
  │                          SessionWindowReader / GroupProfileInvalidator 接口
  ▼
internal/service/admin/      (新增) 配置管理业务：校验 + 读写 + 缓存失效 + 审计 + 注册/登录/改密
  │   Service{ store domain.Storage, mem domain.GroupProfileInvalidator,
  │            win domain.SessionWindowReader, mgr *jwt.Manager }   // 实现 domain.Admin
  │   （按配置域分组方法：Group/Persona/Profile/Jargon/MemberFact/State/Auth；接口签名中的
  │    通信结构体 AuthResult/SessionOverview/SessionMessage 定义在 domain/entity，json 由 web dto 接管）
  ▼
internal/domain/             (扩展) Storage 接口加方法 + 新增 entity.Jargon / entity.AdminUser
internal/infra/sqlite/       (扩展) 实现新方法 + 003_admin_user.sql 迁移（queries.go 加 const SQL）
pkg/jwt/                     (新增) golang-jwt/v5 封装：Manager.Sign / Verify / ParseClaims（HS256）
web 前端                     handler/web/static（embedded，无额外依赖）
```

- **现有 `newWebServer`/`/ping` 迁入 `handler/web`**；`admin.enabled=false` 时仅挂 `/ping`（管理路由与前端页不挂）。
- `pkg/jwt` 放「签发/验签/解析」纯封装，**不依赖 gin**——login handler 与 auth 中间件共用。

### 4.2 依赖方向（遵守 CLAUDE.md §5.5）

```
cmd → handler/web ──(domain.Admin 接口)──→ service/admin → domain(Storage/entity)
                        │
                        └→ service/memory（仅用于 group_profile 缓存失效；service→service 依赖有先例：event→memory）
                        └→ pkg/jwt（登录签发/中间件验签）
```

- handler/web 依赖 `domain.Admin` 接口（不依赖 `*admin.Service` 具体类型）；消费侧接口 `domain.SessionWindowReader`/`domain.GroupProfileInvalidator` 定义在 domain，`*memory.MemoryService` 实现。

- `service/admin` 依赖 **`domain.Storage` 接口 + `*memory.MemoryService` + `*jwt.Manager`**，不 import infra；
- SQL 语句全部留在 `infra/sqlite/queries.go` 包级 const（规则 10），admin 不写 SQL；
- 哨兵错误与校验失败错误统一定义在 `internal/domain/entity/errors.go`（引用 `entity.ErrXxx` / `entity.ValidationError`），上层 `errors.Is` 判断。

### 4.3 Service 划分（定案 A：单 service）

本任务**采用单 `service/admin`**：一个 service 持 `domain.Storage` + `*memory.MemoryService` + `*jwt.Manager`，
内部按配置域分组方法（Group / Persona / Profile / Jargon / MemberFact / State / Auth）。理由：这些域都是
「校验 + 读改写」薄编排，无差异化业务体量，拆分多个 service 包属过度设计。**路由与接口面仍每配置独立**（§7）。

---

## 5. 领域层扩展（domain.Storage 新方法 + 新实体 + 新迁移）

### 5.1 新增 Storage 方法（定案 E：直接扩展 Storage，不另起 AdminStore）

| 方法 | 理由 |
|------|------|
| `ListGroupConfigs(ctx) ([]entity.GroupConfig, error)` | 管理面「列出所有群配置」（当前仅单 PK 查询） |
| `DeleteGroupConfig(ctx, groupID string) error` | 删除配置行 = 恢复全局兜底（当前无删除路径） |
| `ListPersonas(ctx) ([]entity.Persona, error)` | 人格列表 |
| `UpsertPersona(ctx, persona entity.Persona) error` | 按 `agent` 幂等写（存在更新、不存在插入；免两段查 id） |
| `ListJargonWithStatus(ctx, groupID string) ([]entity.Jargon, error)` | 管理面需区分 pending/confirmed（现有 `ListJargon` 只返 []string 无 status） |
| `DeleteGroupProfile(ctx, groupID string) error` | **删除群画像**（定案 F），恢复「无画像」态 |
| `GetAdminUserByName(ctx, username string) (*entity.AdminUser, error)` | 登录校验（新表 `admin_user`） |
| `CreateAdminUser(ctx, u entity.AdminUser) (int64, error)` | 首次注册创建；username 冲突返回 `ErrConflict`（UNIQUE） |
| `ListAdminUsers(ctx) ([]entity.AdminUser, error)` | 注册前置门控：存在任一管理员即不允许再注册 |
| `UpdateAdminUserPassword(ctx, username, hash string) error` | 页面改密（定案 D） |

复用现有方法：`GetGroupConfig` / `UpsertGroupConfig`、`GetPersonaByAgent`、`GetGroupProfile` / `UpsertGroupProfile`、
`AddJargon` / `DeleteJargon` / `ConfirmJargon`、`ListMemberFacts` / `AddMemberFact` / `DeleteMemberFact`、`GetBotState`。

### 5.2 新增实体

```go
// Jargon 黑话条目：管理面视图（现有 ListJargon 只返 []string，无法区分审核状态）。
type Jargon struct {
    GroupID string // 群 ID
    Jargon  string // 黑话/梗文本
    Status  string // pending / confirmed
}

// AdminUser 管理员账号（DB 表 admin_user；凭证唯一事实来源，config 不含账号）。
// API 响应一律不序列化 PasswordHash（由 service 覆盖为空串）。
type AdminUser struct {
    ID           int64
    Username     string
    PasswordHash string
    CreatedAt    int64
    UpdatedAt    int64
}
```

### 5.3 新迁移 `003_admin_user.sql`（版本记录式，B-015 机制）

```sql
CREATE TABLE IF NOT EXISTS admin_user (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    INTEGER NOT NULL DEFAULT 0,
    updated_at    INTEGER NOT NULL DEFAULT 0
);
```

- `queries.go` 新增 `sqlGetAdminUserByName / sqlCreateAdminUser / sqlListAdminUsers / sqlUpdateAdminUserPassword` 包级 const；
- **首个管理员 = 页面注册创建（定案 J）**：`service/admin` 的注册逻辑仅当 `ListAdminUsers` 返回空表时放行，
  创建首账号（bcrypt 散列落库）；此后 **register 端点关闭（4031）**；同名用户名插入由 UNIQUE + `ErrConflict` 兜底（4091）。
- 密码明文只出现在注册/登录请求，落库仅 bcrypt 散列；config **不承载任何账号/密码**。

---

## 6. API 统一约定

- **前缀**：`/api/v1`；`POST /api/v1/auth/login` 免鉴权，其余统一 `Authorization: Bearer <token>`。
- **响应包络**：

```json
// 成功：HTTP 200
{ "code": 0, "message": "ok", "data": { ... } }

// 失败：HTTP 400/401/403/404/409/500 之一
{ "code": 4001, "message": "明文错误说明", "data": null }
```

  HTTP 状态承担传输语义，`code` 承担业务码（0 成功；4001 参数错误、4011 未认证/凭证错误、4031 无权限、
  4041 不存在、4091 冲突、5000 内部错误），`message` 供前端直接展示（中文）。
- **写操作返回**：更新后的资源（`data` 为最新状态），客户端立即同步。
- **删操作返回**：`{"code":0,"message":"ok"}`；目标不存在返回 `4041`。
- **校验纪律**：管理 API **fail-fast 拒绝非法输入**（与运行期 `mergeOverrides`「非法保留默认」的容错语义不同
  ——管理面应即时可见错误，杜绝脏数据入库）。校验在 `service/admin`。
- **审计**：所有写操作 `logger.Info("admin config changed", admin_identity, resource, target)`；登录成败记录；操作者取自 JWT claims。

---

## 7. 配置接口设计（每配置独立）

> 入参/出参均为 `data` 字段，字段语义与 DB/entity 对齐（0/空 = 走全局，`group_mgmt_enabled` 为显式开关）。

### 7.1 鉴权与账号

| 方法 | 路径 | 入参 | 出参 | 说明 |
|------|------|------|------|------|
| POST | `/api/v1/auth/register` | `{"username":"...","password":"..."}` | `{"token":"<JWT>","token_type":"Bearer","expires_at":1740000000}` | **首个管理员注册（免鉴权，定案 J）**：仅当 `ListAdminUsers` 空表时放行并创建首账号，成功即发 token；已存在管理员 → 4031；用户名冲突 → 4091 |
| POST | `/api/v1/auth/login` | `{"username":"...","password":"..."}` | `{"token":"<JWT>","token_type":"Bearer","expires_at":1740000000}` | bcrypt 校验 `admin_user`；失败 4011「用户名或密码错误」（不区分具体项，防探测） |
| PUT | `/api/v1/auth/password` | `{"old_password":"...","new_password":"..."}` | — | **已鉴权**；先验旧码再更新散列（`UpdateAdminUserPassword`） |
| GET | `/api/v1/auth/me` | — | `{"username":"..."}` | 当前登录人（前端刷新校验用，可选） |
| GET | `/api/v1/auth/status` | — | `{"registered": false}` | **免鉴权**：是否已有管理员（首访前端据此显示注册表单还是登录表单） |

- 注册/登录端点均加**每 IP 令牌桶限流**（复用 `golang.org/x/time/rate`，已有依赖），防爆破；
- `password`/`new_password` 最小长度校验（如 ≥ 8，长度常量见 §13 决策清单）。

### 7.2 `group_config` — 每群触发/状态配置（核心）

| 方法 | 路径 | 入参 | 出参 | 说明 |
|------|------|------|------|------|
| GET | `/api/v1/groups/{group_id}/config` | — | 单群配置 + `"configured": bool` | 未配置行返回 **200 + 全零/空字段 + `configured:false`**（不返回 404）。经 bot 触达过的群已有自动建行 → `configured:true`；`false` 仅表示**该群尚未被 bot 触达** |
| PUT | `/api/v1/groups/{group_id}/config` | 全字段 `GroupConfig` JSON | 更新后的配置 | **整行 upsert**（幂等）；0/空列=走全局；`configured` 恒 true |
| DELETE | `/api/v1/groups/{group_id}/config` | — | — | 删行 → 恢复全局兜底（不存在 4041）；**该群下次被 bot 触达时会按当时的全局生效值重新自动建行**（见下方说明，非持久） |
| GET | `/api/v1/groups/configs` | — | `{"items":[...]}` | 所有已配置群列表，**含自动建行的群**；未配置群在首次被 bot 触达前不出现 |

校验（service/admin）：`mode ∈ {mention, auto}`（拒绝空/未知 → 400；**与运行期 normalizeMode 的 fail-closed 兜底不同**）；
`group_mgmt_enabled ∈ {0,1}`；各 int 参数 `>=0`；`quiet_hours_start/end` 严格 `HH:MM`（`time.Parse("15:04")`，非法 → 400；
允许相等 = 空段禁用）。

**自动建行（2026-09-13 补充定案）**：`service/control.resolveParams` 在 `GetGroupConfig` 返回 `ErrNotFound`
时，按**当前全局生效值**快照落一行（`ControlService.defaultGroupConfig`）。触发时机 = 该群首条走到触发判断的
消息（`ShouldReply`），或首次回复记账（`OnReplied`）；**私聊不建行**。落库字段刻意有三处处理：

- `mode` 写**归一化后的生效值**（`mention`/`auto`）而非原始 `cfg.Control.Mode`——本接口校验只接受这两个枚举，
  存空串会让该行在此处回写时报 400；
- 10 个状态参数取合并后的全局生效值；`quiet_hours_*` 由内部分钟数还原为 `"HH:MM"`；
- `group_mgmt_enabled` **固定写 1**——该列是显式开关（0 = 显式关闭），自动建行留零值会静默关掉该群群管理。

建行写库失败**仅告警、不阻断**回复链路（管理面副作用）。

由此产生的语义影响：

1. 快照值 = 建行时的全局生效值，`mergeOverrides` 覆盖后与原「走全局」**等价**，读取链路行为不变；
2. 但该行此后**独立于全局**：`config.yaml` 的 `control.mode` / `control.state` 变更不再影响该群，需在该群行上改；
3. `configured:false`（前端「走全局」态）只在该群**尚未被 bot 触达**时可达；
4. DELETE 是**非持久**的——删行后下一条群消息会重新建行（内容为当时的全局值）。「永久回到走全局」当前无对应操作，
   列入 roadmap B 台账遗留项。

### 7.3 `persona` — 人格模板（改即生效）

| 方法 | 路径 | 入参 | 出参 | 说明 |
|------|------|------|------|------|
| GET | `/api/v1/personas` | — | `{"items":[{agent,name,system_prompt}...]}` | 所有 agent 模板 |
| GET | `/api/v1/personas/{agent}` | — | 单条 | 不存在 4041 |
| PUT | `/api/v1/personas/{agent}` | `{"name":"...","system_prompt":"..."}` | 更新后单条 | `UpsertPersona` 按 agent 幂等；**即时生效（下一条消息起）** |

校验：`name` trim（可空）；`system_prompt` 非空、长度上限（如 ≤ 20000 字符，常数见 §12 讨论项 H）。

### 7.4 `group_profile` — 群画像（写后需缓存失效）

| 方法 | 路径 | 入参 | 出参 | 说明 |
|------|------|------|------|------|
| GET | `/api/v1/groups/{group_id}/profile` | — | 单群画像 | 无画像返回零值 + `configured:false`（形态同 7.2；但 **profile 不自动建行**，故该态长期可达，不受 §7.2 自动建行影响） |
| PUT | `/api/v1/groups/{group_id}/profile` | `{"culture","topics":[],"active_hours","rules":[],"atmosphere":[]}` | 更新后画像 | UpsertGroupProfile **后调 `MemoryService.InvalidateGroupProfile(groupID)`** |
| DELETE | `/api/v1/groups/{group_id}/profile` | — | — | **定案 F：做**。`DeleteGroupProfile` + **同样失效缓存**（不存在 4041） |

> **关键实现约定**：`MemoryService` 新增公开方法 `InvalidateGroupProfile(groupID)`——锁内删除 `ProfileCache.groups[groupID]`。
> 否则改库不失效，prompt 仍读旧画像。`service/admin` 经注入的 `*memory.MemoryService` 调用。

校验：`topics/rules/atmosphere` 数组元素 trim 去空、条数上限（如 ≤ 50）；`culture/active_hours` 长度上限。

### 7.5 `group_jargon` — 黑话（审核流）

| 方法 | 路径 | 入参 | 出参 | 说明 |
|------|------|------|------|------|
| GET | `/api/v1/groups/{group_id}/jargons?status=all\|pending\|confirmed` | — | `{"items":[{jargon,status}...]}` | 默认 `all`；用 `ListJargonWithStatus` |
| POST | `/api/v1/groups/{group_id}/jargons` | `{"jargon":"..."}` | 新条目 | 添加；**定案 F：Admin 添加直接 status='confirmed'**（人工添加即人工认可，写入即注入 prompt） |
| DELETE | `/api/v1/groups/{group_id}/jargons/{jargon}` | — | — | 删除（不存在 4041）；撤销错误学习/错误确认 |
| POST | `/api/v1/groups/{group_id}/jargons/{jargon}/confirm` | — | 更新后条目 | `ConfirmJargon`：pending→confirmed（**确认后下条消息注入 prompt**） |

校验：`jargon` trim 非空、条目数上限（防无限膨胀，如每群 ≤ 500）；**文本一律走 request body**（path 只放 `group_id`），
避免中文/特殊字符进 URL path 的转义问题（`{jargon}` 从 body 取语义不变，见 §12 讨论项 F 注）。

### 7.6 `member_facts` — 成员事实（纠错）

| 方法 | 路径 | 入参 | 出参 | 说明 |
|------|------|------|------|------|
| GET | `/api/v1/member-facts?group_id=&user_id=` | — | `{"items":["..."]}` | 私聊场景 `group_id` 空即查该用户私聊事实 |
| POST | `/api/v1/member-facts` | `{"group_id":"","user_id":"","fact":"..."}` | 新条目 | 管理员补记/纠错（正常写者仍是 Agent 的 store_fact） |
| DELETE | `/api/v1/member-facts?group_id=&user_id=&fact=` | — | — | 删除单条（纠错主场景：Agent 记错的事实） |

校验：`user_id`、`fact` 非空；`fact` trim、长度上限（与 store_fact 对齐，如 ≤ 500 字符）。

### 7.7 `bot_state` — 运行态（**定案：只读端点纳入**）

| 方法 | 路径 | 入参 | 出参 | 说明 |
|------|------|------|------|------|
| GET | `/api/v1/sessions/{session_key}/state` | — | `{"group_id","state":{...}}` | 只读展示精力/冷却/连续/rest；**不提供写入** |

- `state` 为 `bot_state.state` 原样 JSON（`groupState` 字段：energy / energy_updated_at / last_reply_at /
  consecutive_count / rest_until）；不存在返回 4041；
- 只读性由**接口层面保证**（service 只暴露 Get），杜绝误改运行态。

### 7.8 会话窗口与摘要纪要（P7-003，只读）

| 方法 | 路径 | 入参 | 出参 | 说明 |
|------|------|------|------|------|
| GET | `/api/v1/sessions` | — | `{"items":[{"key","count","last_ts","last_render"}]}` | 活跃会话下拉（`domain.Memory.ListSessions`，按键升序） |
| GET | `/api/v1/sessions/{session_key}/window` | — | `{"items":[...],"summaries":[...]}` | 窗口消息（时间正序）+ 摘要热链（旧→新）一并返回 |

- `session_key`：群聊=群号，私聊=`private:QQ`（URL 编码）；未知会话返回空数组（非 404）；
- `summaries` 元素 `{text, keywords, decisions, created_at}`：**读内存摘要热链**
  （`domain.SessionSummaryReader` → `SummaryStore.GetAll`，会话首次访问会先从 SQLite 归档惰性回灌最新若干条）；
  **不区分一级压缩/二级融合**（管理面只需「这里有一段更早的纪要」）；仅显示当前热链，被融合覆盖的一级原件只在归档表；
- 前端「对话历史」tab 渲染为气泡列表**上方**的「更早的对话纪要」区块——摘要是窗口之前那段已压缩的历史，与窗口语义连贯；
- 两个端点均**只读不进审计**（与 `bot_state` 一致）。

---

## 8. 鉴权与 JWT 设计（定案 C：golang-jwt/v5 + DB 账号）

### 8.1 `pkg/jwt`（封装 `github.com/golang-jwt/jwt/v5`，HS256）

```go
type Claims struct {
    Username string `json:"username"`       // 业务字段（接口层取用，不裸 jwt.RegisteredClaims）
    jwt.RegisteredClaims
}
type Manager struct { secret []byte; ttl time.Duration }
func NewManager(secret string, ttl time.Duration) *Manager
func (m *Manager) Sign(username string) (token string, expiresAt int64, err error) // HS256 + iat/exp
func (m *Manager) Verify(tokenString string) (*Claims, error)                      // 验签 + 过期 + parser 收敛错误
```

- 新直接依赖：`github.com/golang-jwt/jwt/v5`（HS256 `NewWithClaims` / `ParseWithClaims` 标准用法）；
- `Manager` 按值注入、非全局单例；中间件与 login handler 共用同一实例。

### 8.2 管理员凭证来源（定案 D + J：DB 表 + **首次注册** + 页面改密）

- **账号唯一事实来源是 `admin_user` 表**（新迁移 §5.3）；注册、登录校验、改密都查/写表，**不读 config**；
- **首次注册**：`POST /auth/register` 仅当 `admin_user` 为空时可用——首个到达前端注册页并提交的人创建唯一初始账号，
  注册成功即签发登录 token；此后 register 永久关闭（4031）。回环绑定保证可达者只有本机操作者；
- `config.yaml` `admin` 段只承担**服务自身配置**（端口、JWT 密钥、有效期），**不含任何账号/密码**：

```yaml
admin:
  enabled: true             # 管理 API + 前端页开关（默认 true；仍绑定 127.0.0.1 回环）
  port: 9321               # gin 监听端口默认值（被占用自动 +1，扫描范围 9321~10024，见 §9.1）
  jwt_secret: ""            # JWT 签名密钥；空 → 首次启动自动生成并持久化 data/admin_jwt_secret（0600）
  token_ttl_seconds: 86400  # token 有效期（默认 24h）
```

- **命名澄清**：`admin` 是「**管理后端**」这个功能模块的命名（config 段名 / `service/admin` 包名 / 前端管理页），
  **不是固定用户名**——账号由首个注册者自定（见上）；
- **JWT 密钥**：`jwt_secret` 显式配置则用之；留空 → 首启生成随机 32 字节写入 `data/admin_jwt_secret`（跨重启 token 保持有效）；
- 密码散列：**`golang.org/x/crypto/bcrypt`**（已在依赖树 indirect，转 direct；成本默认 10）。**不存/不返回明文**；
- 改密：登录后 `PUT /auth/password`（验旧码 → 更新散列），全程无需触碰任何配置文件。

### 8.3 中间件（`handler/web`）

- `authMiddleware(manager *jwt.Manager)`：读 `Authorization: Bearer <token>` → `Verify` →
  通过则 `Claims.Username` 写入 `gin.Context` Key（供 `service/admin` 审计取操作者）；失败 4011；
- 挂载：`/api/v1/*`（除 `/auth/login`）；`/ping`、`/`（前端页）、`/static/*` 不挂；
- 中间件只做鉴权与身份传递，密码学全委托 `pkg/jwt`。

---

## 9. 端口配置与 main 注入

### 9.1 gin 端口进 config.yaml（定案 H：默认 9321，扫描范围 9321~10024，逐次 +1）

- **监听地址**：沿用 `127.0.0.1` 回环绑定（安全边界；本机浏览器访问前端页）；起始端口取 `admin.port`；
- **端口扫描（定案 H）**：`ListenAndServe` 返回 `EADDRINUSE` 时 `port+1` 重试，**扫描 9321~10024（含）**；
  全范围失败 → 日志告警「admin web 未启动（端口范围被占用）」，bot 核心继续运行（沿用「web 辅助不拖垮 bot」语义）；
- 启动日志需打印**实际监听端口**，便于用户打开 `http://127.0.0.1:<实际端口>/`；
- 迁移：现有 `/ping` 与新 `/api/v1/*`、`/` 同服（同一 gin engine）；`admin.enabled=false` 时回退为「仅 /ping」。

### 9.2 main 组装序列

1. `config.Load` 读 `admin` 段；`admin.enabled=false` → `newWebServer` 保持仅 /ping；
2. `enabled=true` 时：
   - `mgr := jwt.NewManager(secret, ttl)`（secret 生成/持久化见 §8.2）；
   - `adminSvc := admin.NewService(storageInfra, memorySvc, mgr, adminConfig)`（注册/登录/校验/审计/缓存失效）；
   - `handler/web.NewRouter(adminSvc)` 注册：`/ping`、`/`（前端静态页）、`/api/v1/*`（auth 中间件保护）；
   - `web.ListenAndServe` goroutine + 端口递增逻辑；
3. 优雅关闭不变（`Shutdown` 已覆盖 web，含端口递增后的同一 `*http.Server`）。

---

## 10. 简易前端页面（定案：单页 HTML，gin 直接服务）

- **形态**：单文件 `internal/handler/web/static/index.html`（`//go:embed`，内联 CSS+JS，零构建链/零前端依赖），
  `GET /` 返回该页；前端用原生 `fetch` 调 `/api/v1/*`，`Authorization: Bearer` 从 `localStorage` 读取；
- **页面模块**（与 API 一一对应）：
  1. **注册 / 登录 / 修改密码**（首页先 `GET /auth/status`：`registered=false` 显示注册表单（注册即登录），否则显示登录表单；登录 → 存 token 到 localStorage；改密 → 旧码+新码 PUT `/auth/password`）；
  2. **群配置**：群配置列表 → 点开单群编辑（mode / 状态参数 / 群管理开关）→ PUT / DELETE；
  3. **人格模板**：agent 列表 → 编辑 system_prompt → PUT；
  4. **群画像**：输入 group_id → 查看/编辑 culture/topics/rules/atmosphere → PUT / DELETE；
  5. **黑话审核**：按群列出 pending/confirmed → confirm / delete；新增黑话（默认 confirmed）；
  6. **成员事实**：group_id+user_id 查事实 → 删除单条 / 补记；
  7. **运行态查看**：session_key → 只读展示精力/冷却等；
- **失败呈现**：非 200 统一弹 `code`+`message`；token 过期 4011 → 跳回登录页；
- 表格/表单风格从简，无框架、无构建（契合「简易前端」）。

---

## 11. 安全考虑

1. **回环绑定**：默认 `127.0.0.1`，端口可配；不改前不暴露局域网；如需远程管理再配监听地址并走内网/反向代理 TLS；
2. **账号安全**：密码只存 bcrypt 散列；登录失败统一文案（不分「用户不存在/密码错误」）；登录端点每 IP 限流防爆破；
   `new_password` 长度/复杂度下限；
3. **密钥**：`jwt_secret` 空则自动生成并持久化 `data/admin_jwt_secret`（0600）；日志绝不打印 token/密码/散列；
4. **首次注册门控**：`/auth/register` 仅当 `admin_user` 空表时开放，首账号创建后即永久关闭（fail-closed，杜绝「已有账号仍可注册」的二次入口）；回环绑定保证可达者仅本机；config 不含任何账号/密码；
5. **fail-fast 校验**：非法入参一律 4xx，不落脏数据（与运行期容错兜底刻意区分）；
6. **审计日志**：写操作与登录成败均可追溯（操作者、资源、时间）；
7. **不越权行动**：管理面无发送消息/群管理能力（那些走既有 `domain.Sender`/`GroupManager` 通道，本面不引入）。

---

## 12. 测试与验证要点

- **pkg/jwt 单测**（golang-jwt/v5）：签发→验签往返、篡改/过期/畸形 token 拒验、claims.Username 正确；
- **sqlite 新方法单测**：List/Delete/UpsertPersona/ListJargonWithStatus/DeleteGroupProfile/GetAdminUserByName/
  Create/UpdatePassword 的增删改与示例（含 4041/4091 语义）；
- **admin_user 迁移/注册单测**：003 迁移可重放（版本记录）、bcrypt 散列落库、空表可注册 / 已有账号注册 4031 / 用户名冲突 4091；
- **service/admin 单测**（真/假 domain.Storage + 假 memory）：字段校验矩阵（非法 mode/非法 HH:MM/越界值）、
  group_profile 写后 `InvalidateGroupProfile` 触发断言、登录成功/失败分支、改密旧码错误拒绝；
- **handler/web 单测**（httptest）：无 token 4011、坏 token 4011、合法 token 通过、`/` 返回前端页、各端点往返 + 包络格式；
- **命令级验证**：`go build ./... && go vet ./... && go test ./...`（`go.mod` 新增 `golang-jwt/jwt/v5`、
  `golang.org/x/crypto` 转 direct）。

---

## 13. 决策记录（已全部定案）

| 项 | 问题 | 定案 |
|----|------|------|
| A | service 划分 | **单 `service/admin`** |
| B | GET 是否返回生效值（合并全局兜底） | 本期只返回**存储值**，生效值由 bot 侧合并 |
| C | JWT 实现 | **`github.com/golang-jwt/jwt/v5`**（HS256） |
| D | 管理员凭证来源 | **DB `admin_user` 表 + 页面改密**；config 段不含账号 |
| E | 领域扩展方式 | **直接扩 `domain.Storage`**（含 admin_user 方法） |
| F | 黑话 POST / group_profile DELETE / bot_state / 复核流 | 黑话 POST 默认 **confirmed**；group_profile **做 DELETE**；bot_state **只读端点纳入**；**不引入复核流** |
| G | config.yaml 全局热更新 | 本期 DB-only，全局参数改后重启 |
| H | gin 端口 | `admin.port` 默认 9321，**扫描范围 9321~10024 逐次 +1**，全范围失败仅告警 |
| I | `admin.enabled` 默认值 | 默认 **true**（管理页为预期功能；回环绑定兜底安全；如需默认关、显式开启改一处） |
| J | 管理员创建方式 | **首次注册**：`/auth/register` 仅空表可用，首账号创建后即关闭；**不 seed、不读 config 账号** |

剩余未决小项（**实现时按建议默认，可在实现评审微调**）：
`password`/`new_password` 最小长度（≥ 8）、`system_prompt` 长度上限（≤ 20000）、每条黑话/事实数量与长度上限
（每群黑话 ≤ 500 · ≤ 100 字符、事实 ≤ 500 字符、topics/rules/atmosphere ≤ 50 条）、`config.admin.port` 的
YAML 字段是否允许显式省略（省略即用「未配置 → 9321」默认语义）。

---

## 14. roadmap 承接建议

在 roadmap 新增阶段「第七阶段・管理后端」：

> **P7-001 管理配置 API（gin + golang-jwt + service/admin + pkg/jwt + 简易前端页）**：落地本文档 §4～§10 全部设计；
> 实现路径（一次一个子任务）：① `003_admin_user.sql` + Storage/entity 扩展 + sqlite 实现；
> ② `pkg/jwt` + `handler/web` auth 中间件 + 注册/登录/改密流（含首账号注册门控）；
> ③ `service/admin` 各配置域读写 + 缓存失效 + 校验；④ `handler/web` 路由 + 前端静态页 +
> `admin` 段 config + main 注入（端口扫描 9321~10024）；⑤ 测试补全。
>
> 验收：`admin.enabled=true` 时浏览器可访问首页 → 无账号先注册、已有账号注册被拒 → 登录 →
> 群配置/人格/群画像（含缓存失效断言）/黑话/成员事实/运行态读写与只读生效、
> 端口 9321~10024 被占用逐次 +1、改密生效、鉴权拦截、单测全绿。

落地时按 CLAUDE.md 流程：一次一个子任务。`admin` 段为 config 新增字段，**双处同步义务（B-006）**适用于
`pkg/config/config.default.yaml` + 根 `config.yaml`（含 `enabled/port/jwt_secret/token_ttl_seconds`）。