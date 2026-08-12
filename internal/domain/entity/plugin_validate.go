// 本文件提供插件指令集（PluginResult）的结构校验。
// 校验为只读的结构规则（枚举合法、必填字段非空），非业务逻辑；
// 业务规则（如禁言时长上限、权限判定）归 domain/service 层。
package entity

import "fmt"

// 枚举合法判断：协议类型定义为强类型常量，但 Go 允许强转任意字符串，
// 类型系统并不能兜底，这里做白名单校验。
func validSegmentKind(k SegmentKind) bool {
	switch k {
	case SegmentKindText, SegmentKindImage, SegmentKindFace:
		return true
	}
	return false
}

func validImageSource(s ImageSource) bool {
	switch s {
	case ImageSourcePath, ImageSourceURL, ImageSourceBase64:
		return true
	}
	return false
}

func validGroupOp(op GroupOp) bool {
	switch op {
	case GroupOpMute, GroupOpUnmute, GroupOpKick, GroupOpSetCard:
		return true
	}
	return false
}

// ValidatePluginResult 校验插件返回的指令集：枚举合法、必填字段非空。
// 返回带位置的描述性 error（调用方仅记录），无对应哨兵。
func ValidatePluginResult(r PluginResult) error {
	if r.Reply != nil {
		if err := validateReply(*r.Reply); err != nil {
			return err
		}
	}
	for i, a := range r.Actions {
		if err := validateGroupAction(a); err != nil {
			return fmt.Errorf("actions[%d]: %v", i, err)
		}
	}
	return nil
}

func validateReply(r Reply) error {
	if len(r.Segments) == 0 {
		return fmt.Errorf("reply: 至少需要 1 段")
	}
	for i, seg := range r.Segments {
		if err := validateSegment(seg); err != nil {
			return fmt.Errorf("reply.segments[%d]: %v", i, err)
		}
	}
	return nil
}

func validateSegment(seg Segment) error {
	if !validSegmentKind(seg.Kind) {
		return fmt.Errorf("未知段类型 %q", seg.Kind)
	}
	switch seg.Kind {
	case SegmentKindImage:
		if seg.Image == nil {
			return fmt.Errorf("image 段缺少 image 引用")
		}
		if !validImageSource(seg.Image.Source) {
			return fmt.Errorf("未知图片来源 %q", seg.Image.Source)
		}
		if seg.Image.Value == "" {
			return fmt.Errorf("image 值不能为空")
		}
	case SegmentKindFace:
		if seg.Face <= 0 {
			return fmt.Errorf("face 段缺少有效表情 id")
		}
	}
	return nil
}

func validateGroupAction(a GroupAction) error {
	if !validGroupOp(a.Op) {
		return fmt.Errorf("未知动作 %q", a.Op)
	}
	if a.Target == "" {
		return fmt.Errorf("缺少目标用户")
	}
	switch a.Op {
	case GroupOpMute:
		if a.Duration <= 0 {
			return fmt.Errorf("mute 时长必须 > 0")
		}
	case GroupOpSetCard:
		if a.Card == "" {
			return fmt.Errorf("set_card 缺少名片内容")
		}
	}
	return nil
}
