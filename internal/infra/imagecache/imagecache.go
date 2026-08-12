// Package imagecache 提供图片内容缓存落盘：base64:// 入链图片解码后的本地缓存
// （data/image_cache/<md5>），URL 存路径闭合（base64 不入库原则不变）。
package imagecache

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Cache 将图片字节按内容哈希落盘到 <dir>/image_cache/，返回绝对路径。
// 内容级去重：同内容幂等（已存在则直接返回既有路径，不覆盖）。
// 注意：文件只增不清（无引用计数/淘汰），见 roadmap B-022。
type Cache struct {
	dir string
}

// New 创建 Cache，dir 为数据根目录（如 "data"），缓存子目录为 <dir>/image_cache。
func New(dir string) *Cache {
	return &Cache{dir: dir}
}

// Save 写入图片字节并返回绝对路径（持久化无歧义，重启/换 cwd 仍可读）。
// 自动创建缓存目录；写入失败返回 error（调用方回退占位，不断链）。
func (c *Cache) Save(data []byte) (string, error) {
	dir := filepath.Join(c.dir, "image_cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建图片缓存目录失败: %w", err)
	}

	sum := md5.Sum(data)
	path := filepath.Join(dir, hex.EncodeToString(sum[:]))
	if _, err := os.Stat(path); err == nil {
		// 已存在：内容级去重，直接返回既有路径。
		return filepath.Abs(path)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("写入图片缓存 %s 失败: %w", path, err)
	}
	return filepath.Abs(path)
}
