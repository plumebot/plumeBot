package memory

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"plumebot/internal/domain/entity"
)

func groupMsg(groupID, content string) entity.Message {
	return entity.Message{GroupID: groupID, MessageType: "group", Parts: textParts(content)}
}

// textParts 构造单文本内容段。
func textParts(content string) []entity.ContentPart {
	return []entity.ContentPart{{Type: entity.PartTypeText, Text: content}}
}

func TestWindowAppendAndGet(t *testing.T) {
	w := NewWindow()
	full, err := w.AppendMessage(context.Background(), groupMsg("g1", "hi"))
	if err != nil {
		t.Fatalf("追加失败: %v", err)
	}
	if full {
		t.Error("首条消息不应触发压缩信号")
	}
	got, err := w.GetWindow(context.Background(), "g1")
	if err != nil {
		t.Fatalf("读取窗口失败: %v", err)
	}
	if len(got) != 1 || got[0].PlainText() != "hi" {
		t.Errorf("窗口内容错误: %+v", got)
	}
}

func TestWindowPerGroupIsolation(t *testing.T) {
	w := NewWindow()
	w.AppendMessage(context.Background(), groupMsg("g1", "a"))
	w.AppendMessage(context.Background(), groupMsg("g2", "b"))

	g1, _ := w.GetWindow(context.Background(), "g1")
	g2, _ := w.GetWindow(context.Background(), "g2")
	if len(g1) != 1 || g1[0].PlainText() != "a" {
		t.Errorf("g1 窗口错误: %+v", g1)
	}
	if len(g2) != 1 || g2[0].PlainText() != "b" {
		t.Errorf("g2 窗口错误: %+v", g2)
	}
}

func TestWindowPrivateSessionKeyIsolated(t *testing.T) {
	w := NewWindow()
	// 私聊消息 GroupID 为空，按用户隔离，避免互相串窗。
	w.AppendMessage(context.Background(), entity.Message{UserID: "u1", MessageType: "private", Parts: textParts("a")})
	w.AppendMessage(context.Background(), entity.Message{UserID: "u2", MessageType: "private", Parts: textParts("b")})

	u1, _ := w.GetWindow(context.Background(), "private:u1")
	u2, _ := w.GetWindow(context.Background(), "private:u2")
	if len(u1) != 1 || u1[0].PlainText() != "a" {
		t.Errorf("u1 私聊窗口错误: %+v", u1)
	}
	if len(u2) != 1 || u2[0].PlainText() != "b" {
		t.Errorf("u2 私聊窗口错误: %+v", u2)
	}
}

// TestWindowAppendToSessionExplicitKey 显式会话键追加：消息自身 SessionKey 与目标会话不同
// （私聊 bot 回复场景：作者是 botID，目标会话是触发用户）仍入目标会话，自身会话不产生孤儿。
func TestWindowAppendToSessionExplicitKey(t *testing.T) {
	w := NewWindow()
	botMsg := entity.Message{
		MessageID: "self:1", UserID: "bot", MessageType: "private", Parts: textParts("ok"),
	}
	full, err := w.AppendToSession(context.Background(), "private:u1", botMsg)
	if err != nil {
		t.Fatalf("AppendToSession 失败: %v", err)
	}
	if full {
		t.Error("首条消息不应触发压缩信号")
	}

	u1, _ := w.GetWindow(context.Background(), "private:u1")
	if len(u1) != 1 || u1[0].UserID != "bot" || u1[0].PlainText() != "ok" {
		t.Errorf("目标会话应含 bot 回复: %+v", u1)
	}
	// 消息自身派生会话（"private:bot"）不应出现孤儿。
	if orphan, _ := w.GetWindow(context.Background(), "private:bot"); len(orphan) != 0 {
		t.Errorf("消息自身会话不应有内容（修复前 bot 回复进 private:bot 孤立会话）: %+v", orphan)
	}
}

func TestWindowReachesCapSignalsCompression(t *testing.T) {
	w := NewWindow()
	for i := 0; i < WindowCap-1; i++ {
		full, err := w.AppendMessage(context.Background(), groupMsg("g1", "m"))
		if err != nil {
			t.Fatalf("追加失败: %v", err)
		}
		if full {
			t.Fatalf("第 %d 条不应触发压缩信号", i+1)
		}
	}
	full, _ := w.AppendMessage(context.Background(), groupMsg("g1", "cap"))
	if !full {
		t.Error("达到上限时应触发压缩信号")
	}
}

