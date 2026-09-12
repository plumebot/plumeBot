package web

// handler/web 鉴权流程测试（P7-001）：tt 鉴权拦截 + 注册/登录/改密 HTTP 全流程 + 包络格式。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"plumebot/internal/handler/web/dto/response"
	"plumebot/internal/infra/sqlite"
	"plumebot/internal/service/admin"
	"plumebot/internal/service/memory"
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
	// 用真 MemoryService：群画像写/删后的缓存失效走真实链路（nil 会导致 nil 指针 panic）。
	memSvc := memory.NewMemoryService(memory.NewWindow(), store, nil, memory.BuilderConfig{})
	svc := admin.NewService(store, memSvc, mgr)
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

// ── 配置域端点（P7-001）──

func TestResourceAuthRequired(t *testing.T) {
	r, _ := newTestRouter(t)
	for _, path := range []string{
		"/api/v1/groups/configs", "/api/v1/personas", "/api/v1/groups/g1/profile",
		"/api/v1/groups/g1/jargons", "/api/v1/member-facts", "/api/v1/sessions/g1/state",
	} {
		if status, _ := doJSON(t, r, http.MethodGet, path, "", nil); status != http.StatusUnauthorized {
			t.Errorf("资源端点 %s 无 token 应 401, 实际 %d", path, status)
		}
	}
}

func TestGroupConfigEndpoints(t *testing.T) {
	r, _ := newTestRouter(t)
	token := registerAndLogin(t, r)

	// 未配置群：GET 单群 → 200 + configured:false（非 404）。
	status, env := doJSON(t, r, http.MethodGet, "/api/v1/groups/g1/config", token, nil)
	if d, _ := env["data"].(map[string]any); status != http.StatusOK || d["configured"] != false {
		t.Fatalf("未配置群 GET 应 200 configured:false, 实际 %d %v", status, env)
	}

	// PUT 写入 → 200（configured:true 随响应）。
	status, _ = doJSON(t, r, http.MethodPut, "/api/v1/groups/g1/config", token, map[string]any{
		"mode": "auto", "energy_max": 50, "group_mgmt_enabled": 1,
		"quiet_hours_start": "", "quiet_hours_end": "",
	})
	if status != http.StatusOK {
		t.Fatalf("PUT 应 200, 实际 %d", status)
	}
	// 非法 mode → 400（fail-fast）。
	status, _ = doJSON(t, r, http.MethodPut, "/api/v1/groups/g1/config", token, map[string]any{"mode": "random"})
	if status != http.StatusBadRequest {
		t.Fatalf("非法 mode 应 400, 实际 %d", status)
	}

	// 列表 + 单群读取。
	status, env = doJSON(t, r, http.MethodGet, "/api/v1/groups/configs", token, nil)
	if status != http.StatusOK {
		t.Fatalf("列表应 200, 实际 %d", status)
	}
	if items := env["data"].(map[string]any)["items"].([]any); len(items) != 1 {
		t.Fatalf("列表应含 1 项, 实际 %v", items)
	}
	status, env = doJSON(t, r, http.MethodGet, "/api/v1/groups/g1/config", token, nil)
	if d, _ := env["data"].(map[string]any); status != http.StatusOK || d["configured"] != true {
		t.Fatalf("写入后 GET 应 configured:true, 实际 %d %v", status, env)
	}

	// DELETE → 恢复未配置；再 DELETE → 404。
	if status, _ := doJSON(t, r, http.MethodDelete, "/api/v1/groups/g1/config", token, nil); status != http.StatusOK {
		t.Fatalf("DELETE 应 200, 实际 %d", status)
	}
	if status, _ := doJSON(t, r, http.MethodDelete, "/api/v1/groups/g1/config", token, nil); status != http.StatusNotFound {
		t.Fatalf("重复 DELETE 应 404, 实际 %d", status)
	}
}

