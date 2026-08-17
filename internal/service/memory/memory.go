package memory

import (
	"context"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
)

// ---------------------------------------------------------------------------
// MemoryService
// ---------------------------------------------------------------------------

// BuilderConfig 是 prompt 组装（BuildMessages，P6-001）的注入参数。
type BuilderConfig struct {
	Prompt         config.PromptConfig // 上限/预算（B-014/B-027）；字段 ≤0 → config 默认常量
	AgentName      string              // persona 查询键（GetPersonaByAgent）；空 → config.DefaultAgentName
	DefaultPersona string              // persona 未命中/空 时的兜底人设；空 → config.DefaultSystemPrompt
}

// MemoryService 负责上下文窗口维护、画像缓存编排、窗口压缩（P3-003）与 prompt 组装（P6-001）。
type MemoryService struct {
	memory         domain.Memory
	store          domain.Storage
	profiles       *ProfileCache
	compressor     *Compressor
	describer      domain.MediaDescriber // 阶段2：图片描述器；nil = 描述关闭（P6-001 惰性装配消费）
	prompt         config.PromptConfig
	agentName      string
	defaultPersona string
}

// NewMemoryService 创建 MemoryService，注入 domain.Memory（窗口实现）、domain.Storage、
// domain.Summarizer（窗口压缩的 LLM 摘要器）、builder（组装预算与 persona 兜底）与
// 可选 domain.MediaDescriber（图片描述器，变参，nil = 描述关闭）。
func NewMemoryService(memory domain.Memory, store domain.Storage, summarizer domain.Summarizer, builder BuilderConfig, describer ...domain.MediaDescriber) *MemoryService {
	s := &MemoryService{
		memory:         memory,
		store:          store,
		profiles:       NewProfileCache(store),
		compressor:     NewCompressor(memory, summarizer, NewSummaryStore(store)),
		prompt:         builder.Prompt,
		agentName:      builder.AgentName,
		defaultPersona: builder.DefaultPersona,
	}
	if len(describer) > 0 {
		s.describer = describer[0]
	}
	return s
}

// PersistMessage 持久化一条消息到其自身会话：写入上下文窗口（内存 ring buffer）+ SQLite messages 表，
// 并触达画像缓存（窗口内成员按需加载）。返回窗口是否达到压缩阈值（P3-003 消费该信号触发一级压缩）。
func (s *MemoryService) PersistMessage(ctx context.Context, msg entity.Message) (bool, error) {
	return s.persist(ctx, msg.SessionKey(), msg)
}

// PersistMessageToSession 持久化一条消息到指定会话（显式会话键，不依赖消息自身 SessionKey）。
// 供 event respond 持久化 bot 自身回复：私聊下 bot 回复的作者（botID）会派生独立会话
// "private:"+botID，必须显式并入触发消息（用户）的会话，bot 回复才进得了用户对话窗（见 event.go）。
func (s *MemoryService) PersistMessageToSession(ctx context.Context, sessionID string, msg entity.Message) (bool, error) {
	return s.persist(ctx, sessionID, msg)
}

// persist 共享落库路径：按会话键写窗口 → SQLite → 画像缓存触达。
func (s *MemoryService) persist(ctx context.Context, sessionID string, msg entity.Message) (bool, error) {
	full, err := s.memory.AppendToSession(ctx, sessionID, msg)
	if err != nil {
		return false, err
	}
	if err := s.store.SaveMessage(ctx, msg); err != nil {
		return false, err
	}
	s.profiles.TouchMessage(ctx, msg)
	return full, nil
}

// GetWindow 返回会话窗口消息（会话键：群聊=GroupID，私聊="private:"+UserID）。
func (s *MemoryService) GetWindow(ctx context.Context, sessionID string) ([]entity.Message, error) {
	return s.memory.GetWindow(ctx, sessionID)
}

// GetGroupProfile 返回缓存的群画像。
func (s *MemoryService) GetGroupProfile(groupID string) (*entity.GroupProfile, bool) {
	return s.profiles.GetGroupProfile(groupID)
}

// Compress 触发一次该会话的异步窗口压缩（窗口满时由事件层消费 full 信号调用）。
// 具体流程见 Compressor：一级压缩 → 热链 → 二级融合/淘汰 → 裁剪窗口。
func (s *MemoryService) Compress(ctx context.Context, msg entity.Message) {
	s.compressor.Trigger(ctx, msg.SessionKey())
}

// GetSummaries 返回会话摘要热链（P6-001 拼 prompt 时拼接，位于人格之后、窗口之前），旧→新。
func (s *MemoryService) GetSummaries(ctx context.Context, chatID string) []entity.Summary {
	return s.compressor.summaries.GetAll(ctx, chatID)
}