func TestWindowEvictsOldestBeyondCap(t *testing.T) {
	w := NewWindow()
	for i := 1; i <= WindowCap+5; i++ {
		m := groupMsg("g1", "m")
		m.MessageID = strconv.Itoa(i)
		_, _ = w.AppendMessage(context.Background(), m)
	}
	got, _ := w.GetWindow(context.Background(), "g1")
	if len(got) != WindowCap {
		t.Fatalf("窗口长度 = %d, want %d", len(got), WindowCap)
	}
	// 最旧的 5 条被淘汰，窗口保留最新 WindowCap 条（MessageID 从 6 开始）。
	if got[0].MessageID != "6" || got[WindowCap-1].MessageID != strconv.Itoa(WindowCap+5) {
		t.Errorf("淘汰最旧消息错误: 首条 %q 末条 %q", got[0].MessageID, got[WindowCap-1].MessageID)
	}
}

func TestWindowGetWindowReturnsCopy(t *testing.T) {
	w := NewWindow()
	w.AppendMessage(context.Background(), groupMsg("g1", "a"))

	got, _ := w.GetWindow(context.Background(), "g1")
	got[0].Parts = textParts("mutated")
	again, _ := w.GetWindow(context.Background(), "g1")
	if again[0].PlainText() != "a" {
		t.Error("GetWindow 应返回副本，外部修改不应影响窗口内部数据")
	}
}

// TestWindowGetWindowDeepCopiesParts 深拷贝（B-040）：修改返回副本的 Parts 元素（如回填描述）
// 不影响窗口内部——组装/压缩各持独立快照，无逃逸数组竞态。
func TestWindowGetWindowDeepCopiesParts(t *testing.T) {
	w := NewWindow()
	m := groupMsg("g1", "a")
	m.MessageID = "m1"
	m.Parts = []entity.ContentPart{{Type: entity.PartTypeImage, URL: "http://x/1.png"}}
	w.AppendMessage(context.Background(), m)

	got, _ := w.GetWindow(context.Background(), "g1")
	got[0].Parts[0].Description = "描述"
	again, _ := w.GetWindow(context.Background(), "g1")
	if again[0].Parts[0].Description != "" {
		t.Error("GetWindow 应深拷贝 Parts，外部改描述不应影响窗口内部")
	}
}

// TestWindowBackfillParts 安全回填（B-040）：窗口锁内写回描述；命中/未命中/空 ID 语义。
func TestWindowBackfillParts(t *testing.T) {
	w := NewWindow()
	m := groupMsg("g1", "a")
	m.MessageID = "m1"
	w.AppendMessage(context.Background(), m)

	// 命中：写回窗口内部，下次 GetWindow 读到描述。
	parts := []entity.ContentPart{
		{Type: entity.PartTypeText, Text: "a"},
		{Type: entity.PartTypeImage, URL: "http://x/1.png", Description: "描述"},
	}
	w.BackfillParts(context.Background(), "g1", "m1", parts)
	got, _ := w.GetWindow(context.Background(), "g1")
	if len(got[0].Parts) != 2 || got[0].Parts[1].Description != "描述" {
		t.Errorf("回填后窗口应含描述: %+v", got[0].Parts)
	}

	// 未命中（消息不存在）：静默忽略，不新增。
	w.BackfillParts(context.Background(), "g1", "nope", parts)
	got, _ = w.GetWindow(context.Background(), "g1")
	if len(got) != 1 || len(got[0].Parts) != 2 {
		t.Errorf("未命中回填不应改动窗口: %+v", got)
	}

	// 空 messageID：直接忽略（与 describeMessage 空 ID 边角一致，B-041）。
	w.BackfillParts(context.Background(), "g1", "", parts)
	got, _ = w.GetWindow(context.Background(), "g1")
	if len(got) != 1 || len(got[0].Parts) != 2 {
		t.Errorf("空 ID 回填应忽略: %+v", got)
	}
}

