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
	Close()
}

// ClientFactory 由 main 注入，绑定具体实现（plugin-sdk 的 NewClient），避免 service import SDK。
type ClientFactory func(exePath string) (PluginClient, error)

// metadata 对应 plugins/<name>/plugin.json（架构 §8.4 插件发现）。
type metadata struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`        // 相对插件目录的可执行文件
	Commands    []string `json:"commands"`    // 支持的命令名（不含 /）
	Description string   `json:"description"` // 一句话描述（/help 使用，可空）
}

// handle 记录单个插件进程的客户端句柄与元数据。
type handle struct {
	plugin      PluginClient
	name        string
	description string
	commands    []string
}

// PluginService 负责插件发现与命令路由编排。
type PluginService struct {
	factory  ClientFactory
	commands map[string]*handle // 命令 → 插件句柄
	byName   map[string]*handle // 插件名 → 插件句柄（/help 用）
	handles  []*handle          // 全部插件句柄，供 Close
}

// NewPluginService 创建 PluginService，注入插件进程工厂。
func NewPluginService(factory ClientFactory) *PluginService {
	return &PluginService{
		factory:  factory,
		commands: make(map[string]*handle),
		byName:   make(map[string]*handle),
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

	h := &handle{
		plugin:      client,
		name:        meta.Name,
		description: meta.Description,
		commands:    meta.Commands,
	}
	s.handles = append(s.handles, h)
	if _, exists := s.byName[meta.Name]; exists {
		logger.Warn("插件名重复，首个生效", logger.S("name", meta.Name))
	} else {
		s.byName[meta.Name] = h
	}
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
		return entity.PluginResult{}, entity.ErrNotFound
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
func (s *PluginService) Close() {
	for _, h := range s.handles {
		h.plugin.Close()
	}
}

// Help 生成 /help 帮助文本（宿主内置命令，不经命令路由）：
//   - 无参数：列出全部已加载插件（名称 + 描述）；
//   - 指定参数：按插件名（忽略大小写）或命令名定位插件，命中后先向插件请求用法
//     （Command="help" 直达插件进程，插件可实现专属用法文本），插件不认则回退到
//     元数据描述 + 命令列表；未命中返回「未找到插件：xxx」。
func (s *PluginService) Help(ctx context.Context, req entity.PluginRequest) string {
	if len(req.Args) == 0 {
		return s.listPluginsHelp()
	}
	h := s.lookupPlugin(req.Args[0])
	if h == nil {
		return "未找到插件：" + req.Args[0]
	}
	if txt := s.pluginHelp(ctx, h, req); txt != "" {
		return txt
	}
	return s.pluginInfoText(h)
}

// listPluginsHelp 列出全部插件。
func (s *PluginService) listPluginsHelp() string {
	var sb strings.Builder
	sb.WriteString("可用插件：")
	if len(s.handles) == 0 {
		sb.WriteString("\n（暂无插件）")
		return sb.String()
	}
	for _, h := range s.handles {
		sb.WriteString("\n- ")
		sb.WriteString(h.name)
		if h.description != "" {
			sb.WriteString("：")
			sb.WriteString(h.description)
		}
	}
	return sb.String()
}

// lookupPlugin 按插件名（忽略大小写）或命令名定位插件。
func (s *PluginService) lookupPlugin(name string) *handle {
	if h, ok := s.byName[name]; ok {
		return h
	}
	for n, h := range s.byName {
		if strings.EqualFold(n, name) {
			return h
		}
	}
	return s.commands[name]
}

// pluginHelp 向插件请求用法（Command="help"），返回其文本回复；失败/空则回退。
func (s *PluginService) pluginHelp(ctx context.Context, h *handle, req entity.PluginRequest) string {
	helpReq := entity.PluginRequest{
		Proto:     req.Proto,
		Command:   "help",
		Args:      req.Args[1:],
		Session:   req.Session,
		MessageID: req.MessageID,
	}
	res, err := h.plugin.Execute(ctx, helpReq)
	if err != nil {
		return ""
	}
	return replyPlainText(res.Reply)
}

// pluginInfoText 回退：插件名 + 描述 + 命令列表。
func (s *PluginService) pluginInfoText(h *handle) string {
	var sb strings.Builder
	sb.WriteString(h.name)
	if h.description != "" {
		sb.WriteString("：")
		sb.WriteString(h.description)
	}
	if len(h.commands) > 0 {
		sb.WriteString("\n命令：")
		for i, cmd := range h.commands {
			if i > 0 {
				sb.WriteString(" / ")
			}
			sb.WriteString("/")
			sb.WriteString(cmd)
		}
	}
	return sb.String()
}

// replyPlainText 提取 Reply 中的文本段。
func replyPlainText(r *entity.Reply) string {
	if r == nil {
		return ""
	}
	var sb strings.Builder
	for _, seg := range r.Segments {
		if seg.Kind == entity.SegmentKindText {
			sb.WriteString(seg.Text)
		}
	}
	return sb.String()
}
