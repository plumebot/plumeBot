package admin

// 管理后端配置域单测（P7-001）：真 SQLite store（DB 语义已由 infra 单测覆盖）+ 假缓存失效器
//（断言 group_profile 写/删后 InvalidateGroupProfile 被触发）。

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"plumebot/internal/domain/entity"
	"plumebot/internal/infra/sqlite"
)

// fakeInvalidator 记录 InvalidateGroupProfile 的调用，断言缓存失效触发。
type fakeInvalidator struct {
	mu      sync.Mutex
	called  map[string]int
}

func (f *fakeInvalidator) InvalidateGroupProfile(groupID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.called == nil {
		f.called = make(map[string]int)
	}
	f.called[groupID]++
}

func (f *fakeInvalidator) got(groupID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.called[groupID]
}

func newTestAdminSvc(t *testing.T) (*Service, *fakeInvalidator, *sqlite.Storage) {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	inv := &fakeInvalidator{}
	return &Service{store: store, mem: inv}, inv, store
}

func TestUpsertGetDeleteListGroupConfig(t *testing.T) {
	svc, _, _ := newTestAdminSvc(t)
	ctx := context.Background()

	got, err := svc.UpsertGroupConfig(ctx, "admin", entity.GroupConfig{GroupID: "g1", Mode: "auto", EnergyMax: 50, GroupMgmtEnabled: 1})
	if err != nil {
		t.Fatalf("UpsertGroupConfig 失败: %v", err)
	}
	if got.Mode != "auto" || got.EnergyMax != 50 || got.GroupMgmtEnabled != 1 {
		t.Fatalf("应返回校验后的最新配置, 实际: %+v", got)
	}
	fetched, err := svc.GetGroupConfig(ctx, "g1")
	if err != nil || fetched.EnergyMax != 50 {
		t.Fatalf("查询结果不符: %v %+v", err, fetched)
	}

	// 删除恢复全局兜底。
	if err := svc.DeleteGroupConfig(ctx, "admin", "g1"); err != nil {
		t.Fatalf("DeleteGroupConfig 失败: %v", err)
	}
	if _, err := svc.GetGroupConfig(ctx, "g1"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("删除后应 ErrNotFound, 实际: %v", err)
	}
	// 删除不存在 → ErrNotFound。
	if err := svc.DeleteGroupConfig(ctx, "admin", "g-nope"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("删除不存在应 ErrNotFound, 实际: %v", err)
	}

	// 列表只含已配置群。
	if _, err := svc.UpsertGroupConfig(ctx, "admin", entity.GroupConfig{GroupID: "g2", Mode: "mention"}); err != nil {
		t.Fatalf("UpsertGroupConfig 失败: %v", err)
	}
	items, err := svc.ListGroupConfigs(ctx)
	if err != nil || len(items) != 1 || items[0].GroupID != "g2" || items[0].Mode != "mention" {
		t.Fatalf("列表应含已配置群: %v %+v", err, items)
	}
}

func TestValidateGroupConfig(t *testing.T) {
	svc, _, _ := newTestAdminSvc(t)
	ctx := context.Background()

	for i, c := range []entity.GroupConfig{
		{GroupID: "g-bad", Mode: "random"},                                   // 非法 mode
		{GroupID: "g-bad", Mode: "auto", GroupMgmtEnabled: 2},                // 非法开关
		{GroupID: "g-bad", Mode: "auto", CooldownSeconds: -1},                // 负数
		{GroupID: "g-bad", Mode: "auto", QuietHoursStart: "25:00"},           // 非法 HH:MM
		{GroupID: "g-bad", Mode: "", GroupMgmtEnabled: 2},                    // 空 mode + 非法开关
	} {
		if _, err := svc.UpsertGroupConfig(ctx, "admin", c); !isValidation(err) {
			t.Errorf("用例 %d 应校验失败: %v", i, err)
		}
	}
	// 合法：空串时段（走全局）+ 跨午夜区间 + 内部整体允许。
	if _, err := svc.UpsertGroupConfig(ctx, "admin", entity.GroupConfig{
		GroupID: "g", Mode: "auto", QuietHoursStart: "22:30", QuietHoursEnd: "07:30",
	}); err != nil {
		t.Errorf("合法时段应通过: %v", err)
	}
	// 校验失败不应落库（脏数据防入，fail-fast 纪律）。
	if _, err := svc.GetGroupConfig(ctx, "g-bad"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("校验失败不应落库, 实际: %v", err)
	}
}

