package logger

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// capture 安装一个写内存缓冲的全局 logger，返回缓冲与恢复函数。
// 仅测试用：直接替换包级 zl（同包可访问），defer restore 避免污染其他用例。
func capture(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	core := zapcore.NewCore(enc, zapcore.AddSync(buf), zapcore.DebugLevel)
	old := zl
	zl = zap.New(core)
	return buf, func() { zl = old }
}

// TestContextInjectsFields 验证 Context 注入的派生 logger 携带 trace_id 等字段。
func TestContextInjectsFields(t *testing.T) {
	buf, restore := capture(t)
	defer restore()

	ctx := Context(context.Background(), S("trace_id", "group:123456"))
	From(ctx).Info("命中", S("k", "v"))

	got := buf.String()
	if !strings.Contains(got, `"trace_id":"group:123456"`) {
		t.Fatalf("派生 logger 未携带 trace_id，输出：%s", got)
	}
	if !strings.Contains(got, `"k":"v"`) {
		t.Fatalf("调用点字段丢失，输出：%s", got)
	}
	if !strings.Contains(got, `"msg":"命中"`) {
		t.Fatalf("消息体丢失，输出：%s", got)
	}
}

// TestFromFallsBackToGlobal 验证未注入时 From 回退全局 logger（渐进接入 trace_id 的前提）。
func TestFromFallsBackToGlobal(t *testing.T) {
	buf, restore := capture(t)
	defer restore()

	From(context.Background()).Info("回退全局")

	if !strings.Contains(buf.String(), "回退全局") {
		t.Fatalf("From 未回退到全局 logger，输出：%s", buf.String())
	}
}

// TestFromWithoutInitIsSafe 验证未 Init（zl==nil）时 From/Context 不返回 nil 且不 panic。
func TestFromWithoutInitIsSafe(t *testing.T) {
	old := zl
	zl = nil
	defer func() { zl = old }()

	l := From(context.Background())
	if l == nil {
		t.Fatal("From 不应返回 nil（调用方无需判空）")
	}
	l.Info("no-op")

	ctx := Context(context.Background(), S("trace_id", "private:10001"))
	if got := From(ctx); got == nil {
		t.Fatal("未 Init 时 Context 注入的 logger 不应为 nil")
	} else {
		got.Warn("no-op")
	}
}

// TestDirMatchesDefaults 验证 Dir 返回与 withDefaults 一致的默认目录（~/.plumebot/logs）。
func TestDirMatchesDefaults(t *testing.T) {
	want := withDefaults(Config{}).Dir
	if got := Dir(); got != want {
		t.Fatalf("Dir()=%q，期望 %q", got, want)
	}
	if !strings.HasSuffix(filepath.ToSlash(want), ".plumebot/logs") {
		t.Fatalf("默认日志目录不符：%q", want)
	}
}
