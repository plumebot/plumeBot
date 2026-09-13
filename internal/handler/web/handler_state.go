package web

// StateHandler 运行态域 handler（P7-001）：会话运行态只读查看（精力/冷却/连续/rest）。
// 不提供任何写端点——只读性由接口层面保证。

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/internal/domain"
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
// 会话键输入来自手填，缺省走 handleError 的「目标不存在」无法定位问题，这里给出
// 该会话无运行态的可读文案（运行态在 bot 于该会话产生 auto 回复后才首次落库）。
func (h *StateHandler) get(c *gin.Context) {
	key := c.Param("session_key")
	st, err := h.svc.GetBotState(c.Request.Context(), key)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			fail(c, http.StatusNotFound, response.CodeNotFound, "该会话暂无运行态（键 "+key+"）")
			return
		}
		handleError(c, err)
		return
	}
	ok(c, response.BotState{GroupID: st.GroupID, State: json.RawMessage(st.State)})
}