package event

import (
	"context"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/ahocorasick"
	"plumebot/pkg/logger"
)

// sensitiveWordFilter 基于 AC 自动机做敏感词过滤，构建后只读、可并发使用。
type sensitiveWordFilter struct {
	matcher *ahocorasick.Matcher
}

// newSensitiveWordFilter 创建过滤器。空词表 = 不过滤（永远放行）。
func newSensitiveWordFilter(words []string) *sensitiveWordFilter {
	return &sensitiveWordFilter{matcher: ahocorasick.New(words)}
}

// sensitiveWordMiddleware 是敏感词过滤中间件：
// 命中 → 结局行 sensitive（含命中词）→ 返回 *domain.SensitiveWordError，
// 由连接层识别后回复固定文案；未命中 → 放行给 next。
func sensitiveWordMiddleware(filter *sensitiveWordFilter) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, msg entity.Message) error {
			word, ok := filter.matcher.Find(msg.PlainText())
			if !ok {
				return next(ctx, msg)
			}
			logOutcome(msg, OutcomeSensitive, logger.S("word", word))
			return &domain.SensitiveWordError{Word: word}
		}
	}
}
