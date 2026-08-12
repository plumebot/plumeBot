package entity

// PluginResult 是插件 → 宿主的指令集：插件零权限、只声明意图，宿主是唯一执行者。
type PluginResult struct {
	Reply   *Reply        // 单条回复（多段混排）
	Actions []GroupAction // 附加动作（群管理等）
}

// Reply 表示插件希望宿主发送的一条回复。
type Reply struct {
	Quote    bool      // 引用触发消息
	At       string    // "" = 不 @；"sender" = @ 触发者；否则为具体 user_id
	Segments []Segment // 至少 1 段
}

// Segment 是回复的一条内容段，多段可混排（文本/图片/表情）。
type Segment struct {
	Kind  SegmentKind // text | image | face
	Text  string      // Kind==text 时的文本
	Image *ImageRef   // Kind==image 时必填
	Face  int         // Kind==face 时的 QQ 表情 id
}

// ImageRef 描述一张图片的来源。
type ImageRef struct {
	Source ImageSource // path | url | base64
	Value  string      // 按 Source 解释：本地路径 / URL / base64 数据
}

// GroupAction 是插件请求的群管理动作（对齐 domain.GroupManager 能力集，见架构 §8.6）。
type GroupAction struct {
	Op       GroupOp // mute | unmute | kick | set_card
	Target   string  // 目标 user_id，非空
	Duration int     // Op==mute 时的禁言时长（秒），>0
	Card     string  // Op==set_card 时的名片
}

// SegmentKind 是回复段类型。
type SegmentKind string

const (
	SegmentKindText  SegmentKind = "text"
	SegmentKindImage SegmentKind = "image"
	SegmentKindFace  SegmentKind = "face"
)

// ImageSource 是图片来源类型。
type ImageSource string

const (
	ImageSourcePath   ImageSource = "path"
	ImageSourceURL    ImageSource = "url"
	ImageSourceBase64 ImageSource = "base64"
)

// GroupOp 是群管理动作类型。
type GroupOp string

const (
	GroupOpMute    GroupOp = "mute"
	GroupOpUnmute  GroupOp = "unmute"
	GroupOpKick    GroupOp = "kick"
	GroupOpSetCard GroupOp = "set_card"
)
