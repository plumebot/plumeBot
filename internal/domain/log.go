package domain

import (
	"context"

	"plumebot/internal/domain/entity"
)

// LogReader 读取 JSON 结构化日志文件（zap 按级别分文件的产物，含 lumberjack 滚动备份）。
// 查询条件与结果类型见 entity.LogQuery / entity.LogPage。
// 由 infra 实现；gin.log（文本格式）不在其语义范围内。
type LogReader interface {
	Query(ctx context.Context, q entity.LogQuery) (entity.LogPage, error)
}
