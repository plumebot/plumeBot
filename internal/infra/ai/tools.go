package ai

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
)

// ToolsRegistry 是工具注册表（注入式实例，无全局状态）。
// 本期内置注册表为空（仅机制），P3-004/P4-001 起通过 Register 注册具体工具。
type ToolsRegistry struct {
	tools map[string]tool.BaseTool
}

// NewToolsRegistry 创建空工具注册表。
func NewToolsRegistry() *ToolsRegistry {
	return &ToolsRegistry{tools: make(map[string]tool.BaseTool)}
}

// Register 注册工具；重名返回错误（防止静默覆盖）。
func (r *ToolsRegistry) Register(name string, t tool.BaseTool) error {
	if _, ok := r.tools[name]; ok {
		return fmt.Errorf("工具 %q 已注册", name)
	}
	r.tools[name] = t
	return nil
}

// EnabledTools 按启用列表过滤出工具（保持列表顺序，重复名去重）。
// 启用列表含未注册名 → 返回错误（错误信息含已注册列表）；空列表 → 返回 nil。
func (r *ToolsRegistry) EnabledTools(enabled []string) ([]tool.BaseTool, error) {
	if len(enabled) == 0 {
		return nil, nil
	}

	seen := make(map[string]bool, len(enabled))
	out := make([]tool.BaseTool, 0, len(enabled))
	for _, name := range enabled {
		if seen[name] {
			continue
		}
		seen[name] = true

		t, ok := r.tools[name]
		if !ok {
			return nil, fmt.Errorf("工具 %q 未注册（已注册：%s）", name, registeredToolNames(r.tools))
		}
		out = append(out, t)
	}
	return out, nil
}

// Names 返回已注册工具名的排序列表（启动日志用，架构 §17.2 cmd/bot main）。
func (r *ToolsRegistry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// registeredToolNames 返回已注册工具名的排序、逗号分隔列表（用于错误提示）。
func registeredToolNames(m map[string]tool.BaseTool) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
