package ai

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
)

// 1x1 透明 PNG 的 base64（与 spike 冒烟同款），用于验证「取图 → base64 → 透传视觉模型」。
const testPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func mustDecodePNG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(testPNGBase64)
	if err != nil {
		t.Fatalf("解码测试 PNG 失败: %v", err)
	}
	return data
}

// runDescribeAndAssert 执行一次 Describe 并断言：返回文本、system+user 两消息形状、
// user 多模态 part 的 base64/mime 透传（与 Summarizer 测试同构）。
func runDescribeAndAssert(t *testing.T, part entity.ContentPart) {
	t.Helper()
	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("一只猫。", nil),
	}}
	d := &EinoMediaDescriber{cm: fake, http: &http.Client{}}

	got, err := d.Describe(context.Background(), part)
	if err != nil {
		t.Fatalf("Describe 失败: %v", err)
	}
	if got != "一只猫。" {
		t.Errorf("描述文本 = %q，期望模型返回", got)
	}

	in := fake.inputs[0]
	if len(in) != 2 || in[0].Role != schema.System || in[1].Role != schema.User {
		t.Fatalf("模型应收到 system+user 两条消息, 实际 %+v", in)
	}
	if in[0].Content != systemDescribePrompt {
		t.Errorf("system prompt 不符: %q", in[0].Content)
	}
	parts := in[1].UserInputMultiContent
	if len(parts) != 2 || parts[0].Type != schema.ChatMessagePartTypeText || parts[1].Image == nil {
		t.Fatalf("user 多模态 part 不符: %+v", parts)
	}
	if parts[1].Image.Base64Data == nil || *parts[1].Image.Base64Data != testPNGBase64 {
		t.Errorf("base64 透传不符: %+v", parts[1].Image)
	}
	if parts[1].Image.MIMEType != "image/png" {
		t.Errorf("mime 应为 image/png（DetectContentType 兜底）, 实际 %q", parts[1].Image.MIMEType)
	}
}

func TestEinoMediaDescriberDescribeLocalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cat.png")
	if err := os.WriteFile(path, mustDecodePNG(t), 0o644); err != nil {
		t.Fatalf("写测试图片失败: %v", err)
	}
	runDescribeAndAssert(t, entity.ContentPart{Type: entity.PartTypeImage, URL: path})
}

// TestEinoMediaDescriberDescribeHTTPURLDirect URL 直传（B-027 增强）：**带 FileHash** 时键已由
// FileHash 给出，无需本地拉图，直接把 URL 交给视觉模型（provider 侧拉图），server 应零请求。
// 注：无 FileHash 的 http URL 会先本地拉字节补算内容键（见 TestEinoMediaDescriberHTTPNoFileHashFetch）。
func TestEinoMediaDescriberDescribeHTTPURLDirect(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(mustDecodePNG(t))
	}))
	defer srv.Close()

	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("一只猫。", nil),
	}}
	d := &EinoMediaDescriber{cm: fake, http: &http.Client{}}

	got, err := d.Describe(context.Background(),
		entity.ContentPart{Type: entity.PartTypeImage, URL: srv.URL, FileHash: "deadbeefdeadbeefdeadbeefdeadbeef"})
	if err != nil {
		t.Fatalf("Describe 失败: %v", err)
	}
	if got != "一只猫。" {
		t.Errorf("描述文本 = %q，期望模型返回", got)
	}
	if hits.Load() != 0 {
		t.Errorf("URL 直传不应本地拉图, 实际 server 请求 %d", hits.Load())
	}
	if len(fake.inputs) != 1 {
		t.Fatalf("应只调模型 1 次, 实际 %d", len(fake.inputs))
	}
	in := fake.inputs[0]
	if len(in) != 2 || in[0].Role != schema.System || in[1].Role != schema.User {
		t.Fatalf("模型应收到 system+user 两条消息, 实际 %+v", in)
	}
	parts := in[1].UserInputMultiContent
	if len(parts) != 2 || parts[0].Type != schema.ChatMessagePartTypeText || parts[1].Image == nil {
		t.Fatalf("user 多模态 part 不符: %+v", parts)
	}
	if parts[1].Image.URL == nil || *parts[1].Image.URL != srv.URL {
		t.Errorf("URL 直传应透传 Image.URL, 实际 %+v", parts[1].Image)
	}
	if parts[1].Image.Base64Data != nil {
		t.Errorf("URL 直传不应带 Base64Data, 实际 %+v", parts[1].Image)
	}
}

