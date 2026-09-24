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
// 指令位于 system 首条（在 ④窗口/⑤当前消息 之上），故称「以下对话内容」。
const replyInstruction = "请根据以下对话内容和自身人设，自然回复最后一条消息。"

// BuildMessages 组装 P6-001 五段完整消息链（架构 §6）：
//
//	[system: ①persona + ②会话画像 + ③历史摘要] → ④窗口（时间序）→ ⑤当前消息。
//
// botID 用于窗口内 bot 自己的消息 → RoleAssistant（RoleUser 之外）；空 = 全部 RoleUser。
// 组装走纯文本：窗口/当前消息渲染为单文本 part = speakerText(msg)（B-026/B-028，原生多模态阶段3 不做）；
// 群聊消息带发送者前缀（区分不同说话人，见 speakerText），私聊对方唯一不加。
// persona 每次现查（改 persona 表即时生效，B 决策 1）；图片惰性描述（B-009②/B-027）+ 持久化（B-025）。
//
// 并发约束（B-040 已落实：GetWindow 深拷贝 + BackfillParts 安全回填）：describeCandidates 惰性
// 描述写回的是 GetWindow 深拷贝（Parts 与窗口内部隔离），描述经 BackfillParts 在窗口锁内回填
// 内部消息——组装与压缩各持独立快照，无逃逸数组竞态、无跨 LLM 持锁，同会话互不阻塞。
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
		Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: speakerText(cur)}},
	})
	return out, nil
}

// ---------------------------------------------------------------------------
// 惰性描述（B-009② / B-025 / B-027）
// ---------------------------------------------------------------------------

// describeCandidates 描述预算：只处理窗口最近 describe_recent_rounds 轮 + 当前消息（B-027）。
// describer nil（无 vision_model）直接返回；每消息上限 describe_per_turn_cap 张。
// 写回的是 GetWindow 深拷贝，描述经 BackfillParts 锁内回填窗口内部（B-040），并发安全。
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
			// 当前消息已被窗口副本描述（covered）：同步描述后版本到 cur，⑤ 用描述后的渲染
			//（P6-001 边角修正：否则当前消息图片在 prompt 中显示 [图片]）。
			cur.Parts = win[i].Parts
			covered = true
		}
	}
	if !covered {
		// 当前消息不在最近 N 轮（如不在窗口）→ 单独走预算。
		s.describeMessage(ctx, cur, capPerMsg)
	}
}

// describeMessage 描述一条消息中 Description 为空的 image part，每条消息最多 limit 张。
// 失败仅记日志不阻断组装 → ForLLM 回退 [图片]；成功回填 part.Description 并持久化（B-025）
// + 窗口锁内回填（B-040 BackfillParts，使下次组装跳过、压缩摘要含图片描述）。
// 空 MessageID（审查 BUG-2）不持久化/不回填（避免 UPDATE 命中全库唯一 message_id=” 行造成错位），
// 仅本次组装在副本上使用。
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
				logger.S("message_id", m.MessageID),
				logger.S("url", m.Parts[j].URL),
				logger.S("file_hash", hashPrefix(m.Parts[j].FileHash)),
				logger.Err(err))
			continue
		}
		m.Parts[j].Description = desc
		described++
		// 成功探针（图片描述链路排障）：描述是否落到消息上、是否持久化/回填，
		// 与 infra/ai 的缓存命中日志对照即可判断「重复描述」是缓存未命中还是回填失效。
		logger.Debug("图片描述成功",
			logger.S("message_id", m.MessageID),
			logger.S("file_hash", hashPrefix(m.Parts[j].FileHash)),
			logger.I("desc_chars", len(desc)))
		if m.MessageID == "" {
			continue // 空 ID 无可靠落库目标，不持久化/不回填
		}
		if err := s.store.UpdateMessageParts(ctx, m.MessageID, m.Parts); err != nil {
			// 不回滚：本次组装仍可用，下次组装重试持久化。
			logger.Warn("持久化图片描述失败", logger.S("message_id", m.MessageID), logger.Err(err))
		}
		s.backfillParts(ctx, m.SessionKey(), m.MessageID, m.Parts)
	}
}

// backfillWindow 是 Window 的可选能力：窗口内消息 Parts 回填（B-040 深拷贝+安全回填）。
// 不并入 domain.Memory 接口：图片描述回填是「最佳努力」优化，跳过无碍（描述已持久化，下次组装
// 重试），可选接口 + 静默跳过正合适；正确性关键操作则应进接口（如 AppendToSession，见 domain.Memory）。
// 无此能力（测试桩）时跳过。
type backfillWindow interface {
	BackfillParts(ctx context.Context, sessionID, messageID string, parts []entity.ContentPart)
}

// backfillParts 把已描述的部分回填到窗口内部消息（窗口锁内，见 Window.BackfillParts）。
func (s *MemoryService) backfillParts(ctx context.Context, sessionID, messageID string, parts []entity.ContentPart) {
	if bw, ok := s.memory.(backfillWindow); ok {
		bw.BackfillParts(ctx, sessionID, messageID, parts)
	}
}

// hashPrefix 返回 FileHash 前 8 位用于日志（避免整串刷屏，足够分辨同一/不同图）。
func hashPrefix(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
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
			names := windowSenderNames(win, msg) // uid → 展示名（有名字用昵称，无名字回落 QQ 号）
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
				if nick, ok := names[uid]; ok && nick != "" {
					uid = nick
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

// toChatMessage 窗口消息 → ChatMessage：单文本 part（speakerText，B-026/B-028），bot 自己的消息 → assistant。
func toChatMessage(m entity.Message, botID string) entity.ChatMessage {
	role := entity.RoleUser
	if botID != "" && m.UserID == botID {
		role = entity.RoleAssistant
	}
	return entity.ChatMessage{Role: role, Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: speakerText(m)}}}
}

// speakerText 返回消息的 LLM 文本视图；群聊消息带发送者前缀（区分不同说话人）。
// 格式 "[昵称]: 内容"，无昵称回落 "[QQ号]: 内容"（P6 联调修复「上下文只有 QQ 号」）——
// 昵称来自 convert 的 SenderName（群名片优先），bot 自己的消息无 SenderName 回落其 QQ 标识，
// 角色仍为 assistant。具备名字后 agent 可用自然称呼跟踪/点名说话人。
// 私聊对方唯一且 ② 会话画像已标「用户 ID」，不加前缀避免噪音。
func speakerText(m entity.Message) string {
	if m.MessageType != "group" || m.UserID == "" {
		return m.ForLLM()
	}
	name := m.SenderName
	if name == "" {
		name = m.UserID // 无名字回落 QQ 号
	}
	return "[" + name + "]: " + m.ForLLM()
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

// windowSenderNames 收集 uid → 展示名（SenderName，群名片优先）；当前消息也计入（可能不在窗口）。
// 供 ② 会话画像成员事实渲染：有名字用昵称，无名字回落 QQ 号（windowMembers 已排除 bot，
// bot 无 SenderName 亦不会注入）。仅内存数据，无 DB 查询。
func windowSenderNames(win []entity.Message, cur entity.Message) map[string]string {
	names := make(map[string]string)
	collect := func(m entity.Message) {
		if m.SenderName != "" {
			names[m.UserID] = m.SenderName
		}
	}
	collect(cur)
	for _, m := range win {
		collect(m)
	}
	return names
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
