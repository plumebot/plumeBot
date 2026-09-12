// Package request 定义管理后端 API 请求体 DTO（P7-001）。
// 与前端交互的入参结构集中于此；handler 仅做 Bind 与校验编排，不内联定义请求结构。
package request

// Auth 注册/登录请求体。
type Auth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// ChangePassword 修改密码请求体。
type ChangePassword struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}