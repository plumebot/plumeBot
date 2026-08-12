// Package onebot 封装 ZeroBot OneBot v11 连接层。
// 第二阶段实现：正向 WebSocket 连接 NapCat，接收原始事件 → 转换为 domain 实体 → 交给 handler。
package onebot

import (
	"context"
	"errors"

	log "github.com/sirupsen/logrus"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/driver"

	"plumebot/internal/domain"
	"plumebot/internal/handler"
	"plumebot/internal/infra/imagecache"
	"plumebot/pkg/config"
	"plumebot/pkg/logger"
)

// Client 是 ZeroBot 连接层的封装，负责连接 NapCat 并将事件分发给 handler。
type Client struct {
	cfg    config.OnebotConfig
	cache  *imagecache.Cache // base64:// 图片入链闭合；nil = 关闭
	msg    *handler.MessageHandler
	notice *handler.NoticeHandler
}

// New 创建 Client，注入消息/通知事件处理入口。
// logLevel 用于对齐 ZeroBot 内部 logrus 日志级别（debug|info|warn|error）。
// cache 用于 base64:// 图片入链闭合（可为 nil 关闭）。
// 空值兜底由本包负责：ws_url 为空时使用默认 NapCat 地址。
func New(cfg config.OnebotConfig, logLevel string, msg *handler.MessageHandler, notice *handler.NoticeHandler, cache *imagecache.Cache) *Client {
	if cfg.WsURL == "" {
		cfg.WsURL = config.DefaultWsURL
	}
	log.SetLevel(parseLogLevel(logLevel))
	return &Client{cfg: cfg, cache: cache, msg: msg, notice: notice}
}

// Run 注册事件分发并启动连接。ZeroBot 底层自动处理断线重连，本方法阻塞运行，不返回。
func (c *Client) Run() {
	c.registerMatchers()
	zero.RunAndBlock(&zero.Config{
		Driver: []zero.Driver{
			driver.NewWebSocketClient(c.cfg.WsURL, c.cfg.AccessToken),
		},
	}, nil)
}

// rateLimitedReply 是限流等待超时后回复的固定文案（暂不配置化）。
// sensitiveWordReply 是敏感词命中后回复的固定文案（暂不配置化）。
const (
	rateLimitedReply   = "消息太多了，等会再说吧"
	sensitiveWordReply = "我拒绝回答"
)

// registerMatchers 注册 ZeroBot 事件匹配器：事件 → domain 实体 → handler。
func (c *Client) registerMatchers() {
	zero.OnMessage().Handle(func(ctx *zero.Ctx) {
		msg, ok := toMessage(ctx.Event, c.cache)
		if !ok {
			logger.Warn("忽略不支持的 message 事件",
				logger.S("post_type", ctx.Event.PostType),
				logger.S("message_type", ctx.Event.MessageType),
			)
			return
		}
		// 消息日志统一由 service/event 日志中间件记录，这里不重复打 Info。
		if err := c.msg.Handle(context.Background(), msg); err != nil {
			if reply := decideReply(err); reply != "" {
				// ctx.Send 自动回事件来源（群回群、私聊回私聊），发送失败由 ZeroBot 内部记录。
				ctx.Send(reply)
				return
			}
			logger.Warn("消息处理失败", logger.Err(err))
		}
	})

	// 通知事件（群增减/戳一戳/禁言等）：转换后交给 notice handler，不走消息中间件链。
	zero.OnNotice().Handle(func(ctx *zero.Ctx) {
		c.dispatchEvent(ctx)
	})
	// 请求事件（好友申请/加群申请）：与通知事件共用分发，当前统一为记录日志 + notice handler stub。
	zero.OnRequest().Handle(func(ctx *zero.Ctx) {
		c.dispatchEvent(ctx)
	})
	// 元事件（心跳/生命周期）：仅记录 Debug 日志，不进入业务管线。
	zero.OnMetaEvent().Handle(func(ctx *zero.Ctx) {
		logger.Debug("收到元事件",
			logger.S("meta_event_type", ctx.Event.RawEvent.Get("meta_event_type").String()),
		)
	})
}

// decideReply 根据管线返回的错误决定回复文案；不需要回复时返回空串。
// 拆成纯函数便于单测：限流/敏感词命中 → 固定文案，其余错误/无错误 → 不回复。
func decideReply(err error) string {
	if errors.Is(err, domain.ErrRateLimited) {
		return rateLimitedReply
	}
	if errors.Is(err, domain.ErrSensitiveWord) {
		return sensitiveWordReply
	}
	return ""
}

// dispatchEvent 转换并分发通知/请求事件。
func (c *Client) dispatchEvent(ctx *zero.Ctx) {
	evt, ok := toEvent(ctx.Event)
	if !ok {
		logger.Warn("忽略不支持的事件", logger.S("post_type", ctx.Event.PostType))
		return
	}
	logger.Info("收到通知事件",
		logger.S("type", string(evt.Type)),
		logger.S("sub_type", evt.SubType),
		logger.S("group_id", evt.GroupID),
		logger.S("user_id", evt.UserID),
	)
	if err := c.notice.Handle(context.Background(), evt); err != nil {
		logger.Warn("通知处理失败", logger.Err(err))
	}
}

// parseLogLevel 将配置的日志级别映射为 logrus 级别，无效值降级为 info。
func parseLogLevel(s string) log.Level {
	switch s {
	case "debug":
		return log.DebugLevel
	case "warn":
		return log.WarnLevel
	case "error":
		return log.ErrorLevel
	default:
		return log.InfoLevel
	}
}
