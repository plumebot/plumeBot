package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
	"plumebot/pkg/logger"
)

// replyInstruction 是 system 块末尾的回复指令（P6-001，架构 §6 ⑤ 的回应提示）。
const replyInstruction = "请根据以上对话内容，自然回复最后一条消息，贴合会话风格，简洁口语化。"

// BuildMessages 组装 P6-001 五段完整消息链（架构 §6）：
//
//	[system: ①persona + ②会话画像 + ③历史摘要] → ④窗口（时间序）→ ⑤当前消息。
//
// botID 用于窗口内 bot 自己的消息 → RoleAssistant（RoleUser 之外）；空 = 全部 RoleUser。
// 组装走纯文本：窗口/当前消息渲染为单文本 part = ForLLM(msg)（B-026/B-028，原生多模态阶段3 不做）。
// persona 每次现查（改 persona 表即时生效，B 决策 1）；图片惰性描述（B-009②/B-027）+ 持久化（B-025）。
//
// 并发约束（审查 BUG-1，P6-002 接线前须落实）：describeCandidates 原地回填依赖 GetWindow 浅拷贝
// （Parts 底层数组与窗口内部共享），**同一会话的 BuildMessages 与窗口压缩（Compressor）必须串行**，
// 否则对共享数组的并发读写产生数据竞态。当前 BuildMessages 仅测试/单 goroutine 调用安全；
// P6-002 接线时以「per-session 串行」或「GetWindow 深拷贝」之一落实（见 roadmap B-040）。
func (s *MemoryService) BuildMessages(ctx context.Context, msg entity.Message, botID string) ([]entity.ChatMessage, error) {
	sessionID := msg.SessionKey()
	win, err := s.memory.GetWindow(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("读取窗口失败: %w", err)
	}

	// 惰性描述最近 N 轮 + 当前消息的图片（回填窗口副本 + SQLite 持久化）。
	cur := msg // 可变副本，⑤ 用描述后的版本渲染
	s.describeCandidates(ctx, win, &cur)

	out := make([]entity.ChatMessage, 0, len(win)+2)
	out = append(out, entity.ChatMessage{
		Role:  entity.RoleSystem,
		Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: s.buildSystemText(ctx, msg, win, botID)}},
	})
	for i, m := range win {
		if msg.MessageID != "" && m.MessageID == msg.MessageID {
			continue // ⑤ 单独追加，避免当前消息重复出现在 ④
		}
		// 空 MessageID 边角（OneBot message_id=0，审查 BUG-2）：当前消息必为窗口末尾（persist 先于组装），
		// 仅对末尾按「同发送者同时刻」去重，避免误删早期同秒消息。
		if msg.MessageID == "" && i == len(win)-1 && m.UserID == msg.UserID && m.Timestamp == msg.Timestamp {
			continue
		}
		out = append(out, toChatMessage(m, botID))
	}
	// ⑤ 当前消息恒为 RoleUser（即使不在窗口也追加）。
	out = append(out, entity.ChatMessage{
		Role:  entity.RoleUser,
		Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: cur.ForLLM()}},
	})
	return out, nil
}

// ---------------------------------------------------------------------------
// 惰性描述（B-009② / B-025 / B-027）
// ---------------------------------------------------------------------------

// describeCandidates 描述预算：只处理窗口最近 describe_recent_rounds 轮 + 当前消息（B-027）。
// describer nil（无 vision_model）直接返回；每消息上限 describe_per_turn_cap 张。
// 窗口副本与窗口内部共享 Parts 底层数组（GetWindow 浅拷贝），原地回填隐式写回窗口内存；
// 并发约束见 BuildMessages 注释（BUG-1，同会话须与压缩串行）。
func (s *MemoryService) describeCandidates(ctx context.Context, win []entity.Message, cur *entity.Message) {
	if s.describer == nil {
		return
	}
	rounds := s.describeRecentRounds()
	capPerMsg := s.describePerTurnCap()

	start := len(win) - rounds
	if start < 0 {
		start = 0
	}
	covered := false
	for i := start; i < len(win); i++ {
		s.describeMessage(ctx, &win[i], capPerMsg)
		if sameMessage(win[i], *cur) {
			covered = true
		}
	}
	if !covered {
		// 当前消息不在最近 N 轮（如不在窗口）→ 单独走预算。
		s.describeMessage(ctx, cur, capPerMsg)
	}
}