func TestEinoMediaDescriberDescribeBase64Direct(t *testing.T) {
	runDescribeAndAssert(t, entity.ContentPart{Type: entity.PartTypeImage, Base64: testPNGBase64})
}

func TestEinoMediaDescriberEmptyInput(t *testing.T) {
	d := &EinoMediaDescriber{cm: &fakeChatModel{}, http: &http.Client{}}
	if _, err := d.Describe(context.Background(), entity.ContentPart{Type: entity.PartTypeImage}); err == nil {
		t.Fatal("URL 与 Base64 均为空应报错")
	}
}

func TestEinoMediaDescriberHTTP404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	d := &EinoMediaDescriber{cm: &fakeChatModel{}, http: &http.Client{}}
	if _, err := d.Describe(context.Background(), entity.ContentPart{Type: entity.PartTypeImage, URL: srv.URL}); err == nil {
		t.Fatal("HTTP 404 应报错")
	}
}

func TestEinoMediaDescriberMissingFile(t *testing.T) {
	d := &EinoMediaDescriber{cm: &fakeChatModel{}, http: &http.Client{}}
	if _, err := d.Describe(context.Background(),
		entity.ContentPart{Type: entity.PartTypeImage, URL: filepath.Join(t.TempDir(), "nope.png")}); err == nil {
		t.Fatal("本地文件不存在应报错")
	}
}

// TestEinoMediaDescriberCacheHit 同图（同 Base64）两次 Describe → 模型只调 1 次（B-027）。
func TestEinoMediaDescriberCacheHit(t *testing.T) {
	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("一只猫。", nil),
	}}
	d := &EinoMediaDescriber{cm: fake, http: &http.Client{}}
	part := entity.ContentPart{Type: entity.PartTypeImage, Base64: testPNGBase64}

	first, err := d.Describe(context.Background(), part)
	if err != nil {
		t.Fatalf("首次 Describe 失败: %v", err)
	}
	second, err := d.Describe(context.Background(), part)
	if err != nil {
		t.Fatalf("二次 Describe 失败: %v", err)
	}
	if first != second {
		t.Errorf("缓存命中应返回一致描述: %q vs %q", first, second)
	}
	if len(fake.inputs) != 1 {
		t.Errorf("同图缓存命中应只调模型 1 次, 实际 %d", len(fake.inputs))
	}
}

// TestEinoMediaDescriberCacheKeyIsolation 不同来源键不共享缓存：相同内容 URL(本地路径) vs Base64。
func TestEinoMediaDescriberCacheKeyIsolation(t *testing.T) {
	// 本地临时图：同 testPNG 内容，避免测试触发真实网络。
	path := filepath.Join(t.TempDir(), "cat.png")
	if err := os.WriteFile(path, mustDecodePNG(t), 0o644); err != nil {
		t.Fatalf("写测试图片失败: %v", err)
	}
	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("一只猫。", nil),
		schema.AssistantMessage("一只猫。", nil),
	}}
	d := &EinoMediaDescriber{cm: fake, http: &http.Client{}}

	// URL(路径) 与 Base64 不同键 → 各调一次模型（2 次）。
	if _, err := d.Describe(context.Background(),
		entity.ContentPart{Type: entity.PartTypeImage, URL: path}); err != nil {
		t.Fatalf("URL Describe 失败: %v", err)
	}
	if _, err := d.Describe(context.Background(),
		entity.ContentPart{Type: entity.PartTypeImage, Base64: testPNGBase64}); err != nil {
		t.Fatalf("Base64 Describe 失败: %v", err)
	}
	if len(fake.inputs) != 2 {
		t.Errorf("URL 与 Base64 应各自触发模型调用, 实际 %d", len(fake.inputs))
	}

	// 同 URL 再调 → 缓存命中，不新增调用。
	if _, err := d.Describe(context.Background(),
		entity.ContentPart{Type: entity.PartTypeImage, URL: path}); err != nil {
		t.Fatalf("同 URL Describe 失败: %v", err)
	}
	if len(fake.inputs) != 2 {
		t.Errorf("同 URL 应缓存命中, 实际模型调用 %d", len(fake.inputs))
	}
}

