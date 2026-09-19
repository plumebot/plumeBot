package onebot

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"

	"plumebot/internal/domain/entity"
	"plumebot/internal/infra/imagecache"
)

func TestFormatID(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"int64 非零", int64(123), "123"},
		{"int64 零值", int64(0), ""},
		{"string", "abc", "abc"},
		{"空字符串", "", ""},
		{"其它类型", 3.14, ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatID(tc.in); got != tc.want {
				t.Errorf("formatID(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestToMessageGroup(t *testing.T) {
	ev := &zero.Event{
		PostType:    "message",
		MessageType: "group",
		MessageID:   int64(1001),
		GroupID:     int64(2002),
		UserID:      int64(3003),
		Time:        1700000000,
		Message:     message.Message{message.Text("你好呀")},
	}
	m, ok := toMessage(ev, nil)
	if !ok {
		t.Fatal("群聊消息应被接受")
	}
	if m.MessageID != "1001" || m.GroupID != "2002" || m.UserID != "3003" {
		t.Errorf("ID 转换错误: %+v", m)
	}
	if m.PlainText() != "你好呀" {
		t.Errorf("PlainText = %q, want %q", m.PlainText(), "你好呀")
	}
	if m.Timestamp != 1700000000 || m.MessageType != "group" {
		t.Errorf("Timestamp/MessageType 错误: %+v", m)
	}
}

func TestToMessagePrivateGroupIDEmpty(t *testing.T) {
	ev := &zero.Event{
		PostType:    "message",
		MessageType: "private",
		MessageID:   int64(1001),
		UserID:      int64(3003),
		Message:     message.Message{message.Text("hi")},
	}
	m, ok := toMessage(ev, nil)
	if !ok {
		t.Fatal("私聊消息应被接受")
	}
	if m.GroupID != "" {
		t.Errorf("私聊 GroupID 应为空，实际 %q", m.GroupID)
	}
}

func TestToMessageRejects(t *testing.T) {
	// 非 message 事件
	if _, ok := toMessage(&zero.Event{PostType: "notice"}, nil); ok {
		t.Error("notice 事件不应被 toMessage 接受")
	}
	// 不支持的 message_type
	if _, ok := toMessage(&zero.Event{PostType: "message", MessageType: "guild"}, nil); ok {
		t.Error("guild 消息不应被接受")
	}
}

// TestToMessageMentioned 校验 toMessage 透传 ZeroBot IsToMe → Message.Mentioned（P5-001）。
func TestToMessageMentioned(t *testing.T) {
	// 群聊被 @：IsToMe=true → Mentioned=true。
	ev := &zero.Event{
		PostType:    "message",
		MessageType: "group",
		MessageID:   int64(1),
		GroupID:     int64(2),
		UserID:      int64(3),
		Message:     message.Message{message.Text("hi")},
		IsToMe:      true,
	}
	m, ok := toMessage(ev, nil)
	if !ok || !m.Mentioned {
		t.Errorf("群聊被 @ 时 Mentioned 应为 true, 实际 %+v", m)
	}
	// 群聊未 @：IsToMe=false → Mentioned=false。
	ev.IsToMe = false
	m, _ = toMessage(ev, nil)
	if m.Mentioned {
		t.Error("群聊未 @ 时 Mentioned 应为 false")
	}
	// 私聊恒 true（ZeroBot 对私聊 IsToMe 恒 true，P5-001 语义：私聊必回复）。
	ev = &zero.Event{
		PostType:    "message",
		MessageType: "private",
		MessageID:   int64(1),
		UserID:      int64(3),
		Message:     message.Message{message.Text("hi")},
		IsToMe:      true,
	}
	m, _ = toMessage(ev, nil)
	if !m.Mentioned {
		t.Error("私聊时 Mentioned 应为 true")
	}
}

// TestToMessageSenderName 校验 toMessage 从 ev.Sender 提取展示名：群名片(card) 优先、回落昵称(nickname)。
// 两者皆空（无 sender/昵称为空）→ 空串（组装方回落 QQ 号）。
func TestToMessageSenderName(t *testing.T) {
	base := &zero.Event{
		PostType:    "message",
		MessageType: "group",
		MessageID:   int64(1),
		GroupID:     int64(2),
		UserID:      int64(3),
		Message:     message.Message{message.Text("hi")},
	}
	// 有群名片 → 群名片优先。
	ev := *base
	ev.Sender = &zero.User{ID: 3, NickName: "昵称A", Card: "群名片B"}
	m, ok := toMessage(&ev, nil)
	if !ok || m.SenderName != "群名片B" {
		t.Errorf("SenderName = %q, want 群名片优先（群名片B）", m.SenderName)
	}
	// 无群名片 → 回落昵称。
	ev.Sender = &zero.User{ID: 3, NickName: "昵称A"}
	m, _ = toMessage(&ev, nil)
	if m.SenderName != "昵称A" {
		t.Errorf("SenderName = %q, want 回落昵称A", m.SenderName)
	}
	// 全空 / nil Sender → 空串（回落 QQ 号）。
	ev.Sender = &zero.User{ID: 3}
	m, _ = toMessage(&ev, nil)
	if m.SenderName != "" {
		t.Errorf("无昵称 SenderName = %q, want 空串", m.SenderName)
	}
	ev.Sender = nil
	m, _ = toMessage(&ev, nil)
	if m.SenderName != "" {
		t.Errorf("nil Sender SenderName = %q, want 空串", m.SenderName)
	}
	// 私聊同样提取 nickname。
	ev = *base
	ev.MessageType = "private"
	ev.UserID = 3
	ev.Sender = &zero.User{ID: 3, NickName: "私聊昵称"}
	m, _ = toMessage(&ev, nil)
	if m.SenderName != "私聊昵称" {
		t.Errorf("私聊 SenderName = %q, want 私聊昵称", m.SenderName)
	}
}

// TestToParts 校验段数组 → Parts 映射（text/at/image/record/video/file + 未知段兜底转文本）。
func TestToParts(t *testing.T) {
	cases := []struct {
		name  string
		in    message.Message
		cache *imagecache.Cache // nil = base64 闭合关闭（旧行为）
		want  []entity.ContentPart
	}{
		{
			name: "纯文本",
			in:   message.Message{message.Text("hi")},
			want: []entity.ContentPart{{Type: entity.PartTypeText, Text: "hi"}},
		},
		{
			name: "文本+@他人+图片",
			in: message.Message{
				message.Text("看看"),
				message.At(12345),
				// NapCat 收图时 url 字段在 data 中；message.Image() 只设 file，故显式构造。
				message.Segment{Type: "image", Data: map[string]string{"url": "https://example.com/cat.png"}},
			},
			want: []entity.ContentPart{
				{Type: entity.PartTypeText, Text: "看看"},
				{Type: entity.PartTypeAt, Text: "[@12345]"},
				{Type: entity.PartTypeImage, URL: "https://example.com/cat.png"},
			},
		},
		{
			name: "@全体",
			in:   message.Message{message.AtAll()},
			want: []entity.ContentPart{{Type: entity.PartTypeAt, Text: "[@全体]"}},
		},
		{
			name: "语音/视频/文件段",
			in: message.Message{
				message.Segment{Type: "record", Data: map[string]string{"url": "http://x/a.amr"}},
				message.Segment{Type: "video", Data: map[string]string{"url": "http://x/b.mp4"}},
				message.Segment{Type: "file", Data: map[string]string{"url": "http://x/c.pdf"}},
			},
			want: []entity.ContentPart{
				{Type: entity.PartTypeAudio, URL: "http://x/a.amr"},
				{Type: entity.PartTypeVideo, URL: "http://x/b.mp4"},
				{Type: entity.PartTypeFile, URL: "http://x/c.pdf"},
			},
		},
		{
			name:  "图片仅base64且无缓存时留空占位",
			in:    message.Message{message.Image("base64://aGVsbG8=")},
			cache: nil,
			want:  []entity.ContentPart{{Type: entity.PartTypeImage, URL: ""}},
		},
		{
			name: "回复/表情段兜底转文本",
			in: message.Message{
				message.Text("正文"),
				message.Reply(999),
				message.Face(178),
			},
			want: []entity.ContentPart{
				{Type: entity.PartTypeText, Text: "正文"},
				{Type: entity.PartTypeText, Text: "未知内容"}, // reply 段走 default 兜底
				{Type: entity.PartTypeText, Text: "178"},  // face 段转表情 id 文本
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toParts(tc.in, tc.cache)
			if len(got) != len(tc.want) {
				t.Fatalf("toParts 长度 = %d, want %d, got %+v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("toParts[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestToPartsBase64Closure 校验 base64:// 图片入链闭合：解码落盘 data/image_cache/<md5>，
// URL 存本地路径（内容 = 解码字节），使 base64 图片可被描述。
func TestToPartsBase64Closure(t *testing.T) {
	cache := imagecache.New(t.TempDir())
	parts := toParts(message.Message{message.Image("base64://aGVsbG8=")}, cache)
	if len(parts) != 1 || parts[0].URL == "" {
		t.Fatalf("base64 图片应落盘闭合 URL, 实际 %+v", parts)
	}
	if parts[0].Type != entity.PartTypeImage {
		t.Errorf("应为 image part, 实际 %+v", parts[0])
	}
	data, err := os.ReadFile(parts[0].URL)
	if err != nil {
		t.Fatalf("读取缓存文件失败: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("缓存内容应为解码字节 hello, 实际 %q", data)
	}
}

// TestToPartsBase64ClosurePlus 校验 base64 内容含 '+'（base64 字母表内常见字符）不被
// URL 解码误当空格（QueryUnescape 的坑；实现用 PathUnescape 只解 %XX）。
func TestToPartsBase64ClosurePlus(t *testing.T) {
	cache := imagecache.New(t.TempDir())
	b64 := base64.StdEncoding.EncodeToString([]byte{0xFB}) // 0xFB 的 base64 为 "+w=="，含 '+'
	if !strings.Contains(b64, "+") {
		t.Fatalf("测试数据应含 '+', 实际 %q", b64)
	}

	parts := toParts(message.Message{message.Image("base64://" + b64)}, cache)
	if len(parts) != 1 || parts[0].URL == "" {
		t.Fatalf("含 + 的 base64 应落盘闭合 URL, 实际 %+v", parts)
	}
	data, err := os.ReadFile(parts[0].URL)
	if err != nil {
		t.Fatalf("读取缓存文件失败: %v", err)
	}
	if len(data) != 1 || data[0] != 0xFB {
		t.Errorf("缓存内容应为字节 0xFB, 实际 %x", data)
	}
}

func TestToEventNotice(t *testing.T) {
	raw := `{"notice_type":"group_increase"}`
	ev := &zero.Event{
		PostType:   "notice",
		DetailType: "group_increase",
		GroupID:    int64(1),
		UserID:     int64(2),
		Time:       1700000001,
		RawEvent:   gjson.Result{Raw: raw},
	}
	e, ok := toEvent(ev)
	if !ok {
		t.Fatal("notice 事件应被接受")
	}
	if e.Type != entity.EventNotice {
		t.Errorf("Type = %v, want EventNotice", e.Type)
	}
	if e.SubType != "group_increase" || e.GroupID != "1" || e.UserID != "2" || e.Timestamp != 1700000001 {
		t.Errorf("字段转换错误: %+v", e)
	}
	if e.RawJSON != raw {
		t.Errorf("RawJSON = %q, want %q", e.RawJSON, raw)
	}
}

func TestToEventTypesAndReject(t *testing.T) {
	if e, ok := toEvent(&zero.Event{PostType: "request"}); !ok || e.Type != entity.EventRequest {
		t.Errorf("request 事件应转为 EventRequest，got %+v", e)
	}
	if e, ok := toEvent(&zero.Event{PostType: "meta_event"}); !ok || e.Type != entity.EventMeta {
		t.Errorf("meta_event 事件应转为 EventMeta，got %+v", e)
	}
	if _, ok := toEvent(&zero.Event{PostType: "message"}); ok {
		t.Error("message 事件不应被 toEvent 接受")
	}
}
