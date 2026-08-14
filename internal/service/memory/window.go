package memory

import (
	"context"
	"sync"

	"plumebot/internal/domain/entity"
)

// 上下文窗口容量常量（轮 = 1 条消息）。
const (
	WindowCap       = 100 // 窗口上限：达到后淘汰最旧并触发压缩信号
	CompressionKeep = 20  // 一级压缩后保留轮数（P3-003 使用，架构 §4.1「初始 20 轮」）
)

// Window 是按会话维护的 ring buffer 上下文窗口，实现 domain.Memory。
// 会话键：群聊=GroupID，私聊="private:"+UserID（避免空 GroupID 的私聊互相串窗）。
type Window struct {
	mu   sync.Mutex
	data map[string][]entity.Message
}

// NewWindow 创建空窗口。
func NewWindow() *Window {
	return &Window{data: make(map[string][]entity.Message)}
}

// AppendMessage 追加一条消息到会话窗口。达到 WindowCap 上限时淘汰最旧消息并返回压缩触发信号。
func (w *Window) AppendMessage(_ context.Context, msg entity.Message) (bool, error) {
	key := msg.SessionKey()
	w.mu.Lock()
	defer w.mu.Unlock()

	buf := w.data[key]
	buf = append(buf, msg)
	full := false
	if len(buf) >= WindowCap {
		full = true
		if len(buf) > WindowCap {
			// 窗口已满：淘汰最旧，保持不超上限。全量数据已落 SQLite，窗口淘汰不丢记忆。
			buf = buf[len(buf)-WindowCap:]
		}
	}
	w.data[key] = buf
	return full, nil
}

// GetWindow 返回会话窗口内消息的副本（时间正序）。会话不存在时返回空切片。
func (w *Window) GetWindow(_ context.Context, sessionID string) ([]entity.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	buf := w.data[sessionID]
	out := make([]entity.Message, len(buf))
	copy(out, buf)
	return out, nil
}

// RemoveByIDs 从会话窗口精确移除指定 MessageID 的消息（P3-003 压缩批次归档后调用），
// 返回实际移除数量。并发追加的新消息（ID 不在批次内）不受影响。
func (w *Window) RemoveByIDs(_ context.Context, sessionID string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	buf := w.data[sessionID]
	kept := buf[:0]
	removed := 0
	for _, m := range buf {
		if _, ok := idSet[m.MessageID]; ok {
			removed++
			continue
		}
		kept = append(kept, m)
	}
	w.data[sessionID] = kept
	return removed, nil
}
