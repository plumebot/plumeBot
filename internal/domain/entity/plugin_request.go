package entity

// PluginRequest 是宿主 → 插件的一次命令调用载荷（插件协议，见架构 §8.6）。
type PluginRequest struct {
	Proto     int      // 协议版本，当前为 1
	Command   string   // 命中的命令名（不含 /）
	Args      []string // 命令参数
	Session   Session  // 会话身份：GroupID 空 = 私聊
	MessageID string   // 触发消息 ID，供引用回复
}
