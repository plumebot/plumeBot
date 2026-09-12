package web

// handler/web 鉴权流程测试（P7-001）：tt 鉴权拦截 + 注册/登录/改密 HTTP 全流程 + 包络格式。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"plumebot/internal/handler/web/dto/response"
	"plumebot/internal/infra/sqlite"
	"plumebot/internal/service/admin"
	"plumebot/pkg/jwt"
)

// newTestRouter 用真实 SQLite store 组装完整编（鉴权 + 注册/登录可用）。
func newTestRouter(t *testing.T) (*gin.Engine, *jwt.Manager) {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	mgr := jwt.NewManager("test-secret", time.Hour)
	svc := admin.NewService(store, nil, mgr)
	return NewRouter(svc, mgr), mgr
}

// doJSON 发送 JSON 请求，返回 HTTP 状态码 + 解包后的响应包络。
func doJSON(t *testing.T, r *gin.Engine, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("编码请求体失败: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应非包络 JSON (%d): %s", w.Code, w.Body.String())
	}
	return w.Code, env
}

// registerAndLogin 走通注册流程，返回登录 token。
func registerAndLogin(t *testing.T, r *gin.Engine) string {
	t.Helper()
	status, env := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "admin", map[string]string{
		"username": "admin", "password": "password123",
	})
	if status != http.StatusOK {
		t.Fatalf("注册应 200, 实际 %d: %v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	token, _ := data["token"].(string)
	if token == "" {
		t.Fatalf("注册应返回 token, 实际: %v", env)
	}
	return token
}

func TestPing(t *testing.T) {
	r, _ := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ping 应 200, 实际 %d", w.Code)
	}
}

func TestAuthRequired(t *testing.T) {
	r, mgr := newTestRouter(t)

	// 无 token → 401。
	status, env := doJSON(t, r, http.MethodGet, "/api/v1/auth/me", "", nil)
	if status != http.StatusUnauthorized || env["code"] != float64(response.CodeUnauthorized) {
		t.Fatalf("无 token 应 401/4011, 实际 %d %v", status, env)
	}
	// 坏 token → 401。
	status, _ = doJSON(t, r, http.MethodGet, "/api/v1/auth/me", "broken-token", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("坏 token 应 401, 实际 %d", status)
	}
	// 合法 token（直接签发）→ 通过并回显用户名。
	token, _, err := mgr.Sign("admin")
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}
	status, env = doJSON(t, r, http.MethodGet, "/api/v1/auth/me", token, nil)
	if status != http.StatusOK {
		t.Fatalf("合法 token 应通过, 实际 %d %v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["username"] != "admin" {
		t.Fatalf("me 应回显当前登录人, 实际: %v", data)
	}
}

func TestAuthStatusAndRegisterFlow(t *testing.T) {
	r, _ := newTestRouter(t)

	// 首访：未注册。
	status, env := doJSON(t, r, http.MethodGet, "/api/v1/auth/status", "", nil)
	if status != http.StatusOK {
		t.Fatalf("auth/status 应 200, 实际 %d", status)
	}
	data, _ := env["data"].(map[string]any)
	if data["registered"] != false {
		t.Fatalf("空库 registered 应为 false, 实际: %v", data)
	}

	// 注册成功 → 200 + token。
	status, env = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"username": "admin", "password": "password123",
	})
	if status != http.StatusOK {
		t.Fatalf("首账号注册应 200, 实际 %d %v", status, env)
	}
	payload, _ := env["data"].(map[string]any)
	if payload["token_type"] != "Bearer" || payload["expires_at"] == nil {
		t.Fatalf("注册应返回 token_type/expires_at, 实际: %v", payload)
	}

	// 已有账号再注册 → 403/4031。
	status, env = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"username": "other", "password": "password123",
	})
	if status != http.StatusForbidden || env["code"] != float64(response.CodeForbidden) {
		t.Fatalf("已有账号注册应 403/4031, 实际 %d %v", status, env)
	}

	// 注册后 status 翻转。
	status, env = doJSON(t, r, http.MethodGet, "/api/v1/auth/status", "", nil)
	data, _ = env["data"].(map[string]any)
	if status != http.StatusOK || data["registered"] != true {
		t.Fatalf("注册后 registered 应为 true, 实际 %d %v", status, env)
	}
}

func TestLoginFlow(t *testing.T) {
	r, _ := newTestRouter(t)
	registerAndLogin(t, r)

	// 正确凭证。
	status, env := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "password123",
	})
	if status != http.StatusOK || env["code"] != float64(response.CodeOK) {
		t.Fatalf("正确凭证登录应 200/0, 实际 %d %v", status, env)
	}
	// 错误密码 → 401/4011（统一文案）。
	status, _ = doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "wrong-password",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("错误密码应 401, 实际 %d", status)
	}
	// 非法 JSON 请求体 → 400/4001。
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应 400, 实际 %d", w.Code)
	}
}

func TestChangePasswordHTTP(t *testing.T) {
	r, _ := newTestRouter(t)
	token := registerAndLogin(t, r)

	// 旧码错误 → 400/4001。
	status, _ := doJSON(t, r, http.MethodPut, "/api/v1/auth/password", token, map[string]string{
		"old_password": "wrong-old", "new_password": "newpass123",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("旧码错误应 400, 实际 %d", status)
	}
	// 新密码过短 → 400（validatePassword）。
	status, _ = doJSON(t, r, http.MethodPut, "/api/v1/auth/password", token, map[string]string{
		"old_password": "password123", "new_password": "short",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("新密码过短应 400, 实际 %d", status)
	}
	// 正确改密 → 200。
	status, env := doJSON(t, r, http.MethodPut, "/api/v1/auth/password", token, map[string]string{
		"old_password": "password123", "new_password": "newpass123",
	})
	if status != http.StatusOK {
		t.Fatalf("正确改密应 200, 实际 %d %v", status, env)
	}
	// 新密码登录成功、旧密码失败。
	status, _ = doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "newpass123",
	})
	if status != http.StatusOK {
		t.Fatalf("新密码应可登录, 实际 %d", status)
	}
	status, _ = doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "password123",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("旧密码改后应失效, 实际 %d", status)
	}
}

