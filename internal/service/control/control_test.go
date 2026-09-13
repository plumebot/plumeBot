package control

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
)

// noon 是固定测试基准时刻（12:00，位于默认静默时段 23:00-07:00 之外）。
var noon = time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)

// fakeStore 嵌入 domain.Storage，覆写 GetGroupConfig / UpsertGroupConfig / GetBotState / UpsertBotState。
// cfgs 中缺失的群 → ErrNotFound（触发自动建行）；getErr 模拟 GetGroupConfig 故障；
// setConfigErr 模拟自动建行写库故障；getStateErr / setStateErr 模拟 bot_state 读/写故障。
// states 访问带锁（并发测试安全）；upserts 记录自动建行落库的快照供断言。
type fakeStore struct {
	domain.Storage
	mu           sync.Mutex
	cfgs         map[string]*entity.GroupConfig
	states       map[string]*entity.BotState
	upserts      map[string]entity.GroupConfig
	getErr       error
	setConfigErr error
	getStateErr  error
	setStateErr  error
}

func (f *fakeStore) GetGroupConfig(_ context.Context, groupID string) (*entity.GroupConfig, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if c, ok := f.cfgs[groupID]; ok {
		return c, nil
	}
	return nil, domain.ErrNotFound
}

func (f *fakeStore) UpsertGroupConfig(_ context.Context, cfg entity.GroupConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setConfigErr != nil {
		return f.setConfigErr
	}
	if f.upserts == nil {
		f.upserts = map[string]entity.GroupConfig{}
	}
	f.upserts[cfg.GroupID] = cfg
	return nil
}

func (f *fakeStore) GetBotState(_ context.Context, groupID string) (*entity.BotState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getStateErr != nil {
		return nil, f.getStateErr
	}
	if s, ok := f.states[groupID]; ok {
		return s, nil
	}
	return nil, domain.ErrNotFound
}

func (f *fakeStore) UpsertBotState(_ context.Context, st entity.BotState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setStateErr != nil {
		return f.setStateErr
	}
	if f.states == nil {
		f.states = map[string]*entity.BotState{}
	}
	s := st
	f.states[st.GroupID] = &s
	return nil
}

// setState 预置某会话的运行态（测试固定 now 需与 state 内时间戳一致）。
func (f *fakeStore) setState(t *testing.T, key string, st groupState) {
	t.Helper()
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal 状态失败: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.states == nil {
		f.states = map[string]*entity.BotState{}
	}
	f.states[key] = &entity.BotState{GroupID: key, State: string(b)}
}

// newTest 构造 ControlService 并固定 now 为可变基准时间（测试内可推进）。
func newTest(cfg config.ControlConfig, st *fakeStore) (*ControlService, *time.Time) {
	svc := NewControlService(cfg, st)
	nowT := noon
	svc.now = func() time.Time { return nowT }
	return svc, &nowT
}

func groupMsg(mentioned bool) entity.Message {
	return entity.Message{
		MessageID: "m1", GroupID: "g1", UserID: "u1", MessageType: "group", Mentioned: mentioned,
		Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: "这是一条足够长的普通群消息内容，超过短消息阈值。"}},
	}
}

func groupMsgText(mentioned bool, text string) entity.Message {
	m := groupMsg(mentioned)
	m.Parts = []entity.ContentPart{{Type: entity.PartTypeText, Text: text}}
	return m
}

func privateMsg() entity.Message {
	return entity.Message{
		MessageID: "m1", UserID: "u1", MessageType: "private", Mentioned: true,
		Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: "私聊消息"}},
	}
}

// ──────────────────────────── 既有 mode 表（P5-001 回归，P5-002 修正）────────────────────────────

