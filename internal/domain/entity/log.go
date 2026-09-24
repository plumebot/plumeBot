package entity

import "time"

// LogLevel 是日志等级枚举（架构 §17.3）。取值与 zap JSON 的 level 字段一致，
// 也是前端日志浏览的等级筛选键（fatal 在查询侧并入 error，见 LogQuery）。
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// LogQuery 是日志浏览页的查询条件（架构 §17.6）。零值字段表示不过滤。
type LogQuery struct {
	Levels  []LogLevel // 空 = 全部级别（LogLevelError 语义含 fatal，映射在 infra 实现内）
	TraceID string     // 空 = 不过滤（管理端=IP，QQ 会话=group:/private: 前缀键）
	Begin   *time.Time // 空 = 不限起点
	End     *time.Time // 空 = 不限终点
	Limit   int        // 返回条数上限（由 service 归一：缺省 200、上限 1000）
	Offset  int        // 跳过的最新条数（分页）
}

// LogEntry 是一条已解析的日志记录（zap JSON 行 → 结构化）。
// Fields 保留去除 ts/level/msg/trace_id 后的其余字段（caller/stacktrace/业务 KV），
// 供前端展开查看；值可能为任意 JSON 类型。
// 纯结构（无 json tag）——前端出参的 json 形态定义在 handler/web/dto/response（LogEntry）。
type LogEntry struct {
	TS      time.Time
	Level   string
	Message string
	TraceID string
	Fields  map[string]any
}

// LogPage 是一页日志查询结果：HasMore 表示本次未读完（还有更早的记录）。
// 纯结构（无 json tag）——json 形态见 handler/web/dto/response.LogPage。
type LogPage struct {
	Items   []LogEntry
	HasMore bool
}
