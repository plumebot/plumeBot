package memory

import (
	"context"
	"sync"
	"time"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// SummaryCap 是每个会话摘要热链的条数上限（P3-003 验收「摘要总数有上限」）。
// 追加后超过上限即触发二级融合（全部→1）；融合失败时 FIFO 淘汰最旧兜底。
const SummaryCap = 5

// summaryReloadLimit 是重启回灌时从归档加载的热链底条数（等于热链上限）。
const summaryReloadLimit = SummaryCap

// SummaryStore 管理每个会话的摘要热链（内存）+ 长程归档（SQLite）。
//
// 热链：当前参与对话上下文的最新摘要，参与二级融合与 FIFO 淘汰。
// 归档：离开热链的摘要（被融合覆盖 / 被淘汰）落库 conversation_summary 表，
//
//	会话首次触达时惰性回灌最新若干条作为热链底 —— 重启后 AI 仍保有长程历史。
//
// 幂等性：落库以 (chat_id, seq) 为键 upsert，回灌的旧摘要再次离开热链时重复归档不产生冗余。
type SummaryStore struct {
	store domain.Storage

	mu      sync.Mutex
	chains  map[string][]entity.Summary // chatID -> 热链摘要（旧→新）
	nextSeq map[string]int64            // chatID -> 下一可用 seq（跨重启单调递增）
}

// NewSummaryStore 创建摘要存储，注入 domain.Storage 用于归档读写。
func NewSummaryStore(store domain.Storage) *SummaryStore {
	return &SummaryStore{
		store:   store,
		chains:  make(map[string][]entity.Summary),
		nextSeq: make(map[string]int64),
	}
}

// ensureLoaded 惰性回灌：会话首次访问时从归档加载最新 summaryReloadLimit 条作为热链底，
// 并将 seq 续号到已归档最大值 +1（保证跨重启不重复）。加载失败仅记日志，用空热链继续。
func (s *SummaryStore) ensureLoaded(ctx context.Context, chatID string) {
	if _, ok := s.chains[chatID]; ok {
		return
	}
	sums, err := s.store.ListSummaries(ctx, chatID, summaryReloadLimit)
	if err != nil {
		logger.Warn("回灌摘要失败，使用空热链",
			logger.S("chat_id", chatID), logger.Err(err))
	}
	maxSeq := int64(0)
	for _, sum := range sums {
		if sum.Seq > maxSeq {
			maxSeq = sum.Seq
		}
	}
	s.chains[chatID] = sums
	s.nextSeq[chatID] = maxSeq + 1
}

// Append 追加一条一级压缩摘要到热链，分配会话内递增 seq，返回该摘要。
func (s *SummaryStore) Append(ctx context.Context, chatID string, text string, keywords, decisions []string) entity.Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded(ctx, chatID)

	sum := entity.Summary{
		ChatID:    chatID,
		Seq:       s.nextSeq[chatID],
		Text:      text,
		Keywords:  keywords,
		Decisions: decisions,
		CreatedAt: time.Now().Unix(),
	}
	s.nextSeq[chatID]++
	s.chains[chatID] = append(s.chains[chatID], sum)
	return sum
}

// ReplaceWithFused 把热链整体替换为一条二级融合摘要（全部→1）。
// 被覆盖的旧摘要落库归档；返回新融合摘要。
func (s *SummaryStore) ReplaceWithFused(ctx context.Context, chatID string, text string, keywords, decisions []string) entity.Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded(ctx, chatID)

	for _, sum := range s.chains[chatID] {
		s.archive(ctx, sum)
	}
	fused := entity.Summary{
		ChatID:    chatID,
		Seq:       s.nextSeq[chatID],
		Text:      text,
		Keywords:  keywords,
		Decisions: decisions,
		CreatedAt: time.Now().Unix(),
	}
	s.nextSeq[chatID]++
	s.chains[chatID] = []entity.Summary{fused}
	return fused
}

// EvictOldest 淘汰热链最旧摘要（FIFO，二级融合失败时的兜底），落库归档后返回该摘要。
func (s *SummaryStore) EvictOldest(ctx context.Context, chatID string) entity.Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded(ctx, chatID)

	chain := s.chains[chatID]
	if len(chain) == 0 {
		return entity.Summary{}
	}
	evicted := chain[0]
	s.archive(ctx, evicted)
	s.chains[chatID] = chain[1:]
	return evicted
}

// GetAll 返回热链摘要副本（旧→新）。会话不存在（含归档为空）时返回空切片。
func (s *SummaryStore) GetAll(ctx context.Context, chatID string) []entity.Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded(ctx, chatID)

	out := make([]entity.Summary, len(s.chains[chatID]))
	copy(out, s.chains[chatID])
	return out
}

// archive 落库归档一条离开热链的摘要；失败仅记日志（热链行为不受影响）。
func (s *SummaryStore) archive(ctx context.Context, sum entity.Summary) {
	if err := s.store.SaveSummary(ctx, sum); err != nil {
		logger.Warn("归档摘要失败",
			logger.S("chat_id", sum.ChatID), logger.I64("seq", sum.Seq), logger.Err(err))
	}
}
