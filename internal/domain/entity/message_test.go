package entity

import (
	"strings"
	"testing"
)

// msgParts 构造 Message 的多段内容。
func forLLMMsg(parts ...ContentPart) Message {
	return Message{MessageID: "m1", GroupID: "g1", Parts: parts}
}

func TestForLLM(t *testing.T) {
	tests := []struct {
		name  string
		parts []ContentPart
		want  string
	}{
		{
			name:  "纯文本",
			parts: []ContentPart{{Type: PartTypeText, Text: "你好"}},
			want:  "你好",
		},
		{
			name:  "文本+at 转文本",
			parts: []ContentPart{{Type: PartTypeText, Text: "你好"}, {Type: PartTypeAt, Text: "[@123]"}},
			want:  "你好[@123]",
		},
		{
			name:  "at 全体",
			parts: []ContentPart{{Type: PartTypeAt, Text: "[@全体]"}},
			want:  "[@全体]",
		},
		{
			name:  "图片无描述占位",
			parts: []ContentPart{{Type: PartTypeImage, URL: "http://x/1.png"}},
			want:  "[图片]",
		},
		{
			name:  "图片有描述注入",
			parts: []ContentPart{{Type: PartTypeImage, URL: "http://x/1.png", Description: "一只猫"}},
			want:  "（图片：一只猫）",
		},
		{
			name: "语音视频文件标记",
			parts: []ContentPart{
				{Type: PartTypeAudio}, {Type: PartTypeVideo}, {Type: PartTypeFile},
			},
			want: "[语音][视频][文件]",
		},
		{
			name: "多段顺序拼接",
			parts: []ContentPart{
				{Type: PartTypeText, Text: "看这"},
				{Type: PartTypeImage, Description: "猫"},
				{Type: PartTypeText, Text: "可爱吗"},
				{Type: PartTypeImage, URL: "http://x/2.png"},
			},
			want: "看这（图片：猫）可爱吗[图片]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := forLLMMsg(tt.parts...).ForLLM()
			if got != tt.want {
				t.Errorf("ForLLM() = %q, want %q", got, tt.want)
			}
		})
	}
}

// 有 Description 时 ForLLM 与 Render 分歧（Render 恒为 [图片]，ForLLM 注入描述）。
func TestForLLMDivergesFromRenderOnDescription(t *testing.T) {
	m := forLLMMsg(ContentPart{Type: PartTypeImage, Description: "一只猫"})
	if m.ForLLM() == m.Render() {
		t.Errorf("ForLLM %q 与 Render %q 不应相同（描述应注入而非占位）", m.ForLLM(), m.Render())
	}
	if !strings.Contains(m.ForLLM(), "一只猫") {
		t.Errorf("ForLLM 应包含图片描述，实际 %q", m.ForLLM())
	}
	if m.Render() != "[图片]" {
		t.Errorf("Render 应恒为 [图片]，实际 %q", m.Render())
	}
}

// 无描述时 ForLLM 与 Render 一致（同为 [图片] 占位）。
func TestForLLMAgreesWithRenderWithoutDescription(t *testing.T) {
	m := forLLMMsg(ContentPart{Type: PartTypeImage, URL: "http://x/1.png"})
	if m.ForLLM() != m.Render() {
		t.Errorf("无描述时 ForLLM %q 应与 Render %q 一致", m.ForLLM(), m.Render())
	}
}