// TestShouldReplyModeTable 表驱动：模式 × 群@/群非@/私聊 × 有/无 per-group 配置。
// P5-002 起：@/私聊 = 强制回复（mention_forced，绕过状态规则）；auto 群非@ 走状态规则。
func TestShouldReplyModeTable(t *testing.T) {
	cases := []struct {
		name   string
		global string                         // cfg.Control.Mode
		cfgs   map[string]*entity.GroupConfig // per-group 配置（nil = 无）
		msg    entity.Message
		want   bool
		reason entity.DecisionReason
	}{
		{"mention+群非@", "mention", nil, groupMsg(false), false, entity.DecisionReasonNotTriggered},
		{"mention+群@", "mention", nil, groupMsg(true), true, entity.DecisionReasonMentionForced},
		{"mention+私聊", "mention", nil, privateMsg(), true, entity.DecisionReasonMentionForced},
		{"auto+群非@", "auto", nil, groupMsg(false), true, entity.DecisionReasonAutoPass},
		{"auto+群@", "auto", nil, groupMsg(true), true, entity.DecisionReasonMentionForced},
		{"auto+私聊", "auto", nil, privateMsg(), true, entity.DecisionReasonMentionForced},
		{"全局mention+per-group auto", "mention",
			map[string]*entity.GroupConfig{"g1": {GroupID: "g1", Mode: "auto"}},
			groupMsg(false), true, entity.DecisionReasonAutoPass},
		{"全局auto+per-group mention", "auto",
			map[string]*entity.GroupConfig{"g1": {GroupID: "g1", Mode: "mention"}},
			groupMsg(true), true, entity.DecisionReasonMentionForced},
		{"per-group mode空→落全局", "mention",
			map[string]*entity.GroupConfig{"g1": {GroupID: "g1"}},
			groupMsg(false), false, entity.DecisionReasonNotTriggered},
		{"per-group 隔离：非配置群落全局", "auto",
			map[string]*entity.GroupConfig{"g2": {GroupID: "g2", Mode: "mention"}},
			groupMsg(false), true, entity.DecisionReasonAutoPass},
		{"全局mode空→mention兜底", "", nil, groupMsg(false), false, entity.DecisionReasonNotTriggered},
		{"全局mode非法→mention兜底", "foo", nil, groupMsg(false), false, entity.DecisionReasonNotTriggered},
		{"per-group mode非法→mention兜底", "auto",
			map[string]*entity.GroupConfig{"g1": {GroupID: "g1", Mode: "foo"}},
			groupMsg(false), false, entity.DecisionReasonNotTriggered},
		{"私聊忽略per-group配置", "mention",
			map[string]*entity.GroupConfig{"g1": {GroupID: "g1", Mode: "auto"}},
			privateMsg(), true, entity.DecisionReasonMentionForced},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTest(config.ControlConfig{Mode: tc.global}, &fakeStore{cfgs: tc.cfgs})
			got, err := svc.ShouldReply(context.Background(), tc.msg)
			if err != nil {
				t.Fatalf("ShouldReply 报错: %v", err)
			}
			if got.Reply != tc.want || got.Reason != tc.reason {
				t.Errorf("ShouldReply = {Reply:%v Reason:%q}, want {Reply:%v Reason:%q}",
					got.Reply, got.Reason, tc.want, tc.reason)
			}
		})
	}
}

// TestShouldReplyStoreError 存储故障（GetGroupConfig）应透传 error，不吞错误。
func TestShouldReplyStoreError(t *testing.T) {
	svc, _ := newTest(config.ControlConfig{Mode: "mention"}, &fakeStore{getErr: errors.New("db down")})
	_, err := svc.ShouldReply(context.Background(), groupMsg(false))
	if err == nil {
		t.Fatal("GetGroupConfig 故障时应返回 error")
	}
}

