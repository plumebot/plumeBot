// Package main 是示例插件：实现 echo 命令，返回文本 + 图片段与一个禁言动作。
// 插件遵循指令集协议（架构 §8.6）：零权限、只声明意图，宿主统一执行。
//
// 本示例演示第三方插件的独立编写方式（方案 A）：只依赖插件 SDK（独立 module
// github.com/plumebot/plumebot-sdk，plugin 包含协议类型 + go-plugin 接线），
// 不 import 宿主 internal 包；独立编译成 exe 后放到 plugins/<name>/ 并配
// plugin.json 即可被宿主发现。
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/plumebot/plumebot-sdk/plugin"
)

// example 实现 plugin.Plugin：/echo 参数... 回显参数，并演示多段回复与群管理动作。
type example struct{}

func (e *example) Execute(_ context.Context, req plugin.PluginRequest) (plugin.PluginResult, error) {
	if req.Command != "echo" {
		return plugin.PluginResult{}, fmt.Errorf("unknown command: %s", req.Command)
	}
	return plugin.PluginResult{
		Reply: &plugin.Reply{
			Quote: true,
			At:    "sender",
			Segments: []plugin.Segment{
				{Kind: plugin.SegmentKindText, Text: "echo: " + strings.Join(req.Args, " ")},
				{Kind: plugin.SegmentKindImage, Image: &plugin.ImageRef{Source: plugin.ImageSourcePath, Value: "plugins/example/cache.png"}},
			},
		},
		Actions: []plugin.GroupAction{
			{Op: plugin.GroupOpMute, Target: req.Session.UserID, Duration: 60},
		},
	}, nil
}

func main() {
	plugin.Serve(&example{})
}
