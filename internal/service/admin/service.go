package admin

import (
	"plumebot/internal/domain"
	"plumebot/internal/service/memory"
	"plumebot/pkg/jwt"
	"plumebot/pkg/logger"
)

// groupProfileInvalidator 是 Service 对「群画像缓存失效」的最小依赖面（消费者侧接口）。
// *memory.MemoryService 实现了该接口；测试可注入假实现断言失效被触发。
type groupProfileInvalidator interface {
	InvalidateGroupProfile(groupID string)
}

// Service 管理后端配置读写（P7-001，单 service 按配置域分组方法，见 docs/admin-web-api-plan.md §4.3）。
// 依赖注入：domain.Storage（配置读写）+ 群画像缓存失效器（*memory.MemoryService）+ *jwt.Manager（签发）。
type Service struct {
	store domain.Storage
	mem   groupProfileInvalidator
	mgr   *jwt.Manager
}

// NewService 创建管理后端 Service。
func NewService(store domain.Storage, mem *memory.MemoryService, mgr *jwt.Manager) *Service {
	return &Service{store: store, mem: mem, mgr: mgr}
}

// audit 记录管理面写操作（谁/何时/改了哪个配置目标，见计划书 §11 安全考虑 6）。
func (s *Service) audit(by, resource, target string) {
	logger.Info("admin config changed",
		logger.S("admin_identity", by), logger.S("resource", resource), logger.S("target", target))
}

// AuthResult 注册/登录成功返回的认证结果（token 由 mgr 签发）。
type AuthResult struct {
	Token     string
	TokenType string
	Username  string
	ExpiresAt int64
}

// issueToken 为 username 签发登录 token。
func (s *Service) issueToken(username string) (*AuthResult, error) {
	tok, expiresAt, err := s.mgr.Sign(username)
	if err != nil {
		return nil, err
	}
	return &AuthResult{Token: tok, TokenType: "Bearer", Username: username, ExpiresAt: expiresAt}, nil
}