// TestGroupConfigAutoCreated 新群首次出现（无配置行）→ 快照全局生效值自动落一行（方案 A）。
func TestGroupConfigAutoCreated(t *testing.T) {
	st := &fakeStore{}
	svc, _ := newTest(config.ControlConfig{Mode: "auto"}, st)

	if _, err := svc.ShouldReply(context.Background(), groupMsg(false)); err != nil {
		t.Fatalf("ShouldReply 报错: %v", err)
	}

	got, ok := st.upserts["g1"]
	if !ok {
		t.Fatal("新群首次判定应自动建行")
	}
	if got.Mode != ModeAuto {
		t.Errorf("Mode = %q, want %q（应写归一化后的生效模式）", got.Mode, ModeAuto)
	}
	if got.GroupMgmtEnabled != 1 {
		t.Errorf("GroupMgmtEnabled = %d, want 1（0 = 显式关闭群管理）", got.GroupMgmtEnabled)
	}
	if got.EnergyMax != config.DefaultEnergyMax || got.CooldownSeconds != config.DefaultCooldownSeconds {
		t.Errorf("状态参数应为全局生效值快照, got %+v", got)
	}
	if got.QuietHoursStart != config.DefaultQuietHoursStart || got.QuietHoursEnd != config.DefaultQuietHoursEnd {
		t.Errorf("静默时段 = %q-%q, want %q-%q", got.QuietHoursStart, got.QuietHoursEnd,
			config.DefaultQuietHoursStart, config.DefaultQuietHoursEnd)
	}
}

// TestGroupConfigAutoCreateSkipAndFailure 已有配置行/私聊不建行；建行写库失败仅告警不阻断判定。
func TestGroupConfigAutoCreateSkipAndFailure(t *testing.T) {
	// 已有配置行：不重复建行。
	existing := &fakeStore{cfgs: map[string]*entity.GroupConfig{"g1": {GroupID: "g1", Mode: "auto"}}}
	svc, _ := newTest(config.ControlConfig{Mode: "auto"}, existing)
	if _, err := svc.ShouldReply(context.Background(), groupMsg(false)); err != nil {
		t.Fatalf("ShouldReply 报错: %v", err)
	}
	if len(existing.upserts) != 0 {
		t.Errorf("已有配置行不应重复建行, got %v", existing.upserts)
	}

	// 私聊无 per-group 概念：不建行。
	priv := &fakeStore{}
	svcPriv, _ := newTest(config.ControlConfig{Mode: "auto"}, priv)
	if _, err := svcPriv.ShouldReply(context.Background(), privateMsg()); err != nil {
		t.Fatalf("私聊 ShouldReply 报错: %v", err)
	}
	if len(priv.upserts) != 0 {
		t.Errorf("私聊不应建行, got %v", priv.upserts)
	}

	// 建行写库失败：仅告警，判定照常按全局配置返回。
	failing := &fakeStore{setConfigErr: errors.New("write db down")}
	svcFail, _ := newTest(config.ControlConfig{Mode: "auto"}, failing)
	got, err := svcFail.ShouldReply(context.Background(), groupMsg(false))
	if err != nil {
		t.Fatalf("建行失败不应阻断判定: %v", err)
	}
	if !got.Reply {
		t.Errorf("建行失败仍应按全局配置放行, got %+v", got)
	}
}

// ──────────────────────────── P5-002 状态规则 ────────────────────────────

// TestForcedReplyBypassesAll 强制回复（@/私聊）绕过全部状态规则，即使精力低/冷却中/静默/短消息。
func TestForcedReplyBypassesAll(t *testing.T) {
	st := &fakeStore{}
	st.setState(t, "g1", groupState{Energy: 0, EnergyUpdatedAt: noon.Unix(), LastReplyAt: noon.Unix(), RestUntil: noon.Add(time.Hour).Unix()})
	svc, nowT := newTest(config.ControlConfig{Mode: "auto"}, st)

	// 静默时段（now=02:00）+ 低精力 + 冷却中 + 休息中：@ 仍强制回复。
	*nowT = time.Date(2026, 1, 1, 2, 0, 0, 0, time.Local)
	got, err := svc.ShouldReply(context.Background(), groupMsgText(true, "在吗"))
	if err != nil {
		t.Fatalf("@ 消息不应报错: %v", err)
	}
	if !got.Reply || got.Reason != entity.DecisionReasonMentionForced {
		t.Errorf("低精力+静默+冷却下 @ 仍应强制回复, got %+v", got)
	}

	// 私聊同样绕过。
	got, err = svc.ShouldReply(context.Background(), privateMsg())
	if err != nil {
		t.Fatalf("私聊不应报错: %v", err)
	}
	if !got.Reply || got.Reason != entity.DecisionReasonMentionForced {
		t.Errorf("私聊应强制回复, got %+v", got)
	}
}

