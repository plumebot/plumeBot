package admin

import (
	"context"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/service/memory"
	"plumebot/pkg/jwt"
	"plumebot/pkg/logger"
)

// 消费侧接口（SessionWindowReader / GroupProfileInvalidator）定义在 domain 层
//（internal/domain/admin.go），与 domain.Admin 接口保持一致的分层纪律。

// clientIPCtxKey 是来源 IP 在 context 中的键类型（私有，避免与其他包键冲突）。
type clientIPCtxKey struct{}

// WithClientIP 把来源 IP 注入 context（handler/web 中间件调用），
// 供 service 层审计日志记录操作来源（架构 §17.5）。
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPCtxKey{}, ip)
}

// ClientIPFrom 从 context 取来源 IP；未注入返回空串（审计字段留空）。
func ClientIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPCtxKey{}).(string)
	return ip
}

// Service 实现 domain.Admin 管理后端配置读写（P7-001，单 service 按配置域分组方法，见 docs/admin-web-api-plan.md §4.3）。
// 依赖注入：domain.Storage（配置读写）+ 群画像缓存失效器（*memory.MemoryService）+
// 窗口只读（*memory.MemoryService）+ 摘要热链只读（*memory.MemoryService）+ *jwt.Manager（签发）。
type Service struct {
	store domain.Storage
	mem   domain.GroupProfileInvalidator
	win   domain.SessionWindowReader
	sum   domain.SessionSummaryReader
	mgr   *jwt.Manager
}

// NewService 创建管理后端 Service。
func NewService(store domain.Storage, mem *memory.MemoryService, mgr *jwt.Manager) *Service {
	return &Service{store: store, mem: mem, win: mem, sum: mem, mgr: mgr}
}

// audit 记录管理面写操作（谁/何时/改了哪个配置目标/来源 IP，见计划书 §11 安全考虑 6）。
// 经 logger.From(ctx) 输出：ctx 内由 handler/web.withClientIP 注入的派生 logger 携带
// trace_id=来源 IP（架构 §17.6），故管理面操作可在日志浏览页按 IP 一键串联；
// ip 字段保留显式记录（与 trace_id 冗余但语义直白）。
func (s *Service) audit(ctx context.Context, by, resource, target string) {
	logger.From(ctx).Info("admin config changed",
		logger.S("admin_identity", by),
		logger.S("resource", resource),
		logger.S("target", target),
		logger.S("ip", ClientIPFrom(ctx)))
}

// issueToken 为 username 签发登录 token（AuthResult 结构定义见 domain/entity，json 由 web dto 接管）。
func (s *Service) issueToken(username string) (*entity.AuthResult, error) {
	tok, expiresAt, err := s.mgr.Sign(username)
	if err != nil {
		return nil, err
	}
	return &entity.AuthResult{Token: tok, TokenType: "Bearer", Username: username, ExpiresAt: expiresAt}, nil
}