package onebot

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"

	"plumebot/internal/domain/entity"
	"plumebot/internal/infra/imagecache"
	"plumebot/pkg/base64util"
)

// toMessage 将 ZeroBot 消息事件转换为领域层 entity.Message。
// 仅接受群聊/私聊消息事件，其余返回 false。cache 用于 base64:// 图片入链闭合（可为 nil 关闭）。
func toMessage(ev *zero.Event, cache *imagecache.Cache) (entity.Message, bool) {
	if ev.PostType != "message" {
		return entity.Message{}, false
	}
	if ev.MessageType != "group" && ev.MessageType != "private" {
		return entity.Message{}, false
	}
	return entity.Message{
		MessageID:   formatID(ev.MessageID),
		GroupID:     formatID(ev.GroupID),
		UserID:      formatID(ev.UserID),
		Parts:       toParts(ev.Message, cache),
		Timestamp:   ev.Time,
		MessageType: ev.MessageType,
		Mentioned:   ev.IsToMe, // P5-001：私聊恒 true；群聊被 @（at-self 段已剥离）为 true
	}, true
}

// toParts 将 ZeroBot 消息段数组转换为领域层 ContentPart 列表。
// 映射规则：
//   - text → text；
//   - at → at（文本标记 "[@qq]"/"[@全体]"；at-self 已被 ZeroBot 预处理裁掉，只剩 @他人/@全体）；
//   - image → image（优先 url 字段；无 url 且为 base64:// 段时解码落盘 data/image_cache/<md5>、
//     以本地路径闭合 URL，使 base64 图片可被描述；解码/落盘失败或 cache 为 nil → 留空占位）；
//   - record/video/file → 对应类型（url 为空则留空占位）；
//   - face → text（表情 id 文本，保留进纯文本视图）；
//   - 其余未知段（reply/forward/json/xml/music 等）→ text 占位「未知内容」（不静默丢弃，
//     保证纯文本视图不丢内容；引用回复解析等留待后续，见 roadmap B-021）。
func toParts(ev message.Message, cache *imagecache.Cache) []entity.ContentPart {
	parts := make([]entity.ContentPart, 0, len(ev))
	for _, seg := range ev {
		switch seg.Type {
		case "text":
			parts = append(parts, entity.ContentPart{Type: entity.PartTypeText, Text: seg.Data["text"]})
		case "at":
			qq := seg.Data["qq"]
			label := "[@" + qq + "]"
			if qq == "all" {
				label = "[@全体]"
			}
			parts = append(parts, entity.ContentPart{Type: entity.PartTypeAt, Text: label})
		case "image":
			p := entity.ContentPart{Type: entity.PartTypeImage, URL: seg.Data["url"]}
			if p.URL == "" && cache != nil {
				if data, err := decodeBase64File(seg.Data["file"]); err == nil {
					if path, err := cache.Save(data); err == nil {
						p.URL = path // 落盘后只存本地路径，base64 不入库
					}
				} // 解码/落盘失败 → 保持占位（[图片]），不断链
			}
			parts = append(parts, p)
		case "record":
			parts = append(parts, entity.ContentPart{Type: entity.PartTypeAudio, URL: seg.Data["url"]})
		case "video":
			parts = append(parts, entity.ContentPart{Type: entity.PartTypeVideo, URL: seg.Data["url"]})
		case "file":
			parts = append(parts, entity.ContentPart{Type: entity.PartTypeFile, URL: seg.Data["url"]})
		case "face":
			parts = append(parts, entity.ContentPart{Type: entity.PartTypeText, Text: seg.Data["id"]})
		default:
			parts = append(parts, entity.ContentPart{Type: entity.PartTypeText, Text: "未知内容"})
		}
	}
	return parts
}

// decodeBase64File 解码 OneBot base64:// 图片段内容为原始字节。
// 用 PathUnescape 只还原 %XX 转义（不动 base64 字母表内的 '+'，避免被当空格误解码），
// 失败则按原样解码；标准 base64 解码失败再试 RawStdEncoding（无填充）。
func decodeBase64File(file string) ([]byte, error) {
	if !strings.HasPrefix(file, "base64://") {
		return nil, errors.New("非 base64:// 图片段")
	}
	data := strings.TrimPrefix(file, "base64://")
	if unescaped, err := url.PathUnescape(data); err == nil {
		data = unescaped
	}
	// Std/RawStd 兜底逻辑统一在 pkg/base64util（B-030）。
	return base64util.Decode(data)
}

// toEvent 将 ZeroBot 通知/请求/元事件转换为领域层 entity.Event。
func toEvent(ev *zero.Event) (entity.Event, bool) {
	var t entity.EventType
	switch ev.PostType {
	case "notice":
		t = entity.EventNotice
	case "request":
		t = entity.EventRequest
	case "meta_event":
		t = entity.EventMeta
	default:
		return entity.Event{}, false
	}
	return entity.Event{
		Type:      t,
		SubType:   ev.DetailType,
		GroupID:   formatID(ev.GroupID),
		UserID:    formatID(ev.UserID),
		Timestamp: ev.Time,
		RawJSON:   ev.RawEvent.Raw,
	}, true
}

// formatID 将 ZeroBot 的 int64/string 类型 ID 统一转为字符串。
// 零值（0/空）归一化为空字符串，保证非群/无 ID 场景下实体字段为空。
func formatID(v any) string {
	switch id := v.(type) {
	case int64:
		if id == 0 {
			return ""
		}
		return strconv.FormatInt(id, 10)
	case string:
		return id
	default:
		return ""
	}
}
