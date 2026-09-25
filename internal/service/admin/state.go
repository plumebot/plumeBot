package admin

// bot_state 域：运行态只读查看（精力/冷却/连续/rest，计划书 §7.7 定案只读端点纳入）。
// 本文件不提供任何写方法——只读性由接口层面保证，杜绝误改运行态。

import (
	"context"

	"plumebot/internal/domain/entity"
)

// GetBotState 按会话键查询运行态；不存在返回 entity.ErrNotFound。
func (s *Service) GetBotState(ctx context.Context, sessionKey string) (*entity.BotState, error) {
	return s.store.GetBotState(ctx, sessionKey)
}