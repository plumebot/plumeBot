package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// CompressBatch 是一级压缩一次取走的窗口消息条数（窗口满 100 时取最早 80，保留最新 20）。
const CompressBatch = WindowCap - CompressionKeep

// CompressCooldown 是压缩失败后的冷却时长，防止 LLM 故障时每条消息都触发重试风暴。
const CompressCooldown = 60 * time.Second

// Compressor 编排窗口压缩（P3-003）：
// 快照窗口最早批次 → LLM 一级压缩 → 入热链 → 超上限二级融合（全部→1）/ FIFO 淘汰兜底
// → 按 MessageID 裁剪窗口批次。经 Trigger 异步执行，会话级防重入 + 失败冷却，不阻塞消息管线。
type Compressor struct {
	window     domain.Memory
	summarizer domain.Summarizer
	summaries  *SummaryStore

	mu          sync.Mutex
	inflight    map[string]bool      // chatID -> 压缩进行中（防并发重复压缩）
	lastAttempt map[string]time.Time // chatID -> 最近一次压缩失败时刻（冷却用）
}

// NewCompressor 创建压缩编排器，注入窗口、摘要器与摘要存储。
func NewCompressor(window domain.Memory, summarizer domain.Summarizer, summaries *SummaryStore) *Compressor {
	return &Compressor{
		window:      window,
		summarizer:  summarizer,
		summaries:   summaries,
		inflight:    make(map[string]bool),
		lastAttempt: make(map[string]time.Time),
	}
}

// Trigger 触发一次该会话的异步压缩。会话压缩中或处于失败冷却期时直接跳过。
func (c *Compressor) Trigger(ctx context.Context, chatID string) {
	c.mu.Lock()
	if c.inflight[chatID] {
		c.mu.Unlock()
		logger.Debug("跳过压缩：会话压缩中", logger.S("chat_id", chatID))
		return
	}
	if t, ok := c.lastAttempt[chatID]; ok && time.Since(t) < CompressCooldown {
		c.mu.Unlock()
		logger.Debug("跳过压缩：失败冷却期", logger.S("chat_id", chatID))
		return
	}
	c.inflight[chatID] = true
	c.mu.Unlock()

	go c.run(ctx, chatID)
}

// run 是异步压缩入口：执行压缩，负责释放防重入锁并记录失败冷却。
func (c *Compressor) run(ctx context.Context, chatID string) {
	defer func() {
		c.mu.Lock()
		delete(c.inflight, chatID)
		c.mu.Unlock()
	}()
	if err := c.compress(ctx, chatID); err != nil {
		c.mu.Lock()
		c.lastAttempt[chatID] = time.Now()
		c.mu.Unlock()
		logger.Warn("窗口压缩失败，进入冷却",
			logger.S("chat_id", chatID), logger.Err(err))
	}
}

// compress 同步执行一次压缩。返回 error 表示压缩未完成（不裁剪窗口，数据不丢，冷却后重试）。
func (c *Compressor) compress(ctx context.Context, chatID string) error {
	// 1. 快照窗口最早批次（保留最新 CompressionKeep 条继续容纳新消息）。
	msgs, err := c.window.GetWindow(ctx, chatID)
	if err != nil {
		return err
	}
	// 批大小 = 满窗时取最早 CompressBatch 条（保留最新 CompressionKeep）；
	// 窗口未满（防御路径）按实际长度取，不会超过 CompressBatch（B-032）。
	batchEnd := len(msgs) - CompressionKeep
	if batchEnd > CompressBatch {
		batchEnd = CompressBatch
	}
	if batchEnd <= 0 {
		logger.Debug("窗口不足一个压缩批次，跳过", logger.S("chat_id", chatID))
		return nil // 窗口不足一个压缩批次（防御：触发时窗口应已满）
	}
	batch := msgs[:batchEnd]

	// 2. 一级压缩：最早批次 → LLM 摘要。
	text, err := c.summarizer.Summarize(ctx, systemSummaryPrompt, buildLevel1UserPrompt(batch))
	if err != nil {
		return fmt.Errorf("一级压缩失败: %w", err)
	}
	sum := parseSummary(text)

	// 3. 入热链。
	c.summaries.Append(ctx, chatID, sum.Text, sum.Keywords, sum.Decisions)

	// 4. 热链超上限 → 二级融合；融合失败则 FIFO 淘汰最旧兜底。
	c.maybeFuse(ctx, chatID)

	// 5. 裁剪窗口：按 MessageID 精确移除被压缩的批次（并发追加的新消息不受影响）。
	ids := make([]string, len(batch))
	for i, m := range batch {
		ids[i] = m.MessageID
	}
	if _, err := c.window.RemoveByIDs(ctx, chatID, ids); err != nil {
		return fmt.Errorf("裁剪窗口失败: %w", err)
	}
	logger.Info("窗口压缩完成",
		logger.S("chat_id", chatID),
		logger.I("batch", len(batch)),
		logger.I("summary_chars", len(sum.Text)))
	return nil
}

