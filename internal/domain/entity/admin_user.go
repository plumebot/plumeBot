package entity

// AdminUser 管理后端管理员账号（P7-001，表 admin_user）。
// 凭证唯一事实来源；凭证仅存 bcrypt 散列（PasswordHash），明文不落库。
// API 响应一律不序列化 PasswordHash（由 service 覆盖为空串）。
type AdminUser struct {
	ID           int64  // 账号 ID
	Username     string // 登录名（UNIQUE）
	PasswordHash string // bcrypt 散列
	CreatedAt    int64  // 创建时间（Unix 秒）
	UpdatedAt    int64  // 最近修改时间（Unix 秒）
}