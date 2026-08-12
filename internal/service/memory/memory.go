package memory

import (
	"context"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
)

// ---------------------------------------------------------------------------
// MemoryService
// ---------------------------------------------------------------------------

// MemoryService 负责上下文窗口维护、画像缓存编排与窗口压缩（P3-003）。
type MemoryService struct {
	memory     domain.Memory
	store      domain.Storage
	profiles   *ProfileCache
	compressor *Compressor
	describer  domain.MediaDescriber // 阶段2：图片描述器；nil = 描述关闭（P6 BuildContext 惰性装配消费）
}

// NewMemoryService 创建 MemoryService，注入 domain.Memory（窗口实现）、domain.Storage、
// domain.Summarizer（窗口压缩的 LLM 摘要器）与可选 domain.MediaDescriber（图片描述器，
// 变参，nil = 描述关闭；P6 装配时经 BuildContext 惰性调用）。
func NewMemoryService(memory domain.Memory, store domain.Storage, summarizer domain.Summarizer, describer ...domain.MediaDescriber) *MemoryService {
	s := &MemoryService{
		memory:     memory,
		store:      store,
		profiles:   NewProfileCache(store),
		compressor: NewCompressor(memory, summarizer, NewSummaryStore(store)),
	}
	if len(describer) > 0 {
		s.describer = describer[0]
	}
	return s
}

// PersistMessage 持久化一条消息：写入上下文窗口（内存 ring buffer）+ SQLite messages 表，
// 并触达画像缓存（窗口内成员按需加载）。返回窗口是否达到压缩阈值（P3-003 消费该信号触发一级压缩）。
func (s *MemoryService) PersistMessage(ctx context.Context, msg entity.Message) (bool, error) {
	full, err := s.memory.AppendMessage(ctx, msg)
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
	s.compressor.Trigger(ctx, sessionKey(msg))
}

// GetSummaries 返回会话摘要热链（P6-001 拼 prompt 时拼接，位于人格之后、窗口之前），旧→新。
func (s *MemoryService) GetSummaries(ctx context.Context, chatID string) []entity.Summary {
	return s.compressor.summaries.GetAll(ctx, chatID)
}

// BuildContext 组装上下文窗口内容。第一阶段返回空。
func (s *MemoryService) BuildContext(_ context.Context, _ string) (string, error) {
	return "", nil
}
