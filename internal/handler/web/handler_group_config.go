package web

// GroupConfigHandler 群配置域 handler（P7-001）：每群的触发/状态配置 CRUD。
// group_id 由 path 提供；body 为 dto/request.GroupConfig；configured=false = 走全局兜底。

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/handler/web/dto/request"
	"plumebot/internal/handler/web/dto/response"
	"plumebot/internal/service/admin"
)

// GroupConfigHandler 群配置域 handler 集。
type GroupConfigHandler struct {
	svc *admin.Service
}

func newGroupConfigHandler(svc *admin.Service) *GroupConfigHandler {
	return &GroupConfigHandler{svc: svc}
}

// RegisterRoutes 挂载群配置路由（挂载到已鉴权路由组）。
func (h *GroupConfigHandler) RegisterRoutes(g *gin.RouterGroup) {
	g.GET("/groups/configs", h.list)
	g.GET("/groups/:group_id/config", h.get)
	g.PUT("/groups/:group_id/config", h.upsert)
	g.DELETE("/groups/:group_id/config", h.delete)
}

// list 所有已配置群（未配置群不出现）。
func (h *GroupConfigHandler) list(c *gin.Context) {
	items, err := h.svc.ListGroupConfigs(c.Request.Context())
	if err != nil {
		handleError(c, err)
		return
	}
	out := make([]response.GroupConfig, 0, len(items))
	for _, it := range items {
		out = append(out, response.GroupConfig{GroupConfig: it, Configured: true})
	}
	ok(c, response.Items[response.GroupConfig]{Items: out})
}

// get 单群配置；未配置行返回 200 + 零值 + configured:false（非 404）。
func (h *GroupConfigHandler) get(c *gin.Context) {
	cfg, err := h.svc.GetGroupConfig(c.Request.Context(), c.Param("group_id"))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			ok(c, response.GroupConfig{Configured: false})
			return
		}
		handleError(c, err)
		return
	}
	ok(c, response.GroupConfig{GroupConfig: *cfg, Configured: true})
}

// upsert 整行写单群配置（0/空列 = 走全局）。
func (h *GroupConfigHandler) upsert(c *gin.Context) {
	var req request.GroupConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, response.CodeBadRequest, "请求体格式错误")
		return
	}
	cfg := entity.GroupConfig{
		GroupID: c.Param("group_id"), Mode: req.Mode, EnergyMax: req.EnergyMax,
		EnergyCost: req.EnergyCost, EnergyRecover: req.EnergyRecover, EnergyThreshold: req.EnergyThreshold,
		CooldownSeconds: req.CooldownSeconds, ConsecutiveLimit: req.ConsecutiveLimit, RestSeconds: req.RestSeconds,
		QuietHoursStart: req.QuietHoursStart, QuietHoursEnd: req.QuietHoursEnd,
		ShortMessageChars: req.ShortMessageChars, GroupMgmtEnabled: req.GroupMgmtEnabled,
	}
	got, err := h.svc.UpsertGroupConfig(c.Request.Context(), c.GetString(ctxKeyUsername), cfg)
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, response.GroupConfig{GroupConfig: *got, Configured: true})
}

// delete 删除单群配置 → 恢复全局兜底。
func (h *GroupConfigHandler) delete(c *gin.Context) {
	if err := h.svc.DeleteGroupConfig(c.Request.Context(), c.GetString(ctxKeyUsername), c.Param("group_id")); err != nil {
		handleError(c, err)
		return
	}
	ok(c, nil)
}