// describeMessage 描述一条消息中 Description 为空的 image part，每条消息最多 limit 张。
// 失败仅记日志不阻断组装 → ForLLM 回退 [图片]；成功回填 part.Description 并持久化（B-025）。
// 空 MessageID（审查 BUG-2）不持久化（避免 UPDATE 命中全库唯一 message_id=” 行造成错位），
// 仅内存回填本次组装使用。
func (s *MemoryService) describeMessage(ctx context.Context, m *entity.Message, limit int) {
	described := 0
	for j := range m.Parts {
		if m.Parts[j].Type != entity.PartTypeImage || m.Parts[j].Description != "" {
			continue
		}
		if described >= limit {
			break // 每消息上限：超出的图片本轮回退 [图片]
		}
		desc, err := s.describer.Describe(ctx, m.Parts[j])
		if err != nil {
			logger.Warn("图片描述失败，回退 [图片]",
				logger.S("message_id", m.MessageID), logger.Err(err))
			continue
		}
		m.Parts[j].Description = desc
		described++
		if m.MessageID == "" {
			continue // 空 ID 无可靠落库目标，不持久化
		}
		if err := s.store.UpdateMessageParts(ctx, m.MessageID, m.Parts); err != nil {
			// 不回滚：本次组装仍可用，下次组装重试持久化。
			logger.Warn("持久化图片描述失败", logger.S("message_id", m.MessageID), logger.Err(err))
		}
	}
}

// sameMessage 判断窗口消息 m 是否为当前消息 cur。
// MessageID 非空按 ID 匹配；为空（OneBot 边角）退化为「同发送者同时刻」
// （生产上当前消息必为窗口末尾，位置安全由调用方保证，见 BuildMessages 去重）。
func sameMessage(m, cur entity.Message) bool {
	if cur.MessageID != "" {
		return m.MessageID == cur.MessageID
	}
	return m.UserID == cur.UserID && m.Timestamp == cur.Timestamp
}

// ---------------------------------------------------------------------------
// system 块：①persona + ②会话画像 + ③历史摘要
// ---------------------------------------------------------------------------

// buildSystemText 组装 messages[0] 的 system 文本：①persona → ②会话画像 → ③历史摘要 → 回复指令。
// 单文本 part（infra ToSchema 要求 system 只能单文本 part）。
func (s *MemoryService) buildSystemText(ctx context.Context, msg entity.Message, win []entity.Message, botID string) string {
	blocks := []string{
		s.personaText(ctx),
		s.buildProfileText(ctx, msg, win, botID),
		s.buildSummaryText(ctx, msg.SessionKey()),
	}
	return strings.Join(blocks, "\n\n") + "\n\n" + replyInstruction
}

// personaText 返回 ① 系统人格：每次现查 persona 表（改库即时生效，B 决策 1）。
// 未命中/空/DB 报错 → 兜底 defaultPersona → config.DefaultSystemPrompt（不阻断组装）。
func (s *MemoryService) personaText(ctx context.Context) string {
	name := s.agentName
	if name == "" {
		name = config.DefaultAgentName
	}
	p, err := s.store.GetPersonaByAgent(ctx, name)
	if err == nil && p != nil && strings.TrimSpace(p.SystemPrompt) != "" {
		return p.SystemPrompt
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		logger.Warn("加载人格模板失败，使用兜底人设", logger.S("agent", name), logger.Err(err))
	}
	if s.defaultPersona != "" {
		return s.defaultPersona
	}
	return config.DefaultSystemPrompt
}