// TestImageContentKey 校验内容键规则（B-027 增强）：FileHash 优先（跨 URL 内容寻址）、
// Base64 按解码字节 md5、URL 按串兜底；无可描述来源（URL/Base64 皆空）返回空键
// （防失败冷却污染：仅 FileHash 的占位段不生成键）。
func TestImageContentKey(t *testing.T) {
	sum := md5.Sum(mustDecodePNG(t))
	h := hex.EncodeToString(sum[:])
	if got := imageContentKey(entity.ContentPart{Type: entity.PartTypeImage, Base64: testPNGBase64}); got != "md5:"+h {
		t.Errorf("Base64 键应为解码字节 md5, 实际 %q", got)
	}
	if got := imageContentKey(entity.ContentPart{Type: entity.PartTypeImage, URL: "u"}); got != "url:u" {
		t.Errorf("URL 键应按串, 实际 %q", got)
	}
	if got := imageContentKey(entity.ContentPart{Type: entity.PartTypeImage, URL: "https://a/1.png", FileHash: "abc123"}); got != "md5:abc123" {
		t.Errorf("FileHash 应优先于 URL, 实际 %q", got)
	}
	if got := imageContentKey(entity.ContentPart{Type: entity.PartTypeImage, FileHash: "abc123"}); got != "" {
		t.Errorf("仅 FileHash 无来源应返回空键, 实际 %q", got)
	}
	if got := imageContentKey(entity.ContentPart{Type: entity.PartTypeImage}); got != "" {
		t.Errorf("URL/Base64 皆空应返回空键, 实际 %q", got)
	}
}

// TestEinoMediaDescriberFileHashShareAcrossURL 同内容不同 URL + 相同 FileHash → 第二次 Describe
// 命中缓存（B-027 增强·问题1核心）：不再因 URL 串变化重复拉取/调模型（全程无网络，URL 直传）。
func TestEinoMediaDescriberFileHashShareAcrossURL(t *testing.T) {
	sum := md5.Sum(mustDecodePNG(t))
	h := hex.EncodeToString(sum[:])
	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("一只猫。", nil),
	}}
	d := &EinoMediaDescriber{cm: fake, http: &http.Client{}}

	p1 := entity.ContentPart{Type: entity.PartTypeImage, URL: "https://cdn1.example.com/a.png", FileHash: h}
	p2 := entity.ContentPart{Type: entity.PartTypeImage, URL: "https://cdn2.example.com/a.png?term=1&w=100", FileHash: h}

	first, err := d.Describe(context.Background(), p1)
	if err != nil {
		t.Fatalf("首次 Describe 失败: %v", err)
	}
	second, err := d.Describe(context.Background(), p2)
	if err != nil {
		t.Fatalf("二次 Describe 失败: %v", err)
	}
	if first != second {
		t.Errorf("同内容应命中缓存返回一致描述: %q vs %q", first, second)
	}
	if len(fake.inputs) != 1 {
		t.Errorf("同内容不同 URL 应只调模型 1 次, 实际 %d", len(fake.inputs))
	}
	// 唯一一次调用的输入应为 URL 直传（各自的 URL）。
	in := fake.inputs[0]
	parts := in[1].UserInputMultiContent
	if parts[1].Image.URL == nil || *parts[1].Image.URL != p1.URL {
		t.Errorf("首调应直传 p1 URL, 实际 %+v", parts[1].Image)
	}
}

// flakyFirstModel 首次 Generate 报错、二次返回成功的脚本化 ChatModel，用于 URL 直传失败回退测试。
type flakyFirstModel struct {
	calls int
	user  *schema.Message // 最近一次调用的输入（断言回退是否走 base64）
}

var _ model.BaseChatModel = (*flakyFirstModel)(nil)

func (m *flakyFirstModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.calls++
	m.user = input[len(input)-1]
	if m.calls == 1 {
		return nil, errors.New("provider 无法访问远程图片 URL（模拟）")
	}
	return schema.AssistantMessage("一只猫。", nil), nil
}

func (m *flakyFirstModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("flakyFirstModel: Stream 未实现")
}

// TestEinoMediaDescriberURLDirectFallback URL 直传失败 → 回退 base64 重试一次。
// **带 FileHash** 时键无需本地拉图，URL 直传优先：server 应恰好被拉 1 次（仅在直传失败后回退时），
// 第二次调用的 user part 为 base64 透传（内容 = server 返回的 PNG）。
func TestEinoMediaDescriberURLDirectFallback(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(mustDecodePNG(t))
	}))
	defer srv.Close()

	fm := &flakyFirstModel{}
	d := &EinoMediaDescriber{cm: fm, http: &http.Client{}}

	got, err := d.Describe(context.Background(),
		entity.ContentPart{Type: entity.PartTypeImage, URL: srv.URL, FileHash: "deadbeefdeadbeefdeadbeefdeadbeef"})
	if err != nil {
		t.Fatalf("回退后 Describe 应成功: %v", err)
	}
	if got != "一只猫。" {
		t.Errorf("描述文本 = %q，期望回退成功返回", got)
	}
	if hits.Load() != 1 {
		t.Errorf("回退应本地拉图恰好 1 次, 实际 %d", hits.Load())
	}
	if fm.calls != 2 {
		t.Errorf("应模型调用 2 次（直传失败 + 回退）, 实际 %d", fm.calls)
	}
	parts := fm.user.UserInputMultiContent
	if parts[1].Image.Base64Data == nil || *parts[1].Image.Base64Data != testPNGBase64 {
		t.Errorf("回退应透传 base64, 实际 %+v", parts[1].Image)
	}
	if parts[1].Image.MIMEType != "image/png" {
		t.Errorf("回退 mime 应为 image/png, 实际 %q", parts[1].Image.MIMEType)
	}
}

