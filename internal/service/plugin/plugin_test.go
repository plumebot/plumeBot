package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// TestMain 初始化全局 logger（error 级，避免 loadPlugin 的 Info/Warn 打 nil 指针）。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "plumebot-plugin-test-logs")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	logger.Init(logger.Config{Level: "error", Dir: dir})
	os.Exit(m.Run())
}

// fakePlugin 是内存中的假插件客户端。
type fakePlugin struct {
	execute func(context.Context, entity.PluginRequest) (entity.PluginResult, error)
	closed  bool
}

func (f *fakePlugin) Execute(ctx context.Context, req entity.PluginRequest) (entity.PluginResult, error) {
	if f.execute != nil {
		return f.execute(ctx, req)
	}
	return entity.PluginResult{}, nil
}

func (f *fakePlugin) Close() { f.closed = true }

// writePluginJSON 在 dir/<name>/ 下构造 plugin.json 测试元数据。
func writePluginJSON(t *testing.T, dir, name, path string, commands []string) {
	t.Helper()
	writePluginJSONDesc(t, dir, name, path, "", commands)
}

// writePluginJSONDesc 构造带描述与命令的 plugin.json。
func writePluginJSONDesc(t *testing.T, dir, name, path, description string, commands []string) {
	t.Helper()
	b, err := json.Marshal(metadata{Name: name, Path: path, Commands: commands, Description: description})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, "plugin.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverAndDispatch(t *testing.T) {
	dir := t.TempDir()
	writePluginJSON(t, dir, "echo", "echo.exe", []string{"echo"})

	var launched []string
	svc := NewPluginService(func(exePath string) (PluginClient, error) {
		launched = append(launched, exePath)
		return &fakePlugin{}, nil
	})
	if err := svc.Discover(dir); err != nil {
		t.Fatalf("Discover 失败: %v", err)
	}
	wantExe := filepath.Join(dir, "echo", "echo.exe")
	if len(launched) != 1 || launched[0] != wantExe {
		t.Fatalf("拉起路径 %v，期望 %q", launched, wantExe)
	}

	if _, err := svc.Dispatch(context.Background(), entity.PluginRequest{Proto: 1, Command: "echo", Args: []string{"hi"}}); err != nil {
		t.Fatalf("Dispatch 失败: %v", err)
	}
	if _, err := svc.Dispatch(context.Background(), entity.PluginRequest{Proto: 1, Command: "nope"}); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("未知命令应返回 ErrNotFound，实际 %v", err)
	}
}

func TestDiscoverMissingDir(t *testing.T) {
	svc := NewPluginService(func(string) (PluginClient, error) { return &fakePlugin{}, nil })
	if err := svc.Discover(filepath.Join(t.TempDir(), "nonexistent")); err != nil {
		t.Fatalf("插件目录不存在应放行: %v", err)
	}
}

func TestDiscoverMalformedPluginSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad", "plugin.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	var launched int
	svc := NewPluginService(func(string) (PluginClient, error) {
		launched++
		return &fakePlugin{}, nil
	})
	if err := svc.Discover(dir); err != nil {
		t.Fatalf("Discover 不应因坏插件失败: %v", err)
	}
	if launched != 0 {
		t.Fatalf("坏插件不应被拉起，实际 %d", launched)
	}
}

func TestDispatchValidatesResult(t *testing.T) {
	dir := t.TempDir()
	writePluginJSON(t, dir, "bad", "bad.exe", []string{"bad"})
	svc := NewPluginService(func(string) (PluginClient, error) {
		return &fakePlugin{execute: func(context.Context, entity.PluginRequest) (entity.PluginResult, error) {
			return entity.PluginResult{Actions: []entity.GroupAction{{Op: "ban", Target: "1"}}}, nil
		}}, nil
	})
	if err := svc.Discover(dir); err != nil {
		t.Fatalf("Discover 失败: %v", err)
	}
	if _, err := svc.Dispatch(context.Background(), entity.PluginRequest{Proto: 1, Command: "bad"}); err == nil {
		t.Fatal("非法指令集应被校验拦截")
	}
}

func TestDuplicateCommandFirstWins(t *testing.T) {
	dir := t.TempDir()
	writePluginJSON(t, dir, "a", "a.exe", []string{"dup"})
	writePluginJSON(t, dir, "b", "b.exe", []string{"dup"})

	var launched []string
	svc := NewPluginService(func(exePath string) (PluginClient, error) {
		launched = append(launched, exePath)
		return &fakePlugin{}, nil
	})
	if err := svc.Discover(dir); err != nil {
		t.Fatalf("Discover 失败: %v", err)
	}
	if len(launched) != 2 {
		t.Fatalf("两个插件都应拉起，实际 %d", len(launched))
	}
	if svc.commands["dup"] == nil {
		t.Fatal("命令 dup 应存在")
	}
}

