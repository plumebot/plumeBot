package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"plumebot/internal/domain/entity"
	"plumebot/internal/infra/sqlite"
)

// openTestStore 打开临时目录下的测试数据库。
func openTestStore(t *testing.T) *sqlite.Storage {
	t.Helper()
	s, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// invoke 把 args 序列化为 JSON 后执行工具的 InvokableRun。
func invoke(t *testing.T, bt tool.BaseTool, ctx context.Context, args any) (string, error) {
	t.Helper()
	inv, ok := bt.(tool.InvokableTool)
	if !ok {
		t.Fatalf("工具 %T 未实现 InvokableTool", bt)
	}
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("序列化参数失败: %v", err)
	}
	return inv.InvokableRun(ctx, string(b))
}

// TestStoreFact user_id 缺省取当前说话人；显式传值记到指定用户。
func TestStoreFact(t *testing.T) {
	s := openTestStore(t)
	mt := NewMemoryTools(s)
	ctx := entity.WithSession(context.Background(), entity.Session{GroupID: "g1", UserID: "u1"})

	if _, err := invoke(t, mt.StoreFact(), ctx, map[string]string{"fact": "喜欢猫"}); err != nil {
		t.Fatalf("store_fact(缺省 user_id) 失败: %v", err)
	}
	facts, err := s.ListMemberFacts(ctx, "g1", "u1")
	if err != nil {
		t.Fatalf("ListMemberFacts 失败: %v", err)
	}
	if len(facts) != 1 || facts[0] != "喜欢猫" {
		t.Errorf("缺省 user_id 应记到当前说话人 u1，实际 %v", facts)
	}

	if _, err := invoke(t, mt.StoreFact(), ctx, map[string]string{"user_id": "u2", "fact": "是程序员"}); err != nil {
		t.Fatalf("store_fact(显式 user_id) 失败: %v", err)
	}
	u2facts, _ := s.ListMemberFacts(ctx, "g1", "u2")
	if len(u2facts) != 1 || u2facts[0] != "是程序员" {
		t.Errorf("显式 user_id 应记到 u2，实际 %v", u2facts)
	}
	u1facts, _ := s.ListMemberFacts(ctx, "g1", "u1")
	if len(u1facts) != 1 {
		t.Errorf("u1 不应被显式调用污染，实际 %v", u1facts)
	}
}

// TestStoreFactNoSession 无会话身份时工具应报错且不落库。
func TestStoreFactNoSession(t *testing.T) {
	s := openTestStore(t)
	mt := NewMemoryTools(s)

	_, err := invoke(t, mt.StoreFact(), context.Background(), map[string]string{"fact": "不该落库"})
	if err == nil || !strings.Contains(err.Error(), "会话上下文") {
		t.Fatalf("无会话上下文应报错，实际 %v", err)
	}
	facts, _ := s.ListMemberFacts(context.Background(), "g1", "u1")
	if len(facts) != 0 {
		t.Errorf("无会话上下文不应落库，实际 %v", facts)
	}
}

// TestStoreFactIdempotent 完全相同的重复事实被去重，不产生重复行。
func TestStoreFactIdempotent(t *testing.T) {
	s := openTestStore(t)
	mt := NewMemoryTools(s)
	ctx := entity.WithSession(context.Background(), entity.Session{GroupID: "g1", UserID: "u1"})

	for i := 0; i < 2; i++ {
		if _, err := invoke(t, mt.StoreFact(), ctx, map[string]string{"fact": "喜欢猫"}); err != nil {
			t.Fatalf("第 %d 次 store_fact 失败: %v", i+1, err)
		}
	}
	facts, _ := s.ListMemberFacts(ctx, "g1", "u1")
	if len(facts) != 1 {
		t.Errorf("重复事实应去重，实际 %d 条: %v", len(facts), facts)
	}
}