func TestPersonaEndpoints(t *testing.T) {
	r, _ := newTestRouter(t)
	token := registerAndLogin(t, r)

	// 不存在 agent → 404。
	if status, _ := doJSON(t, r, http.MethodGet, "/api/v1/personas/PlumeBot", token, nil); status != http.StatusNotFound {
		t.Fatalf("不存在人格应 404, 实际 %d", status)
	}
	// PUT 创建 → GET。
	status, _ := doJSON(t, r, http.MethodPut, "/api/v1/personas/PlumeBot", token,
		map[string]any{"name": "默认", "system_prompt": "你是赛博群友。"})
	if status != http.StatusOK {
		t.Fatalf("PUT 人格应 200, 实际 %d", status)
	}
	status, env := doJSON(t, r, http.MethodGet, "/api/v1/personas/PlumeBot", token, nil)
	if d, _ := env["data"].(map[string]any); status != http.StatusOK || d["agent"] != "PlumeBot" {
		t.Fatalf("GET 人格不符: %d %v", status, env)
	}
	// 列表。
	status, env = doJSON(t, r, http.MethodGet, "/api/v1/personas", token, nil)
	if items := env["data"].(map[string]any)["items"].([]any); status != http.StatusOK || len(items) != 1 {
		t.Fatalf("人格列表应 1 项: %d %v", status, env)
	}
	// 空 system_prompt → 400。
	if status, _ := doJSON(t, r, http.MethodPut, "/api/v1/personas/x", token, map[string]any{"name": "", "system_prompt": ""}); status != http.StatusBadRequest {
		t.Fatalf("空 system_prompt 应 400, 实际 %d", status)
	}
}

func TestGroupProfileEndpoints(t *testing.T) {
	r, _ := newTestRouter(t)
	token := registerAndLogin(t, r)

	// 无画像 → 200 + configured:false。
	status, env := doJSON(t, r, http.MethodGet, "/api/v1/groups/g1/profile", token, nil)
	if d, _ := env["data"].(map[string]any); status != http.StatusOK || d["configured"] != false {
		t.Fatalf("无画像 GET 应 configured:false, 实际 %d %v", status, env)
	}
	// PUT 写 → GET roundtrip。
	status, _ = doJSON(t, r, http.MethodPut, "/api/v1/groups/g1/profile", token, map[string]any{
		"culture": "认真", "topics": []string{"Go", "Bot"}, "active_hours": "晚上",
		"rules": []string{}, "atmosphere": []string{"友好"},
	})
	if status != http.StatusOK {
		t.Fatalf("PUT 画像应 200, 实际 %d", status)
	}
	status, env = doJSON(t, r, http.MethodGet, "/api/v1/groups/g1/profile", token, nil)
	d := env["data"].(map[string]any)
	if status != http.StatusOK || d["configured"] != true || d["culture"] != "认真" {
		t.Fatalf("画像内容不符: %d %v", status, d)
	}
	// DELETE → 无画像；再 DELETE → 404。
	if status, _ := doJSON(t, r, http.MethodDelete, "/api/v1/groups/g1/profile", token, nil); status != http.StatusOK {
		t.Fatalf("DELETE 画像应 200, 实际 %d", status)
	}
	if status, _ := doJSON(t, r, http.MethodDelete, "/api/v1/groups/g1/profile", token, nil); status != http.StatusNotFound {
		t.Fatalf("重复 DELETE 画像应 404, 实际 %d", status)
	}
}

