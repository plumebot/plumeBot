package domain

import (
	"context"

	"plumebot/internal/domain/entity"
)

// Plugin 执行命名插件命令。插件为独立子进程（stdio 通信），
// 只声明意图（entity.PluginResult，协议类型为 plugin-sdk/entity 的类型别名），
// 宿主是唯一执行者（架构 §8.6）。
type Plugin interface {
	Execute(ctx context.Context, req entity.PluginRequest) (entity.PluginResult, error)
}
