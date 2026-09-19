package memory

import (
	"context"
	"strings"
	"testing"
)

// 本文件是「上下文混乱」问题的审计基线（用户反馈驱动，P6 联调期写测试解析机制）：
//
//	issue② 回复对象混淆：A 说「你是人机」、B 说 hello，bot 却对 B 回「你才是人机」；
//	issue③ 上下文只有 QQ 号、无昵称：AI 只能用 QQ 号称呼/回复。
//
// issue③ 已落地（昵称注入：entity.Message.SenderName → speakerText/画像成员事实「昵称」优先、
// 无昵称回落 QQ 号，见 convert.go）；本文件断言已切到新行为，并保留 issue② 的
// 「⑤ 与 ④ 同构无目标标记」基线（prompt 侧标记修复未实施，仍待评审）。

// TestBuildMessagesCurrentSameFormatAsHistory 复现 A/B 双人场景：
// 窗口④ [10001]: 你是人机，当前⑤ [10002]: hello —— 除位置外没有任何标记区分
// 「这正是本轮要回复的消息」。模型只能靠 system 里 replyInstruction 一句位置性提示
// 「回复最后一条消息」锚定，而历史中含高对抗内容时很容易锚错（“你才是人机”落给 B）。
func TestBuildMessagesCurrentSameFormatAsHistory(t *testing.T) {
	svc := newBuilderService(&builderStorage{}, nil)
	ctx := context.Background()

	a := bMsg("g1", "10001", "m-a", txt("你是人机"))
	b := bMsg("g1", "10002", "m-b", txt("hello"))
	persist(t, svc, a, b)

	msgs, err := svc.BuildMessages(ctx, b, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	// msgs = [system, a(④), b(⑤)]
	if msgs[1].Parts[0].Text != "[10001]: 你是人机" {
		t.Fatalf("④ 应为 A 消息: %q", msgs[1].Parts[0].Text)
	}
	cur := msgs[2].Parts[0].Text
	if cur != "[10002]: hello" {
		t.Fatalf("⑤ 应为当前消息: %q", cur)
	}
	// 根因 1：⑤ 与 ④ 前缀完全同构（[QQ]: 内容），当前消息无显式目标标记。
	if !strings.HasPrefix(cur, "[10002]: ") {
		t.Errorf("⑤ 前缀格式 = %q，与 ④ 同构，无「当前/待回复」标记", cur)
	}
	// 根因 2：system 侧也没有「当前消息」的显式隔离标记。
	if strings.Contains(msgs[0].Parts[0].Text, "【当前消息】") {
		t.Errorf("现状 system 不应有当前消息标记（修复后才会出现）: %s", msgs[0].Parts[0].Text)
	}
	// 根因 3：回复对象只靠一句位置性指令传达。
	if !strings.Contains(msgs[0].Parts[0].Text, replyInstruction) {
		t.Errorf("system 应含回复指令: %s", msgs[0].Parts[0].Text)
	}
	t.Logf("现状 prompt 尾部:\n%s", strings.Join([]string{msgs[1].Parts[0].Text, cur}, "\n"))
	// 期望改进：⑤ 加上显式标记（如 "【当前消息】[昵称]: hello，请回复这条"），
	// 或 replyInstruction 增强为「最后一条（[10002]: hello）是这次要回复的对象」。
}

// TestSpeakerTextQQOnly 群聊消息无昵称（SenderName 空，如旧窗口消息/未解析）时回落 QQ 号前缀。
func TestSpeakerTextQQOnly(t *testing.T) {
	msg := bMsg("g1", "10001", "m", txt("在吗"))
	got := speakerText(msg)
	if got != "[10001]: 在吗" {
		t.Errorf("speakerText = %q，无昵称应回落 QQ 号前缀", got)
	}
}

// TestSpeakerTextNicknamePreferred 群聊消息有昵称（SenderName 群名片优先）→ 用昵称前缀，
// 不再把 QQ 号当名字（修复后期望行为，原「只渲染 QQ 号」为问题根源）。
func TestSpeakerTextNicknamePreferred(t *testing.T) {
	msg := bMsg("g1", "10001", "m", txt("在吗"))
	msg.SenderName = "小明"
	if got := speakerText(msg); got != "[小明]: 在吗" {
		t.Errorf("speakerText = %q，有昵称应渲染 [昵称]: 在吗", got)
	}
}

// TestProfileTextMemberFactsQQOnly ② 会话画像成员事实：窗口成员无昵称 → 按 QQ 号键渲染（回落）。
func TestProfileTextMemberFactsQQOnly(t *testing.T) {
	store := &builderStorage{facts: map[string][]string{"g1|10001": {"喜欢猫"}}}
	svc := newBuilderService(store, nil)
	cur := bMsg("g1", "10001", "cur", txt("hi"))
	persist(t, svc, cur)

	msgs, err := svc.BuildMessages(context.Background(), cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	if !strings.Contains(msgs[0].Parts[0].Text, "10001：喜欢猫") {
		t.Errorf("成员事实应回落 QQ 号键渲染: %s", msgs[0].Parts[0].Text)
	}
}

// TestProfileTextMemberFactNickname ② 会话画像成员事实：窗口成员带昵称 → 渲染「昵称：事实」。
// 窗口内同一 uid 的昵称即该成员展示名（当前消息亦计入，保证触发者事实也用昵称）。
func TestProfileTextMemberFactNickname(t *testing.T) {
	store := &builderStorage{facts: map[string][]string{"g1|10001": {"喜欢猫"}}}
	svc := newBuilderService(store, nil)
	// 历史消息带昵称（小明），当前消息同 uid 无昵称 → 画像仍应从历史取到昵称。
	hist := bMsg("g1", "10001", "hist", txt("早"))
	hist.SenderName = "小明"
	cur := bMsg("g1", "10002", "cur", txt("hi"))
	persist(t, svc, hist, cur)

	msgs, err := svc.BuildMessages(context.Background(), cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	if !strings.Contains(msgs[0].Parts[0].Text, "小明：喜欢猫") {
		t.Errorf("成员事实应渲染昵称「小明：喜欢猫」: %s", msgs[0].Parts[0].Text)
	}
}
