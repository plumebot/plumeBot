package imagecache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheSaveWritesFile(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	data := []byte("hello image")

	path, err := c.Save(data)
	if err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回缓存文件失败: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("缓存内容不符: %q", got)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("应返回绝对路径, 实际 %q", path)
	}
	if !strings.Contains(filepath.ToSlash(path), filepath.ToSlash(filepath.Join(dir, "image_cache"))) {
		t.Errorf("路径应位于缓存目录, 实际 %q", path)
	}
}

func TestCacheSaveIdempotent(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	data := []byte("same content")

	p1, err := c.Save(data)
	if err != nil {
		t.Fatalf("首次 Save 失败: %v", err)
	}
	p2, err := c.Save(data)
	if err != nil {
		t.Fatalf("二次 Save 失败: %v", err)
	}
	if p1 != p2 {
		t.Errorf("同内容应幂等同路径, %q vs %q", p1, p2)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "image_cache"))
	if err != nil {
		t.Fatalf("读缓存目录失败: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("同内容应只落一个文件, 实际 %d", len(entries))
	}
}

func TestCacheSaveCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	c := New(dir)
	if _, err := c.Save([]byte("x")); err != nil {
		t.Fatalf("Save 应自动创建目录: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "image_cache")); err != nil {
		t.Errorf("image_cache 目录应存在: %v", err)
	}
}
