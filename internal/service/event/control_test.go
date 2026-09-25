package event

import (
	"context"
	"errors"
	"strings"
	"testing"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/service/agent"
	"plumebot/internal/service/memory"
	"plumebot/internal/service/plugin"
	"plumebot/pkg/config"
)

// tailStore 嵌入 domain.Storage，覆写消息管线（tail/respond/BuildMessages）用到的存储方法。
// 其余方法嵌入 nil 接口，调用即 panic（但本测试不会调用）。
type tailStore struct {
	domain.Storage
	saved []entity.Message // SaveMessage 记录（含 bot 回复），供持久化断言
}

func (t *tailStore) SaveMessage(_ context.Context, msg entity.Message) error {
	t.saved = append(t.saved, msg)
	return nil
}

func (t *tailStore) GetGroupProfile(_ context.Context, _ string) (*entity.GroupProfile, error) {
	return nil, entity.ErrNotFound
}

// BuildMessages 查询桩：persona 未命中（走兜底 defaultPersona）、无黑话、无成员事实。
func (t *tailStore) GetPersonaByAgent(_ context.Context, _ string) (*entity.Persona, error) {
	return nil, entity.ErrNotFound
}

func (t *tailStore) ListConfirmedJargon(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (t *tailStore) ListMemberFacts(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

// ListSummaries 摘要热链懒加载桩：无历史摘要（BuildMessages ③ 占位）。
func (t *tailStore) ListSummaries(_ context.Context, _ string, _ int) ([]entity.Summary, error) {
	return nil, nil
}

// fakeControl 嵌入 domain.Control，记录 ShouldReply/OnReplied 调用次数并可注入 decision/error。
type fakeControl struct {
	domain.Control
	calls          int
	onRepliedCalls int
	dec            entity.Decision
	err            error
	onRepliedErr   error
}

func (f *fakeControl) ShouldReply(_ context.Context, _ entity.Message) (entity.Decision, error) {
	f.calls++
	return f.dec, f.err
}

func (f *fakeControl) OnReplied(_ context.Context, _ entity.Message) error {
	f.onRepliedCalls++
	return f.onRepliedErr
}

// fakeAgent 嵌入 domain.Agent，记录 Generate 调用并可注入回复/错误。
type fakeAgent struct {
	domain.Agent
	calls int
	reply string
	err   error
}

func (f *fakeAgent) Generate(_ context.Context, _ []entity.ChatMessage) (string, error) {
	f.calls++
	return f.reply, f.err
}

// fakeSender 嵌入 domain.Sender，记录 Send 调用（含载荷）并可注入错误。
type fakeSender struct {
	domain.Sender
	calls int
	err   error
	last  entity.Reply
}

func (f *fakeSender) Send(_ context.Context, r entity.Reply) error {
	f.calls++
	f.last = r
	return f.err
}

// withSender 构造带 Sender 的 ctx（B-003 注入语义，模拟 matcher 闭包）。
func withSender(s domain.Sender) context.Context {
	return domain.WithSender(context.Background(), s)
}

// tailFixture 组装最小 EventService：fakeAgent（默认回复 "ok"）+ memory（fake 存储、describer nil）
// + plugin 无插件（命令分发返回 ErrNotFound，短路语义不变）+ fakeControl + botID(bot1)/botName(mifi)，
// 并暴露各桩便于断言。
type tailFixture struct {
	svc     *EventService
	store   *tailStore
	control *fakeControl
	agent   *fakeAgent
	sender  *fakeSender
}

func newTailFixture() *tailFixture {
	st := &tailStore{}
	c := &fakeControl{}
	a := &fakeAgent{reply: "ok"}
	return &tailFixture{
		svc: NewEventService(
			agent.NewAgentService(a),
			memory.NewMemoryService(memory.NewWindow(), st, nil, memory.BuilderConfig{}),
			plugin.NewPluginService(nil),
			c,
			config.MiddlewareConfig{},
			"bot1",
			"mifi",
		),
		store:   st,
		control: c,
		agent:   a,
		sender:  &fakeSender{},
	}
}

// groupMsg 由 ratelimit_test.go 提供：构造一条普通群消息（MessageType=group，未 @，无内容段）。

// TestTailJudgeReplyForNormalMessage 普通消息应触发一次触发判断，管线不报错。
func TestTailJudgeReplyForNormalMessage(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	if err := f.svc.tail(context.Background(), groupMsg("m1")); err != nil {
		t.Fatalf("普通消息 tail 不应报错: %v", err)
	}
	if f.control.calls != 1 {
		t.Errorf("普通消息应触发 1 次触发判断, 实际 %d", f.control.calls)
	}
}

// TestTailSkipsJudgeReplyForCommand 命令消息（/开头）应短路绕过触发判断（架构 §10.2）。
func TestTailSkipsJudgeReplyForCommand(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	cmd := entity.Message{
		MessageID:   "m2",
		GroupID:     "g1",
		UserID:      "u1",
		MessageType: "group",
		Parts:       []entity.ContentPart{{Type: entity.PartTypeText, Text: "/echo hi"}},
	}
	if err := f.svc.tail(context.Background(), cmd); err != nil {
		t.Fatalf("命令消息 tail 不应报错: %v", err)
	}
	if f.control.calls != 0 {
		t.Errorf("命令消息应短路不触发判断, 实际 %d", f.control.calls)
	}
	if f.sender.calls != 0 {
		t.Errorf("无插件命令不应发送回复, 实际 %d 次", f.sender.calls)
	}
}

// TestTailJudgeReplyErrorDoesNotBlock 触发判断失败不应阻断管线（保守忽略）。
func TestTailJudgeReplyErrorDoesNotBlock(t *testing.T) {
	f := newTailFixture()
	f.control.err = errors.New("boom")
	if err := f.svc.tail(context.Background(), groupMsg("m1")); err != nil {
		t.Fatalf("触发判断失败不应阻断管线, 实际 %v", err)
	}
}

// TestTailNoOnRepliedWhenNotTrigger 未触发回复不应调 OnReplied。
func TestTailNoOnRepliedWhenNotTrigger(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: false}
	if err := f.svc.tail(context.Background(), groupMsg("m1")); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	if f.control.onRepliedCalls != 0 {
		t.Errorf("未触发不应调 OnReplied, 实际 %d", f.control.onRepliedCalls)
	}
	if f.sender.calls != 0 {
		t.Errorf("未触发不应发送, 实际 %d", f.sender.calls)
	}
}

// TestRespondSendsOnSuccess B-038：发送成功后窗口追加 bot 回复 + OnReplied 记账 1 次。
func TestRespondSendsOnSuccess(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	ctx := withSender(f.sender)
	if err := f.svc.tail(ctx, groupMsg("m1")); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	if f.sender.calls != 1 {
		t.Fatalf("应发送 1 次回复, 实际 %d", f.sender.calls)
	}
	if got := f.sender.last.Segments[0].Text; got != "ok" {
		t.Errorf("回复文本 = %q, want %q", got, "ok")
	}
	if f.control.onRepliedCalls != 1 {
		t.Errorf("发送成功应调 OnReplied 1 次, 实际 %d", f.control.onRepliedCalls)
	}
	// bot 回复已持久化：saved = [入站消息, bot 回复]；bot 回复用合成 ID 且 UserID=botID。
	if len(f.store.saved) != 2 {
		t.Fatalf("应持久化 2 条消息（入站 + bot 回复）, 实际 %d", len(f.store.saved))
	}
	bot := f.store.saved[1]
	if !strings.HasPrefix(bot.MessageID, "self:") || bot.UserID != "bot1" || bot.Parts[0].Text != "ok" ||
		bot.SenderName != "mifi" {
		t.Errorf("bot 回复持久化错误（应带 UserID=bot1/SenderName=mifi）: %+v", bot)
	}
}

// TestRespondPrivateBotReplyJoinsUserSession 私聊下 bot 回复应并入用户会话窗口（而非 bot 自身
// 孤立会话）：修复前 botReplyMessage 按自身 SessionKey("private:"+botID) 入窗，bot 永远看不到
// 自己的回复，上下文变成用户单声道（见 event.go respond）。
func TestRespondPrivateBotReplyJoinsUserSession(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	msg := entity.Message{
		MessageID:   "p1",
		UserID:      "u1",
		MessageType: "private",
		Mentioned:   true, // 私聊恒 Mentioned（onebot 转换），强制回复
		Parts:       []entity.ContentPart{{Type: entity.PartTypeText, Text: "hi"}},
	}
	if err := f.svc.tail(withSender(f.sender), msg); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	win, err := f.svc.memory.GetWindow(context.Background(), msg.SessionKey())
	if err != nil {
		t.Fatalf("GetWindow 失败: %v", err)
	}
	if len(win) != 2 {
		t.Fatalf("用户会话窗口应含 [入站消息, bot 回复] 2 条, 实际 %d: %+v", len(win), win)
	}
	if win[1].UserID != "bot1" || win[1].Parts[0].Text != "ok" {
		t.Errorf("窗口第 2 条应为 bot 回复（UserID=bot1）: %+v", win[1])
	}
	// 修复前的错窗现象：bot 自身会话不应出现孤儿。
	if orphan, _ := f.svc.memory.GetWindow(context.Background(), "private:bot1"); len(orphan) != 0 {
		t.Errorf("不应存在 private:bot1 孤立会话, 实际 %d 条", len(orphan))
	}
}

// TestRespondAgentReplyAtWhenMentioned 群聊被 @ → 回复载荷 @ 触发者但不引用触发消息
// （引用预览会带出触发消息内的 @bot，见 event.go agentReply）。
func TestRespondAgentReplyAtWhenMentioned(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	msg := groupMsg("m1")
	msg.Mentioned = true
	if err := f.svc.tail(withSender(f.sender), msg); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	if f.sender.calls != 1 {
		t.Fatalf("应发送 1 次, 实际 %d", f.sender.calls)
	}
	if f.sender.last.Quote || f.sender.last.At != "sender" {
		t.Errorf("被 @ 时应 @ 触发者且不引用, 实际 Quote=%v At=%q", f.sender.last.Quote, f.sender.last.At)
	}
}

// TestRespondNoOnRepliedWhenSendFails 发送失败 = bot 未说话：不记账、不追加 bot 回复。
func TestRespondNoOnRepliedWhenSendFails(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	f.sender.err = errors.New("send failed")
	if err := f.svc.tail(withSender(f.sender), groupMsg("m1")); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	if f.sender.calls != 1 {
		t.Fatalf("应尝试发送 1 次, 实际 %d", f.sender.calls)
	}
	if f.control.onRepliedCalls != 0 {
		t.Errorf("发送失败不应调 OnReplied, 实际 %d", f.control.onRepliedCalls)
	}
	if len(f.store.saved) != 1 {
		t.Errorf("发送失败不应持久化 bot 回复, saved=%d", len(f.store.saved))
	}
}

// TestRespondNoOnRepliedWhenGenerateFails Agent 推理失败：不发送、不记账。
func TestRespondNoOnRepliedWhenGenerateFails(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	f.agent.err = errors.New("llm down")
	if err := f.svc.tail(withSender(f.sender), groupMsg("m1")); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	if f.sender.calls != 0 || f.control.onRepliedCalls != 0 {
		t.Errorf("Agent 失败不应发送/记账: sender=%d onReplied=%d", f.sender.calls, f.control.onRepliedCalls)
	}
}

// TestRespondEmptyReplyNoSend Agent 返回空回复：不发送、不记账。
func TestRespondEmptyReplyNoSend(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	f.agent.reply = "  "
	if err := f.svc.tail(withSender(f.sender), groupMsg("m1")); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	if f.sender.calls != 0 || f.control.onRepliedCalls != 0 {
		t.Errorf("空回复不应发送/记账: sender=%d onReplied=%d", f.sender.calls, f.control.onRepliedCalls)
	}
}

// TestRespondNoSenderSkips 缺少 Sender（未注入）→ fail-fast 跳过，不调 Agent、不记账。
func TestRespondNoSenderSkips(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	if err := f.svc.tail(context.Background(), groupMsg("m1")); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	if f.agent.calls != 0 || f.sender.calls != 0 || f.control.onRepliedCalls != 0 {
		t.Errorf("无 Sender 应跳过 Agent/发送/记账: agent=%d sender=%d onReplied=%d",
			f.agent.calls, f.sender.calls, f.control.onRepliedCalls)
	}
}

// TestSendReplyWithSender sendReply 应把载荷转发给 ctx 内 Sender（插件回复发送路径，B-017）。
func TestSendReplyWithSender(t *testing.T) {
	f := newTailFixture()
	r := entity.Reply{Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: "插件回复"}}}
	if err := f.svc.sendReply(withSender(f.sender), r); err != nil {
		t.Fatalf("sendReply 不应报错: %v", err)
	}
	if f.sender.calls != 1 || f.sender.last.Segments[0].Text != "插件回复" {
		t.Errorf("sendReply 应转发给 Sender, calls=%d last=%+v", f.sender.calls, f.sender.last)
	}
}

// TestSendReplyWithoutSenderSkips 无 Sender 时 sendReply 告警跳过（不报错）。
func TestSendReplyWithoutSenderSkips(t *testing.T) {
	f := newTailFixture()
	r := entity.Reply{Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: "x"}}}
	if err := f.svc.sendReply(context.Background(), r); err != nil {
		t.Fatalf("无 Sender 时 sendReply 不应报错: %v", err)
	}
	if f.sender.calls != 0 {
		t.Errorf("无 Sender 不应发送, calls=%d", f.sender.calls)
	}
}

// TestRespondOnRepliedErrorDoesNotBlock OnReplied 失败仅告警，不阻断管线（返回 nil）。
func TestRespondOnRepliedErrorDoesNotBlock(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	f.control.onRepliedErr = errors.New("state write failed")
	if err := f.svc.tail(withSender(f.sender), groupMsg("m1")); err != nil {
		t.Fatalf("OnReplied 失败不应阻断管线, 实际 %v", err)
	}
	if f.sender.calls != 1 || f.control.onRepliedCalls != 1 {
		t.Errorf("应发送并记 1 次账: sender=%d onReplied=%d", f.sender.calls, f.control.onRepliedCalls)
	}
}
