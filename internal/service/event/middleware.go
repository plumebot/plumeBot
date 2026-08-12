package event

import (
	"context"

	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// Handler 是消息管线的处理函数签名：接收一条消息，返回错误表示管线失败。
// 中间件拦截时返回领域哨兵（如 domain.ErrRateLimited），
// 由连接层通过 errors.Is 识别后按约定处理，避免上层重复记录日志。
type Handler func(ctx context.Context, msg entity.Message) error

// Middleware 是消息管线中间件：包装下一个 Handler。
type Middleware func(next Handler) Handler

// chain 按给定顺序组合中间件：第一个中间件最先执行。
func chain(mws []Middleware, final Handler) Handler {
	h := final
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// logMiddleware 记录每条消息（含后续被限流丢弃的）。
// 消息日志统一由本中间件输出，连接层不再重复记录。
func logMiddleware(next Handler) Handler {
	return func(ctx context.Context, msg entity.Message) error {
		logger.Info("收到消息",
			logger.S("message_id", msg.MessageID),
			logger.S("group_id", msg.GroupID),
			logger.S("user_id", msg.UserID),
			logger.S("message_type", msg.MessageType),
			logger.S("content", msg.Render()),
		)
		return next(ctx, msg)
	}
}
