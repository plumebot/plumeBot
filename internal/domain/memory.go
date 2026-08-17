package domain

import (
	"context"

	"plumebot/internal/domain/entity"
)

// Memory 管理短期上下文窗口：追加消息、获取窗口、移除被压缩的批次。
type Memory interface {
	// AppendToSession 追加一条消息到指定会话窗口（显式会话键，不依赖消息自身 SessionKey）。
	// 私聊 bot 自身回复的作者与用户不同，需显式并入触发消息的会话（见 MemoryService.PersistMessageToSession）。
	AppendToSession(ctx context.Context, sessionID string, msg entity.Message) (full bool, err error)
	// GetWindow 返回会话窗口内消息（会话键：群聊=GroupID，私聊="private:"+UserID）。
	GetWindow(ctx context.Context, sessionID string) ([]entity.Message, error)
	// RemoveByIDs 从会话窗口精确移除指定 MessageID 的消息（压缩批次归档后调用），返回实际移除数量。
	RemoveByIDs(ctx context.Context, sessionID string, ids []string) (int, error)
}
