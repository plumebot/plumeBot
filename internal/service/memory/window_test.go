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
