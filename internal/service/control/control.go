package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
)

// 触发模式合法值（service 侧唯一；空/未知在归一化时兜底 mention，B-005）。
const (
	ModeMention = "mention"
	ModeAuto    = "auto"
)

var _ domain.Control = (*ControlService)(nil)

// ControlService 负责触发判断：P5-001 模式判定（mention/auto）+ P5-002 状态规则
// （精力/冷却/连续/时段/短消息，纯规则层不调 LLM，架构 §9.2）。
type ControlService struct {
	defaultMode string         // 全局默认模式（cfg.Control.Mode；空 → mention 兜底）
	defaults    ruleConfig     // 全局状态规则参数（cfg.Control.state + 代码默认兜底）
	store       domain.Storage // 读 per-group group_config（P5-002 起同时读 bot_state 运行态）
	now         func() time.Time
	locks       sync.Map // sessionKey → *sync.Mutex：串行化同会话状态读改写（M1，防并发丢更新）
}

// ruleConfig 是状态规则参数（已合并 per-group 覆盖后的生效值）。
type ruleConfig struct {
	energyMax         int
	energyCost        int
	energyRecover     int // 点/分钟
	energyThreshold   int
	cooldownSeconds   int64
	consecutiveLimit  int
	restSeconds       int64
	quietHoursStart   int  // 分钟 0-1439；-1 = 解析无效
	quietHoursEnd     int  // 分钟 0-1439；-1 = 解析无效
	quietHoursSet     bool // 时段有效且非空段（start != end）
	shortMessageChars int
}

// groupState 是 bot_state.state 的 JSON 结构（每群/每私聊一条，运行态）。
type groupState struct {
	Energy           int   `json:"energy"`
	EnergyUpdatedAt  int64 `json:"energy_updated_at"` // Unix 秒
	LastReplyAt      int64 `json:"last_reply_at"`     // Unix 秒
	ConsecutiveCount int   `json:"consecutive_count"`
	RestUntil        int64 `json:"rest_until"` // Unix 秒
}

// NewControlService 创建 ControlService。
// 模式解析优先级：per-group group_config.mode（非空）→ 全局 cfg.Control.Mode → "mention"。
// 状态参数解析优先级：per-group group_config 列（非 0/非空）→ 全局 cfg.Control.state → 代码默认。
func NewControlService(cfg config.ControlConfig, store domain.Storage) *ControlService {
	return &ControlService{
		defaultMode: cfg.Mode,
		defaults:    newRuleConfig(cfg.State),
		store:       store,
		now:         time.Now,
	}
}

// ShouldReply 判断 bot 在当前消息下是否应回复（P5-001 模式 + P5-002 状态规则）。
// 评估序（架构 §9.2）：被 @/私聊 = 强制回复，绕过全部规则（0 查询）；
// 群非 @ 消息：mention 模式不触发；auto 模式按 短消息 → 静默时段 → 精力 → 冷却/连续休息 → 通过。
func (s *ControlService) ShouldReply(ctx context.Context, msg entity.Message) (entity.Decision, error) {
	// 强制回复（@ 或私聊）：绕过全部状态规则，不读任何配置/状态。
	if msg.Mentioned {
		return entity.Decision{Reply: true, Reason: entity.DecisionReasonMentionForced}, nil
	}

	mode, p, err := s.resolveParams(ctx, msg.GroupID)
	if err != nil {
		return entity.Decision{}, err
	}
	if mode != ModeAuto {
		// mention（含空/未知兜底，fail-closed）：群聊未被 @ → 不触发。
		return entity.Decision{Reply: false, Reason: entity.DecisionReasonNotTriggered}, nil
	}
	return s.shouldReplyAuto(ctx, msg, p)
}

// shouldReplyAuto auto 模式普通消息（群聊非 @）的状态规则评估。
// 短消息/静默时段不读 bot_state（短路省查询）；精力/冷却需读运行态。
func (s *ControlService) shouldReplyAuto(ctx context.Context, msg entity.Message, p ruleConfig) (entity.Decision, error) {
	now := s.now()

	// 1. 短消息忽略：<N 字不触发 Agent 判断（中文按字计）。
	if utf8.RuneCountInString(msg.PlainText()) < p.shortMessageChars {
		return entity.Decision{Reply: false, Reason: entity.DecisionReasonShortMessage}, nil
	}
	// 2. 时段控制：深夜/凌晨不参与对话。
	if inQuietHours(p, now) {
		return entity.Decision{Reply: false, Reason: entity.DecisionReasonQuietHours}, nil
	}
	// 3. 精力值：低于阈值不主动说话（惰性恢复后判断）。
	st, err := s.loadState(ctx, msg.SessionKey())
	if err != nil {
		return entity.Decision{}, err
	}
	s.recoverEnergy(&st, p, now)
	if st.Energy < p.energyThreshold {
		return entity.Decision{Reply: false, Reason: entity.DecisionReasonLowEnergy}, nil
	}
	// 4. 冷却 / 连续休息：两次主动回复最小间隔，或连续达上限强制休息期内。
	if now.Unix() < st.RestUntil || now.Unix() < st.LastReplyAt+p.cooldownSeconds {
		return entity.Decision{Reply: false, Reason: entity.DecisionReasonCooldown}, nil
	}
	return entity.Decision{Reply: true, Reason: entity.DecisionReasonAutoPass}, nil
}

