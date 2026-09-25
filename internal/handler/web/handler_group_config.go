package web

// GroupConfigHandler 群配置域 handler（P7-001）：每群的触发/状态配置 CRUD。
// group_id 由 path 提供；body 为 dto/request.GroupConfig。
// configured=false 仅表示该群「尚未被 bot 触达」——群首次被触达时 service/control 会按
// 当时的全局生效值自动建行（见 admin-web-api-plan.md §7.2），此后 configured 恒 true。

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/handler/web/dto/request"
	"plumebot/internal/handler/web/dto/response"
)

// GroupConfigHandler 群配置域 handler 集。
type GroupConfigHandler struct {
	svc domain.Admin
}

func newGroupConfigHandler(svc domain.Admin) *GroupConfigHandler {
	return &GroupConfigHandler{svc: svc}
}

// RegisterRoutes 挂载群配置路由（挂载到已鉴权路由组）。
func (h *GroupConfigHandler) RegisterRoutes(g *gin.RouterGroup) {
	g.GET("/groups/configs", h.list)
	g.GET("/groups/:group_id/config", h.get)
	g.PUT("/groups/:group_id/config", h.upsert)
	g.DELETE("/groups/:group_id/config", h.delete)
}

// list 所有已配置群（含 bot 触达时自动建行的群；从未被触达的群不出现）。
func (h *GroupConfigHandler) list(c *gin.Context) {
	items, err := h.svc.ListGroupConfigs(c.Request.Context())
	if err != nil {
		handleError(c, err)
		return
	}
	out := make([]response.GroupConfig, 0, len(items))
	for _, it := range items {
		out = append(out, toGroupConfigDTO(it, true))
	}
	ok(c, response.Items[response.GroupConfig]{Items: out})
}

// toGroupConfigDTO 把 entity.GroupConfig 映射为出参 dto（json 形态归 dto 层，见 response.GroupConfig）。
func toGroupConfigDTO(cfg entity.GroupConfig, configured bool) response.GroupConfig {
	return response.GroupConfig{
		GroupID: cfg.GroupID, Mode: cfg.Mode,
		EnergyMax: cfg.EnergyMax, EnergyCost: cfg.EnergyCost, EnergyRecover: cfg.EnergyRecover,
		EnergyThreshold: cfg.EnergyThreshold, CooldownSeconds: cfg.CooldownSeconds,
		ConsecutiveLimit: cfg.ConsecutiveLimit, RestSeconds: cfg.RestSeconds,
		QuietHoursStart: cfg.QuietHoursStart, QuietHoursEnd: cfg.QuietHoursEnd,
		ShortMessageChars: cfg.ShortMessageChars, GroupMgmtEnabled: cfg.GroupMgmtEnabled,
		Configured: configured,
	}
}

// get 单群配置；无配置行（该群尚未被 bot 触达）返回 200 + 零值 + configured:false（非 404）。
func (h *GroupConfigHandler) get(c *gin.Context) {
	cfg, err := h.svc.GetGroupConfig(c.Request.Context(), c.Param("group_id"))
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			ok(c, response.GroupConfig{Configured: false})
			return
		}
		handleError(c, err)
		return
	}
	ok(c, toGroupConfigDTO(*cfg, true))
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
	ok(c, toGroupConfigDTO(*got, true))
}

// delete 删除单群配置 → 恢复全局兜底。注意非持久：该群下次被 bot 触达时会重新自动建行（按当时的全局值）。
func (h *GroupConfigHandler) delete(c *gin.Context) {
	if err := h.svc.DeleteGroupConfig(c.Request.Context(), c.GetString(ctxKeyUsername), c.Param("group_id")); err != nil {
		handleError(c, err)
		return
	}
	ok(c, nil)
}
