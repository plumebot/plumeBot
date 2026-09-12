package web

// StateHandler 运行态域 handler（P7-001）：会话运行态只读查看（精力/冷却/连续/rest）。
// 不提供任何写端点——只读性由接口层面保证。

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"plumebot/internal/handler/web/dto/response"
	"plumebot/internal/service/admin"
)

// StateHandler 运行态域 handler 集。
type StateHandler struct {
	svc *admin.Service
}

func newStateHandler(svc *admin.Service) *StateHandler {
	return &StateHandler{svc: svc}
}

// RegisterRoutes 挂载运行态路由（挂载到已鉴权路由组）。
func (h *StateHandler) RegisterRoutes(g *gin.RouterGroup) {
	g.GET("/sessions/:session_key/state", h.get)
}

// get 查询会话运行态；不存在 4041。
func (h *StateHandler) get(c *gin.Context) {
	st, err := h.svc.GetBotState(c.Request.Context(), c.Param("session_key"))
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, response.BotState{GroupID: st.GroupID, State: json.RawMessage(st.State)})
}