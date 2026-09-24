package domain

import (
	"context"

	"plumebot/internal/domain/entity"
)

// Admin 管理后端配置读写服务接口（P7-001 起，单 service 按配置域分组，见 docs/admin-web-api-plan.md §4.3）。
// 与 Control/Memory/LogReader 等既有接口一致：接口定义在 domain 层，实现放 service/admin
//（Service 经 NewService 注入具体依赖），handler/web 依赖本接口而非具体实现。
// 方法签名以 entity 承载通信数据（entity.AuthResult / entity.SessionOverview 等）；
// HTTP 入参/出参的 json 结构定义在 handler/web/dto（web 层职责，entity 保持纯结构）。
//
// 方法按配置域分组：auth / group_config / persona / group_profile / group_jargon /
// member_facts / bot_state(只读) / 会话窗口(只读)。
type Admin interface {
	// ── 认证域 ──
	// Register 创建首个管理员账号（空表门控，定案 J）并签发登录 token。
	// 已有任一管理员 → ErrAlreadyRegistered；同名冲突 → ErrConflict。
	Register(ctx context.Context, username, password string) (*entity.AuthResult, error)
	// Login 校验用户名/密码并签发 token；失败统一收敛为 ErrInvalidCredentials。
	Login(ctx context.Context, username, password string) (*entity.AuthResult, error)
	// ChangePassword 修改密码：先验旧码再更新散列。
	ChangePassword(ctx context.Context, username, oldPwd, newPwd string) error
	// AuthStatus 返回是否已注册管理员（前端据此显示注册还是登录表单）。
	AuthStatus(ctx context.Context) (bool, error)

	// ── group_config ──
	// ListGroupConfigs 列出全部已配置群的静态配置。
	ListGroupConfigs(ctx context.Context) ([]entity.GroupConfig, error)
	// GetGroupConfig 查询单群配置；不存在时返回 ErrNotFound。
	GetGroupConfig(ctx context.Context, groupID string) (*entity.GroupConfig, error)
	// UpsertGroupConfig 整行 upsert 单群配置（校验通过才写库）；返回校验后的最新配置。
	UpsertGroupConfig(ctx context.Context, by string, cfg entity.GroupConfig) (*entity.GroupConfig, error)
	// DeleteGroupConfig 删除单群配置（恢复全局兜底）；不存在时返回 ErrNotFound。
	DeleteGroupConfig(ctx context.Context, by, groupID string) error

	// ── persona ──
	// ListPersonas 列出全部人格模板。
	ListPersonas(ctx context.Context) ([]entity.Persona, error)
	// GetPersonaByAgent 按 agent 查询人格模板；不存在时返回 ErrNotFound。
	GetPersonaByAgent(ctx context.Context, agent string) (*entity.Persona, error)
	// UpsertPersona 按 agent 幂等写人格模板；返回校验后的最新模板。
	UpsertPersona(ctx context.Context, by string, p entity.Persona) (*entity.Persona, error)

	// ── group_profile（写/删后失效内存缓存）──
	// GetGroupProfile 查询群画像；不存在时返回 ErrNotFound。
	GetGroupProfile(ctx context.Context, groupID string) (*entity.GroupProfile, error)
	// UpsertGroupProfile 写群画像并失效内存缓存（先写库成功再失效）。
	UpsertGroupProfile(ctx context.Context, by string, p entity.GroupProfile) (*entity.GroupProfile, error)
	// DeleteGroupProfile 删除群画像并失效内存缓存；不存在时返回 ErrNotFound。
	DeleteGroupProfile(ctx context.Context, by, groupID string) error

	// ── group_jargon（审核流：管理面添加默认 confirmed）──
	// ListJargonWithStatus 列黑话；status 为空或 all 时返回全部，否则过滤 pending/confirmed。
	ListJargonWithStatus(ctx context.Context, groupID, status string) ([]entity.Jargon, error)
	// AddJargon 管理面添加黑话（默认 confirmed）；新增返回条目。
	AddJargon(ctx context.Context, by, groupID, jargon string) (*entity.Jargon, error)
	// DeleteJargon 删除黑话；不存在时返回 ErrNotFound。
	DeleteJargon(ctx context.Context, by, groupID, jargon string) error
	// ConfirmJargon 审核确认 pending → confirmed；黑话不存在时返回 ErrNotFound。
	ConfirmJargon(ctx context.Context, by, groupID, jargon string) (*entity.Jargon, error)

	// ── member_facts ──
	// ListMemberFacts 列出指定群/用户的事实。
	ListMemberFacts(ctx context.Context, groupID, userID string) ([]string, error)
	// AddMemberFact 管理面补记/纠错一条事实。
	AddMemberFact(ctx context.Context, by, groupID, userID, fact string) error
	// DeleteMemberFact 删除单条事实；不存在时返回 ErrNotFound。
	DeleteMemberFact(ctx context.Context, by, groupID, userID, fact string) error

	// ── bot_state（只读）──
	// GetBotState 按会话键查询运行态；不存在返回 ErrNotFound。
	GetBotState(ctx context.Context, sessionKey string) (*entity.BotState, error)

	// ── 会话窗口（只读，P7-003）──
	// ListSessions 返回当前持有活跃窗口的全部会话概览，按会话键升序。
	ListSessions(ctx context.Context) []entity.SessionOverview
	// GetSessionWindow 返回会话窗口内消息的只读视图（时间正序）；会话不存在/窗口为空返回空切片。
	GetSessionWindow(ctx context.Context, sessionKey string) []entity.SessionMessage
	// GetSessionSummaries 返回会话摘要热链的只读视图（旧→新），供管理前端展示「窗口之前已压缩的历史纪要」。
	// 读内存热链（会话首次访问会先从 SQLite 归档回灌最新若干条）；
	// 不区分一级压缩/二级融合（管理面只需「这里有一段更早的纪要」这一信息）。
	GetSessionSummaries(ctx context.Context, sessionKey string) []entity.SessionSummary
}

// SessionWindowReader 是 Admin 对「内存上下文窗口只读」的最小依赖面（消费侧接口，
// 定义在 domain 层，P7-003）。*memory.MemoryService 实现了该接口；测试可注入假实现。
type SessionWindowReader interface {
	// GetWindow 返回会话窗口内消息（会话键：群聊=GroupID，私聊="private:"+UserID）；
	// 会话不存在返回空切片（非错误）。
	GetWindow(ctx context.Context, sessionID string) ([]entity.Message, error)
	// ListSessions 返回当前持有活跃窗口的全部会话键。
	ListSessions() []string
}

// GroupProfileInvalidator 是 Admin 对「群画像缓存失效」的最小依赖面（消费侧接口，
// 定义在 domain 层）。*memory.MemoryService 实现了该接口；测试可注入假实现断言失效被触发。
type GroupProfileInvalidator interface {
	InvalidateGroupProfile(groupID string)
}

// SessionSummaryReader 是 Admin 对「会话摘要热链只读」的最小依赖面（消费侧接口，
// 定义在 domain 层）。*memory.MemoryService 实现了该接口（GetSummaries 转发 SummaryStore 内存热链）。
type SessionSummaryReader interface {
	// GetSummaries 返回会话摘要热链（旧→新）；会话不存在/归档为空返回空切片。
	GetSummaries(ctx context.Context, chatID string) []entity.Summary
}