// TestEinoMediaDescriberHTTPNoFileHashFetch 无 FileHash 的 http URL（QQ 图片带时效签名 URL 的典型形态）：
// 先本地拉字节按内容 md5 作缓存键，再走模型。
//   - 拉取成功：描述直接走 base64（字节已在手，不依赖 provider 再拉一次 URL）；
//   - 同图换 URL（签名变化）→ 命中同一内容键，模型只调 1 次（**缓存失效修复的核心断言**）。
func TestEinoMediaDescriberHTTPNoFileHashFetch(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(mustDecodePNG(t))
	}))
	defer srv.Close()

	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("一只猫。", nil),
	}}
	d := &EinoMediaDescriber{cm: fake, http: &http.Client{}}

	// 两次「同图不同 URL」：模拟 QQ 图片 URL 的时效签名逐轮变化。
	p1 := entity.ContentPart{Type: entity.PartTypeImage, URL: srv.URL + "/a.png?term=1&sign=aaa"}
	p2 := entity.ContentPart{Type: entity.PartTypeImage, URL: srv.URL + "/a.png?term=2&sign=bbb"}
	first, err := d.Describe(context.Background(), p1)
	if err != nil {
		t.Fatalf("首次 Describe 失败: %v", err)
	}
	second, err := d.Describe(context.Background(), p2)
	if err != nil {
		t.Fatalf("二次 Describe 失败: %v", err)
	}
	if first != second {
		t.Errorf("同内容异 URL 应命中缓存返回一致描述: %q vs %q", first, second)
	}
	if len(fake.inputs) != 1 {
		t.Errorf("同图不同 URL 应只调模型 1 次, 实际 %d", len(fake.inputs))
	}
	// 首次描述走 base64（预拉取字节），base64 内容应等于 server 返回的 PNG。
	parts := fake.inputs[0][1].UserInputMultiContent
	if parts[1].Image.Base64Data == nil || *parts[1].Image.Base64Data != testPNGBase64 {
		t.Errorf("预拉取后应走 base64 透传, 实际 %+v", parts[1].Image)
	}
	// 缓存键应为内容 md5（fetched_md5），非 URL 串。
	wantKey := "md5:" + md5Hex(mustDecodePNG(t))
	if _, ok := d.cache[wantKey]; !ok {
		t.Errorf("缓存应写入内容键 %q, 实际键集合 %v", wantKey, mapKeys(d.cache))
	}
}

// TestEinoMediaDescriberHTTPNoFileHashCooldown 无 FileHash 的 http URL 失败后记冷却：
// 冷却期内直接失败、**不再本地预拉取**——否则每轮组装都白付一次拉取（不可达图还带 30s 超时）。
// 锁定「冷却判定先于预拉取」的查询顺序。
func TestEinoMediaDescriberHTTPNoFileHashCooldown(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	// 空脚本 fakeChatModel：每次 Generate 均报错 → 强制走「直传失败 → 回退拉图失败」路径。
	d := &EinoMediaDescriber{cm: &fakeChatModel{}, http: &http.Client{}}
	part := entity.ContentPart{Type: entity.PartTypeImage, URL: srv.URL + "/a.png"}

	if _, err := d.Describe(context.Background(), part); err == nil {
		t.Fatal("不可达图片应报错")
	}
	first := hits.Load()
	if first == 0 {
		t.Fatal("首次应尝试本地预拉取")
	}
	if _, err := d.Describe(context.Background(), part); err == nil || !strings.Contains(err.Error(), "失败冷却") {
		t.Fatalf("冷却期内应直接返回失败, 实际 %v", err)
	}
	if hits.Load() != first {
		t.Errorf("冷却期内不应再次拉图, 拉取次数 %d → %d", first, hits.Load())
	}
}

