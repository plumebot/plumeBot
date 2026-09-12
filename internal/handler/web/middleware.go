package web

// 中间件层（P7-001）：JWT 鉴权 + 每 IP 限流。与 handler 逻辑完全分离——
// verifyAuth 只负责鉴权与身份写入；ipLimiter 只负责注册/登录端点的速率节流。

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"plumebot/internal/handler/web/dto/response"
	"plumebot/pkg/jwt"
)

// verifyAuth 鉴权中间件：校验 Authorization: Bearer <token>，
// 通过后把 Claims.Username 写入 gin.Context（Key 见 ctxKeyUsername），供改密/审计取操作者。
// 鉴权失败统一 401/4011。密码学全委托 pkg/jwt。
func verifyAuth(mgr *jwt.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		tok, ok := strings.CutPrefix(raw, "Bearer ")
		if !ok {
			fail(c, http.StatusUnauthorized, response.CodeUnauthorized, "未认证")
			c.Abort()
			return
		}
		claims, err := mgr.Verify(tok)
		if err != nil {
			fail(c, http.StatusUnauthorized, response.CodeUnauthorized, "未认证或登录已过期")
			c.Abort()
			return
		}
		c.Set(ctxKeyUsername, claims.Username)
		c.Next()
	}
}

// authRateLimit / authBurst 注册/登录端点的每 IP 令牌桶限流（防爆破）。
// 持续速率 2/s 已远低于爆破所需；burst 容纳登录页短暂操作。
const (
	authRateLimit = 2  // 每秒 2 个
	authBurst     = 10 // 突发容量 10
)

// ipLimiter 按客户端 IP 维度的令牌桶（golang.org/x/time/rate）。
type ipLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	b        int
}

func newIPLimiter(r rate.Limit, b int) *ipLimiter {
	return &ipLimiter{limiters: make(map[string]*rate.Limiter), r: r, b: b}
}

// middleware 中间件：IP 令牌桶不足 → 429 拒绝。
func (l *ipLimiter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		l.mu.Lock()
		lim, exists := l.limiters[ip]
		if !exists {
			lim = rate.NewLimiter(l.r, l.b)
			l.limiters[ip] = lim
		}
		l.mu.Unlock()
		if !lim.Allow() {
			fail(c, http.StatusTooManyRequests, response.CodeBadRequest, "请求过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}