package onebot

import (
	"crypto/md5"
	"encoding/hex"
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
		SenderName:  senderDisplayName(ev.Sender),
		Parts:       toParts(ev.Message, cache),
		Timestamp:   ev.Time,
		MessageType: ev.MessageType,
		Mentioned:   ev.IsToMe, // P5-001：私聊恒 true；群聊被 @（at-self 段已剥离）为 true
	}, true
}

// senderDisplayName 从事件 sender 提取展示名（供组装渲染，P6 联调修复「上下文只有 QQ 号」）：
// 群名片 card 优先，回落昵称 nickname；两者皆空（无 sender/匿名/无昵称）→ 空串，
// 由组装方回落到 QQ 号（见 service/memory speakerText）。
func senderDisplayName(u *zero.User) string {
	if u == nil {
		return ""
	}
	if u.Card != "" {
		return u.Card
	}
	if u.NickName != "" {
		return u.NickName
	}
	return ""
}

// toParts 将 ZeroBot 消息段数组转换为领域层 ContentPart 列表。
// 映射规则：
//   - text → text；
//   - at → at（文本标记 "[@qq]"/"[@全体]"；at-self 已被 ZeroBot 预处理裁掉，只剩 @他人/@全体）；
//   - image → image（优先 url 字段；依 file/file_md5 提取内容 FileHash（一图一值，供描述
//     缓存跨 URL 去重，B-027 增强）；无 url 且为 base64:// 段时解码落盘 data/image_cache/<md5>、
//     以本地路径闭合 URL、算字节 md5 入 FileHash，使 base64 图片可被描述；
//     解码/落盘失败或 cache 为 nil → 留空占位）；
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
			if file := seg.Data["file"]; strings.HasPrefix(file, "base64://") {
				// base64:// 入链：解码算内容 md5（base64 已在内存，解码算 hash 免费）→ FileHash；
				// URL 空且 cache 非 nil → 落盘闭合（原逻辑，缓存文件名 = 同内容 md5，天然去重）。
				if data, err := decodeBase64File(file); err == nil {
					p.FileHash = md5Hex(data)
					if p.URL == "" && cache != nil {
						if path, err := cache.Save(data); err == nil {
							p.URL = path // 落盘后只存本地路径，base64 不入库
						}
					}
				} // 解码/落盘失败 → 保持占位（[图片]），不断链
			} else {
				// file 优先（NapCat 收图通常即内容 md5，可能带 .image 后缀）；file 为非 md5 的
				// 文件名/垃圾值时，规范层 file_md5 兜底，绝不误判。
				if h := normalizeFileHash(seg.Data["file"]); h != "" {
					p.FileHash = h
				} else if h := normalizeFileHash(seg.Data["file_md5"]); h != "" {
					p.FileHash = h
				}
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

// normalizeFileHash 提取图片内容 MD5（规范化为 32 位小写 hex）。识别 OneBot image 段
// file/file_md5 的典型形态，垃圾值返回 ""（不误判）：
//   - "<32hex>"：内容 md5 本身（NapCat 收图 file 常见）；
//   - "<32hex>.image" / "<32hex>.pic"：md5 + NapCat 文件名后缀（需剥离）；
//   - 全路径（如 C:\qq\cache\<md5>.image）：先取 basename 再剥后缀；
//   - "base64://..."（发送方段）：非 hex，返回 ""（由调用方解码→字节 md5 路径）。
func normalizeFileHash(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "base64://") {
		return ""
	}
	// 取纯文件名（兼容 / 与 \ 两种分隔符，不依赖 filepath 的运行时 GOOS）。
	if i := strings.LastIndexAny(v, "/\\"); i >= 0 {
		v = v[i+1:]
	}
	v = strings.TrimSuffix(v, ".image")
	v = strings.TrimSuffix(v, ".pic")
	if len(v) != 32 {
		return ""
	}
	b, err := hex.DecodeString(v)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(b) // 规范化小写（大小写混合输入也归一）
}

// md5Hex 计算字节内容 md5（32 位小写 hex）。与 imagecache.Save 文件名算法一致。
func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
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
