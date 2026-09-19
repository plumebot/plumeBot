package control

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
)

// 本文件审计「auto 模式不及时回复」问题（issue①）。
//
//	结论：精力足够仍常被拦，主要来自冷却窗（默认 60s）+ 连续休息（5 条后 300s）；
//	另一结构性来源是同会话并发消息无串行化（A/B 先后到达、LLM 尚在跑时各自放行，
//	可双发/错序，见 TestAutoTwoMessagesBothPass）。

// TestAutoEnergyOkButCooldownBlocks 精力满（90≥20）但 30 秒前刚回复过 → 冷却拦截。
// 用户看到「精力满足仍不回复」，最常来自这条 60s 冷却窗：auto 下两条紧密消息最多回一条。
func TestAutoEnergyOkButCooldownBlocks(t *testing.T) {
	st := &fakeStore{}
	svc, nowT := newTest(config.ControlConfig{Mode: "auto"}, st)

	// OnReplied 记一次（默认 cost 10 → 精力 90，仍 ≥ 阈值 20），LastReplyAt=noon。
	if err := svc.OnReplied(context.Background(), groupMsg(false)); err != nil {
		t.Fatalf("OnReplied 失败: %v", err)
	}
	var gs groupState
	if err := json.Unmarshal([]byte(st.states["g1"].State), &gs); err != nil {
		t.Fatalf("解析状态失败: %v", err)
	}
	if gs.Energy < config.DefaultEnergyThreshold {
		t.Fatalf("前置不成立：精力 %d 已低于阈值", gs.Energy)
	}

	*nowT = noon.Add(30 * time.Second) // 冷却窗（60s）内
	got, err := svc.ShouldReply(context.Background(), groupMsg(false))
	if err != nil {
		t.Fatalf("ShouldReply 报错: %v", err)
	}
	if got.Reason != entity.DecisionReasonCooldown {
		t.Errorf("精力足够但 30s 冷却窗内应 cooldown: %+v", got)
	}
	// 期望改进（评审权衡）：冷却窗内不直接丢弃，而是延后/改为限频——当前是“冷却即丢弃进不了 LLM”。
}

// TestAutoTwoMessagesBothPass 同会话两条消息先后到达、之间没有 OnReplied干预（LLM 还在跑）时，
// ShouldReply 两次都放行 auto_pass —— 没有任何机制把这些“未回复完成的消息”串起来。
// 这是并发双发/回复错序的结构性来源（ZeroBot 每事件一 goroutine，respond 无 per-session 队列）。
func TestAutoTwoMessagesBothPass(t *testing.T) {
	st := &fakeStore{}
	svc, _ := newTest(config.ControlConfig{Mode: "auto"}, st) // 空状态 = 精力满格、无冷却

	// 模拟先后两条消息：两条独立 ShouldReply 调用之间没有 OnReplied 介入。
	first, err := svc.ShouldReply(context.Background(), groupMsg(false))
	if err != nil {
		t.Fatalf("第一条 ShouldReply 报错: %v", err)
	}
	second, err := svc.ShouldReply(context.Background(), groupMsg(false))
	if err != nil {
		t.Fatalf("第二条 ShouldReply 报错: %v", err)
	}
	if first.Reason != entity.DecisionReasonAutoPass || second.Reason != entity.DecisionReasonAutoPass {
		t.Fatalf("两条消息应都放行（现状无串行化）: first=%+v second=%+v", first, second)
	}
	// 现状后果：两条都进 LLM、都可能发回复；OnReplied 的 per-session 锁只防状态丢更新，不防双发。
	// 期望改进：per-session 触发判断串行化（前后消息共用同一“决定到记账”间隙）或短窗口合并，
	// 与 B-004/B-036 的无界 map 治理一并评估。
}