// mapKeys 返回缓存键集合（失败信息可读用）。
func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestEinoMediaDescriberFailCooldown 描述失败记冷却（审查 BUG-3）：同一失败图片在冷却期内
// 直接返回失败，不再触发拉图/模型调用（防每轮组装重试风暴）。
func TestEinoMediaDescriberFailCooldown(t *testing.T) {
	d := &EinoMediaDescriber{cm: &fakeChatModel{}, http: &http.Client{}}
	missing := entity.ContentPart{Type: entity.PartTypeImage, URL: filepath.Join(t.TempDir(), "nope.png")}

	first, err := d.Describe(context.Background(), missing)
	if err == nil || !strings.Contains(err.Error(), "读取本地图片") {
		t.Fatalf("首次应报拉图失败, 实际 %q", err)
	}
	_ = first
	if len(d.fails) != 1 {
		t.Fatalf("失败应记冷却条目, 实际 %d 条", len(d.fails))
	}

	// 冷却期内再调同图 → 直接失败，错误为冷却信息而非重新拉图。
	second, err := d.Describe(context.Background(), missing)
	if err == nil || !strings.Contains(err.Error(), "失败冷却") {
		t.Errorf("冷却期内应直接返回失败（不重新拉图）: %v", err)
	}
	_ = second
}

// TestEinoMediaDescriberNoFailKeyNoCooldown URL/Base64 皆空的 part 无键，失败不记冷却。
func TestEinoMediaDescriberNoFailKeyNoCooldown(t *testing.T) {
	d := &EinoMediaDescriber{cm: &fakeChatModel{}, http: &http.Client{}}
	if _, err := d.Describe(context.Background(), entity.ContentPart{Type: entity.PartTypeImage}); err == nil {
		t.Fatal("URL 与 Base64 均为空应报错")
	}
	if len(d.fails) != 0 {
		t.Errorf("无键失败不应记冷却, 实际 %d 条", len(d.fails))
	}
}

// TestSpikeNapCatImageReachable 验证 NapCat 图片 URL（本机 127.0.0.1 私网地址）bot 侧可达
// （roadmap B-009 ①）。设置 PLUMEBOT_TEST_IMAGE_URL=<NapCat 图片URL> 时执行真实拉取。
func TestSpikeNapCatImageReachable(t *testing.T) {
	url := os.Getenv("PLUMEBOT_TEST_IMAGE_URL")
	if url == "" {
		t.Skip("PLUMEBOT_TEST_IMAGE_URL=<NapCat 图片URL> 时执行真实可达性验证")
	}
	data, _, err := loadImageBytes(context.Background(), &http.Client{Timeout: fetchTimeout},
		entity.ContentPart{Type: entity.PartTypeImage, URL: url})
	if err != nil {
		t.Fatalf("拉取 NapCat 图片失败（bot 侧不可达）: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("拉取到空图片")
	}
	t.Logf("拉取成功，%d 字节", len(data))
}

func TestNewMediaDescriberConfig(t *testing.T) {
	ctx := context.Background()
	base := config.Config{LLM: config.LLMConfig{
		Models: []config.LLMModelConfig{
			{Name: "chat", Model: "deepseek-chat"},
			{Name: "vision", Provider: "openai", BaseURL: "https://api.vision.com/v1", APIKey: "k", Model: "qwen-vl-plus"},
		},
		ChatModel:   "chat",
		VisionModel: "vision",
	}}

	// 合法配置 → 构造可用描述器。
	d, err := NewMediaDescriber(ctx, base)
	if err != nil {
		t.Fatalf("合法配置应构造成功: %v", err)
	}
	if d == nil {
		t.Fatal("构造结果不应为 nil")
	}

	// vision_model 空 → 报错（描述关闭由调用方判空）。
	cfg := base
	cfg.LLM.VisionModel = ""
	if _, err := NewMediaDescriber(ctx, cfg); err == nil {
		t.Error("vision_model 空应报错")
	}
	// vision_model 未指向任何条目 → 报错。
	cfg = base
	cfg.LLM.VisionModel = "nope"
	if _, err := NewMediaDescriber(ctx, cfg); err == nil {
		t.Error("vision_model 未命中条目应报错")
	}
	// vision 条目 model 空 → 报错（不可猜测）。
	cfg = base
	cfg.LLM.Models[1].Model = ""
	if _, err := NewMediaDescriber(ctx, cfg); err == nil {
		t.Error("vision 条目 model 空应报错")
	}
}