func TestJargonEndpoints(t *testing.T) {
	r, _ := newTestRouter(t)
	token := registerAndLogin(t, r)

	// 添加默认 confirmed → 列表。
	if status, _ := doJSON(t, r, http.MethodPost, "/api/v1/groups/g1/jargons", token, map[string]any{"jargon": "yyds"}); status != http.StatusOK {
		t.Fatalf("POST 黑话应 200, 实际 %d", status)
	}
	status, env := doJSON(t, r, http.MethodGet, "/api/v1/groups/g1/jargons?status=all", token, nil)
	items := env["data"].(map[string]any)["items"].([]any)
	if status != http.StatusOK || len(items) != 1 {
		t.Fatalf("黑话列表应 1 项: %d %v", status, env)
	}
	if it := items[0].(map[string]any); it["status"] != "confirmed" {
		t.Fatalf("管理面添加应默认 confirmed, 实际: %v", it)
	}
	// status 过滤。
	status, env = doJSON(t, r, http.MethodGet, "/api/v1/groups/g1/jargons?status=pending", token, nil)
	if items := env["data"].(map[string]any)["items"].([]any); status != http.StatusOK || len(items) != 0 {
		t.Fatalf("pending 过滤应空: %d %v", status, env)
	}
	// 重复添加视为成功（幂等）。
	if status, _ := doJSON(t, r, http.MethodPost, "/api/v1/groups/g1/jargons", token, map[string]any{"jargon": "yyds"}); status != http.StatusOK {
		t.Fatalf("重复添加应视为成功, 实际 %d", status)
	}
	// 删除 → 再删 → 404。
	if status, _ := doJSON(t, r, http.MethodDelete, "/api/v1/groups/g1/jargons", token, map[string]any{"jargon": "yyds"}); status != http.StatusOK {
		t.Fatalf("DELETE 黑话应 200, 实际 %d", status)
	}
	if status, _ := doJSON(t, r, http.MethodDelete, "/api/v1/groups/g1/jargons", token, map[string]any{"jargon": "yyds"}); status != http.StatusNotFound {
		t.Fatalf("重复 DELETE 黑话应 404, 实际 %d", status)
	}
}

func TestMemberFactEndpoints(t *testing.T) {
	r, _ := newTestRouter(t)
	token := registerAndLogin(t, r)

	// user_id 空 → 400。
	if status, _ := doJSON(t, r, http.MethodPost, "/api/v1/member-facts", token, map[string]any{"group_id": "g1", "user_id": "", "fact": "喜欢猫"}); status != http.StatusBadRequest {
		t.Fatalf("空 user_id 应 400, 实际 %d", status)
	}
	// 补记 → 列表。
	if status, _ := doJSON(t, r, http.MethodPost, "/api/v1/member-facts", token, map[string]any{"group_id": "g1", "user_id": "u1", "fact": "喜欢猫"}); status != http.StatusOK {
		t.Fatalf("POST 事实应 200, 实际 %d", status)
	}
	status, env := doJSON(t, r, http.MethodGet, "/api/v1/member-facts?group_id=g1&user_id=u1", token, nil)
	items := env["data"].(map[string]any)["items"].([]any)
	if status != http.StatusOK || len(items) != 1 || items[0] != "喜欢猫" {
		t.Fatalf("事实列表应 1 项: %d %v", status, env)
	}
	// 删除 → 再删 → 404。
	if status, _ := doJSON(t, r, http.MethodDelete, "/api/v1/member-facts?group_id=g1&user_id=u1&fact=%E5%96%9C%E6%AC%A2%E7%8C%AB", token, nil); status != http.StatusOK {
		t.Fatalf("DELETE 事实应 200, 实际 %d", status)
	}
	if status, _ := doJSON(t, r, http.MethodDelete, "/api/v1/member-facts?group_id=g1&user_id=u1&fact=%E5%96%9C%E6%AC%A2%E7%8C%AB", token, nil); status != http.StatusNotFound {
		t.Fatalf("重复 DELETE 事实应 404, 实际 %d", status)
	}
}

func TestStateEndpointNotFound(t *testing.T) {
	r, _ := newTestRouter(t)
	token := registerAndLogin(t, r)
	// 运行态只读：无数据时 404，路由可达。
	if status, _ := doJSON(t, r, http.MethodGet, "/api/v1/sessions/g-none/state", token, nil); status != http.StatusNotFound {
		t.Fatalf("无运行态应 404, 实际 %d", status)
	}
}

func TestServeIndexPage(t *testing.T) {
	r, _ := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "PlumeBot 管理") {
		t.Fatalf("首页应 200 且包含标题, 实际 %d", w.Code)
	}
}

