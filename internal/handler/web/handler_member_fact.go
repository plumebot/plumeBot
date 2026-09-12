package web

// MemberFactHandler 成员事实域 handler（P7-001）：查询/补记/删除单条（纠错主场景）。

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/internal/handler/web/dto/request"
	"plumebot/internal/handler/web/dto/response"
	"plumebot/internal/service/admin"
)

// MemberFactHandler 成员事实域 handler 集。
type MemberFactHandler struct {
	svc *admin.Service
}

func newMemberFactHandler(svc *admin.Service) *MemberFactHandler {
	return &MemberFactHandler{svc: svc}
}

// RegisterRoutes 挂载成员事实路由（挂载到已鉴权路由组）。
func (h *MemberFactHandler) RegisterRoutes(g *gin.RouterGroup) {
	g.GET("/member-facts", h.list)
	g.POST("/member-facts", h.add)
	g.DELETE("/member-facts", h.delete)
}

// list 按 group_id+user_id 查事实（私聊 group_id 空即查该用户私聊事实）。
func (h *MemberFactHandler) list(c *gin.Context) {
	items, err := h.svc.ListMemberFacts(c.Request.Context(), c.Query("group_id"), c.Query("user_id"))
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, response.Items[string]{Items: items})
}

// add 管理员补记/纠错一条事实。
func (h *MemberFactHandler) add(c *gin.Context) {
	var req request.MemberFact
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, response.CodeBadRequest, "请求体格式错误")
		return
	}
	if err := h.svc.AddMemberFact(c.Request.Context(), c.GetString(ctxKeyUsername), req.GroupID, req.UserID, req.Fact); err != nil {
		handleError(c, err)
		return
	}
	ok(c, response.MemberFact{Fact: req.Fact})
}

// delete 删除单条（query 取 group_id/user_id/fact；不存在 4041）。
func (h *MemberFactHandler) delete(c *gin.Context) {
	groupID, userID, fact := c.Query("group_id"), c.Query("user_id"), c.Query("fact")
	if err := h.svc.DeleteMemberFact(c.Request.Context(), c.GetString(ctxKeyUsername), groupID, userID, fact); err != nil {
		handleError(c, err)
		return
	}
	ok(c, nil)
}