// TestStoreFactPrivate 私聊（会话无群 ID）时 store_fact 落库 group_id=""，验证「私聊个人记忆」。
func TestStoreFactPrivate(t *testing.T) {
	s := openTestStore(t)
	mt := NewMemoryTools(s)
	ctx := entity.WithSession(context.Background(), entity.Session{UserID: "u1"})

	if _, err := invoke(t, mt.StoreFact(), ctx, map[string]string{"fact": "私聊里的爱好"}); err != nil {
		t.Fatalf("store_fact(私聊) 失败: %v", err)
	}
	facts, err := s.ListMemberFacts(ctx, "", "u1")
	if err != nil {
		t.Fatalf("ListMemberFacts 失败: %v", err)
	}
	if len(facts) != 1 || facts[0] != "私聊里的爱好" {
		t.Errorf("私聊事实应落库到 group_id=\"\"，实际 %v", facts)
	}
	// 群聊隔离：g1 下无该事实。
	groupFacts, _ := s.ListMemberFacts(ctx, "g1", "u1")
	if len(groupFacts) != 0 {
		t.Errorf("私聊事实不应出现在群聊 g1，实际 %v", groupFacts)
	}
}

// TestLearnJargonPending 黑话写入后为 pending（不在 confirmed 列表），确认后进入 confirmed。
func TestLearnJargonPending(t *testing.T) {
	s := openTestStore(t)
	mt := NewMemoryTools(s)
	ctx := entity.WithSession(context.Background(), entity.Session{GroupID: "g1", UserID: "u1"})

	if _, err := invoke(t, mt.LearnJargon(), ctx, map[string]string{"jargon": "kwi"}); err != nil {
		t.Fatalf("learn_jargon 失败: %v", err)
	}

	confirmed, _ := s.ListConfirmedJargon(ctx, "g1")
	if len(confirmed) != 0 {
		t.Errorf("新学黑话应为 pending（不在 confirmed 列表），实际 %v", confirmed)
	}
	all, _ := s.ListJargon(ctx, "g1")
	if len(all) != 1 || all[0] != "kwi" {
		t.Errorf("全量黑话应含刚学的词，实际 %v", all)
	}

	if err := s.ConfirmJargon(ctx, "g1", "kwi"); err != nil {
		t.Fatalf("ConfirmJargon 失败: %v", err)
	}
	confirmed, _ = s.ListConfirmedJargon(ctx, "g1")
	if len(confirmed) != 1 || confirmed[0] != "kwi" {
		t.Errorf("确认后应进入 confirmed 列表，实际 %v", confirmed)
	}

	// 重复确认幂等（已 confirmed 再次确认不报错）。
	if err := s.ConfirmJargon(ctx, "g1", "kwi"); err != nil {
		t.Errorf("重复确认应幂等，实际 %v", err)
	}
}

// TestLearnJargonPrivate 私聊（无群 ID）时 learn_jargon 应报错。
func TestLearnJargonPrivate(t *testing.T) {
	s := openTestStore(t)
	mt := NewMemoryTools(s)
	ctx := entity.WithSession(context.Background(), entity.Session{UserID: "u1"})

	_, err := invoke(t, mt.LearnJargon(), ctx, map[string]string{"jargon": "kwi"})
	if err == nil || !strings.Contains(err.Error(), "仅群聊") {
		t.Fatalf("私聊学黑话应报错，实际 %v", err)
	}
	all, _ := s.ListJargon(ctx, "g1")
	if len(all) != 0 {
		t.Errorf("私聊不应写入黑话，实际 %v", all)
	}
}

// TestForgetFact 先存后删：forget_fact 移除对应事实。
func TestForgetFact(t *testing.T) {
	s := openTestStore(t)
	mt := NewMemoryTools(s)
	ctx := entity.WithSession(context.Background(), entity.Session{GroupID: "g1", UserID: "u1"})

	if _, err := invoke(t, mt.StoreFact(), ctx, map[string]string{"fact": "喜欢猫"}); err != nil {
		t.Fatalf("store_fact 失败: %v", err)
	}
	if _, err := invoke(t, mt.ForgetFact(), ctx, map[string]string{"fact": "喜欢猫"}); err != nil {
		t.Fatalf("forget_fact 失败: %v", err)
	}
	facts, _ := s.ListMemberFacts(ctx, "g1", "u1")
	if len(facts) != 0 {
		t.Errorf("forget_fact 后事实应被删除，实际 %v", facts)
	}
}
