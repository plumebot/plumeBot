package onebot

import (
	"path/filepath"
	"testing"

	zero "github.com/wdvxdr1123/ZeroBot"

	"plumebot/internal/domain/entity"
)

// TestReplyToChainPlainText 纯文本回复 → 单 text 段。
func TestReplyToChainPlainText(t *testing.T) {
	ev := &zero.Event{MessageID: 123, UserID: 456}
	chain := replyToChain(entity.Reply{
		Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: "你好"}},
	}, ev)
	if len(chain) != 1 {
		t.Fatalf("纯文本应只有 1 段, 实际 %d: %+v", len(chain), chain)
	}
	if chain[0].Type != "text" || chain[0].Data["text"] != "你好" {
		t.Errorf("text 段错误: %+v", chain[0])
	}
}

// TestReplyToChainQuoteAndAt 引用 + @ 触发者 + 文本 → reply/at/text 三段。
func TestReplyToChainQuoteAndAt(t *testing.T) {
	ev := &zero.Event{MessageID: 123, UserID: 456}
	chain := replyToChain(entity.Reply{
		Quote:    true,
		At:       "sender",
		Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: "hi"}},
	}, ev)
	if len(chain) != 3 {
		t.Fatalf("引用+@+文本应为 3 段, 实际 %d: %+v", len(chain), chain)
	}
	if chain[0].Type != "reply" || chain[0].Data["id"] != "123" {
		t.Errorf("reply 段错误（应引用触发消息 id）: %+v", chain[0])
	}
	if chain[1].Type != "at" || chain[1].Data["qq"] != "456" {
		t.Errorf("at 段错误（应 @ 触发者）: %+v", chain[1])
	}
	if chain[2].Type != "text" {
		t.Errorf("text 段错误: %+v", chain[2])
	}
}

// TestReplyToChainAtSpecificID At 为具体 user_id → @ 该用户。
func TestReplyToChainAtSpecificID(t *testing.T) {
	ev := &zero.Event{MessageID: 123, UserID: 456}
	chain := replyToChain(entity.Reply{
		At:       "789",
		Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: "hi"}},
	}, ev)
	if len(chain) != 2 || chain[0].Type != "at" || chain[0].Data["qq"] != "789" {
		t.Errorf("at 指定 id 段错误: %+v", chain)
	}
}

// TestReplyToChainAtInvalidIDIgnored At 非法 → 忽略不 @，不报错。
func TestReplyToChainAtInvalidIDIgnored(t *testing.T) {
	ev := &zero.Event{MessageID: 123, UserID: 456}
	chain := replyToChain(entity.Reply{
		At:       "not-a-number",
		Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: "hi"}},
	}, ev)
	if len(chain) != 1 || chain[0].Type != "text" {
		t.Errorf("非法 at id 应忽略, 实际 %+v", chain)
	}
}

// TestReplyToChainAtSenderUserIDZero 防御：事件 UserID 为 0 时 At="sender" 应跳过不 @
// （message.At(0) 会退化为 AtAll 即 @全体，绝不允许误 @ 全群）。
func TestReplyToChainAtSenderUserIDZero(t *testing.T) {
	ev := &zero.Event{MessageID: 123, UserID: 0}
	chain := replyToChain(entity.Reply{
		At:       "sender",
		Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: "hi"}},
	}, ev)
	for _, seg := range chain {
		if seg.Type == "at" {
			t.Fatalf("UserID=0 时不应产生 at 段（防 AtAll）: %+v", chain)
		}
	}
}

// TestReplyToChainFaceAndImage face/image 段转换（image path → file:///）。
func TestReplyToChainFaceAndImage(t *testing.T) {
	ev := &zero.Event{MessageID: 123, UserID: 456}
	chain := replyToChain(entity.Reply{
		Segments: []entity.Segment{
			{Kind: entity.SegmentKindFace, Face: 14},
			{Kind: entity.SegmentKindImage, Image: &entity.ImageRef{Source: entity.ImageSourcePath, Value: `C:\img\x.png`}},
		},
	}, ev)
	if len(chain) != 2 {
		t.Fatalf("face+image 应为 2 段, 实际 %d: %+v", len(chain), chain)
	}
	if chain[0].Type != "face" || chain[0].Data["id"] != "14" {
		t.Errorf("face 段错误: %+v", chain[0])
	}
	want := "file:///" + filepath.ToSlash(`C:\img\x.png`)
	if chain[1].Type != "image" || chain[1].Data["file"] != want {
		t.Errorf("image path 段错误: %+v, want file=%q", chain[1], want)
	}
}

// TestReplyToChainImageURLAndBase64 image url/base64 来源转换。
func TestReplyToChainImageURLAndBase64(t *testing.T) {
	ev := &zero.Event{MessageID: 123, UserID: 456}
	chain := replyToChain(entity.Reply{
		Segments: []entity.Segment{
			{Kind: entity.SegmentKindImage, Image: &entity.ImageRef{Source: entity.ImageSourceURL, Value: "https://x/y.png"}},
			{Kind: entity.SegmentKindImage, Image: &entity.ImageRef{Source: entity.ImageSourceBase64, Value: "AAAA"}},
		},
	}, ev)
	if chain[0].Type != "image" || chain[0].Data["file"] != "https://x/y.png" {
		t.Errorf("image url 段错误: %+v", chain[0])
	}
	if chain[1].Type != "image" || chain[1].Data["file"] != "base64://AAAA" {
		t.Errorf("image base64 段错误: %+v", chain[1])
	}
}
