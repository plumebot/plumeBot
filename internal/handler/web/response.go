package web

// 响应包络与业务码定义在 dto/response 包（前端交互出参结构集中管理）；
// 本文件仅承载 handler 侧的响应编排 helper（ok/fail/handleError）与共享 ctx Key。

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"plumebot/internal/domain/entity"
	"plumebot/internal/handler/web/dto/response"
	"plumebot/pkg/logger"
)

// ctxKeyUsername 鉴权中间件写入 gin.Context 的操作者用户名 Key（供审计/改密等使用）。
const ctxKeyUsername = "web.username"

// ok 输出成功包络（HTTP 200）。
func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, response.Envelope{Code: response.CodeOK, Message: "ok", Data: data})
}

// fail 输出失败包络。
func fail(c *gin.Context, httpStatus, code int, msg string) {
	c.JSON(httpStatus, response.Envelope{Code: code, Message: msg})
}

// handleError 把 service 层错误映射为「HTTP 状态 + 业务码 + 中文消息」。
func handleError(c *gin.Context, err error) {
	var ve *entity.ValidationError
	switch {
	case errors.Is(err, entity.ErrNotFound):
		fail(c, http.StatusNotFound, response.CodeNotFound, "目标不存在")
	case errors.Is(err, entity.ErrConflict):
		fail(c, http.StatusConflict, response.CodeConflict, "资源冲突，请刷新后重试")
	case errors.As(err, &ve):
		fail(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
	case errors.Is(err, entity.ErrAlreadyRegistered):
		fail(c, http.StatusForbidden, response.CodeForbidden, "已存在管理员账号，注册入口已关闭")
	case errors.Is(err, entity.ErrInvalidCredentials):
		fail(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户名或密码错误")
	case errors.Is(err, entity.ErrWrongOldPassword):
		fail(c, http.StatusBadRequest, response.CodeBadRequest, "旧密码不正确")
	default:
		logger.Error("admin api 内部错误", logger.Err(err))
		fail(c, http.StatusInternalServerError, response.CodeInternal, "内部错误")
	}
}