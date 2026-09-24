package web

// LogHandler 日志浏览域 handler（架构 §17.6）：把 entity.LogQuery 的查询条件暴露为
// 单一 GET /api/v1/logs 端点（等级 × 时间段 × trace_id + 分页）。
// 依赖 service/log（logsvc），不依赖 admin.Service——日志读取与配置管理是两条独立链路。

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"plumebot/internal/domain/entity"
	"plumebot/internal/handler/web/dto/response"
	logsvc "plumebot/internal/service/log"
)

// LogHandler 日志浏览域 handler 集。
type LogHandler struct {
	svc *logsvc.Service
}

// newLogHandler 创建日志域 handler。
func newLogHandler(svc *logsvc.Service) *LogHandler {
	return &LogHandler{svc: svc}
}

// RegisterRoutes 挂载日志路由（挂载到已鉴权路由组）。
func (h *LogHandler) RegisterRoutes(g *gin.RouterGroup) {
	g.GET("/logs", h.list)
}

// logLevelParams 是 levels 参数允许的取值（error 语义含 fatal，映射在 infra 实现内）。
var logLevelParams = map[string]entity.LogLevel{
	string(entity.LogLevelDebug): entity.LogLevelDebug,
	string(entity.LogLevelInfo):  entity.LogLevelInfo,
	string(entity.LogLevelWarn):  entity.LogLevelWarn,
	string(entity.LogLevelError): entity.LogLevelError,
}

// list 查询日志：levels（csv，缺省=全部）、trace_id、begin/end（RFC3339）、limit、offset。
// 参数非法一律 400（不静默忽略，避免前端以为筛选生效却看到全量结果）。
func (h *LogHandler) list(c *gin.Context) {
	q, err := parseLogQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	page, err := h.svc.Query(c.Request.Context(), q)
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, response.LogPage{Items: toLogEntryDTOs(page.Items), HasMore: page.HasMore})
}

// toLogEntryDTOs 把 entity.LogEntry 映射为出参 dto（json 形态归 dto 层，见 response.LogEntry）。
func toLogEntryDTOs(items []entity.LogEntry) []response.LogEntry {
	out := make([]response.LogEntry, 0, len(items))
	for _, it := range items {
		out = append(out, response.LogEntry{
			TS: it.TS, Level: it.Level, Message: it.Message, TraceID: it.TraceID, Fields: it.Fields,
		})
	}
	return out
}

// parseLogQuery 解析并校验查询参数。
func parseLogQuery(c *gin.Context) (entity.LogQuery, error) {
	q := entity.LogQuery{TraceID: c.Query("trace_id")}

	if raw := strings.TrimSpace(c.Query("levels")); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			lv, ok := logLevelParams[s]
			if !ok {
				return q, &paramError{"levels 取值非法（可选 debug/info/warn/error）：" + s}
			}
			q.Levels = append(q.Levels, lv)
		}
	}

	for name, dst := range map[string]**time.Time{"begin": &q.Begin, "end": &q.End} {
		raw := strings.TrimSpace(c.Query(name))
		if raw == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return q, &paramError{name + " 需为 RFC3339 时间（如 2026-09-21T10:00:00+08:00）"}
		}
		*dst = &t
	}
	if q.Begin != nil && q.End != nil && q.End.Before(*q.Begin) {
		return q, &paramError{"end 不能早于 begin"}
	}

	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return q, &paramError{"limit 需为非负整数"}
		}
		q.Limit = n
	}
	if raw := strings.TrimSpace(c.Query("offset")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return q, &paramError{"offset 需为非负整数"}
		}
		q.Offset = n
	}
	return q, nil
}

// paramError 是查询参数校验错误（文案直接回给前端）。
type paramError struct{ msg string }

func (e *paramError) Error() string { return e.msg }
