// Package logsvc 是日志浏览的编排层（架构 §17.6）：对 domain.LogReader 的薄封装，
// 负责查询参数归一（limit 缺省/上限、offset 负数），供 handler/web 调用。
// 包名用 logsvc 而非 log，避免与标准库 log 混淆（目录名 service/log 不变）。
package logsvc

import (
	"context"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
)

const (
	// defaultLimit 是未指定 limit 时的默认返回条数。
	defaultLimit = 200
	// maxLimit 是单次查询条数上限（防一次拉爆响应体与前端渲染）。
	maxLimit = 1000
)

// Service 提供日志查询服务（依赖 domain.LogReader，实现经 main 注入 infra/logfile）。
type Service struct {
	reader domain.LogReader
}

// New 创建日志查询 service。
func New(reader domain.LogReader) *Service {
	return &Service{reader: reader}
}

// Query 归一查询参数后转调读取实现：
// limit ≤0 → 200、>1000 → 1000；offset <0 → 0。其余条件原样透传。
func (s *Service) Query(ctx context.Context, q entity.LogQuery) (entity.LogPage, error) {
	if q.Limit <= 0 {
		q.Limit = defaultLimit
	}
	if q.Limit > maxLimit {
		q.Limit = maxLimit
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return s.reader.Query(ctx, q)
}
