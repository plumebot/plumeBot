package entity

import sdkentity "github.com/plumebot/plumebot-sdk/plugin"

// 插件协议类型（Reply/Segment/GroupAction 等）单一事实来源已迁至 plugin-sdk/entity
//（方案 A：方便第三方独立编写插件）。以下为类型别名与常量透传，宿主业务代码
// 继续经 entity 引用，gob 序列化类型名与插件侧一致（同一 SDK module 路径）。

type (
	PluginResult = sdkentity.PluginResult
	Reply        = sdkentity.Reply
	Segment      = sdkentity.Segment
	ImageRef     = sdkentity.ImageRef
	GroupAction  = sdkentity.GroupAction
	SegmentKind  = sdkentity.SegmentKind
	ImageSource  = sdkentity.ImageSource
	GroupOp      = sdkentity.GroupOp
)

const (
	SegmentKindText  = sdkentity.SegmentKindText
	SegmentKindImage = sdkentity.SegmentKindImage
	SegmentKindFace  = sdkentity.SegmentKindFace

	ImageSourcePath   = sdkentity.ImageSourcePath
	ImageSourceURL    = sdkentity.ImageSourceURL
	ImageSourceBase64 = sdkentity.ImageSourceBase64

	GroupOpMute    = sdkentity.GroupOpMute
	GroupOpUnmute  = sdkentity.GroupOpUnmute
	GroupOpKick    = sdkentity.GroupOpKick
	GroupOpSetCard = sdkentity.GroupOpSetCard
)
