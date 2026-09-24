package web

// handler/web 鉴权流程测试（P7-001）：tt 鉴权拦截 + 注册/登录/改密 HTTP 全流程 + 包络格式。

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"plumebot/internal/domain/entity"
	"plumebot/internal/handler/web/dto/response"
	"plumebot/internal/infra/logfile"
	"plumebot/internal/infra/sqlite"
	"plumebot/internal/service/admin"
	logsvc "plumebot/internal/service/log"
	"plumebot/internal/service/memory"
	"plumebot/pkg/jwt"
)

// testLogTS 是测试日志样本的时间戳（固定值，避免依赖真实时钟）。
var testLogTS = float64(time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC).UnixNano()) / 1e9

// newTestRouter 用真实 SQLite store 组装完整编（鉴权 + 注册/登录可用）；
// 日志读取用真实 logfile.Reader（临时日志目录，预置 info/error 样本各一条，见 writeTestLogs）。
func newTestRouter(t *testing.T) (*gin.Engine, *jwt.Manager) {
	t.Helper()
	return newTestRouterWithWindow(t, memory.NewWindow())
}

// newTestRouterWithWindow 同 newTestRouter，但注入指定窗口（P7-003 会话窗口测试预置消息用）。
// seedSummaries 预置归档摘要（conversation_summary 表）——摘要热链会话首次访问时惰性回灌，
// 故预置即在 /window 的 summaries 中可见（顺带覆盖回灌路径）。
func newTestRouterWithWindow(t *testing.T, win *memory.Window, seedSummaries ...entity.Summary) (*gin.Engine, *jwt.Manager) {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	for _, s := range seedSummaries {
		if err := store.SaveSummary(context.Background(), s); err != nil {
			t.Fatalf("预置摘要失败: %v", err)
		}
	}
	mgr := jwt.NewManager("test-secret", time.Hour)
	// 用真 MemoryService：群画像写/删后的缓存失效走真实链路（nil 会导致 nil 指针 panic）。
	memSvc := memory.NewMemoryService(win, store, nil, memory.BuilderConfig{})
	svc := admin.NewService(store, memSvc, mgr)

	logDir := t.TempDir()
	writeTestLogs(t, logDir)
	return NewRouter(svc, logsvc.New(logfile.New(logDir)), mgr), mgr
}

