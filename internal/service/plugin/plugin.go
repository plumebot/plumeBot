package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// PluginClient 是 service 对单个插件进程句柄的抽象：可执行命令 + 可关闭。
type PluginClient interface {
	domain.Plugin
	Close() error
}

// ClientFactory 由 main 注入，绑定具体实现（infra/plugin_exe），避免 service import infra。
type ClientFactory func(exePath string) (PluginClient, error)

// metadata 对应 plugins/<name>/plugin.json（架构 §8.4 插件发现）。
type metadata struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`     // 相对插件目录的可执行文件
	Commands    []string `json:"commands"` // 支持的命令名（不含 /）
	Description string   `json:"description"`
}

// handle 记录单个插件进程的客户端句柄。
type handle struct {
	plugin PluginClient
}

// PluginService 负责插件发现与命令路由编排。
type PluginService struct {
	factory  ClientFactory
	commands map[string]*handle // 命令 → 插件句柄
	handles  []*handle          // 全部插件句柄，供 Close
}

// NewPluginService 创建 PluginService，注入插件进程工厂。
func NewPluginService(factory ClientFactory) *PluginService {
	return &PluginService{
		factory:  factory,
		commands: make(map[string]*handle),
	}
}

// Discover 扫描 rootDir 下的 plugins/<name>/plugin.json，拉起各插件进程并建立命令路由。
// 目录不存在视为无插件；单个插件加载失败仅告警跳过（不让坏插件阻断 bot 启动）。
func (s *PluginService) Discover(rootDir string) error {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("插件目录不存在，跳过", logger.S("dir", rootDir))
			return nil
		}
		return fmt.Errorf("扫描插件目录 %s: %w", rootDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := s.loadPlugin(filepath.Join(rootDir, e.Name())); err != nil {
			logger.Warn("插件加载失败，已跳过", logger.S("dir", e.Name()), logger.Err(err))
		}
	}
	return nil
}

// loadPlugin 读取单个插件目录的 plugin.json，解析相对 exe 路径后拉起进程。
func (s *PluginService) loadPlugin(dir string) error {
	metaPath := filepath.Join(dir, "plugin.json")
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("读取 %s: %w", metaPath, err)
	}
	var meta metadata
	if err := json.Unmarshal(b, &meta); err != nil {
		return fmt.Errorf("解析 %s: %w", metaPath, err)
	}
	if meta.Name == "" || meta.Path == "" || len(meta.Commands) == 0 {
		return fmt.Errorf("%s: plugin.json 缺少 name/path/commands", metaPath)
	}

	exePath := meta.Path
	if !filepath.IsAbs(exePath) {
		exePath = filepath.Join(dir, meta.Path)
	}
	client, err := s.factory(exePath)
	if err != nil {
		return fmt.Errorf("拉起插件 %s: %w", meta.Name, err)
	}

	h := &handle{plugin: client}
	s.handles = append(s.handles, h)
	for _, cmd := range meta.Commands {
		if _, exists := s.commands[cmd]; exists {
			logger.Warn("插件命令重复，首个生效", logger.S("command", cmd), logger.S("plugin", meta.Name))
			continue
		}
		s.commands[cmd] = h
	}
	logger.Info("插件已加载",
		logger.S("name", meta.Name),
		logger.S("exe", exePath),
		logger.S("commands", strings.Join(meta.Commands, ",")),
	)
	return nil
}

// Dispatch 按 req.Command 路由到对应插件执行，并校验返回的指令集。
func (s *PluginService) Dispatch(ctx context.Context, req entity.PluginRequest) (entity.PluginResult, error) {
	h, ok := s.commands[req.Command]
	if !ok {
		return entity.PluginResult{}, domain.ErrNotFound
	}
	if req.Proto == 0 {
		req.Proto = 1
	}
	res, err := h.plugin.Execute(ctx, req)
	if err != nil {
		return entity.PluginResult{}, err
	}
	if err := entity.ValidatePluginResult(res); err != nil {
		return entity.PluginResult{}, err
	}
	return res, nil
}

// Close 停止全部插件进程。
func (s *PluginService) Close() error {
	for _, h := range s.handles {
		if err := h.plugin.Close(); err != nil {
			logger.Warn("插件关闭失败", logger.Err(err))
		}
	}
	return nil
}
