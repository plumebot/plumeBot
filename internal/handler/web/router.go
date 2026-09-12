package web

// handler/web 层职责划分（P7-001）：
//   - router.go          引擎装配 + 路由挂载（薄；不做业务）
//   - handler_auth.go    认证域 handler（注册/登录/改密/me/status）
//   - handler_group_config.go / handler_persona.go / handler_group_profile.go /
//     handler_jargon.go / handler_member_fact.go / handler_state.go  配置域 handler
//   - middleware.go      中间件：verifyAuth（JWT 鉴权）+ ipLimiter（每 IP 限流）
//   - response.go        响应编排 helper：ok / fail / handleError + ctx Key
//   - dto/request        与前端交互的请求体结构（含包络与业务码在 dto/response）
//   - static/index.html  简易前端单页（go:embed，无构建链）

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/internal/service/admin"
	"plumebot/pkg/jwt"
)

// staticFS 内嵌前端静态资源（单页 HTML + 若有静态子资源）。
//
//go:embed static/index.html
var staticFS embed.FS

// NewRouter 构建管理后端 gin 路由。
//   - 免鉴权：GET /ping（存活探针）、GET / 与 /static/*（前端页）、认证域的 register/login/status；
//   - /api/v1 其余路由统一经 verifyAuth（4011）；各配置域路由由独立 Handler 的
//     RegisterRoutes 挂载（决策 D11：单一 web 包 + 每域一文件）。
//
// admin.enabled=false 时 main 侧不调用本函数（回退仅 /ping 的 newWebServer）。
func NewRouter(svc *admin.Service, mgr *jwt.Manager) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	// 存活探针 + 前端页。
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})
	homeHTML, err := staticFS.ReadFile("static/index.html")
	if err == nil {
		r.GET("/", func(c *gin.Context) {
			c.Data(http.StatusOK, "text/html; charset=utf-8", homeHTML)
		})
	}
	if sub, err := fs.Sub(staticFS, "static"); err == nil {
		r.StaticFS("/static", http.FS(sub))
	}

	// API：各职责域 handler 挂载自己的路由。
	api := r.Group("/api/v1")
	newAuthHandler(svc).RegisterRoutes(api, mgr)

	authed := api.Group("", verifyAuth(mgr))
	newGroupConfigHandler(svc).RegisterRoutes(authed)
	newPersonaHandler(svc).RegisterRoutes(authed)
	newGroupProfileHandler(svc).RegisterRoutes(authed)
	newJargonHandler(svc).RegisterRoutes(authed)
	newMemberFactHandler(svc).RegisterRoutes(authed)
	newStateHandler(svc).RegisterRoutes(authed)

	return r
}