// buildProfileText 组装 ② 会话画像（B-014）：
//
//	群聊 = 群画像 + confirmed 黑话(jargon_cap) + 窗口内成员事实(每成员 facts_per_member)；
//	私聊 = 当前用户 member_facts(group_id="")，无群画像无黑话。
//
// 查询报错记 Warn 跳过该子段；空子段不输出标题，保持 prompt 紧凑。
func (s *MemoryService) buildProfileText(ctx context.Context, msg entity.Message, win []entity.Message, botID string) string {
	var sb strings.Builder
	sb.WriteString("## 会话画像\n")
	if msg.GroupID != "" {
		if prof, ok := s.profiles.GetGroupProfile(msg.GroupID); ok && prof != nil {
			sb.WriteString("【群信息】\n")
			fmt.Fprintf(&sb, "群 ID：%s\n", msg.GroupID)
			if prof.Culture != "" {
				sb.WriteString("群文化：")
				sb.WriteString(prof.Culture)
				sb.WriteString("\n")
			}
			if prof.ActiveHours != "" {
				sb.WriteString("活跃时段：")
				sb.WriteString(prof.ActiveHours)
				sb.WriteString("\n")
			}
			if len(prof.Topics) > 0 {
				sb.WriteString("主要话题：")
				sb.WriteString(strings.Join(prof.Topics, "、"))
				sb.WriteString("\n")
			}
			if len(prof.Rules) > 0 {
				sb.WriteString("群规：")
				sb.WriteString(strings.Join(prof.Rules, "；"))
				sb.WriteString("\n")
			}
			if len(prof.Atmosphere) > 0 {
				sb.WriteString("氛围：")
				sb.WriteString(strings.Join(prof.Atmosphere, "、"))
				sb.WriteString("\n")
			}
		}
		if jargon, err := s.store.ListConfirmedJargon(ctx, msg.GroupID); err == nil && len(jargon) > 0 {
			if n := s.jargonCap(); len(jargon) > n {
				jargon = jargon[:n]
			}
			sb.WriteString("【群内黑话】\n")
			sb.WriteString(strings.Join(jargon, "；"))
			sb.WriteString("\n")
		} else if err != nil {
			logger.Warn("查询群黑话失败", logger.S("group_id", msg.GroupID), logger.Err(err))
		}
		if members := windowMembers(win, botID); len(members) > 0 {
			sb.WriteString("【成员事实】\n")
			for _, uid := range members {
				facts, err := s.store.ListMemberFacts(ctx, msg.GroupID, uid)
				if err != nil {
					logger.Warn("查询成员事实失败", logger.S("group_id", msg.GroupID), logger.S("user_id", uid), logger.Err(err))
					continue
				}
				if len(facts) == 0 {
					continue
				}
				if n := s.factsPerMember(); len(facts) > n {
					facts = facts[:n]
				}
				fmt.Fprintf(&sb, "%s：%s\n", uid, strings.Join(facts, "；"))
			}
		}
	} else {
		fmt.Fprintf(&sb, "用户 ID：%s\n", msg.UserID)
		if facts, err := s.store.ListMemberFacts(ctx, "", msg.UserID); err == nil && len(facts) > 0 {
			if n := s.factsPerMember(); len(facts) > n {
				facts = facts[:n]
			}
			sb.WriteString("用户事实：")
			sb.WriteString(strings.Join(facts, "；"))
			sb.WriteString("\n")
		} else if err != nil {
			logger.Warn("查询用户事实失败", logger.S("user_id", msg.UserID), logger.Err(err))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// buildSummaryText 组装 ③ 压缩摘要：GetSummaries 热链全量拼接（旧→新），无则占位。
func (s *MemoryService) buildSummaryText(ctx context.Context, chatID string) string {
	sums := s.GetSummaries(ctx, chatID)
	var sb strings.Builder
	sb.WriteString("## 历史摘要\n")
	if len(sums) == 0 {
		sb.WriteString("（暂无历史摘要）")
		return sb.String()
	}
	for _, sum := range sums {
		sb.WriteString(sum.Text)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// toChatMessage 窗口消息 → ChatMessage：单文本 part（ForLLM，B-026/B-028），bot 自己的消息 → assistant。
func toChatMessage(m entity.Message, botID string) entity.ChatMessage {
	role := entity.RoleUser
	if botID != "" && m.UserID == botID {
		role = entity.RoleAssistant
	}
	return entity.ChatMessage{Role: role, Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: m.ForLLM()}}}
}

// windowMembers 窗口内去重成员（首次出现顺序），排除 bot 自身（bot 的上下文由其人格承担）。
func windowMembers(win []entity.Message, botID string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range win {
		if m.UserID == "" || (botID != "" && m.UserID == botID) || seen[m.UserID] {
			continue
		}
		seen[m.UserID] = true
		out = append(out, m.UserID)
	}
	return out
}

// jargonCap 返回 confirmed 黑话注入条数上限（B-014），≤0 → config 默认。
func (s *MemoryService) jargonCap() int {
	if s.prompt.JargonCap <= 0 {
		return config.DefaultJargonCap
	}
	return s.prompt.JargonCap
}

// factsPerMember 返回窗口内每成员事实注入条数上限（B-014），≤0 → config 默认。
func (s *MemoryService) factsPerMember() int {
	if s.prompt.FactsPerMember <= 0 {
		return config.DefaultFactsPerMember
	}
	return s.prompt.FactsPerMember
}

// describeRecentRounds 返回最近可描述轮数上限（B-027），≤0 → config 默认。
func (s *MemoryService) describeRecentRounds() int {
	if s.prompt.DescribeRecentRounds <= 0 {
		return config.DefaultDescribeRecentRounds
	}
	return s.prompt.DescribeRecentRounds
}

// describePerTurnCap 返回每条消息图片描述条数上限（B-027），≤0 → config 默认。
func (s *MemoryService) describePerTurnCap() int {
	if s.prompt.DescribePerTurnCap <= 0 {
		return config.DefaultDescribePerTurnCap
	}
	return s.prompt.DescribePerTurnCap
}
