package event

import (
	"context"
	"errors"
	"testing"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/internal/service/agent"
	"plumebot/internal/service/memory"
	"plumebot/internal/service/plugin"
	"plumebot/pkg/config"
)

// tailStore 嵌入 domain.Storage，只覆写 PersistMessage 需要的 SaveMessage + GetGroupProfile。
// 其余方法嵌入 nil 接口，调用即 panic（但本测试不会调用）。
type tailStore struct {
	domain.Storage
}

func (t *tailStore) SaveMessage(_ context.Context, _ entity.Message) error { return nil }

func (t *tailStore) GetGroupProfile(_ context.Context, _ string) (*entity.GroupProfile, error) {
	return nil, domain.ErrNotFound
}

// fakeControl 嵌入 domain.Control，记录 ShouldReply/OnReplied 调用次数并可注入 decision/error。
type fakeControl struct {
	domain.Control
	calls         int
	onRepliedCalls int
	dec           entity.Decision
	err           error
	onRepliedErr  error
}

func (f *fakeControl) ShouldReply(_ context.Context, _ entity.Message) (entity.Decision, error) {
	f.calls++
	return f.dec, f.err
}

func (f *fakeControl) OnReplied(_ context.Context, _ entity.Message) error {
	f.onRepliedCalls++
	return f.onRepliedErr
}

// newTailTestService 组装最小 EventService：agent nil（tail 不调）、memory + fake 存储、
// plugin 无插件（命令分发返回 ErrNotFound，短路语义不变）、注入 fakeControl。
func newTailTestService(c *fakeControl) *EventService {
	return NewEventService(
		agent.NewAgentService(nil),
		memory.NewMemoryService(memory.NewWindow(), &tailStore{}, nil),
		plugin.NewPluginService(nil),
		c,
		config.MiddlewareConfig{},
	)
}

// TestTailJudgeReplyForNormalMessage 普通消息应触发一次触发判断，管线不报错。
func TestTailJudgeReplyForNormalMessage(t *testing.T) {
	c := &fakeControl{dec: entity.Decision{Reply: true}}
	svc := newTailTestService(c)
	if err := svc.tail(context.Background(), groupMsg("m1")); err != nil {
		t.Fatalf("普通消息 tail 不应报错: %v", err)
	}
	if c.calls != 1 {
		t.Errorf("普通消息应触发 1 次触发判断, 实际 %d", c.calls)
	}
}

// TestTailSkipsJudgeReplyForCommand 命令消息（/开头）应短路绕过触发判断（架构 §10.2）。
func TestTailSkipsJudgeReplyForCommand(t *testing.T) {
	c := &fakeControl{dec: entity.Decision{Reply: true}}
	svc := newTailTestService(c)
	cmd := entity.Message{
		MessageID:   "m2",
		GroupID:     "g1",
		UserID:      "u1",
		MessageType: "group",
		Parts:       []entity.ContentPart{{Type: entity.PartTypeText, Text: "/echo hi"}},
	}
	if err := svc.tail(context.Background(), cmd); err != nil {
		t.Fatalf("命令消息 tail 不应报错: %v", err)
	}
	if c.calls != 0 {
		t.Errorf("命令消息应短路不触发判断, 实际 %d", c.calls)
	}
}

// TestTailJudgeReplyErrorDoesNotBlock 触发判断失败不应阻断管线（保守忽略）。
func TestTailJudgeReplyErrorDoesNotBlock(t *testing.T) {
	c := &fakeControl{err: errors.New("boom")}
	svc := newTailTestService(c)
	if err := svc.tail(context.Background(), groupMsg("m1")); err != nil {
		t.Fatalf("触发判断失败不应阻断管线, 实际 %v", err)
	}
}

// TestTailOnRepliedOnTrigger P5-002：触发回复（Reply=true）应追调一次 OnReplied 更新运行态。
func TestTailOnRepliedOnTrigger(t *testing.T) {
	c := &fakeControl{dec: entity.Decision{Reply: true}}
	svc := newTailTestService(c)
	if err := svc.tail(context.Background(), groupMsg("m1")); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	if c.onRepliedCalls != 1 {
		t.Errorf("触发回复应调 OnReplied 1 次, 实际 %d", c.onRepliedCalls)
	}
}

// TestTailNoOnRepliedWhenNotTrigger 未触发回复不应调 OnReplied。
func TestTailNoOnRepliedWhenNotTrigger(t *testing.T) {
	c := &fakeControl{dec: entity.Decision{Reply: false}}
	svc := newTailTestService(c)
	if err := svc.tail(context.Background(), groupMsg("m1")); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	if c.onRepliedCalls != 0 {
		t.Errorf("未触发不应调 OnReplied, 实际 %d", c.onRepliedCalls)
	}
}

// TestTailOnRepliedErrorDoesNotBlock OnReplied 失败不应阻断管线（仅告警）。
func TestTailOnRepliedErrorDoesNotBlock(t *testing.T) {
	c := &fakeControl{dec: entity.Decision{Reply: true}, onRepliedErr: errors.New("state write failed")}
	svc := newTailTestService(c)
	if err := svc.tail(context.Background(), groupMsg("m1")); err != nil {
		t.Fatalf("OnReplied 失败不应阻断管线, 实际 %v", err)
	}
	if c.onRepliedCalls != 1 {
		t.Errorf("OnReplied 应被调用, 实际 %d", c.onRepliedCalls)
	}
}