// OnReplied 在 bot 决定回复后回调，维护运行态（P5-002）：消耗精力、记冷却、连续计数，
// 连续达上限进入强制休息。被 @/私聊 强制回复同样消耗（规则不拦，但状态照记）。
// 同一会话的状态「读-改-写」以 per-session 锁串行化（M1），防并发触发丢更新/绕过冷却。
func (s *ControlService) OnReplied(ctx context.Context, msg entity.Message) error {
	key := msg.SessionKey()
	l := s.lock(key)
	l.Lock()
	defer l.Unlock()

	_, p, err := s.resolveParams(ctx, msg.GroupID)
	if err != nil {
		return err
	}
	st, err := s.loadState(ctx, key)
	if err != nil {
		return err
	}
	now := s.now()
	s.recoverEnergy(&st, p, now)

	// 连续计数：休息期内不递增（防 @ 风暴反复延长休息）；距上次回复 ≥ 冷却窗口则重置为 1
	// （与 ShouldReply 的「now ≥ LastReplyAt+cooldown 放行」对称，L1 边界统一）。
	if now.Unix() >= st.RestUntil {
		if now.Unix()-st.LastReplyAt >= p.cooldownSeconds {
			st.ConsecutiveCount = 1
		} else {
			st.ConsecutiveCount++
		}
		if st.ConsecutiveCount >= p.consecutiveLimit {
			st.RestUntil = now.Unix() + p.restSeconds
			st.ConsecutiveCount = 0
		}
	}

	// 消耗精力（下限 0），记回复时刻。
	st.Energy -= p.energyCost
	if st.Energy < 0 {
		st.Energy = 0
	}
	st.EnergyUpdatedAt = now.Unix()
	st.LastReplyAt = now.Unix()
	return s.persistState(ctx, key, st)
}

// resolveParams 解析消息应使用的触发模式与生效参数。私聊 GroupID 为空 → 无 per-group，直接落全局。
func (s *ControlService) resolveParams(ctx context.Context, groupID string) (string, ruleConfig, error) {
	if groupID == "" {
		return s.defaultMode, s.defaults, nil
	}
	gc, err := s.store.GetGroupConfig(ctx, groupID)
	switch {
	case err == nil:
		mode := s.defaultMode
		if gc.Mode != "" {
			mode = gc.Mode
		}
		return normalizeMode(mode), mergeOverrides(s.defaults, *gc), nil
	case errors.Is(err, domain.ErrNotFound):
		// 无 per-group 配置 → 落全局
		return normalizeMode(s.defaultMode), s.defaults, nil
	default:
		return "", ruleConfig{}, err
	}
}

// normalizeMode 归一化模式：仅 "auto" 为 auto；空/未知 → mention（fail-closed，B-005）。
func normalizeMode(mode string) string {
	if mode == ModeAuto {
		return ModeAuto
	}
	return ModeMention
}

// newRuleConfig 由全局配置构造生效参数：0/空 → config 包默认常量兜底（消费方兜底，配置层不改写）。
func newRuleConfig(cfg config.ControlStateConfig) ruleConfig {
	d := ruleConfig{
		energyMax:         config.DefaultEnergyMax,
		energyCost:        config.DefaultEnergyCost,
		energyRecover:     config.DefaultEnergyRecover,
		energyThreshold:   config.DefaultEnergyThreshold,
		cooldownSeconds:   config.DefaultCooldownSeconds,
		consecutiveLimit:  config.DefaultConsecutiveLimit,
		restSeconds:       config.DefaultRestSeconds,
		shortMessageChars: config.DefaultShortMessageChars,
	}
	overrideInt(&d.energyMax, cfg.EnergyMax)
	overrideInt(&d.energyCost, cfg.EnergyCost)
	overrideInt(&d.energyRecover, cfg.EnergyRecover)
	overrideInt(&d.energyThreshold, cfg.EnergyThreshold)
	overrideInt64(&d.cooldownSeconds, cfg.CooldownSeconds)
	overrideInt(&d.consecutiveLimit, cfg.ConsecutiveLimit)
	overrideInt64(&d.restSeconds, cfg.RestSeconds)
	overrideInt(&d.shortMessageChars, cfg.ShortMessageChars)
	// 静默时段：默认常量必须合法（嵌入常量，fail-fast）；cfg 覆盖走 overrideHM（非法保留默认）。
	d.quietHoursStart = mustHM(config.DefaultQuietHoursStart)
	d.quietHoursEnd = mustHM(config.DefaultQuietHoursEnd)
	overrideHM(&d.quietHoursStart, cfg.QuietHoursStart)
	overrideHM(&d.quietHoursEnd, cfg.QuietHoursEnd)
	d.quietHoursSet = d.quietHoursStart != d.quietHoursEnd
	return d
}

