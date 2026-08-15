package ai

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestEinoMediaDescriberDescribeHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(mustDecodePNG(t))
	}))
	defer srv.Close()
	runDescribeAndAssert(t, entity.ContentPart{Type: entity.PartTypeImage, URL: srv.URL})
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

// TestImageContentKey 校验内容键规则：Base64 按 md5 内容寻址，URL 按串。
func TestImageContentKey(t *testing.T) {
	if got := imageContentKey(entity.ContentPart{Type: entity.PartTypeImage, Base64: testPNGBase64}); got == "" || got[:4] != "b64:" {
		t.Errorf("Base64 键应带 b64: 前缀, 实际 %q", got)
	}
	if got := imageContentKey(entity.ContentPart{Type: entity.PartTypeImage, URL: "u"}); got != "url:u" {
		t.Errorf("URL 键应按串, 实际 %q", got)
	}
	if got := imageContentKey(entity.ContentPart{Type: entity.PartTypeImage}); got != "" {
		t.Errorf("URL/Base64 皆空应返回空键, 实际 %q", got)
	}
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
