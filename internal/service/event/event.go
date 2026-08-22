package event

import (
	"context"
	"strconv"
	"strings"
	"time"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/service/agent"
	"plumebot/internal/service/memory"
	"plumebot/internal/service/plugin"
	"plumebot/pkg/config"
	"plumebot/pkg/logger"
)

// EventService 是顶层事件调度编排，组合所有子 service。
type EventService struct {
	agent    *agent.AgentService
	memory   *memory.MemoryService
	plugin   *plugin.PluginService
	control  domain.Control
	botID    string // bot 自身 QQ 号（BuildMessages 的 assistant 角色映射 + bot 回复 UserID）
	msgChain Handler
}

// NewEventService 创建 EventService，注入全部子 service 与中间件配置。
// botID 为 bot 自身 QQ 号（cfg.Bot.SelfID），空值退化为「窗口内 bot 消息渲染为 user 角色」。
// 消息管线顺序固定为：日志 → 限流 → 敏感词 → 末端处理。
func NewEventService(
	agent *agent.AgentService,
	memory *memory.MemoryService,
	plugin *plugin.PluginService,
	control domain.Control,
	mwCfg config.MiddlewareConfig,
	botID string,
) *EventService {
	s := &EventService{
		agent:   agent,
		memory:  memory,
		plugin:  plugin,
		control: control,
		botID:   botID,
	}
	mws := []Middleware{
		logMiddleware,
		rateLimitMiddleware(newRateLimiter(mwCfg.RateLimit)),
		sensitiveWordMiddleware(newSensitiveWordFilter(mwCfg.SensitiveWords)),
	}
	s.msgChain = chain(mws, s.tail)
	return s
}

// tail 是消息管线的末端处理：持久化消息（写入上下文窗口 + SQLite），
// 窗口满时触发 P3-003 异步窗口压缩；随后命令分支（/开头 → 插件分发，P4-002，
// 命令消息短路绕过触发判断，插件回复经 Sender 发送，B-017，架构 §10.2）；
// 最后对普通消息走回复闭环 respond（触发判断 → 组装 → Agent 推理 → 回复，P6-002）。
func (s *EventService) tail(ctx context.Context, msg entity.Message) error {
	full, err := s.memory.PersistMessage(ctx, msg)
	if err != nil {
		return err
	}
	if full {
		s.memory.Compress(ctx, msg)
	}
	handled, err := s.dispatchCommand(ctx, msg)
	if err != nil {
		return err
	}
	if handled {
		return nil // 命令消息已走插件分支，绕过触发判断
	}
	return s.respond(ctx, msg)
}

// respond 是普通消息的回复闭环（P6-002，架构 §14.4）：
// 触发判断 → 拼 prompt（BuildMessages）→ Agent 推理（GenerateReply，记忆工具经 ctx
// 会话身份写入 member_facts/group_jargon）→ 经 ctx 内 domain.Sender 发送（B-003）
// → 发送成功后窗口追加 bot 回复 + OnReplied 记账（B-038：移出 judge 触发点，防重复记账）。
// 任何一步失败保守忽略（不发送、不记账），管线不报错；Agent 故障时 bot 保持沉默。
func (s *EventService) respond(ctx context.Context, msg entity.Message) error {
	d, err := s.control.ShouldReply(ctx, msg)
	if err != nil {
		logger.Warn("触发判断失败",
			logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID), logger.Err(err))
		return nil
	}
	if !d.Reply {
		logger.Debug("未触发回复",
			logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID),
			logger.S("message_id", msg.MessageID), logger.S("reason", string(d.Reason)))
		return nil
	}
	sender, ok := domain.SenderFrom(ctx)
	if !ok {
		logger.Warn("缺少回复发送能力（Sender 未注入），跳过回复",
			logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID))
		return nil
	}

	// 会话身份注入 ctx：记忆工具（store_fact/learn_jargon/forget_fact）经 entity.SessionFrom 确定记忆归属。
	sctx := entity.WithSession(ctx, entity.Session{GroupID: msg.GroupID, UserID: msg.UserID})
	msgs, err := s.memory.BuildMessages(sctx, msg, s.botID)
	if err != nil {
		logger.Warn("Prompt 组装失败，跳过回复",
			logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID), logger.Err(err))
		return nil
	}
	reply, err := s.agent.GenerateReply(sctx, msgs)
	if err != nil {
		logger.Warn("Agent 推理失败，跳过回复",
			logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID), logger.Err(err))
		return nil
	}
	// 剥除 reply 开头可能出现的 "[1527301260]:"（可能连续出现多个）。
	for strings.HasPrefix(reply, "[1527301260]:") {
		reply = strings.TrimPrefix(reply, "[1527301260]:")
	}
	if strings.TrimSpace(reply) == "" {
		logger.Debug("Agent 返回空回复，跳过",
			logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID))
		return nil
	}

	// 发送（B-003）。发送失败 = bot 未说话，不记账、不追加窗口。
	if err := sender.Send(ctx, agentReply(msg, reply)); err != nil {
		logger.Warn("回复发送失败",
			logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID), logger.Err(err))
		return nil
	}

	// 发送成功：窗口追加 bot 回复（持久化到触发消息的会话——私聊下 bot 回复的作者是 botID，
	// 按自身 SessionKey 会另起 "private:"+botID 孤立会话，须显式并入用户会话）+ OnReplied 记账（B-038）。
	botMsg := s.botReplyMessage(msg, reply)
	if full, err := s.memory.PersistMessageToSession(ctx, msg.SessionKey(), botMsg); err != nil {
		logger.Warn("bot 回复持久化失败",
			logger.S("group_id", msg.GroupID), logger.Err(err))
	} else if full {
		s.memory.Compress(ctx, msg) // 压缩触发消息所属会话（bot 回复已并入该会话）
	}
	if err := s.control.OnReplied(ctx, msg); err != nil {
		logger.Warn("回复状态记录失败",
			logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID), logger.Err(err))
	}
	logger.Info("Agent 回复已发送",
		logger.S("group_id", msg.GroupID), logger.S("user_id", msg.UserID),
		logger.S("message_id", msg.MessageID), logger.S("reason", string(d.Reason)))
	return nil
}