// TestShortMessageRule 短消息忽略：<N 字不触发 Agent 判断；恰好 N 字通过；@ 短消息绕过。
func TestShortMessageRule(t *testing.T) {
	st := &fakeStore{} // 空状态 → 精力满格，排除精力干扰
	svc, _ := newTest(config.ControlConfig{Mode: "auto"}, st)

	cases := []struct {
		name   string
		text   string
		want   bool
		reason entity.DecisionReason
	}{
		{"2字", "在吗", false, entity.DecisionReasonShortMessage},
		{"3字", "你好呀", false, entity.DecisionReasonShortMessage},
		{"恰好4字", "你好呀我", true, entity.DecisionReasonAutoPass},
		{"5字", "你好呀我是", true, entity.DecisionReasonAutoPass},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.ShouldReply(context.Background(), groupMsgText(false, tc.text))
			if err != nil {
				t.Fatalf("ShouldReply 报错: %v", err)
			}
			if got.Reply != tc.want || got.Reason != tc.reason {
				t.Errorf("短消息 %q = %+v, want {Reply:%v Reason:%q}", tc.text, got, tc.want, tc.reason)
			}
		})
	}

	// @ 短消息绕过。
	got, err := svc.ShouldReply(context.Background(), groupMsgText(true, "在吗"))
	if err != nil || !got.Reply || got.Reason != entity.DecisionReasonMentionForced {
		t.Errorf("@ 短消息应绕过规则, got %+v err %v", got, err)
	}
}

// TestQuietHoursRule 时段控制：默认 23:00-07:00；区内拦截、区外放行、start==end 空段禁用、跨午夜边界。
func TestQuietHoursRule(t *testing.T) {
	svc, nowT := newTest(config.ControlConfig{Mode: "auto"}, &fakeStore{})

	at := func(h, m int) { *nowT = time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }

	// 区内拦截。
	at(0, 30)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonQuietHours {
		t.Errorf("00:30 应静默, got %+v", got)
	}
	// 区外放行。
	at(12, 0)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonAutoPass {
		t.Errorf("12:00 应放行, got %+v", got)
	}
	// 跨午夜边界：23:00 整在区内（>= start）。
	at(23, 0)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonQuietHours {
		t.Errorf("23:00 应在区内, got %+v", got)
	}
	// 07:00 整在区外（< end 不成立）。
	at(7, 0)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonAutoPass {
		t.Errorf("07:00 应在区外, got %+v", got)
	}

	// per-group start==end → 空段禁用静默。
	st := &fakeStore{cfgs: map[string]*entity.GroupConfig{
		"g1": {GroupID: "g1", Mode: "auto", QuietHoursStart: "00:00", QuietHoursEnd: "00:00"},
	}}
	svc2, _ := newTest(config.ControlConfig{Mode: "auto"}, st)
	at(2, 0)
	if got, _ := svc2.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonAutoPass {
		t.Errorf("start==end 应禁用静默, got %+v", got)
	}
}

