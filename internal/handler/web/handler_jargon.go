package web

// JargonHandler 黑话域 handler（P7-001）：按群列出（status 过滤）、添加（默认 confirmed）、
// 删除、确认。文本一律走 body（path 只放 group_id，避免中文/特殊字符进 URL（计划书 §7.5 注）。

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/internal/handler/web/dto/request"
	"plumebot/internal/handler/web/dto/response"
	"plumebot/internal/service/admin"
)

// JargonHandler 黑话域 handler 集。
type JargonHandler struct {
	svc *admin.Service
}

func newJargonHandler(svc *admin.Service) *JargonHandler {
	return &JargonHandler{svc: svc}
}

// RegisterRoutes 挂载黑话路由（挂载到已鉴权路由组）。
func (h *JargonHandler) RegisterRoutes(g *gin.RouterGroup) {
	g.GET("/groups/:group_id/jargons", h.list)
	g.POST("/groups/:group_id/jargons", h.add)
	g.DELETE("/groups/:group_id/jargons", h.delete)
	g.POST("/groups/:group_id/jargons/confirm", h.confirm)
}

// list 按群列出黑话；?status=all|pending|confirmed，缺省 all。
func (h *JargonHandler) list(c *gin.Context) {
	items, err := h.svc.ListJargonWithStatus(c.Request.Context(), c.Param("group_id"), c.Query("status"))
	if err != nil {
		handleError(c, err)
		return
	}
	out := make([]response.Jargon, 0, len(items))
	for _, it := range items {
		out = append(out, response.Jargon{Jargon: it.Jargon, Status: it.Status})
	}
	ok(c, response.Items[response.Jargon]{Items: out})
}

// add 管理面添加黑话（默认 confirmed，人工添加即人工认可）。
func (h *JargonHandler) add(c *gin.Context) {
	var req request.Jargon
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, response.CodeBadRequest, "请求体格式错误")
		return
	}
	got, err := h.svc.AddJargon(c.Request.Context(), c.GetString(ctxKeyUsername), c.Param("group_id"), req.Jargon)
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, response.Jargon{Jargon: got.Jargon, Status: got.Status})
}

// delete 删除黑话（体取 jargon；不存在 4041，撤销错误学习/错误确认）。
func (h *JargonHandler) delete(c *gin.Context) {
	var req request.Jargon
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, response.CodeBadRequest, "请求体格式错误")
		return
	}
	if err := h.svc.DeleteJargon(c.Request.Context(), c.GetString(ctxKeyUsername), c.Param("group_id"), req.Jargon); err != nil {
		handleError(c, err)
		return
	}
	ok(c, nil)
}

// confirm 审核确认 pending → confirmed（体取 jargon）。
func (h *JargonHandler) confirm(c *gin.Context) {
	var req request.Jargon
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, response.CodeBadRequest, "请求体格式错误")
		return
	}
	got, err := h.svc.ConfirmJargon(c.Request.Context(), c.GetString(ctxKeyUsername), c.Param("group_id"), req.Jargon)
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, response.Jargon{Jargon: got.Jargon, Status: got.Status})
}