// agentReply 构造 Agent 文本回复载荷（B-003 entity.Reply）。
// 群聊被 @（mention_forced）：@ 触发者但**不引用触发消息**——`[CQ:reply]` 引用预览会带出触发消息
// 原文（含剥除前的 @bot），QQ 客户端把 @自己 渲染为 bot 的 QQ 号，出现「人名后跟两个 bot 的 QQ」；
// auto 主动发言/私聊：纯文本不引用不 @。
func agentReply(msg entity.Message, reply string) entity.Reply {
	if msg.MessageType == "group" && msg.Mentioned {
		return entity.Reply{
			At:       "sender",
			Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: reply}},
		}
	}
	return entity.Reply{
		Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: reply}},
	}
}

// botReplyMessage 构造 bot 自身回复的 entity.Message（窗口追加 + SQLite 持久化）。
// MessageID 用合成 ID（"self:"+UnixNano）：messages.message_id 全局唯一 + INSERT OR IGNORE，
// 空串会被丢弃/互相覆盖（B-041 相邻边角，仅 bot 回复侧根修；入站 message_id=0 根修仍归 B-041）。
// 注意：私聊下 UserID=botID 使 SessionKey() 派生 "private:"+botID，持久化须经
// PersistMessageToSession 显式并入触发消息（用户）的会话，否则 bot 回复永远进不了对话窗（见 respond）。
func (s *EventService) botReplyMessage(msg entity.Message, reply string) entity.Message {
	return entity.Message{
		MessageID:   "self:" + strconv.FormatInt(time.Now().UnixNano(), 10),
		GroupID:     msg.GroupID,
		UserID:      s.botID,
		Parts:       []entity.ContentPart{{Type: entity.PartTypeText, Text: reply}},
		Timestamp:   time.Now().Unix(),
		MessageType: msg.MessageType,
		Mentioned:   false,
	}
}

// sendReply 经 ctx 内 Sender 发送回复载荷；无 Sender 时告警跳过（不报错）。
// 供插件命令分支复用（B-017）；Agent 分支在 respond 内自行 fail-fast 检查后调用 Sender。
func (s *EventService) sendReply(ctx context.Context, r entity.Reply) error {
	sender, ok := domain.SenderFrom(ctx)
	if !ok {
		logger.Warn("缺少回复发送能力（Sender 未注入），跳过回复")
		return nil
	}
	return sender.Send(ctx, r)
}

// HandleMessage 处理消息事件：走「日志 → 限流 → 敏感词 → 末端」管线。
func (s *EventService) HandleMessage(ctx context.Context, msg entity.Message) error {
	return s.msgChain(ctx, msg)
}

// HandleNotice 处理通知事件。第一阶段返回 nil。
func (s *EventService) HandleNotice(_ context.Context, _ entity.Event) error {
	return nil
}