// maybeFuse 热链条数超过 SummaryCap 时执行二级融合（全部→1）；融合失败则 FIFO 淘汰最旧。
func (c *Compressor) maybeFuse(ctx context.Context, chatID string) {
	chain := c.summaries.GetAll(ctx, chatID)
	if len(chain) <= SummaryCap {
		return
	}
	fused, err := c.fuseSummaries(ctx, chain)
	if err != nil {
		logger.Warn("二级融合失败，FIFO 淘汰最旧摘要",
			logger.S("chat_id", chatID), logger.I64("count", int64(len(chain))), logger.Err(err))
		c.summaries.EvictOldest(ctx, chatID)
		return
	}
	c.summaries.ReplaceWithFused(ctx, chatID, fused.Text, fused.Keywords, fused.Decisions)
}

// fuseSummaries 调用 LLM 把多条历史摘要融合为一条综合摘要。
func (c *Compressor) fuseSummaries(ctx context.Context, chain []entity.Summary) (entity.Summary, error) {
	text, err := c.summarizer.Summarize(ctx, systemFusePrompt, buildLevel2UserPrompt(chain))
	if err != nil {
		return entity.Summary{}, fmt.Errorf("二级融合失败: %w", err)
	}
	return parseSummary(text), nil
}

// --- prompt 与输出解析 ---

// summaryJSON 是一级压缩 / 二级融合期望的 LLM 输出结构。
type summaryJSON struct {
	Summary   string   `json:"summary"`
	Keywords  []string `json:"keywords"`
	Decisions []string `json:"decisions"`
}

// parseSummary 解析压缩/融合的 LLM 输出。期望 JSON {summary,keywords,decisions}；
// 解析失败或 summary 为空时回退为把整段输出当作纯摘要文本（不阻断压缩流程）。
func parseSummary(out string) entity.Summary {
	var j summaryJSON
	if err := json.Unmarshal([]byte(out), &j); err == nil && strings.TrimSpace(j.Summary) != "" {
		return entity.Summary{Text: strings.TrimSpace(j.Summary), Keywords: j.Keywords, Decisions: j.Decisions}
	}
	return entity.Summary{Text: strings.TrimSpace(out)}
}

// systemSummaryPrompt 一级压缩系统指令。
const systemSummaryPrompt = `你是 QQ 群聊记录的摘要助手。你的任务是把一段群聊记录压缩成结构化摘要，保留话题脉络、关键事件与重要信息，丢弃闲聊与寒暄噪音。

输出严格为单个 JSON 对象，不要输出任何其他内容，格式如下：
{"summary":"不超过 150 字的中文摘要","keywords":["3 到 5 个话题关键词"],"decisions":["群聊中做出的关键决定或重要共识；没有则留空数组"]}`

// systemFusePrompt 二级融合系统指令。
const systemFusePrompt = `你是群聊长期记忆的融合助手。你会收到多条按时间排序的历史摘要，请把它们融合成一条更全面的综合摘要：去重、保留每条的关键信息、按时间脉络组织。

输出严格为单个 JSON 对象，不要输出任何其他内容，格式如下：
{"summary":"不超过 200 字的中文综合摘要","keywords":["3 到 5 个关键词"],"decisions":["关键决定或重要共识；没有则留空数组"]}`

// buildLevel1UserPrompt 把压缩批次格式化为待摘要的群聊记录文本。
// 用 ForLLM 文本视图（P6-001 B-026）：text+at 原文 + 图片描述（无则 [图片]），
// 与 prompt 组装共用；Render() 留给日志。
func buildLevel1UserPrompt(batch []entity.Message) string {
	var sb strings.Builder
	sb.WriteString("以下是群聊记录（按时间顺序，每条为「发送者QQ号 + 内容」）：\n")
	for i, m := range batch {
		fmt.Fprintf(&sb, "%d [%s]: %s\n", i+1, m.UserID, m.ForLLM())
	}
	return sb.String()
}

// buildLevel2UserPrompt 把热链摘要列表格式化为待融合的历史摘要文本。
func buildLevel2UserPrompt(chain []entity.Summary) string {
	var sb strings.Builder
	sb.WriteString("以下是若干条历史摘要（按时间顺序）：\n")
	for i, s := range chain {
		fmt.Fprintf(&sb, "[%d] %s\n", i+1, s.Text)
	}
	sb.WriteString("\n请融合为一条综合摘要。")
	return sb.String()
}
