package admin

// 会话窗口域（P7-003）：当前内存上下文窗口的只读查看（管理前端「对话历史」栏目）。
// 本文件不提供任何写方法——只读性由接口层面保证；删除语义（仅内存窗口 or 连 SQLite 影响记忆）
// 未定案，记入 roadmap B 台账（来源=本任务，处理时机=用户拍板后）。

import (
	"context"
	"strings"

	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// ListSessions 返回当前持有活跃窗口的全部会话概览，按会话键升序（窗口实现已排序，稳定输出）。
// 单个会话读取失败仅跳过（窗口为内存运行态，不因个别会话失败阻断整体列表）。
func (s *Service) ListSessions(ctx context.Context) []entity.SessionOverview {
	keys := s.win.ListSessions()
	out := make([]entity.SessionOverview, 0, len(keys))
	for _, key := range keys {
		msgs, err := s.win.GetWindow(ctx, key)
		if err != nil {
			logger.From(ctx).Warn("会话概览读取失败", logger.S("session", key), logger.Err(err))
			continue
		}
		ov := entity.SessionOverview{Key: key, Count: len(msgs)}
		if n := len(msgs); n > 0 {
			ov.LastTS = msgs[n-1].Timestamp
			ov.LastRender = msgs[n-1].ForLLM()
		}
		out = append(out, ov)
	}
	return out
}

// GetSessionWindow 返回会话窗口内消息的只读视图（时间正序）。
// 会话不存在/窗口为空返回空切片（非错误），前端据此渲染空态提示。
func (s *Service) GetSessionWindow(ctx context.Context, sessionKey string) []entity.SessionMessage {
	msgs, err := s.win.GetWindow(ctx, sessionKey)
	if err != nil {
		logger.From(ctx).Warn("会话窗口读取失败", logger.S("session", sessionKey), logger.Err(err))
		return []entity.SessionMessage{}
	}
	out := make([]entity.SessionMessage, 0, len(msgs))
	for _, m := range msgs {
		sender := m.SenderName
		if sender == "" {
			sender = m.UserID
		}
		out = append(out, entity.SessionMessage{
			MessageID: m.MessageID,
			Sender:    sender,
			SendAt:    m.Timestamp,
			Self:      isBotReply(m.MessageID),
			Render:    m.ForLLM(), // ForLLM 视图：已回填描述的图片显示「（图片：描述）」，未描述回落 [图片]
		})
	}
	return out
}

// isBotReply 识别 bot 自身回复：合成 MessageID 前缀 "self:"（P6-002 botReplyMessage 约定）。
// 判断藏在服务侧，前端无需感知内部消息 ID 契约。
func isBotReply(messageID string) bool {
	return strings.HasPrefix(messageID, "self:")
}