// TestWindowBackfillPartsDeepCopies 回填应深拷贝（B-040 竞态修复）：调用方在回填后继续写/扩容
// parts 切片（describeMessage 逐图描述循环），不影响窗口内部——窗口不与该消息共享数组。
func TestWindowBackfillPartsDeepCopies(t *testing.T) {
	w := NewWindow()
	m := groupMsg("g1", "a")
	m.MessageID = "m1"
	w.AppendMessage(context.Background(), m)

	parts := []entity.ContentPart{{Type: entity.PartTypeImage, URL: "http://x/1.png", Description: "第一张"}}
	w.BackfillParts(context.Background(), "g1", "m1", parts)

	// 模拟 describeMessage 循环在回填后继续描述下一条图片：改写 + 扩容调用方 slice。
	parts[0].Description = "改后"
	parts = append(parts, entity.ContentPart{Type: entity.PartTypeImage, URL: "http://x/2.png", Description: "第二张"})

	got, _ := w.GetWindow(context.Background(), "g1")
	if len(got[0].Parts) != 1 || got[0].Parts[0].Description != "第一张" {
		t.Errorf("回填应深拷贝，调用方后续写/扩容不应影响窗口: %+v", got[0].Parts)
	}
}

func TestWindowRemoveByIDs(t *testing.T) {
	w := NewWindow()
	for i := 1; i <= 5; i++ {
		m := groupMsg("g1", "m")
		m.MessageID = strconv.Itoa(i)
		_, _ = w.AppendMessage(context.Background(), m)
	}

	removed, err := w.RemoveByIDs(context.Background(), "g1", []string{"2", "4"})
	if err != nil {
		t.Fatalf("移除失败: %v", err)
	}
	if removed != 2 {
		t.Errorf("应移除 2 条, 实际 %d", removed)
	}
	got, _ := w.GetWindow(context.Background(), "g1")
	if len(got) != 3 || got[0].MessageID != "1" || got[1].MessageID != "3" || got[2].MessageID != "5" {
		t.Errorf("窗口应保留 1/3/5, 实际: %+v", got)
	}
}

func TestWindowRemoveByIDsPreservesConcurrentAdds(t *testing.T) {
	w := NewWindow()
	// 批次 1~4 被压缩移除，5~6 是压缩期间并发追加的新消息（ID 不在批次内），应保留。
	for i := 1; i <= 6; i++ {
		m := groupMsg("g1", "m")
		m.MessageID = strconv.Itoa(i)
		_, _ = w.AppendMessage(context.Background(), m)
	}

	_, err := w.RemoveByIDs(context.Background(), "g1", []string{"1", "2", "3", "4"})
	if err != nil {
		t.Fatalf("移除失败: %v", err)
	}
	got, _ := w.GetWindow(context.Background(), "g1")
	if len(got) != 2 || got[0].MessageID != "5" || got[1].MessageID != "6" {
		t.Errorf("压缩期追加的新消息不应被移除, 实际: %+v", got)
	}
}

// TestWindowConcurrentSessions 并发操作不同会话（群 g0~g7）：每会话独立锁互不阻塞，
// 无数据竞态（-race 下执行），且各会话窗口隔离不串。单会话内仍保序。
func TestWindowConcurrentSessions(t *testing.T) {
	w := NewWindow()
	ctx := context.Background()
	const groups = 8
	const perGroup = 100

	var wg sync.WaitGroup
	for g := 0; g < groups; g++ {
		wg.Add(1)
		go func(group string) {
			defer wg.Done()
			for i := 0; i < perGroup; i++ {
				m := groupMsg(group, "m")
				m.MessageID = group + "-" + strconv.Itoa(i)
				if _, err := w.AppendMessage(ctx, m); err != nil {
					t.Errorf("会话 %s 追加失败: %v", group, err)
					return
				}
				if _, err := w.GetWindow(ctx, group); err != nil {
					t.Errorf("会话 %s 读取失败: %v", group, err)
					return
				}
			}
		}("g" + strconv.Itoa(g))
	}
	wg.Wait()

	for g := 0; g < groups; g++ {
		got, _ := w.GetWindow(ctx, "g"+strconv.Itoa(g))
		if len(got) != perGroup {
			t.Errorf("会话 %d 窗口长度 = %d, want %d", g, len(got), perGroup)
		}
	}
}
