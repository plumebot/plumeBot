package web

// GroupProfileHandler 群画像域 handler（P7-001）：读/写/删群画像。
// 管理面写/删后 service 侧自动失效缓存（下一条群消息重新加载）。

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/handler/web/dto/request"
	"plumebot/internal/handler/web/dto/response"
)

// GroupProfileHandler 群画像域 handler 集。
type GroupProfileHandler struct {
	svc domain.Admin
}

func newGroupProfileHandler(svc domain.Admin) *GroupProfileHandler {
	return &GroupProfileHandler{svc: svc}
}

// RegisterRoutes 挂载群画像路由（挂载到已鉴权路由组）。
func (h *GroupProfileHandler) RegisterRoutes(g *gin.RouterGroup) {
	g.GET("/groups/:group_id/profile", h.get)
	g.PUT("/groups/:group_id/profile", h.upsert)
	g.DELETE("/groups/:group_id/profile", h.delete)
}

// get 查询群画像；无画像返回 200 + 零值 + configured:false（非 404）。
func (h *GroupProfileHandler) get(c *gin.Context) {
	p, err := h.svc.GetGroupProfile(c.Request.Context(), c.Param("group_id"))
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			ok(c, response.GroupProfile{Configured: false})
			return
		}
		handleError(c, err)
		return
	}
	ok(c, toProfileDTO(*p, true))
}

// upsert 写群画像（写后 service 失效缓存）。
func (h *GroupProfileHandler) upsert(c *gin.Context) {
	var req request.GroupProfile
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, response.CodeBadRequest, "请求体格式错误")
		return
	}
	got, err := h.svc.UpsertGroupProfile(c.Request.Context(), c.GetString(ctxKeyUsername),
		entity.GroupProfile{
			GroupID: c.Param("group_id"), Culture: req.Culture, Topics: req.Topics,
			ActiveHours: req.ActiveHours, Rules: req.Rules, Atmosphere: req.Atmosphere,
		})
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, toProfileDTO(*got, true))
}

// delete 删除群画像（恢复无画像态；不存在 4041）。
func (h *GroupProfileHandler) delete(c *gin.Context) {
	if err := h.svc.DeleteGroupProfile(c.Request.Context(), c.GetString(ctxKeyUsername), c.Param("group_id")); err != nil {
		handleError(c, err)
		return
	}
	ok(c, nil)
}

func toProfileDTO(p entity.GroupProfile, configured bool) response.GroupProfile {
	return response.GroupProfile{
		Culture: p.Culture, Topics: p.Topics, ActiveHours: p.ActiveHours,
		Rules: p.Rules, Atmosphere: p.Atmosphere, Configured: configured,
	}
}