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

// Window 是按会话维护的上下文窗口，实现 domain.Memory。
// 会话键：群聊=GroupID，私聊="private:"+UserID（避免空 GroupID 的私聊互相串窗）。
// 锁粒度：每会话一把锁（sessionWindow.mu），**不同会话（群/私聊）的窗口操作互不阻塞**；
// 会话注册表用 sync.Map（LoadOrStore 原子 get-or-create，无全局锁）。同一会话内仍串行保序。
// 与 service/control 的 per-session 锁（control.go lock()）同一模式（P6-001 审查定案）。
// 注意：per-session 锁只保证窗口内部数据结构的串行访问，不覆盖 GetWindow 浅拷贝逃逸出的
// Parts 数组——同会话 BuildMessages 与窗口压缩的数组读写约束见 roadmap B-040。
type Window struct {
	sessions sync.Map // sessionKey → *sessionWindow
}

// sessionWindow 是单个会话的窗口条目：自己的锁 + 消息切片。
type sessionWindow struct {
	mu   sync.Mutex
	data []entity.Message
}

// NewWindow 创建空窗口。
func NewWindow() *Window {
	return &Window{}
}

// AppendMessage 追加一条消息到会话窗口。达到 WindowCap 上限时淘汰最旧消息并返回压缩触发信号。
func (w *Window) AppendMessage(_ context.Context, msg entity.Message) (bool, error) {
	key := msg.SessionKey()
	raw, _ := w.sessions.LoadOrStore(key, &sessionWindow{})
	sw := raw.(*sessionWindow)
	sw.mu.Lock()
	defer sw.mu.Unlock()

	buf := sw.data
	buf = append(buf, msg)
	full := false
	if len(buf) >= WindowCap {
		full = true
		if len(buf) > WindowCap {
			// 窗口已满：淘汰最旧，保持不超上限。全量数据已落 SQLite，窗口淘汰不丢记忆。
			buf = buf[len(buf)-WindowCap:]
		}
	}
	sw.data = buf
	return full, nil
}

// GetWindow 返回会话窗口内消息的副本（时间正序）。会话不存在时返回空切片。
// 注意：为浅拷贝——Message 结构体被复制，但 Parts 切片头共享窗口内部底层数组。
// 只可改 Parts 元素（如回填 Description），勿替换整个 Parts 切片或增删元素
// （P6-001 BuildMessages 惰性图片描述依赖此共享语义原地写回；并发约束见 B-040）。
func (w *Window) GetWindow(_ context.Context, sessionID string) ([]entity.Message, error) {
	v, ok := w.sessions.Load(sessionID)
	if !ok {
		return []entity.Message{}, nil
	}
	sw := v.(*sessionWindow)
	sw.mu.Lock()
	defer sw.mu.Unlock()
	buf := sw.data
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
	v, ok := w.sessions.Load(sessionID)
	if !ok {
		return 0, nil
	}
	sw := v.(*sessionWindow)
	sw.mu.Lock()
	defer sw.mu.Unlock()

	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	buf := sw.data
	kept := buf[:0]
	removed := 0
	for _, m := range buf {
		if _, ok := idSet[m.MessageID]; ok {
			removed++
			continue
		}
		kept = append(kept, m)
	}
	sw.data = kept
	return removed, nil
}
