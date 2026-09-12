package response

// Auth 注册/登录成功响应。
type Auth struct {
	Token     string `json:"token"`
	TokenType string `json:"token_type"`
	ExpiresAt int64  `json:"expires_at"`
}

// Me 当前登录人响应。
type Me struct {
	Username string `json:"username"`
}

// Status 是否已注册管理员响应（前端据此显示注册还是登录登录表单）。
type Status struct {
	Registered bool `json:"registered"`
}