// TestEnergyRule 精力值：低于阈值不主动说话；恢复后回升放行；@ 绕过。
func TestEnergyRule(t *testing.T) {
	st := &fakeStore{}
	svc, _ := newTest(config.ControlConfig{Mode: "auto"}, st)

	// 预置低精力（10 < 阈值 20），时间戳 = noon（无恢复）。
	st.setState(t, "g1", groupState{Energy: 10, EnergyUpdatedAt: noon.Unix(), LastReplyAt: noon.Unix()})
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonLowEnergy {
		t.Errorf("精力 10 < 20 应 low_energy, got %+v", got)
	}

	// 恢复 2 分钟（+2*5=10 → 20，等于阈值不拦）；LastReplyAt 也在冷却窗口外。
	st.setState(t, "g1", groupState{Energy: 10, EnergyUpdatedAt: noon.Add(-2 * time.Minute).Unix(), LastReplyAt: noon.Add(-2 * time.Minute).Unix()})
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonAutoPass {
		t.Errorf("恢复后精力 20 应放行, got %+v", got)
	}

	// @ 低精力也强制回复。
	st.setState(t, "g1", groupState{Energy: 0, EnergyUpdatedAt: noon.Unix(), LastReplyAt: noon.Unix()})
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(true)); got.Reason != entity.DecisionReasonMentionForced {
		t.Errorf("@ 低精力应强制回复, got %+v", got)
	}
}

// TestCooldownRule 冷却时间：两次主动回复之间最小间隔（默认 60s）。
func TestCooldownRule(t *testing.T) {
	st := &fakeStore{}
	svc, nowT := newTest(config.ControlConfig{Mode: "auto"}, st)

	// 满精力 + 30 秒前回复过 → 冷却中。
	st.setState(t, "g1", groupState{Energy: 100, EnergyUpdatedAt: noon.Unix(), LastReplyAt: noon.Unix()})
	*nowT = noon.Add(30 * time.Second)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonCooldown {
		t.Errorf("30s 内应冷却, got %+v", got)
	}

	// 61 秒后 → 放行。
	*nowT = noon.Add(61 * time.Second)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonAutoPass {
		t.Errorf("61s 后应放行, got %+v", got)
	}
}

// TestConsecutiveRest 连续回复上限：达 N 进入固定休息期；期内主动发言拦截；
// 期内 @ 回复不递增计数（防无限延长）；休息结束恢复。
func TestConsecutiveRest(t *testing.T) {
	st := &fakeStore{}
	svc, nowT := newTest(config.ControlConfig{Mode: "auto"}, st)

	// 连续回复 5 次（默认 consecutive_limit=5），每次间隔 < cooldown（60s）视为连续。
	for i := 0; i < 5; i++ {
		*nowT = noon.Add(time.Duration(i) * 10 * time.Second)
		if err := svc.OnReplied(context.Background(), groupMsg(false)); err != nil {
			t.Fatalf("OnReplied[%d] 失败: %v", i, err)
		}
	}
	stState := st.states["g1"]
	var gs groupState
	if err := json.Unmarshal([]byte(stState.State), &gs); err != nil {
		t.Fatalf("解析状态失败: %v", err)
	}
	if gs.RestUntil != noon.Add(340*time.Second).Unix() {
		t.Errorf("连续达上限应进入休息, RestUntil=%d, 期望 %d", gs.RestUntil, noon.Add(340*time.Second).Unix())
	}

	// 休息期内（+100s < RestUntil=+340s）主动发言 → cooldown。
	*nowT = noon.Add(100 * time.Second)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonCooldown {
		t.Errorf("休息期应拦截, got %+v", got)
	}

	// 休息期内 @ 回复：应回复（绕过），且不递增计数/延长休息。
	*nowT = noon.Add(150 * time.Second)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(true)); got.Reason != entity.DecisionReasonMentionForced {
		t.Errorf("休息期 @ 应强制回复, got %+v", got)
	}
	if err := svc.OnReplied(context.Background(), groupMsg(true)); err != nil {
		t.Fatalf("休息期 OnReplied 失败: %v", err)
	}
	if err := json.Unmarshal([]byte(st.states["g1"].State), &gs); err != nil {
		t.Fatalf("解析状态失败: %v", err)
	}
	if gs.RestUntil != noon.Add(340*time.Second).Unix() {
		t.Errorf("休息期内回复不应延长休息, RestUntil=%d", gs.RestUntil)
	}

	// 休息结束（+360s > RestUntil=+340s）→ 放行；下一条回复计数重置为 1。
	*nowT = noon.Add(360 * time.Second)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonAutoPass {
		t.Errorf("休息结束应放行, got %+v", got)
	}
	if err := svc.OnReplied(context.Background(), groupMsg(false)); err != nil {
		t.Fatalf("休息后 OnReplied 失败: %v", err)
	}
	if err := json.Unmarshal([]byte(st.states["g1"].State), &gs); err != nil {
		t.Fatalf("解析状态失败: %v", err)
	}
	if gs.ConsecutiveCount != 1 {
		t.Errorf("休息后计数应重置为 1, got %d", gs.ConsecutiveCount)
	}
}