func TestUpsertGetListPersona(t *testing.T) {
	svc, _, _ := newTestAdminSvc(t)
	ctx := context.Background()

	if _, err := svc.UpsertPersona(ctx, "admin", entity.Persona{Agent: "PlumeBot", Name: "默认", SystemPrompt: "你是赛博群友。"}); err != nil {
		t.Fatalf("UpsertPersona 失败: %v", err)
	}
	got, err := svc.GetPersonaByAgent(ctx, "PlumeBot")
	if err != nil || got.SystemPrompt != "你是赛博群友。" {
		t.Fatalf("查询人格失败: %v %+v", err, got)
	}
	// 同 agent 更新（幂等覆盖）。
	if _, err := svc.UpsertPersona(ctx, "admin", entity.Persona{Agent: "PlumeBot", Name: "新名", SystemPrompt: "改版人设。"}); err != nil {
		t.Fatalf("UpsertPersona 覆盖失败: %v", err)
	}
	items, err := svc.ListPersonas(ctx)
	if err != nil || len(items) != 1 || items[0].SystemPrompt != "改版人设。" {
		t.Fatalf("列表应含更新后模板: %v %+v", err, items)
	}

	// 校验：空 system_prompt / 超长。
	if _, err := svc.UpsertPersona(ctx, "admin", entity.Persona{Agent: "x", SystemPrompt: ""}); !isValidation(err) {
		t.Fatalf("空 system_prompt 应校验失败: %v", err)
	}
	if _, err := svc.UpsertPersona(ctx, "admin", entity.Persona{Agent: "x", SystemPrompt: strings.Repeat("a", maxSystemPromptLen+1)}); !isValidation(err) {
		t.Fatalf("超长 system_prompt 应校验失败: %v", err)
	}
}

func TestUpsertDeleteGroupProfileInvalidate(t *testing.T) {
	svc, inv, _ := newTestAdminSvc(t)
	ctx := context.Background()

	if _, err := svc.UpsertGroupProfile(ctx, "admin", entity.GroupProfile{GroupID: "g1", Culture: "文化A"}); err != nil {
		t.Fatalf("UpsertGroupProfile 失败: %v", err)
	}
	if inv.got("g1") != 1 {
		t.Fatalf("写后应失效缓存 g1, 实际 %d", inv.got("g1"))
	}
	// 删除 → 再次失效。
	if err := svc.DeleteGroupProfile(ctx, "admin", "g1"); err != nil {
		t.Fatalf("DeleteGroupProfile 失败: %v", err)
	}
	if inv.got("g1") != 2 {
		t.Fatalf("删后应失效缓存 g1, 实际 %d", inv.got("g1"))
	}
	// 删不存在 → ErrNotFound 且不触发失效（写库失败先返回）。
	if err := svc.DeleteGroupProfile(ctx, "admin", "g-nope"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("删不存在应 ErrNotFound, 实际: %v", err)
	}
	if inv.got("g-nope") != 0 {
		t.Fatalf("删除失败不应失效缓存, 实际 %d", inv.got("g-nope"))
	}
	// 画像数组 sanitize：trim 去空。
	if _, err := svc.UpsertGroupProfile(ctx, "admin", entity.GroupProfile{
		GroupID: "g2", Topics: []string{"  a ", "", "  b "},
	}); err != nil {
		t.Fatalf("UpsertGroupProfile 失败: %v", err)
	}
	got, err := svc.GetGroupProfile(ctx, "g2")
	if err != nil || len(got.Topics) != 2 || got.Topics[0] != "a" || got.Topics[1] != "b" {
		t.Fatalf("数组应 trim 去空: %v %+v", err, got)
	}
}

