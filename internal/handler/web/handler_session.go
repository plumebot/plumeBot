package web

// SessionHandler 会话窗口域 handler（P7-003）：活跃会话列表 + 窗口只读查看（对话历史）。
// 仅 GET；只读端点不进写操作审计（与 bot_state 只读一致）。会话键形如 群号 / private:QQ。

import (
	"github.com/gin-gonic/gin"

	"plumebot/internal/handler/web/dto/response"
	"plumebot/internal/service/admin"
)

// SessionHandler 会话窗口域 handler 集。
type SessionHandler struct {
	svc *admin.Service
}

func newSessionHandler(svc *admin.Service) *SessionHandler {
	return &SessionHandler{svc: svc}
}

// RegisterRoutes 挂载会话窗口路由（挂载到已鉴权路由组）。
func (h *SessionHandler) RegisterRoutes(g *gin.RouterGroup) {
	g.GET("/sessions", h.list)
	g.GET("/sessions/:session_key/window", h.window)
}

// list 返回活跃会话概览（会话键 / 条数 / 最近消息时间与文本）。无活跃会话返回空 items。
func (h *SessionHandler) list(c *gin.Context) {
	ovs := h.svc.ListSessions(c.Request.Context())
	items := make([]response.SessionOverview, 0, len(ovs))
	for _, o := range ovs {
		items = append(items, response.SessionOverview{
			Key: o.Key, Count: o.Count, LastTS: o.LastTS, LastRender: o.LastRender,
		})
	}
	ok(c, response.Items[response.SessionOverview]{Items: items})
}

// window 返回指定会话窗口内消息（时间正序）。会话不存在/窗口为空返回空 items（非 404）。
func (h *SessionHandler) window(c *gin.Context) {
	msgs := h.svc.GetSessionWindow(c.Request.Context(), c.Param("session_key"))
	items := make([]response.SessionMessage, 0, len(msgs))
	for _, m := range msgs {
		items = append(items, response.SessionMessage{
			MessageID: m.MessageID, Sender: m.Sender, SendAt: m.SendAt, Self: m.Self, Render: m.Render,
		})
	}
	ok(c, response.Items[response.SessionMessage]{Items: items})
}