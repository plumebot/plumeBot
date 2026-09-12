package web

// PersonaHandler 人格域 handler（P7-001）：人格模板列表/按 agent 查询/按 agent 幂等 upsert。
// 改即生效（组装每次现查 persona 表，无需缓存失效）。

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/internal/domain/entity"
	"plumebot/internal/handler/web/dto/request"
	"plumebot/internal/handler/web/dto/response"
	"plumebot/internal/service/admin"
)

// PersonaHandler 人格域 handler 集。
type PersonaHandler struct {
	svc *admin.Service
}

func newPersonaHandler(svc *admin.Service) *PersonaHandler {
	return &PersonaHandler{svc: svc}
}

// RegisterRoutes 挂载人格路由（挂载到已鉴权路由组）。
func (h *PersonaHandler) RegisterRoutes(g *gin.RouterGroup) {
	g.GET("/personas", h.list)
	g.GET("/personas/:agent", h.get)
	g.PUT("/personas/:agent", h.upsert)
}

// list 全部人格模板。
func (h *PersonaHandler) list(c *gin.Context) {
	items, err := h.svc.ListPersonas(c.Request.Context())
	if err != nil {
		handleError(c, err)
		return
	}
	out := make([]response.Persona, 0, len(items))
	for _, it := range items {
		out = append(out, toPersonaDTO(it))
	}
	ok(c, response.Items[response.Persona]{Items: out})
}

// get 按 agent 查询；不存在 4041。
func (h *PersonaHandler) get(c *gin.Context) {
	p, err := h.svc.GetPersonaByAgent(c.Request.Context(), c.Param("agent"))
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, toPersonaDTO(*p))
}

// upsert 按 agent 幂等写（存在更新、不存在插入）。
func (h *PersonaHandler) upsert(c *gin.Context) {
	var req request.Persona
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, response.CodeBadRequest, "请求体格式错误")
		return
	}
	got, err := h.svc.UpsertPersona(c.Request.Context(), c.GetString(ctxKeyUsername),
		entity.Persona{Agent: c.Param("agent"), Name: req.Name, SystemPrompt: req.SystemPrompt})
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, toPersonaDTO(*got))
}

func toPersonaDTO(p entity.Persona) response.Persona {
	return response.Persona{Agent: p.Agent, Name: p.Name, SystemPrompt: p.SystemPrompt}
}