// mergeOverrides 以全局参数为底，用 per-group group_config 非 0/非空列覆盖。
func mergeOverrides(d ruleConfig, gc entity.GroupConfig) ruleConfig {
	overrideInt(&d.energyMax, gc.EnergyMax)
	overrideInt(&d.energyCost, gc.EnergyCost)
	overrideInt(&d.energyRecover, gc.EnergyRecover)
	overrideInt(&d.energyThreshold, gc.EnergyThreshold)
	overrideInt64(&d.cooldownSeconds, gc.CooldownSeconds)
	overrideInt(&d.consecutiveLimit, gc.ConsecutiveLimit)
	overrideInt64(&d.restSeconds, gc.RestSeconds)
	overrideInt(&d.shortMessageChars, gc.ShortMessageChars)
	overrideHM(&d.quietHoursStart, gc.QuietHoursStart)
	overrideHM(&d.quietHoursEnd, gc.QuietHoursEnd)
	d.quietHoursSet = d.quietHoursStart != d.quietHoursEnd
	return d
}

// overrideInt 非零才覆盖整数参数（0 = 未配置/未覆盖，走全局/默认兜底）。
func overrideInt(dst *int, v int) {
	if v != 0 {
		*dst = v
	}
}

// overrideInt64 非零才覆盖秒级时长参数（源为配置 int，目标为运行秒 int64）。
func overrideInt64(dst *int64, v int) {
	if v != 0 {
		*dst = int64(v)
	}
}

// overrideHM 解析 "HH:MM" 成功才覆盖时段参数（空/非法 → 保留现有值）。
func overrideHM(dst *int, v string) {
	if minute, ok := parseHM(v); ok {
		*dst = minute
	}
}

// mustHM 解析嵌入的默认时段常量；非法即 panic（仅默认常量，构造期 fail-fast，杜绝静默兜底为 00:00）。
func mustHM(s string) int {
	minute, ok := parseHM(s)
	if !ok {
		panic("invalid default quiet hours: " + s)
	}
	return minute
}

// parseHM 解析 "HH:MM" → 当日分钟数（0-1439）。格式非法返回 (0, false)。
func parseHM(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// inQuietHours 判断当前时刻是否处于静默时段。start==end → 空段（禁用）；
// end<start → 跨午夜取并集 [start,24:00) ∪ [00:00,end)。
func inQuietHours(p ruleConfig, now time.Time) bool {
	if !p.quietHoursSet {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	if p.quietHoursEnd < p.quietHoursStart {
		return cur >= p.quietHoursStart || cur < p.quietHoursEnd
	}
	return cur >= p.quietHoursStart && cur < p.quietHoursEnd
}

// lock 返回该会话的互斥锁（并发安全，per-session 只增不删，规模 = 会话数，见 B-004）。
func (s *ControlService) lock(key string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// loadState 读取运行态；不存在（ErrNotFound）→ 零值新状态。
func (s *ControlService) loadState(ctx context.Context, key string) (groupState, error) {
	var st groupState
	bs, err := s.store.GetBotState(ctx, key)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return st, nil
		}
		return st, err
	}
	if err := json.Unmarshal([]byte(bs.State), &st); err != nil {
		return st, fmt.Errorf("解析 bot_state[%s] 失败: %w", key, err)
	}
	return st, nil
}

// persistState 序列化并落库运行态。
func (s *ControlService) persistState(ctx context.Context, key string, st groupState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.store.UpsertBotState(ctx, entity.BotState{GroupID: key, State: string(b)})
}

// recoverEnergy 精力惰性恢复（读不落库）：按整分钟数增益，保留子分钟余数防漂移。
func (s *ControlService) recoverEnergy(st *groupState, p ruleConfig, now time.Time) {
	elapsedMin := (now.Unix() - st.EnergyUpdatedAt) / 60
	if elapsedMin <= 0 {
		return
	}
	gain := int(elapsedMin) * p.energyRecover
	if st.Energy+gain >= p.energyMax {
		// 满格归位：更新时间戳到当前，避免下次重复计算。
		st.Energy = p.energyMax
		st.EnergyUpdatedAt = now.Unix()
		return
	}
	st.Energy += gain
	// 只前进整分钟，子分钟余数留待下次累计。
	st.EnergyUpdatedAt += elapsedMin * 60
}
