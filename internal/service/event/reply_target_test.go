package event

import (
	"testing"

	"plumebot/internal/domain/entity"
)

// 本文件审计「回复对象」问题的发送侧机制（issue②：A 说“你是人机”、B 说 hello，
// bot 却对 B 回“你才是人机”）。
//
//	完整机制链：prompt ⑤ 无显式目标标记（见 service/memory 审计）→ 模型可能锚 A 的消息
//	→ 发送侧曾是纯文本无 @/引用 → 回复对象不明确。已修复：auto 群聊回复统一 @ 触发者
//	（TestAgentReplyAutoTargetsSender），发送侧 pin 到 B；prompt 侧标记仍未做（待评审）。
//	另一旁证：模型若确实带着“[QQ]: 开头”回复，respond 会剥掉前缀（TestRespondStripsLeadingQQPrefix），
//	仍会丢失对象指向 —— 该剥除仅处理“模型把 QQ 号当名字”的产出，昵称注入后应减少。

// TestAgentReplyAutoTargetsSender auto 主动发言（群聊非 @ 非私聊）：@ 触发者、不引用、文本内容不带前缀。
// 发送侧把回复 pin 到触发者：即使模型内容仍锚 A 的消息，“你才是人机”也会 @ B，对象从“群内所有人
// 各自认领”变成明确指向。
func TestAgentReplyAutoTargetsSender(t *testing.T) {
	r := agentReply(groupMsg("m1"), "你才是人机")
	if r.Quote {
		t.Errorf("auto 主动发言不应引用触发消息: %+v", r)
	}
	if r.At != "sender" {
		t.Errorf("auto 群聊回复应 @ 触发者（At=sender）, 实际 %q", r.At)
	}
	if len(r.Segments) != 1 || r.Segments[0].Kind != entity.SegmentKindText || r.Segments[0].Text != "你才是人机" {
		t.Errorf("应为纯文本段: %+v", r.Segments)
	}
}

// TestAgentReplyPrivateNoAt 私聊对方唯一：不 @、纯文本。
func TestAgentReplyPrivateNoAt(t *testing.T) {
	r := agentReply(entity.Message{MessageID: "p1", UserID: "u1", MessageType: "private"}, "好的")
	if r.At != "" || r.Quote {
		t.Errorf("私聊不应 @/引用: %+v", r)
	}
}

// TestRespondStripsLeadingQQPrefix 剥前缀循环：模型若真的以 “[10001]: …” 开头回复，
// respond 会剥掉 “[10001]:” 再发送 —— 剥除后文本不再携带对象指向。
// 现状文档：说明 AI 确有可能在回复里写 QQ 号（正是上下文只有 QQ 号导致），而管线对它的
// 处理是“剥掉名字”，不是“改造成点名”。
func TestRespondStripsLeadingQQPrefix(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	f.agent.reply = "[10001]: 你才是人机"

	if err := f.svc.tail(withSender(f.sender), groupMsg("m1")); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	if f.sender.calls != 1 {
		t.Fatalf("应发送 1 次, 实际 %d", f.sender.calls)
	}
	got := f.sender.last.Segments[0].Text
	if got != " 你才是人机" {
		t.Errorf("剥前缀后文本 = %q（前缀被剥离、对象信息丢失），期望 \" 你才是人机\"", got)
	}
}
