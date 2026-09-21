package web

// 管理后端日志接线（架构 §17.1/§17.2）：
//   - AccessLogger：gin 每请求访问日志经 logger.GinAccessWriter() 落 logs/gin.log
//     （文本，lumberjack 与业务日志同滚动策略），不再写 stdout——/ping 健康轮询不刷终端；
//   - Recovery：panic 记 logger.Error（进 error.log，带栈）并返回 500。
// 新路由（NewRouter）与回退 server（cmd/bot.newWebServer）共用本文件的中间件。

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/pkg/logger"
)

// AccessLogger 返回写文件（logs/gin.log）的 gin 访问日志中间件。
func AccessLogger() gin.HandlerFunc {
	return gin.LoggerWithWriter(logger.GinAccessWriter())
}

// Recovery 替代 gin.Recovery：panic 经 zap Error 记入 error.log（含栈），返回 500。
// gin.Recovery 默认写 stderr，脱离 zap 文件体系，故此处自实现（保持 §17.1 单一输出形态）。
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logger.Error("admin api panic",
			logger.S("path", c.FullPath()),
			logger.Any("panic", recovered))
		c.AbortWithStatus(http.StatusInternalServerError)
	})
}
