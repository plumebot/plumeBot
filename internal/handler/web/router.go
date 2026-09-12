package web

// handler/web 层职责划分（P7-001）：
//   - router.go          引擎装配 + 路由挂载（薄；不做业务）
//   - handler_auth.go    认证域 handler（注册/登录/改密/me/status，含本域路由声明）
//   - handler_resource.go 配置域 handler（子任务④面：群配置/人格/画像/黑话/成员事实/运行态）
//   - middleware.go      中间件：verifyAuth（JWT 鉴权）+ ipLimiter（每 IP 限流）
//   - response.go        响应编排 helper：ok / fail / handleError + ctx Key
//   - dto/request        与前端交互的请求体结构（含包络与业务码在 dto/response）

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/internal/service/admin"
	"plumebot/pkg/jwt"
)

// NewRouter 构建管理后端 gin 路由。
//   - 免鉴权：GET /ping（存活探针，admin 启用时与 API 同服）；
//   - /api/v1：由各职责域 handler 的 RegisterRoutes 挂载（认证域先挂，
//     配置域子任务④挂）；免鉴权路由在其域内声明，其余统一经 verifyAuth（4011）。
//
// 子任务④在此基础上挂配置域路由与前端静态页（/ 与 /static/*，go:embed）。
func NewRouter(svc *admin.Service, mgr *jwt.Manager) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	api := r.Group("/api/v1")
	newAuthHandler(svc).RegisterRoutes(api, mgr)
	// 子任务④：配置域路由 newResourceHandler(svc).RegisterRoutes(api)。

	return r
}