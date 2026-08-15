package memory

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"plumebot/internal/domain/entity"
)

// fillWindow 向窗口追加 count 条消息，MessageID 从 startID 起递增（供 RemoveByIDs 精确匹配）。
func fillWindow(t *testing.T, w *Window, chatID string, startID, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		m := groupMsg(chatID, "m")
		m.MessageID = strconv.Itoa(startID + i)
		if _, err := w.AppendMessage(context.Background(), m); err != nil {
			t.Fatalf("追加失败: %v", err)
		}
	}
}

func TestParseSummary(t *testing.T) {
	s := parseSummary(`{"summary":"本周话题","keywords":["游戏","动画"],"decisions":["周五团建"]}`)
	if s.Text != "本周话题" || len(s.Keywords) != 2 || s.Keywords[0] != "游戏" || len(s.Decisions) != 1 {
		t.Errorf("JSON 解析错误: %+v", s)
	}

	fallback := parseSummary("模型没有按 JSON 输出，直接一段文字")
	if fallback.Text != "模型没有按 JSON 输出，直接一段文字" || len(fallback.Keywords) != 0 {
		t.Errorf("非 JSON 输出应回退为纯摘要文本: %+v", fallback)
	}
}

// TestLevel1PromptUsesForLLM 压缩 prompt 用 ForLLM 视图（B-026）：有描述的图片注入描述而非 [图片]。
func TestLevel1PromptUsesForLLM(t *testing.T) {
	batch := []entity.Message{
		{MessageID: "1", GroupID: "g1", UserID: "u1", Parts: []entity.ContentPart{
			{Type: entity.PartTypeText, Text: "看看"},
			{Type: entity.PartTypeImage, URL: "http://x/1.png", Description: "一只猫"},
		}},
	}
	got := buildLevel1UserPrompt(batch)
	if !strings.Contains(got, "（图片：一只猫）") {
		t.Errorf("压缩 prompt 应注入图片描述（ForLLM）: %q", got)
	}
	if strings.Contains(got, "[图片]") {
		t.Errorf("有描述的图片不应回退 [图片]: %q", got)
	}
}

func TestCompressWindowFullCompressesAndTrims(t *testing.T) {
	store := &fakeStorage{}
	w := NewWindow()
	fillWindow(t, w, "g1", 1, WindowCap)
	sum := &fakeSummarizer{results: []string{`{"summary":"一周热点","keywords":["游戏"],"decisions":["周五团建"]}`}}
	c := NewCompressor(w, sum, NewSummaryStore(store))

	if err := c.compress(context.Background(), "g1"); err != nil {
		t.Fatalf("压缩失败: %v", err)
	}
	if sum.calls != 1 {
		t.Errorf("一级压缩应只调一次 LLM, 实际 %d", sum.calls)
	}

	chain := c.summaries.GetAll(context.Background(), "g1")
	if len(chain) != 1 || chain[0].Text != "一周热点" || len(chain[0].Keywords) != 1 || chain[0].Keywords[0] != "游戏" {
		t.Errorf("热链应包含解析后的摘要: %+v", chain)
	}

	got, _ := w.GetWindow(context.Background(), "g1")
	if len(got) != CompressionKeep {
		t.Fatalf("窗口应保留最新 %d 条, 实际 %d", CompressionKeep, len(got))
	}
	// 保留的是最新 20 条（ID 81~100），被压缩的最早 80 条已移除。
	if got[0].MessageID != "81" || got[CompressionKeep-1].MessageID != "100" {
		t.Errorf("窗口应保留最新 20 条（81~100）, 实际首条 %q 末条 %q",
			got[0].MessageID, got[CompressionKeep-1].MessageID)
	}
}

func TestCompressFailureKeepsWindowAndChain(t *testing.T) {
	store := &fakeStorage{}
	w := NewWindow()
	fillWindow(t, w, "g1", 1, WindowCap)
	sum := &fakeSummarizer{err: errors.New("LLM 不可用")}
	c := NewCompressor(w, sum, NewSummaryStore(store))

	if err := c.compress(context.Background(), "g1"); err == nil {
		t.Fatal("压缩失败时应返回错误")
	}
	got, _ := w.GetWindow(context.Background(), "g1")
	if len(got) != WindowCap {
		t.Errorf("失败时不应裁剪窗口（数据不丢）, 实际 %d", len(got))
	}
	if len(c.summaries.GetAll(context.Background(), "g1")) != 0 {
		t.Error("失败时不应产生摘要")
	}
}

func TestCompressFusesAtCap(t *testing.T) {
	store := &fakeStorage{}
	w := NewWindow()
	c := NewCompressor(w, &fakeSummarizer{}, NewSummaryStore(store))

	// 每轮压缩消耗 80 条：首轮填满 100 条，之后每轮追加 80 条再压缩。
	nextID := 1
	fillWindow(t, w, "g1", nextID, WindowCap)
	nextID += WindowCap
	for round := 1; round <= SummaryCap+1; round++ {
		if round > 1 {
			fillWindow(t, w, "g1", nextID, CompressBatch)
			nextID += CompressBatch
		}
		if err := c.compress(context.Background(), "g1"); err != nil {
			t.Fatalf("第 %d 轮压缩失败: %v", round, err)
		}
	}

	// 第 6 轮追加后热链超上限，应整体融合为 1 条。
	chain := c.summaries.GetAll(context.Background(), "g1")
	if len(chain) != 1 {
		t.Errorf("超上限后应融合为 1 条, 实际 %d 条", len(chain))
	}
	// 被融合覆盖的 6 条摘要应落库归档。
	if len(store.archived) != SummaryCap+1 {
		t.Errorf("被覆盖摘要应归档 %d 条, 实际 %d", SummaryCap+1, len(store.archived))
	}
}

func TestCompressFuseFailureEvictsOldest(t *testing.T) {
	store := &fakeStorage{}
	w := NewWindow()
	// 前 6 次一级压缩成功（默认 JSON），第 7 次（二级融合）返回错误 → 走 FIFO 淘汰兜底。
	sum := &fakeSummarizer{errAt: 7, err: errors.New("融合失败")}
	c := NewCompressor(w, sum, NewSummaryStore(store))

	nextID := 1
	fillWindow(t, w, "g1", nextID, WindowCap)
	nextID += WindowCap
	for round := 1; round <= SummaryCap+1; round++ {
		if round > 1 {
			fillWindow(t, w, "g1", nextID, CompressBatch)
			nextID += CompressBatch
		}
		if err := c.compress(context.Background(), "g1"); err != nil {
			t.Fatalf("第 %d 轮压缩失败: %v", round, err)
		}
	}

	// 6 条入链、融合失败 → FIFO 淘汰最旧 1 条，热链剩 5 条。
	chain := c.summaries.GetAll(context.Background(), "g1")
	if len(chain) != SummaryCap {
		t.Errorf("融合失败应 FIFO 淘汰最旧, 热链剩 %d 条, 实际 %d", SummaryCap, len(chain))
	}
	if len(store.archived) != 1 {
		t.Errorf("被淘汰的最旧摘要应归档 1 条, 实际 %d", len(store.archived))
	}
}
