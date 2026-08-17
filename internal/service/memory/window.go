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
// 并发约束（B-040 定案：深拷贝 + 安全回填）：GetWindow 深拷贝 Parts，组装/压缩各自持有
// 独立快照，**不存在逃逸数组竞态**；图片描述经 BackfillParts 在窗口锁内写回内部消息，
// 无跨 LLM 持锁，同会话组装与压缩互不阻塞。
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

// AppendMessage 追加一条消息到消息自身会话的窗口（会话键：群聊=GroupID，私聊="private:"+UserID）。
// 达到 WindowCap 上限时淘汰最旧消息并返回压缩触发信号。
func (w *Window) AppendMessage(ctx context.Context, msg entity.Message) (bool, error) {
	return w.AppendToSession(ctx, msg.SessionKey(), msg)
}

// AppendToSession 把消息追加到指定会话的窗口（显式会话键，不依赖消息自身 SessionKey）。
// 供 MemoryService.PersistMessageToSession 持久化 bot 自身回复：私聊下 bot 回复的作者是 botID，
// 其自身 SessionKey() 会派生 "private:"+botID 独立会话，必须显式并入触发消息（用户）的会话，
// bot 回复才进得了用户对话窗（见 event respond 与架构 §4.1 会话键约定）。
func (w *Window) AppendToSession(_ context.Context, sessionID string, msg entity.Message) (bool, error) {
	raw, _ := w.sessions.LoadOrStore(sessionID, &sessionWindow{})
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
// B-040 定案：深拷贝（Message 与 Parts 均复制）——调用方对返回切片任意读写都不影响窗口内部，
// 组装/压缩并发各持快照，无逃逸数组竞态。图片描述回填走 BackfillParts（锁内写回）。
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
	for i := range out {
		if buf[i].Parts == nil {
			continue
		}
		parts := make([]entity.ContentPart, len(buf[i].Parts))
		copy(parts, buf[i].Parts)
		out[i].Parts = parts
	}
	return out, nil
}

// BackfillParts 在窗口锁内把 messageID 对应消息的 Parts 更新为最新副本（含回填的图片描述，B-040）。
// **入窗前深拷贝**：窗口内部持有独立数组，不与该消息的调用方（describeMessage 的 GetWindow 深拷贝）
// 共享——调用方后续继续写 `Parts[j].Description`（循环内逐图描述）不会触碰窗口内部，杜绝逃逸数组竞态。
// 消息已被压缩移除/淘汰（未找到）时静默忽略——描述已持久化，不阻断；
// 空 messageID 直接忽略（与 describeMessage 的空 ID 边角一致，B-041）。
func (w *Window) BackfillParts(_ context.Context, sessionID, messageID string, parts []entity.ContentPart) {
	if messageID == "" {
		return
	}
	v, ok := w.sessions.Load(sessionID)
	if !ok {
		return
	}
	sw := v.(*sessionWindow)
	sw.mu.Lock()
	defer sw.mu.Unlock()
	for i := range sw.data {
		if sw.data[i].MessageID == messageID {
			sw.data[i].Parts = append([]entity.ContentPart(nil), parts...)
			return
		}
	}
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