// TestPrivateIndependentState 私聊独立状态：key = "private:"+UserID，互不影响。
func TestPrivateIndependentState(t *testing.T) {
	st := &fakeStore{}
	svc, _ := newTest(config.ControlConfig{Mode: "auto"}, st)

	u1 := entity.Message{MessageID: "p1", UserID: "u1", MessageType: "private", Mentioned: true,
		Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: "私聊1"}}}
	u2 := entity.Message{MessageID: "p2", UserID: "u2", MessageType: "private", Mentioned: true,
		Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: "私聊2"}}}

	if err := svc.OnReplied(context.Background(), u1); err != nil {
		t.Fatalf("u1 私聊 OnReplied 失败: %v", err)
	}
	if err := svc.OnReplied(context.Background(), u2); err != nil {
		t.Fatalf("u2 私聊 OnReplied 失败: %v", err)
	}
	if _, ok := st.states["private:u1"]; !ok {
		t.Error("u1 私聊应有独立状态 private:u1")
	}
	if _, ok := st.states["private:u2"]; !ok {
		t.Error("u2 私聊应有独立状态 private:u2")
	}
	if _, ok := st.states["u1"]; ok {
		t.Error("私聊状态不应落 group key")
	}
}

// TestPerGroupOverride per-group 列覆盖全局；全 0 行 → 全局兜底。
func TestPerGroupOverride(t *testing.T) {
	// per-group energy_threshold=50 覆盖全局 20：精力 30 全局下放行、覆盖后拦截。
	st := &fakeStore{cfgs: map[string]*entity.GroupConfig{
		"g1": {GroupID: "g1", Mode: "auto", EnergyThreshold: 50},
	}}
	svc, _ := newTest(config.ControlConfig{Mode: "auto", State: config.ControlStateConfig{EnergyThreshold: 20}}, st)
	st.setState(t, "g1", groupState{Energy: 30, EnergyUpdatedAt: noon.Unix(), LastReplyAt: noon.Unix()})
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonLowEnergy {
		t.Errorf("per-group 阈值 50 > 精力 30 应 low_energy, got %+v", got)
	}

	// per-group 全 0 行（仅 mode）→ 全局阈值 20：精力 30 放行（LastReplyAt 在冷却窗口外）。
	st2 := &fakeStore{cfgs: map[string]*entity.GroupConfig{
		"g1": {GroupID: "g1", Mode: "auto"},
	}}
	svc2, _ := newTest(config.ControlConfig{Mode: "auto", State: config.ControlStateConfig{EnergyThreshold: 20}}, st2)
	st2.setState(t, "g1", groupState{Energy: 30, EnergyUpdatedAt: noon.Unix(), LastReplyAt: noon.Add(-2 * time.Minute).Unix()})
	if got, _ := svc2.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonAutoPass {
		t.Errorf("per-group 全 0 应落全局阈值 20 放行, got %+v", got)
	}
}

