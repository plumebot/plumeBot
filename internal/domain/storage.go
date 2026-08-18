package domain

import (
	"context"

	"plumebot/internal/domain/entity"
)

// Storage 持久化接口，覆盖全部 8 张业务表。
type Storage interface {
	// ── messages ──
	SaveMessage(ctx context.Context, msg entity.Message) error
	GetMessages(ctx context.Context, groupID string, limit int, offset int) ([]entity.Message, error)
	// UpdateMessageParts 覆盖一条消息的内容段（P6-001 B-025：图片描述写回 messages.parts）。
	// 幂等：message_id 不存在时不做任何事、不报错。
	UpdateMessageParts(ctx context.Context, messageID string, parts []entity.ContentPart) error

	// ── conversation_summary ──
	// SaveSummary 归档一条摘要（(chat_id, seq) 冲突时覆盖，幂等）。
	SaveSummary(ctx context.Context, summary entity.Summary) error
	// ListSummaries 返回指定会话最新的 limit 条归档摘要（时间序）。
	ListSummaries(ctx context.Context, chatID string, limit int) ([]entity.Summary, error)

	// ── group_profile ──
	UpsertGroupProfile(ctx context.Context, profile entity.GroupProfile) error
	GetGroupProfile(ctx context.Context, groupID string) (*entity.GroupProfile, error)

	// ── group_jargon ──
	AddJargon(ctx context.Context, groupID string, jargon string) error
	ListJargon(ctx context.Context, groupID string) ([]string, error)
	DeleteJargon(ctx context.Context, groupID string, jargon string) error
	// ListConfirmedJargon 列出指定群内已确认（status='confirmed'）的黑话。
	// P6 prompt 组装只暴露 confirmed 黑话（pending 待人工审核）。
	ListConfirmedJargon(ctx context.Context, groupID string) ([]string, error)
	// ConfirmJargon 把一条黑话置为 confirmed；黑话不存在时返回 domain.ErrNotFound。
	ConfirmJargon(ctx context.Context, groupID, jargon string) error

	// ── member_facts ──
	AddMemberFact(ctx context.Context, groupID, userID, fact string) error
	ListMemberFacts(ctx context.Context, groupID, userID string) ([]string, error)
	DeleteMemberFact(ctx context.Context, groupID, userID, fact string) error

	// ── persona ──
	InsertPersona(ctx context.Context, persona entity.Persona) (int64, error)
	UpdatePersona(ctx context.Context, persona entity.Persona) error
	GetPersona(ctx context.Context, id int64) (*entity.Persona, error)
	// GetPersonaByAgent 按绑定的 agent 名查询人格模板；不存在时返回 domain.ErrNotFound。
	GetPersonaByAgent(ctx context.Context, agent string) (*entity.Persona, error)

	// ── bot_state ──
	UpsertBotState(ctx context.Context, state entity.BotState) error
	GetBotState(ctx context.Context, groupID string) (*entity.BotState, error)

	// ── group_config ──
	UpsertGroupConfig(ctx context.Context, cfg entity.GroupConfig) error
	GetGroupConfig(ctx context.Context, groupID string) (*entity.GroupConfig, error)

	// Close 关闭数据库连接。
	Close() error
}