// writeTestLogs 写入 zap 形状的 JSON 日志样本：按级别落各自文件
//（info.log 两条含 trace_id、error.log 一条），与 pkg/logger 的实际产物一致。
func writeTestLogs(t *testing.T, dir string) {
	t.Helper()
	lines := []map[string]any{
		{"ts": testLogTS, "level": "info", "msg": "收到消息", "trace_id": "group:100", "message_id": "m1"},
		{"ts": testLogTS + 1, "level": "info", "msg": "消息结局", "trace_id": "group:100", "outcome": "agent_replied"},
		{"ts": testLogTS + 2, "level": "error", "msg": "启动失败", "error": "boom"},
	}
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatalf("序列化样本失败: %v", err)
		}
		name, _ := l["level"].(string)
		f, err := os.OpenFile(filepath.Join(dir, name+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatalf("打开样本日志失败: %v", err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatalf("写入样本日志失败: %v", err)
		}
		f.Close()
	}
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
		"/api/v1/sessions", "/api/v1/sessions/g1/window",
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
	// 文案需带会话键——前端手填会话键，通用「目标不存在」无法定位是哪个会话查不到。
	status, env := doJSON(t, r, http.MethodGet, "/api/v1/sessions/g-none/state", token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("无运行态应 404, 实际 %d", status)
	}
	if env["code"] != float64(response.CodeNotFound) {
		t.Fatalf("无运行态应返回 4041, 实际 %v", env["code"])
	}
	if msg, _ := env["message"].(string); !strings.Contains(msg, "g-none") {
		t.Fatalf("无运行态应返回带会话键的可读文案, 实际 message=%q", msg)
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

// TestLogEndpoint 验证日志浏览接口（架构 §17.6）：鉴权、全量/按 level/trace_id 过滤、参数校验。
func TestLogEndpoint(t *testing.T) {
	r, _ := newTestRouter(t)
	token := registerAndLogin(t, r)

	// 无 token → 401（与其余管理接口一致）。
	if status, _ := doJSON(t, r, http.MethodGet, "/api/v1/logs", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401, 实际 %d", status)
	}

	// 全量：3 条样本，最新在前，字段齐全（ts/level/message/trace_id 可被前端直接消费）。
	status, env := doJSON(t, r, http.MethodGet, "/api/v1/logs", token, nil)
	if status != http.StatusOK {
		t.Fatalf("查询日志应 200, 实际 %d %v", status, env)
	}
	data := env["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 3 || data["has_more"] != false {
		t.Fatalf("应返回 3 条且 has_more=false, 实际 %v", data)
	}
	first := items[0].(map[string]any)
	if first["message"] != "启动失败" || first["level"] != "error" {
		t.Fatalf("最新一条应为 error「启动失败」, 实际 %v", first)
	}

	// 按 level 过滤（error 语义含 fatal，infra 侧映射）。
	_, env = doJSON(t, r, http.MethodGet, "/api/v1/logs?levels=info", token, nil)
	items = env["data"].(map[string]any)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("levels=info 应 2 条, 实际 %d", len(items))
	}

	// 按 trace_id 过滤：命中同会话的两条（入口行 + 结局行）。
	_, env = doJSON(t, r, http.MethodGet, "/api/v1/logs?trace_id=group:100", token, nil)
	items = env["data"].(map[string]any)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("trace_id=group:100 应 2 条, 实际 %d", len(items))
	}

	// 分页：limit=1 首页 has_more=true，offset=2 末页 has_more=false。
	_, env = doJSON(t, r, http.MethodGet, "/api/v1/logs?limit=1", token, nil)
	if d := env["data"].(map[string]any); len(d["items"].([]any)) != 1 || d["has_more"] != true {
		t.Fatalf("limit=1 应 1 条且 has_more=true, 实际 %v", d)
	}
	_, env = doJSON(t, r, http.MethodGet, "/api/v1/logs?limit=1&offset=2", token, nil)
	if d := env["data"].(map[string]any); d["has_more"] != false {
		t.Fatalf("offset=2 应为末页, 实际 %v", d)
	}

	// 非法参数 → 400（不静默忽略，避免筛选未生效却看到全量）。
	for _, q := range []string{"levels=fatal", "begin=2026-09-21", "limit=-1", "offset=abc",
		"begin=2026-09-22T00:00:00Z&end=2026-09-21T00:00:00Z"} {
		status, env := doJSON(t, r, http.MethodGet, "/api/v1/logs?"+q, token, nil)
		if status != http.StatusBadRequest || env["code"] != float64(response.CodeBadRequest) {
			t.Fatalf("非法参数 %q 应 400/4000, 实际 %d %v", q, status, env)
		}
	}
}