// TestShouldReplyStateReadError 状态读取故障：主动发言 fail-closed 透传 error；@ 强制回复不受影响。
func TestShouldReplyStateReadError(t *testing.T) {
	st := &fakeStore{getStateErr: errors.New("state db down")}
	svc, _ := newTest(config.ControlConfig{Mode: "auto"}, st)

	if _, err := svc.ShouldReply(context.Background(), groupMsg(false)); err == nil {
		t.Fatal("状态读取故障时主动发言应返回 error")
	}
	if got, err := svc.ShouldReply(context.Background(), groupMsg(true)); err != nil || got.Reason != entity.DecisionReasonMentionForced {
		t.Errorf("@ 强制回复不应受状态故障影响, got %+v err %v", got, err)
	}
}

// TestOnReplied 回复后回调：消耗精力、记 LastReplyAt、递增连续计数、持久化。
func TestOnReplied(t *testing.T) {
	st := &fakeStore{}
	svc, _ := newTest(config.ControlConfig{Mode: "auto"}, st)

	if err := svc.OnReplied(context.Background(), groupMsg(false)); err != nil {
		t.Fatalf("OnReplied 失败: %v", err)
	}
	bs, ok := st.states["g1"]
	if !ok {
		t.Fatal("OnReplied 后应有持久化状态")
	}
	var gs groupState
	if err := json.Unmarshal([]byte(bs.State), &gs); err != nil {
		t.Fatalf("解析状态失败: %v", err)
	}
	if gs.Energy != config.DefaultEnergyMax-config.DefaultEnergyCost {
		t.Errorf("精力应扣减为 %d, got %d", config.DefaultEnergyMax-config.DefaultEnergyCost, gs.Energy)
	}
	if gs.ConsecutiveCount != 1 {
		t.Errorf("连续计数应为 1, got %d", gs.ConsecutiveCount)
	}
	if gs.LastReplyAt != noon.Unix() || gs.EnergyUpdatedAt != noon.Unix() {
		t.Errorf("回复/更新时刻应 = noon, got LastReplyAt=%d EnergyUpdatedAt=%d", gs.LastReplyAt, gs.EnergyUpdatedAt)
	}
}

// TestOnRepliedPersistError 状态写失败应返回 error（event 层告警不阻断）。
func TestOnRepliedPersistError(t *testing.T) {
	svc, _ := newTest(config.ControlConfig{Mode: "auto"}, &fakeStore{setStateErr: errors.New("write db down")})
	if err := svc.OnReplied(context.Background(), groupMsg(false)); err == nil {
		t.Fatal("状态写失败应返回 error")
	}
}

// TestMergeQuietHoursPartial per-group 只配 quiet_hours_start：start 覆盖全局、end 保留全局默认。
func TestMergeQuietHoursPartial(t *testing.T) {
	st := &fakeStore{cfgs: map[string]*entity.GroupConfig{
		"g1": {GroupID: "g1", Mode: "auto", QuietHoursStart: "22:00"},
	}}
	svc, nowT := newTest(config.ControlConfig{Mode: "auto"}, st)

	at := func(h, m int) { *nowT = time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }
	// per-group start=22:00 + 全局默认 end=07:00 → 静默段 22:00-07:00（跨午夜）。
	at(22, 30)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonQuietHours {
		t.Errorf("per-group start=22:00 应 22:30 静默, got %+v", got)
	}
	// 21:00 在段外（全局默认 23:00 起才静默，per-group 已提前到 22:00）。
	at(21, 0)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonAutoPass {
		t.Errorf("21:00 应在段外放行, got %+v", got)
	}
	// end 保留全局默认 07:00：07:00 整在段外。
	at(7, 0)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonAutoPass {
		t.Errorf("end 保留全局 07:00, 07:00 应在段外, got %+v", got)
	}
}

// TestMergeInvalidQuietHours per-group 时段非法 HH:MM → 静默回落全局默认，不破坏规则。
func TestMergeInvalidQuietHours(t *testing.T) {
	st := &fakeStore{cfgs: map[string]*entity.GroupConfig{
		"g1": {GroupID: "g1", Mode: "auto", QuietHoursStart: "25:00"},
	}}
	svc, nowT := newTest(config.ControlConfig{Mode: "auto"}, st)

	at := func(h, m int) { *nowT = time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }
	// start 非法 → 保留全局默认 23:00-07:00。
	at(0, 30)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonQuietHours {
		t.Errorf("非法 start 应回落全局静默段, 00:30 应静默, got %+v", got)
	}
	at(12, 0)
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonAutoPass {
		t.Errorf("非法 start 回落全局, 12:00 应放行, got %+v", got)
	}
}

