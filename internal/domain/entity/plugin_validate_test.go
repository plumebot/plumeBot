package entity

import "testing"

// TestValidatePluginResult 表驱动校验指令集：合法全量 / 各类非法。
func TestValidatePluginResult(t *testing.T) {
	tests := []struct {
		name string
		res  PluginResult
		want bool
	}{
		{"空结果合法", PluginResult{}, true},
		{"合法全量", PluginResult{
			Reply: &Reply{
				Quote: true,
				At:    "sender",
				Segments: []Segment{
					{Kind: SegmentKindText, Text: "hi"},
					{Kind: SegmentKindImage, Image: &ImageRef{Source: ImageSourcePath, Value: "/tmp/a.png"}},
					{Kind: SegmentKindFace, Face: 178},
				},
			},
			Actions: []GroupAction{
				{Op: GroupOpMute, Target: "123", Duration: 60},
				{Op: GroupOpUnmute, Target: "123"},
				{Op: GroupOpKick, Target: "123"},
				{Op: GroupOpSetCard, Target: "123", Card: "新名片"},
			},
		}, true},
		{"未知段类型", PluginResult{Reply: &Reply{Segments: []Segment{{Kind: "video"}}}}, false},
		{"回复无段", PluginResult{Reply: &Reply{Segments: nil}}, false},
		{"image 段缺引用", PluginResult{Reply: &Reply{Segments: []Segment{{Kind: SegmentKindImage}}}}, false},
		{"未知图片来源", PluginResult{Reply: &Reply{Segments: []Segment{{Kind: SegmentKindImage, Image: &ImageRef{Source: "blob", Value: "x"}}}}}, false},
		{"image 值空", PluginResult{Reply: &Reply{Segments: []Segment{{Kind: SegmentKindImage, Image: &ImageRef{Source: ImageSourceURL, Value: ""}}}}}, false},
		{"face 无 id", PluginResult{Reply: &Reply{Segments: []Segment{{Kind: SegmentKindFace}}}}, false},
		{"未知动作", PluginResult{Actions: []GroupAction{{Op: "ban", Target: "1"}}}, false},
		{"动作缺目标", PluginResult{Actions: []GroupAction{{Op: GroupOpKick}}}, false},
		{"mute 无时长", PluginResult{Actions: []GroupAction{{Op: GroupOpMute, Target: "1"}}}, false},
		{"set_card 缺名片", PluginResult{Actions: []GroupAction{{Op: GroupOpSetCard, Target: "1"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidatePluginResult(tt.res); (err == nil) != tt.want {
				t.Fatalf("ValidatePluginResult() = %v，期望 valid=%v", err, tt.want)
			}
		})
	}
}