func TestJargonAddConfirmListDelete(t *testing.T) {
	svc, _, store := newTestAdminSvc(t)
	ctx := context.Background()

	// 管理面添加默认 confirmed。
	j, err := svc.AddJargon(ctx, "admin", "g1", "yyds")
	if err != nil || j.Status != "confirmed" {
		t.Fatalf("添加应默认 confirmed: %v %+v", err, j)
	}
	// 重复添加强视为成功（D8）。
	if _, err := svc.AddJargon(ctx, "admin", "g1", "yyds"); err != nil {
		t.Fatalf("重复添加应视为成功: %v", err)
	}
	items, err := svc.ListJargonWithStatus(ctx, "g1", "all")
	if err != nil || len(items) != 1 || items[0].Status != "confirmed" {
		t.Fatalf("列表应含 1 条 confirmed: %v %+v", err, items)
	}

	// 删除存在 → 成功；删除不存在 → ErrNotFound（D7）。
	if err := svc.DeleteJargon(ctx, "admin", "g1", "yyds"); err != nil {
		t.Fatalf("DeleteJargon 失败: %v", err)
	}
	if err := svc.DeleteJargon(ctx, "admin", "g1", "nope"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("删不存在应 ErrNotFound, 实际: %v", err)
	}

	// pending → confirm 流转（模拟 Agent learn_jargon 写入）。
	if err := store.AddJargon(ctx, "g2", "绝绝子"); err != nil {
		t.Fatalf("store.AddJargon 失败: %v", err)
	}
	if _, err := svc.ConfirmJargon(ctx, "admin", "g2", "绝绝子"); err != nil {
		t.Fatalf("ConfirmJargon 失败: %v", err)
	}
	confirmed, _ := svc.ListJargonWithStatus(ctx, "g2", "confirmed")
	pending, _ := svc.ListJargonWithStatus(ctx, "g2", "pending")
	if len(confirmed) != 1 || len(pending) != 0 {
		t.Fatalf("确认后应仅出现在 confirmed: %d/%d", len(confirmed), len(pending))
	}
	// Confirm 不存在 → ErrNotFound。
	if _, err := svc.ConfirmJargon(ctx, "admin", "g2", "nope"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("确认不存在应 ErrNotFound, 实际: %v", err)
	}
	// 空黑话校验。
	if _, err := svc.AddJargon(ctx, "admin", "g1", "  "); !isValidation(err) {
		t.Fatalf("空黑话应校验失败: %v", err)
	}
}

func TestMemberFactAddDelete(t *testing.T) {
	svc, _, _ := newTestAdminSvc(t)
	ctx := context.Background()

	// 校验：user_id 空 / fact 空。
	if err := svc.AddMemberFact(ctx, "admin", "g1", "", "喜欢猫"); !isValidation(err) {
		t.Fatalf("空 user_id 应校验失败: %v", err)
	}
	if err := svc.AddMemberFact(ctx, "admin", "g1", "u1", "  "); !isValidation(err) {
		t.Fatalf("空 fact 应校验失败: %v", err)
	}
	// trim 落库。
	if err := svc.AddMemberFact(ctx, "admin", "g1", "u1", "  喜欢猫  "); err != nil {
		t.Fatalf("AddMemberFact 失败: %v", err)
	}
	items, err := svc.ListMemberFacts(ctx, "g1", "u1")
	if err != nil || len(items) != 1 || items[0] != "喜欢猫" {
		t.Fatalf("应 trim 后落库: %v %+v", err, items)
	}
	// 删不存在 → ErrNotFound；存在 → 成功。
	if err := svc.DeleteMemberFact(ctx, "admin", "g1", "u1", "nope"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("删不存在应 ErrNotFound, 实际: %v", err)
	}
	if err := svc.DeleteMemberFact(ctx, "admin", "g1", "u1", "喜欢猫"); err != nil {
		t.Fatalf("DeleteMemberFact 失败: %v", err)
	}
}

func TestGetBotStateReadOnly(t *testing.T) {
	svc, _, store := newTestAdminSvc(t)
	ctx := context.Background()

	if err := store.UpsertBotState(ctx, entity.BotState{GroupID: "g1", State: `{"energy":50}`}); err != nil {
		t.Fatalf("UpsertBotState 失败: %v", err)
	}
	st, err := svc.GetBotState(ctx, "g1")
	if err != nil || !strings.Contains(st.State, "energy") {
		t.Fatalf("应能读运行态: %v %+v", err, st)
	}
	if _, err := svc.GetBotState(ctx, "g-nope"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("不存在应 ErrNotFound, 实际: %v", err)
	}
}