// TestEnergyRefill 精力恢复：满格归位 + 整分钟粒度（子分钟余数保留到下次）。
func TestEnergyRefill(t *testing.T) {
	st := &fakeStore{}
	svc, _ := newTest(config.ControlConfig{Mode: "auto"}, st)

	// 满格归位：Energy=95、10 分钟前 → 恢复 50 达上限 → 满格 100 再扣 cost → 90，EnergyUpdatedAt 归位到 now。
	st.setState(t, "g1", groupState{Energy: 95, EnergyUpdatedAt: noon.Add(-10 * time.Minute).Unix(), LastReplyAt: noon.Unix()})
	if err := svc.OnReplied(context.Background(), groupMsg(false)); err != nil {
		t.Fatalf("OnReplied 失败: %v", err)
	}
	var gs groupState
	if err := json.Unmarshal([]byte(st.states["g1"].State), &gs); err != nil {
		t.Fatalf("解析状态失败: %v", err)
	}
	if gs.Energy != config.DefaultEnergyMax-config.DefaultEnergyCost {
		t.Errorf("满格恢复再扣后 Energy 应 = %d, got %d", config.DefaultEnergyMax-config.DefaultEnergyCost, gs.Energy)
	}
	if gs.EnergyUpdatedAt != noon.Unix() {
		t.Errorf("满格归位后 EnergyUpdatedAt 应 = now, got %d", gs.EnergyUpdatedAt)
	}

	// 整分钟粒度：跨 1 分钟恢复 5 点 → 越过阈值（19→24 放行）；跨 30 秒不足 1 分钟不恢复（19 仍低）。
	st.setState(t, "g1", groupState{Energy: 19, EnergyUpdatedAt: noon.Add(-1 * time.Minute).Unix(), LastReplyAt: noon.Add(-2 * time.Minute).Unix()})
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonAutoPass {
		t.Errorf("跨 1 分钟恢复 5 点后 24≥20 应放行, got %+v", got)
	}
	st.setState(t, "g1", groupState{Energy: 19, EnergyUpdatedAt: noon.Add(-30 * time.Second).Unix(), LastReplyAt: noon.Add(-2 * time.Minute).Unix()})
	if got, _ := svc.ShouldReply(context.Background(), groupMsg(false)); got.Reason != entity.DecisionReasonLowEnergy {
		t.Errorf("跨 30 秒不足 1 分钟不恢复, 19<20 应 low_energy, got %+v", got)
	}
}

// TestOnRepliedConcurrent 并发 OnReplied 同一会话：per-session 锁串行化读-改-写，
// 能量精确扣减 10 次（无锁会丢更新导致能量偏高）。
func TestOnRepliedConcurrent(t *testing.T) {
	st := &fakeStore{}
	svc, _ := newTest(config.ControlConfig{Mode: "auto"}, st)

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.OnReplied(context.Background(), groupMsg(false)); err != nil {
				t.Errorf("并发 OnReplied 失败: %v", err)
			}
		}()
	}
	wg.Wait()

	st.mu.Lock()
	bs, ok := st.states["g1"]
	st.mu.Unlock()
	if !ok {
		t.Fatal("并发回复后应有持久化状态")
	}
	var gs groupState
	if err := json.Unmarshal([]byte(bs.State), &gs); err != nil {
		t.Fatalf("解析状态失败: %v", err)
	}
	if want := config.DefaultEnergyMax - n*config.DefaultEnergyCost; gs.Energy != want {
		t.Errorf("并发 %d 次回复后 Energy 应 = %d（无丢更新）, got %d", n, want, gs.Energy)
	}
}
