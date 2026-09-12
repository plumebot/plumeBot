package admin

// 管理后端 auth 域单测（P7-001）：注册门控 / bcrypt 落库 / 登录分支 / 改密。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/jwt"
)

// fakeAdminStore 嵌入 domain.Storage（其余方法桩），显式实现 admin_user 四个方法。
type fakeAdminStore struct {
	domain.Storage
	users map[string]entity.AdminUser
}

func (f *fakeAdminStore) GetAdminUserByName(_ context.Context, username string) (*entity.AdminUser, error) {
	u, ok := f.users[username]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &u, nil
}

func (f *fakeAdminStore) ListAdminUsers(_ context.Context) ([]entity.AdminUser, error) {
	out := make([]entity.AdminUser, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeAdminStore) CreateAdminUser(_ context.Context, u entity.AdminUser) (int64, error) {
	if _, ok := f.users[u.Username]; ok {
		return 0, domain.ErrConflict
	}
	u.ID = int64(len(f.users) + 1)
	f.users[u.Username] = u
	return u.ID, nil
}

func (f *fakeAdminStore) UpdateAdminUserPassword(_ context.Context, username, hash string) error {
	u, ok := f.users[username]
	if !ok {
		return domain.ErrNotFound
	}
	u.PasswordHash = hash
	f.users[username] = u
	return nil
}

func newTestSvc() *Service {
	store := &fakeAdminStore{users: make(map[string]entity.AdminUser)}
	return NewService(store, nil, jwt.NewManager("test-secret", time.Hour))
}

// isValidation 判断错误是否为 ValidationError。
func isValidation(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

func TestRegisterFirstAdmin(t *testing.T) {
	svc := newTestSvc()
	ctx := context.Background()

	res, err := svc.Register(ctx, "admin", "password123")
	if err != nil {
		t.Fatalf("空表首个注册应成功: %v", err)
	}
	if res.Token == "" || res.TokenType != "Bearer" || res.Username != "admin" || res.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("注册应返回有效凭证, 实际: %+v", res)
	}

	// bcrypt 散列落库（非明文）。
	u, err := svc.store.(*fakeAdminStore).GetAdminUserByName(ctx, "admin")
	if err != nil {
		t.Fatalf("GetAdminUserByName 失败: %v", err)
	}
	if u.PasswordHash == "password123" || !strings.HasPrefix(u.PasswordHash, "$2") {
		t.Fatalf("应落 bcrypt 散列而非明文, 实际: %q", u.PasswordHash)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("password123")) != nil {
		t.Fatalf("散列应能校验原密码")
	}

	// 门控：已有账号 → 注册关闭。
	if _, err := svc.Register(ctx, "someone", "password123"); !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("已有账号注册应报 ErrAlreadyRegistered, 实际: %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	svc := newTestSvc()
	ctx := context.Background()

	// 密码不足 8 位。
	if _, err := svc.Register(ctx, "admin", "short1"); !isValidation(err) {
		t.Fatalf("短密码应报 ValidationError, 实际: %v", err)
	}
	// 用户名为空。
	if _, err := svc.Register(ctx, "  ", "password123"); !isValidation(err) {
		t.Fatalf("空用户名应报 ValidationError, 实际: %v", err)
	}
	// 校验失败不落库（门控仍开放）。
	if _, err := svc.Register(ctx, "admin", "password123"); err != nil {
		t.Fatalf("校验失败不应消耗注册机会: %v", err)
	}
}

func TestLogin(t *testing.T) {
	svc := newTestSvc()
	ctx := context.Background()

	if _, err := svc.Register(ctx, "admin", "password123"); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	// 正确凭证 → 成功。
	res, err := svc.Login(ctx, "admin", "password123")
	if err != nil || res.Token == "" {
		t.Fatalf("正确凭证登录应成功: %v", err)
	}
	// 密码错误 → 统一 ErrInvalidCredentials（防探测）。
	if _, err := svc.Login(ctx, "admin", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("密码错误应报 ErrInvalidCredentials, 实际: %v", err)
	}
	// 用户不存在 → 同一错误（不区分）。
	if _, err := svc.Login(ctx, "ghost", "password123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("用户不存在应报 ErrInvalidCredentials, 实际: %v", err)
	}
}

func TestChangePassword(t *testing.T) {
	svc := newTestSvc()
	ctx := context.Background()

	if _, err := svc.Register(ctx, "admin", "password123"); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	// 旧码错误 → 拒绝。
	if err := svc.ChangePassword(ctx, "admin", "wrong-old", "newpass123"); !errors.Is(err, ErrWrongOldPassword) {
		t.Fatalf("旧码错误应报 ErrWrongOldPassword, 实际: %v", err)
	}
	// 成功改密。
	if err := svc.ChangePassword(ctx, "admin", "password123", "newpass123"); err != nil {
		t.Fatalf("改密失败: %v", err)
	}
	// 旧密码登录失败、新密码登录成功。
	if _, err := svc.Login(ctx, "admin", "password123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("旧密码改后应失效, 实际: %v", err)
	}
	if _, err := svc.Login(ctx, "admin", "newpass123"); err != nil {
		t.Fatalf("新密码应可登录: %v", err)
	}
}

func TestAuthStatus(t *testing.T) {
	svc := newTestSvc()
	ctx := context.Background()

	registered, err := svc.AuthStatus(ctx)
	if err != nil {
		t.Fatalf("AuthStatus 失败: %v", err)
	}
	if registered {
		t.Fatal("空库应返回未注册")
	}
	if _, err := svc.Register(ctx, "admin", "password123"); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	registered, _ = svc.AuthStatus(ctx)
	if !registered {
		t.Fatal("注册后应返回已注册")
	}
}