// Package entity 是插件协议的 wire 类型（单一事实来源，见架构 §8.6）。
//
// 宿主与插件都引用本包：第三方插件只需依赖 plugin-sdk 即可独立编写，无需 import
// 宿主 internal 包。协议经 net/rpc + gob 序列化，类型名 = 本包路径 + 类型名，
// 因此宿主与插件必须引用同一 module 路径（宿主内 replace 到本地目录即可）。
package entity

// Session 是一次插件调用/Agent 推理的会话身份：当前群 + 当前说话人。
// GroupID 空 = 私聊（与 member_facts 约定一致）。纯数据；宿主在其 domain/entity
// 以类型别名复用，WithSession/SessionFrom 等 ctx 注入逻辑留在宿主侧。
type Session struct {
	GroupID string // 群 ID（私聊时为空）
	UserID  string // 当前说话人 QQ 号
}
