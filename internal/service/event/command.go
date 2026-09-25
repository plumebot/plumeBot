package event

import (
	"context"
	"errors"
	"fmt"
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
// 文本/图片/引用/@，Reply.Segments/Quote/At）；群管理动作（Actions）经 ctx 内
// GroupManager 执行（B-015，与 AI 工具共用执行路径，护栏在 Execute 内把关）。
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
		logOutcome(ctx, msg, OutcomeCommand, logger.S("command", cmd),
			logger.S("reply_segments", replySegmentSummary(res.Reply)),
			logger.S("actions", groupActionSummary(res.Actions)))
		// B-017：校验通过后执行回复发送（失败仅告警，命令分支吞错误语义维持现状）。
		if res.Reply != nil {
			if err := s.sendReply(ctx, *res.Reply); err != nil {
				logger.From(ctx).Warn("插件回复发送失败", logger.S("command", cmd), logger.Err(err))
			}
		}
		// B-015：校验通过后执行群管理动作（Actions）——与 AI 工具共用 domain.GroupManager
		// 执行路径。先发回复后执行动作（用户能立刻看到插件反馈）；失败仅告警（命令分支
		// 吞错误语义维持现状）；护栏（per-group 开关 + 管理员校验）在 Execute 内统一把关，
		// 此处不重复。
		if len(res.Actions) > 0 {
			if err := s.executeActions(ctx, res.Actions); err != nil {
				logger.From(ctx).Warn("插件群管理动作执行失败", logger.S("command", cmd), logger.Err(err))
			}
		}
	case errors.Is(err, entity.ErrNotFound):
		// 未找到插件命令：命令消息已消费（防 confess 提示），记 Info 结局（架构 §17.4）。
		logOutcome(ctx, msg, OutcomeCommandNotFound, logger.S("command", cmd))
	default:
		logOutcome(ctx, msg, OutcomeCommandError, logger.S("command", cmd), logger.Err(err))
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
		logger.From(ctx).Warn("help 回复发送失败", logger.Err(err))
	}
	logOutcome(ctx, msg, OutcomeCommand, logger.S("command", "help"))
	return true
}

// executeActions 依次执行插件声明的群管理动作；失败即停（返回首个错误）。
// 护栏（per-group 开关 + 管理员校验）在 domain.GroupManager 实现内把关。
func (s *EventService) executeActions(ctx context.Context, actions []entity.GroupAction) error {
	gm, ok := domain.GroupManagerFrom(ctx)
	if !ok {
		return errors.New("缺少群管理执行能力（GroupManager 未注入）")
	}
	for _, a := range actions {
		if err := gm.Execute(ctx, a); err != nil {
			return fmt.Errorf("动作 %s(%s) 执行失败: %w", a.Op, a.Target, err)
		}
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
