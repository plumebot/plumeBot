package event

import (
	"context"
	"errors"
	"strings"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// parseCommand 解析命令消息："/cmd arg1 arg2" → (cmd, [args], true)；非命令返回 ok=false。
// 以 / 开头且其后至少一个 token 才算命令。
func parseCommand(content string) (cmd string, args []string, ok bool) {
	s := strings.TrimSpace(content)
	if len(s) < 2 || s[0] != '/' {
		return "", nil, false
	}
	fields := strings.Fields(s[1:])
	if len(fields) == 0 {
		return "", nil, false
	}
	if len(fields) > 1 {
		args = fields[1:]
	}
	return fields[0], args, true
}

// dispatchCommand 是命令分支（架构 §10.2「是命令? → 插件分发」）：以 / 开头的消息
// 路由到插件执行，校验通过后记录指令集摘要；插件回复经 ctx 内 Sender 发送（B-017，
// 文本/图片/引用/@，Reply.Segments/Quote/At）；群管理动作（Actions）仅记录不执行（B-015）。
// 返回 handled：true = 命令消息已分发（调用方短路，不再走触发判断）；
// false = 非命令消息（调用方继续触发判断）。
func (s *EventService) dispatchCommand(ctx context.Context, msg entity.Message) (bool, error) {
	cmd, args, ok := parseCommand(msg.PlainText())
	if !ok {
		return false, nil
	}
	// 宿主内置命令：/help 列插件、/help <插件名> 查用法，不进入插件分发。
	if cmd == "help" {
		return s.handleHelpCommand(ctx, msg, args), nil
	}
	res, err := s.plugin.Dispatch(ctx, entity.PluginRequest{
		Proto:     1,
		Command:   cmd,
		Args:      args,
		Session:   entity.Session{GroupID: msg.GroupID, UserID: msg.UserID},
		MessageID: msg.MessageID,
	})
	switch {
	case err == nil:
		logger.Info("插件命令执行成功",
			logger.S("command", cmd),
			logger.S("group_id", msg.GroupID),
			logger.S("user_id", msg.UserID),
			logger.S("reply_segments", replySegmentSummary(res.Reply)),
			logger.S("actions", groupActionSummary(res.Actions)),
		)
		// B-017：校验通过后执行回复发送（失败仅告警，命令分支吞错误语义维持现状）。
		if res.Reply != nil {
			if err := s.sendReply(ctx, *res.Reply); err != nil {
				logger.Warn("插件回复发送失败", logger.S("command", cmd), logger.Err(err))
			}
		}
	case errors.Is(err, domain.ErrNotFound):
		logger.Debug("未找到插件命令", logger.S("command", cmd), logger.S("group_id", msg.GroupID))
	default:
		logger.Warn("插件命令执行失败", logger.S("command", cmd), logger.Err(err))
	}
	return true, nil // 命令分支吞错误只记日志（维持现状），始终 handled
}

// handleHelpCommand 处理 /help 与 /help <插件名>：列出插件或查插件用法。
// 文本由 PluginService.Help 生成（列插件/查用法/未找到均返回文本），经 Sender 发送。
// 返回 handled=true（宿主内置命令，消息不再进入插件分发）。
func (s *EventService) handleHelpCommand(ctx context.Context, msg entity.Message, args []string) bool {
	text := s.plugin.Help(ctx, entity.PluginRequest{
		Proto:     1,
		Command:   "help",
		Args:      args,
		Session:   entity.Session{GroupID: msg.GroupID, UserID: msg.UserID},
		MessageID: msg.MessageID,
	})
	if err := s.sendReply(ctx, entity.Reply{
		Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: text}},
	}); err != nil {
		logger.Warn("help 回复发送失败", logger.Err(err))
	}
	return true
}

// replySegmentSummary 汇总回复段类型，如 "text,image"。
func replySegmentSummary(r *entity.Reply) string {
	if r == nil || len(r.Segments) == 0 {
		return ""
	}
	kinds := make([]string, 0, len(r.Segments))
	for _, seg := range r.Segments {
		kinds = append(kinds, string(seg.Kind))
	}
	return strings.Join(kinds, ",")
}

// groupActionSummary 汇总动作类型，如 "mute,set_card"。
func groupActionSummary(actions []entity.GroupAction) string {
	if len(actions) == 0 {
		return ""
	}
	ops := make([]string, 0, len(actions))
	for _, a := range actions {
		ops = append(ops, string(a.Op))
	}
	return strings.Join(ops, ",")
}