// TestSessionEndpoints 会话窗口域端点（P7-003）：会话列表 + 窗口只读 + 空态 + 鉴权 + 私聊会话键
// URL 编码访问。窗口为真 memory.Window 预置消息（不经 event 链路，窗口即唯一数据源）。
func TestSessionEndpoints(t *testing.T) {
	win := memory.NewWindow()
	ctx := context.Background()
	now := time.Now().Unix()
	mk := func(id, groupID, userID, name, msg string, ts int64) entity.Message {
		return entity.Message{
			MessageID: id, GroupID: groupID, UserID: userID, SenderName: name, MessageType: "group",
			Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: msg}}, Timestamp: ts,
		}
	}
	if _, err := win.AppendMessage(ctx, mk("m1", "g1", "10001", "小明", "早", now)); err != nil {
		t.Fatalf("追加消息失败: %v", err)
	}
	if _, err := win.AppendMessage(ctx, mk("self:1", "g1", "bot", "PlumeBot", "你好", now+1)); err != nil {
		t.Fatalf("追加 bot 回复失败: %v", err)
	}
	if _, err := win.AppendMessage(ctx, entity.Message{
		MessageID: "self:2", UserID: "u9", MessageType: "private",
		Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: "私聊"}}, Timestamp: now + 2,
	}); err != nil {
		t.Fatalf("追加私聊消息失败: %v", err)
	}

	r, _ := newTestRouterWithWindow(t, win, entity.Summary{
		ChatID: "g1", Seq: 1, Text: "早前聊了周末安排",
		Keywords: []string{"周末"}, Decisions: []string{"周六爬山"}, CreatedAt: now,
	})
	token := registerAndLogin(t, r)

	// 会话列表：g1 + private:u9 两个会话，按键升序；g1 概览含条数与最近消息文本。
	status, env := doJSON(t, r, http.MethodGet, "/api/v1/sessions", token, nil)
	if status != http.StatusOK {
		t.Fatalf("会话列表应 200, 实际 %d", status)
	}
	items := env["data"].(map[string]any)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("应 2 个活跃会话, 实际 %v", items)
	}
	g1, _ := items[0].(map[string]any)
	if g1["key"] != "g1" || g1["count"] != float64(2) || g1["last_render"] != "你好" || g1["last_ts"] != float64(now+1) {
		t.Fatalf("g1 概览字段错误: %v", g1)
	}

	// 窗口查看：时间正序 2 条，首条非 self（展示名优先），次条 self（bot 回复）；
	// summaries 同响应返回（该会话预置 1 条归档摘要，经热链回灌可见）。
	status, env = doJSON(t, r, http.MethodGet, "/api/v1/sessions/g1/window", token, nil)
	if status != http.StatusOK {
		t.Fatalf("窗口应 200, 实际 %d", status)
	}
	sums := env["data"].(map[string]any)["summaries"].([]any)
	if len(sums) != 1 {
		t.Fatalf("g1 应 1 条摘要, 实际 %v", sums)
	}
	if s0, _ := sums[0].(map[string]any); s0["text"] != "早前聊了周末安排" || s0["created_at"] != float64(now) {
		t.Fatalf("摘要字段错误: %v", s0)
	}
	msgs := env["data"].(map[string]any)["items"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("g1 窗口应 2 条, 实际 %d", len(msgs))
	}
	w0, _ := msgs[0].(map[string]any)
	w1, _ := msgs[1].(map[string]any)
	if w0["is_self"] != false || w0["sender"] != "小明" || w0["render"] != "早" {
		t.Fatalf("首条窗口消息错误: %v", w0)
	}
	if w1["is_self"] != true || w1["sender"] != "PlumeBot" || w1["render"] != "你好" {
		t.Fatalf("bot 消息错误: %v", w1)
	}

	// 私聊会话键含冒号，经 URL 编码访问（形态对齐 /sessions/:session_key/state）。
	status, env = doJSON(t, r, http.MethodGet, "/api/v1/sessions/private%3Au9/window", token, nil)
	if status != http.StatusOK {
		t.Fatalf("私聊窗口应 200, 实际 %d", status)
	}
	if msgs := env["data"].(map[string]any)["items"].([]any); len(msgs) != 1 {
		t.Fatalf("私聊窗口应 1 条, 实际 %d", len(msgs))
	}

	// 未知会话（无窗口/无摘要）→ 200 + 空 items + 空 summaries（非 404，前端渲染空态）。
	status, env = doJSON(t, r, http.MethodGet, "/api/v1/sessions/nosuch/window", token, nil)
	d := env["data"].(map[string]any)
	if status != http.StatusOK || len(d["items"].([]any)) != 0 || len(d["summaries"].([]any)) != 0 {
		t.Fatalf("未知会话应 200 空 items/summaries, 实际 %d %v", status, env)
	}
}