// newHelpSvc 构造带指定插件元数据与执行行为的 PluginService（Discover 一个插件目录）。
func newHelpSvc(t *testing.T, name, path, description string, commands []string, execute func(context.Context, entity.PluginRequest) (entity.PluginResult, error)) *PluginService {
	t.Helper()
	dir := t.TempDir()
	writePluginJSONDesc(t, dir, name, path, description, commands)
	svc := NewPluginService(func(string) (PluginClient, error) {
		return &fakePlugin{execute: execute}, nil
	})
	if err := svc.Discover(dir); err != nil {
		t.Fatalf("Discover 失败: %v", err)
	}
	return svc
}

// TestHelpList /help 无参数应列出插件名与描述。
func TestHelpList(t *testing.T) {
	svc := newHelpSvc(t, "echo", "echo.exe", "回显演示", []string{"echo"}, nil)
	text := svc.Help(context.Background(), entity.PluginRequest{Proto: 1, Command: "help"})
	for _, want := range []string{"可用插件", "echo", "回显演示"} {
		if !strings.Contains(text, want) {
			t.Fatalf("缺少 %q：\n%s", want, text)
		}
	}
}

// TestHelpListEmpty 无插件时 /help 应提示暂无插件。
func TestHelpListEmpty(t *testing.T) {
	svc := NewPluginService(func(string) (PluginClient, error) { return &fakePlugin{}, nil })
	text := svc.Help(context.Background(), entity.PluginRequest{Proto: 1, Command: "help"})
	if !strings.Contains(text, "暂无插件") {
		t.Fatalf("无插件时应提示暂无插件：%s", text)
	}
}

// TestHelpPluginUsage 插件实现 Command="help" 时返回插件专属用法。
func TestHelpPluginUsage(t *testing.T) {
	svc := newHelpSvc(t, "echo", "echo.exe", "回显演示", []string{"echo"}, func(ctx context.Context, req entity.PluginRequest) (entity.PluginResult, error) {
		if req.Command != "help" {
			return entity.PluginResult{}, entity.ErrNotFound
		}
		return entity.PluginResult{Reply: &entity.Reply{Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: "echo 专属用法"}}}}, nil
	})
	text := svc.Help(context.Background(), entity.PluginRequest{Proto: 1, Command: "help", Args: []string{"echo"}})
	if text != "echo 专属用法" {
		t.Fatalf("/help echo = %q, 期望插件用法文本", text)
	}
}

// TestHelpPluginFallback 插件不认 help 时回退到元数据描述 + 命令列表。
func TestHelpPluginFallback(t *testing.T) {
	svc := newHelpSvc(t, "echo", "echo.exe", "回显演示", []string{"echo"}, func(context.Context, entity.PluginRequest) (entity.PluginResult, error) {
		return entity.PluginResult{}, entity.ErrNotFound
	})
	text := svc.Help(context.Background(), entity.PluginRequest{Proto: 1, Command: "help", Args: []string{"echo"}})
	for _, want := range []string{"echo", "回显演示", "/echo"} {
		if !strings.Contains(text, want) {
			t.Fatalf("回退文本缺少 %q：\n%s", want, text)
		}
	}
}

// TestHelpPluginNotFound 未知插件名返回未找到提示。
func TestHelpPluginNotFound(t *testing.T) {
	svc := newHelpSvc(t, "echo", "echo.exe", "回显演示", []string{"echo"}, nil)
	text := svc.Help(context.Background(), entity.PluginRequest{Proto: 1, Command: "help", Args: []string{"nope"}})
	if !strings.Contains(text, "未找到插件：nope") {
		t.Fatalf("未知插件提示错误：%s", text)
	}
}

// TestHelpByCommandName 按命令名也能定位到所属插件。
func TestHelpByCommandName(t *testing.T) {
	svc := newHelpSvc(t, "demo", "demo.exe", "演示", []string{"ping"}, func(ctx context.Context, req entity.PluginRequest) (entity.PluginResult, error) {
		if req.Command != "help" {
			return entity.PluginResult{}, entity.ErrNotFound
		}
		return entity.PluginResult{Reply: &entity.Reply{Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: "ping 用法"}}}}, nil
	})
	text := svc.Help(context.Background(), entity.PluginRequest{Proto: 1, Command: "help", Args: []string{"ping"}})
	if text != "ping 用法" {
		t.Fatalf("/help ping 应命中插件 demo 用法，got %q", text)
	}
}

// TestHelpCaseInsensitiveName 插件名查找忽略大小写。
func TestHelpCaseInsensitiveName(t *testing.T) {
	svc := newHelpSvc(t, "echo", "echo.exe", "回显演示", []string{"echo"}, nil)
	text := svc.Help(context.Background(), entity.PluginRequest{Proto: 1, Command: "help", Args: []string{"ECHO"}})
	if !strings.Contains(text, "回显演示") {
		t.Fatalf("忽略大小写查插件应命中：%s", text)
	}
}
