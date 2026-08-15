// Package entity 定义领域层公共实体，所有结构体均为纯数据结构，
// 不包含任何业务逻辑或外部依赖；仅允许只读的结构校验/派生视图函数（见 plugin_validate.go、message.go）。
package entity

import "strings"

// Message 表示一条聊天消息的核心字段。
// 内容以多段 ContentPart 承载（含多模态与 @），纯文本/展示文本经 PlainText/Render 派生。
type Message struct {
	MessageID   string        // 消息唯一 ID
	GroupID     string        // 群 ID（私聊时为空）
	UserID      string        // 发送者 QQ 号
	Parts       []ContentPart // 消息内容段（text/at/image/audio/video/file）
	Timestamp   int64         // Unix 时间戳（秒）
	MessageType string        // 消息类型：group / private
	Mentioned   bool          // 是否点名 bot：私聊恒 true；群聊被 @（at-self 段已被 ZeroBot 剥离）为 true。
	// 来源：ZeroBot 事件 IsToMe（P5-001）。不落 SQLite（触发判断只发生在实时事件上，窗口内存保留）。
}

// SessionKey 返回消息归属的会话键：群聊=GroupID，私聊="private:"+UserID。
// 避免空 GroupID 的私聊互相串窗/串状态（domain.Memory 会话键、control 运行态 stateKey 统一使用）。
// 判断以 GroupID 为准：entity.Message 语义「私聊时 GroupID 为空」。
func (m Message) SessionKey() string {
	if m.GroupID != "" {
		return m.GroupID
	}
	return "private:" + m.UserID
}

// PlainText 返回消息的纯文本视图：仅拼接 text 段（不含 @、图片等标记）。
// 语义与 OneBot 段数组的 ExtractPlainText 一致，供敏感词/命令/短消息忽略等纯文本场景使用。
func (m Message) PlainText() string {
	var sb strings.Builder
	for _, p := range m.Parts {
		if p.Type == PartTypeText {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// Render 返回消息的展示文本：text 段原文 + @/图片/语音/视频/文件 等标记。
// 供日志、压缩摘要等需要感知媒体存在的场景使用。
func (m Message) Render() string {
	var sb strings.Builder
	for _, p := range m.Parts {
		switch p.Type {
		case PartTypeText, PartTypeAt:
			sb.WriteString(p.Text)
		case PartTypeImage:
			sb.WriteString("[图片]")
		case PartTypeAudio:
			sb.WriteString("[语音]")
		case PartTypeVideo:
			sb.WriteString("[视频]")
		case PartTypeFile:
			sb.WriteString("[文件]")
		}
	}
	return sb.String()
}

// ForLLM 返回消息的 LLM 文本视图（P6-001 B-026）：
//
//	text 段原文；at 段 p.Text（"[@qq]"/"[@全体]"）；
//	image 段 Description 非空 → "（图片：<desc>）"，空 → "[图片]"；
//	audio/video/file → "[语音]"/"[视频]"/"[文件]"。
//
// 压缩 buildLevel1UserPrompt 与 prompt 组装共用（B-028：at 段同样直接转文本，不保留 at part）；
// Render() 留给日志。
func (m Message) ForLLM() string {
	var sb strings.Builder
	for _, p := range m.Parts {
		switch p.Type {
		case PartTypeText, PartTypeAt:
			sb.WriteString(p.Text)
		case PartTypeImage:
			if p.Description != "" {
				sb.WriteString("（图片：")
				sb.WriteString(p.Description)
				sb.WriteString("）")
			} else {
				sb.WriteString("[图片]")
			}
		case PartTypeAudio:
			sb.WriteString("[语音]")
		case PartTypeVideo:
			sb.WriteString("[视频]")
		case PartTypeFile:
			sb.WriteString("[文件]")
		}
	}
	return sb.String()
}
