package entity

import sdkentity "github.com/plumebot/plumebot-sdk/plugin"

// PluginRequest 是宿主 → 插件的一次命令调用载荷（插件协议，见架构 §8.6）。
// 协议类型单一事实来源已迁至 plugin-sdk/entity（方案 A：方便第三方独立编写插件，
// 不依赖宿主 internal）；本处为类型别名，宿主业务代码继续经 entity 引用，
// gob 序列化类型名与插件侧一致（宿主 replace 到同一 SDK module 路径）。
type PluginRequest = sdkentity.PluginRequest
