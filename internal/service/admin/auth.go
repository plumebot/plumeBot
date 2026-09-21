package admin

import (
	"context"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// Register 创建首个管理员账号（空表门控，定案 J）并签发 token。
// 已有任一管理员 → ErrAlreadyRegistered；同名冲突由 UNIQUE 兜底 → ErrConflict。
func (s *Service) Register(ctx context.Context, username, password string) (*AuthResult, error) {
	if err := validateAuthInput(username, password); err != nil {
		return nil, err
	}
	users, err := s.store.ListAdminUsers(ctx)
	if err != nil {
		return nil, err
	}
	if len(users) > 0 {
		return nil, ErrAlreadyRegistered
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		logger.Warn("admin 注册失败：密码散列错误", logger.S("username", username),
			logger.S("ip", ClientIPFrom(ctx)), logger.Err(err))
		return nil, err
	}
	now := time.Now().Unix()
	if _, err := s.store.CreateAdminUser(ctx, entity.AdminUser{
		Username:     username,
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		return nil, err
	}
	logger.Info("admin 注册首个账号",
		logger.S("username", username), logger.S("ip", ClientIPFrom(ctx)))
	return s.issueToken(username)
}

// Login 校验用户名/密码，成功签发 token。
// 失败统一收敛为 ErrInvalidCredentials（不区分「用户不存在/密码错误」，防探测）。
func (s *Service) Login(ctx context.Context, username, password string) (*AuthResult, error) {
	u, err := s.store.GetAdminUserByName(ctx, username)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		logger.Warn("admin 登录失败",
			logger.S("username", username), logger.S("ip", ClientIPFrom(ctx)))
		return nil, ErrInvalidCredentials
	}
	logger.Info("admin 登录成功",
		logger.S("username", username), logger.S("ip", ClientIPFrom(ctx)))
	return s.issueToken(username)
}

// ChangePassword 修改密码：先验旧码再更新散列（页面改密，定案 D）。
func (s *Service) ChangePassword(ctx context.Context, username, oldPwd, newPwd string) error {
	if err := validatePassword(newPwd); err != nil {
		return err
	}
	u, err := s.store.GetAdminUserByName(ctx, username)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(oldPwd)) != nil {
		// 旧密码错误：Warn 带 IP（改密端点无每 IP 限流，猜旧密码需可发现，架构 §17.5）。
		logger.Warn("admin 改密失败：旧密码错误",
			logger.S("username", username), logger.S("ip", ClientIPFrom(ctx)))
		return ErrWrongOldPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPwd), bcrypt.DefaultCost)
	if err != nil {
		logger.Warn("admin 改密失败：密码散列错误",
			logger.S("username", username), logger.S("ip", ClientIPFrom(ctx)), logger.Err(err))
		return err
	}
	if err := s.store.UpdateAdminUserPassword(ctx, username, string(hash)); err != nil {
		return err
	}
	logger.Info("admin 修改密码",
		logger.S("username", username), logger.S("ip", ClientIPFrom(ctx)))
	return nil
}

// AuthStatus 返回是否已注册管理员（前端据此显示注册还是登录表单）。
func (s *Service) AuthStatus(ctx context.Context) (bool, error) {
	users, err := s.store.ListAdminUsers(ctx)
	if err != nil {
		return false, err
	}
	return len(users) > 0, nil
}