// Package main 是示例插件：实现 echo 命令，返回文本 + 图片段与一个禁言动作。
// 插件遵循指令集协议（架构 §8.6）：零权限、只声明意图，宿主统一执行。
package main

import (
	"context"
	"strings"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/infra/plugin_exe"
)

// example 实现 domain.Plugin：/echo 参数... 回显参数，并演示多段回复与群管理动作。
type example struct{}

func (e *example) Execute(_ context.Context, req entity.PluginRequest) (entity.PluginResult, error) {
	if req.Command != "echo" {
		return entity.PluginResult{}, domain.ErrNotFound
	}
	return entity.PluginResult{
		Reply: &entity.Reply{
			Quote: true,
			At:    "sender",
			Segments: []entity.Segment{
				{Kind: entity.SegmentKindText, Text: "echo: " + strings.Join(req.Args, " ")},
				{Kind: entity.SegmentKindImage, Image: &entity.ImageRef{Source: entity.ImageSourcePath, Value: "plugins/example/cache.png"}},
			},
		},
		Actions: []entity.GroupAction{
			{Op: entity.GroupOpMute, Target: req.Session.UserID, Duration: 60},
		},
	}, nil
}

func main() {
	plugin_exe.Serve(&example{})
}
