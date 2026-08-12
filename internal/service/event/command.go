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
// 路由到插件执行，校验通过后记录指令集摘要。P4-002 只定义协议 + 校验，不执行回复/动作
// （发送归 P6-002 B-003，群管理归 B-015）。
func (s *EventService) dispatchCommand(ctx context.Context, msg entity.Message) error {
	cmd, args, ok := parseCommand(msg.Content)
	if !ok {
		return nil
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
	case errors.Is(err, domain.ErrNotFound):
		logger.Debug("未找到插件命令", logger.S("command", cmd), logger.S("group_id", msg.GroupID))
	default:
		logger.Warn("插件命令执行失败", logger.S("command", cmd), logger.Err(err))
	}
	return nil
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
