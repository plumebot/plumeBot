// Package response 定义管理后端 API 响应 DTO（P7-001）。
// 与前端交互的出参结构（含响应包络与业务码）集中于此；handler 仅负责编排输出，不内联定义响应结构。
package response

// 业务码（响应包络 code 字段；0 成功，4001~5000 失败，见 docs/admin-web-api-plan.md §6）。
const (
	CodeOK           = 0
	CodeBadRequest   = 4001
	CodeUnauthorized = 4011
	CodeForbidden    = 4031
	CodeNotFound     = 4041
	CodeConflict     = 4091
	CodeInternal     = 5000
)

// Envelope 统一响应包络。HTTP 状态承担传输语义，code 承担业务码，message 供前端直接展示。
type Envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}