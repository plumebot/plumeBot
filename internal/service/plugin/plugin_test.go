package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"plumebot/internal/domain"
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
	b, err := json.Marshal(metadata{Name: name, Path: path, Commands: commands})
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
	if _, err := svc.Dispatch(context.Background(), entity.PluginRequest{Proto: 1, Command: "nope"}); !errors.Is(err, domain.ErrNotFound) {
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
