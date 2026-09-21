package onebot

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// botSender 是 domain.Sender 的 onebot 实现（B-003）：持有当前事件 *zero.Ctx，
// Send 将结构化 entity.Reply 转换为 OneBot 消息段后 ctx.SendChain 发送到事件来源
// （群回群、私聊回私聊，与 ctx.Send 同源语义）。
type botSender struct {
	ctx *zero.Ctx
}

// newSender 构造 per-event 的 domain.Sender（matcher 闭包内调用，B-003 注入 ctx）。
func newSender(ctx *zero.Ctx) domain.Sender {
	return &botSender{ctx: ctx}
}

// Send 把回复载荷发送到当前事件来源。空 Segments 视为无效回复。
// 发送成功记 Debug（架构 §17.2 防重复规则 4：成功只 Debug，Info 锚点归调用方的结局行/
// 命令结局，避免同一事实两层各打一条）；失败原样上抛由调用方按结局处理。
func (s *botSender) Send(_ context.Context, r entity.Reply) error {
	if len(r.Segments) == 0 {
		return errors.New("回复载荷缺少内容段（Segments 为空）")
	}
	s.ctx.SendChain(replyToChain(r, s.ctx.Event)...)
	logger.Debug("回复已发送",
		logger.S("group_id", strconv.FormatInt(s.ctx.Event.GroupID, 10)),
		logger.S("user_id", strconv.FormatInt(s.ctx.Event.UserID, 10)),
		logger.I("segments", len(r.Segments)))
	return nil
}

// replyToChain 将领域层 entity.Reply 转换为 OneBot 消息段链（纯函数，便于单测）。
// 转换规则（对齐架构 §8.6 / B-003）：
//   - Quote → message.ReplyWithMessage(触发消息 ID)（引用当前事件消息）；
//   - At："sender" → @ 触发者（当前事件 UserID）；空 → 不 @；具体 user_id → 解析后 @；
//   - Segments：text → 纯文本；image → message.Image（按 ImageRef.Source 解释 Value）；
//     face → message.Face（QQ 表情 id）。
func replyToChain(r entity.Reply, ev *zero.Event) message.Message {
	chain := make(message.Message, 0, len(r.Segments)+2)
	if r.Quote {
		chain = append(chain, message.ReplyWithMessage(ev.MessageID)...)
	}
	switch r.At {
	case "":
		// 不 @
	case "sender":
		// 防御：事件 UserID 为 0（异常事件）时 message.At(0) 会退化为 AtAll（@全体），跳过不 @。
		if ev.UserID != 0 {
			chain = append(chain, message.At(ev.UserID))
		}
	default:
		if id, err := strconv.ParseInt(r.At, 10, 64); err == nil {
			chain = append(chain, message.At(id))
		}
		// 非法 user_id：忽略（不 @），不报错
	}
	for _, seg := range r.Segments {
		switch seg.Kind {
		case entity.SegmentKindText:
			chain = append(chain, message.Text(seg.Text))
		case entity.SegmentKindImage:
			chain = append(chain, message.Image(imageFile(seg.Image)))
		case entity.SegmentKindFace:
			chain = append(chain, message.Face(seg.Face))
		}
	}
	return chain
}

// imageFile 把 ImageRef 转成 OneBot image 段的 file 字段取值。
// path → "file:///绝对路径"；url → 原样；base64 → "base64://数据"。
func imageFile(ref *entity.ImageRef) string {
	if ref == nil {
		return ""
	}
	switch ref.Source {
	case entity.ImageSourcePath:
		return "file:///" + filepath.ToSlash(ref.Value)
	case entity.ImageSourceURL:
		return ref.Value
	case entity.ImageSourceBase64:
		return "base64://" + ref.Value
	default:
		return ""
	}
}
