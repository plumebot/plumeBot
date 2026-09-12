package web

// AuthHandler 认证域 handler（P7-001）：注册 / 登录 / 改密 / 当前用户 / 注册状态。
// 按职责域拆分 handler 对象——认证路由与配置域路由（handler_resource.go）互不耦合；
// 每个 handler 只持本域所需依赖（admin.Service + 认证限流器），请求/响应结构来自
// dto/request 与 dto/response，本文件不内联定义。

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/internal/handler/web/dto/request"
	"plumebot/internal/handler/web/dto/response"
	"plumebot/internal/service/admin"
	"plumebot/pkg/jwt"
)

// AuthHandler 认证域 handler 集。
type AuthHandler struct {
	svc     *admin.Service
	limiter *ipLimiter // 注册/登录每 IP 限流（防爆破）
}

// newAuthHandler 创建认证域 handler。
func newAuthHandler(svc *admin.Service) *AuthHandler {
	return &AuthHandler{svc: svc, limiter: newIPLimiter(authRateLimit, authBurst)}
}

// RegisterRoutes 挂载认证域路由：
//   - 免鉴权：register/login（每 IP 限流）、status（前端判表单）；
//   - 已鉴权：password（改密）、me（当前用户）。
func (h *AuthHandler) RegisterRoutes(g *gin.RouterGroup, mgr *jwt.Manager) {
	g.POST("/auth/register", h.limiter.middleware(), h.register)
	g.POST("/auth/login", h.limiter.middleware(), h.login)
	g.GET("/auth/status", h.authStatus)

	authed := g.Group("", verifyAuth(mgr))
	authed.PUT("/auth/password", h.changePassword)
	authed.GET("/auth/me", h.me)
}

// register 首次注册（空表单可用，创建后关闭；成功后直接签发 token 免二次登录）。
func (h *AuthHandler) register(c *gin.Context) {
	var req request.Auth
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, response.CodeBadRequest, "请求体格式错误")
		return
	}
	res, err := h.svc.Register(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, response.Auth{Token: res.Token, TokenType: res.TokenType, ExpiresAt: res.ExpiresAt})
}

// login 登录（失败统一文案防探测）。
func (h *AuthHandler) login(c *gin.Context) {
	var req request.Auth
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, response.CodeBadRequest, "请求体格式错误")
		return
	}
	res, err := h.svc.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, response.Auth{Token: res.Token, TokenType: res.TokenType, ExpiresAt: res.ExpiresAt})
}

// changePassword 修改密码（已鉴权，操作者是 JWT claims 里的用户名）。
func (h *AuthHandler) changePassword(c *gin.Context) {
	var req request.ChangePassword
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, response.CodeBadRequest, "请求体格式错误")
		return
	}
	username := c.GetString(ctxKeyUsername)
	if err := h.svc.ChangePassword(c.Request.Context(), username, req.OldPassword, req.NewPassword); err != nil {
		handleError(c, err)
		return
	}
	ok(c, nil)
}

// me 返回当前登录人（前端刷新校验用）。
func (h *AuthHandler) me(c *gin.Context) {
	username := c.GetString(ctxKeyUsername)
	ok(c, response.Me{Username: username})
}

// authStatus 是否已有管理员（免鉴权，首访前端据此显示注册还是登录表单）。
func (h *AuthHandler) authStatus(c *gin.Context) {
	registered, err := h.svc.AuthStatus(c.Request.Context())
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, response.Status{Registered: registered})
}