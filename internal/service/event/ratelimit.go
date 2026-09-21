package event

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
	"plumebot/pkg/logger"
)

// rateLimiter 按会话（群聊=群ID，私聊=用户ID）维护独立令牌桶。
// 注册表懒创建且不做淘汰清理：会话数增长有限（活跃群/用户），
// 后续若需要可引入 LRU 上限或空闲过期（当前阶段明确不做）。
type rateLimiter struct {
	mu    sync.Mutex
	cfg   config.RateLimitConfig
	limit map[string]*rate.Limiter
}

// newRateLimiter 创建限流器。空值/非法值兜底由本包负责：
// rate<=0 → 2/s、burst<=0 → 20、max_wait<=0 → 10s。
func newRateLimiter(cfg config.RateLimitConfig) *rateLimiter {
	if cfg.Rate <= 0 {
		cfg.Rate = 2
	}
	if cfg.Burst <= 0 {
		cfg.Burst = 20
	}
	if cfg.MaxWaitSeconds <= 0 {
		cfg.MaxWaitSeconds = 10
	}
	return &rateLimiter{
		cfg:   cfg,
		limit: make(map[string]*rate.Limiter),
	}
}

// wait 为 msg 排队等待令牌。超时返回 domain.ErrRateLimited（已记结局行 rate_limited）。
func (r *rateLimiter) wait(ctx context.Context, msg entity.Message) error {
	key := msg.SessionKey()
	scope := "group"
	if msg.MessageType != "group" {
		scope = "user" // 私聊按用户分桶（SessionKey() = "private:"+UserID，群聊 = GroupID）
	}

	r.mu.Lock()
	lim, ok := r.limit[key]
	if !ok {
		lim = rate.NewLimiter(rate.Limit(r.cfg.Rate), r.cfg.Burst)
		r.limit[key] = lim
	}
	r.mu.Unlock()

	timeout := time.Duration(r.cfg.MaxWaitSeconds) * time.Second
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := lim.Wait(waitCtx); err != nil {
		// 上游 context 已取消：原样上抛，不按限流丢弃。
		// 否则视为限流超时（含 x/time/rate 预判超时直接返回错误的情况）。
		if ctx.Err() != nil {
			return err
		}
		logOutcome(msg, OutcomeRateLimited,
			logger.S("scope", scope), logger.S("max_wait", fmt.Sprintf("%ds", r.cfg.MaxWaitSeconds)))
		return domain.ErrRateLimited
	}
	return nil
}

// rateLimitMiddleware 为消息排队获取令牌：突发（burst 内）直通，
// 超出后排队等待，超时则拦截并返回 domain.ErrRateLimited。
func rateLimitMiddleware(rl *rateLimiter) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, msg entity.Message) error {
			if err := rl.wait(ctx, msg); err != nil {
				return err
			}
			return next(ctx, msg)
		}
	}
}
