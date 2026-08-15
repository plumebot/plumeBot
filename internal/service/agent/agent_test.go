package agent

import (
	"context"
	"testing"

	"plumebot/internal/domain/entity"
)

// fakeAgent 记录收到的消息列表，返回预设回复，用于验证组装逻辑。
type fakeAgent struct {
	messages []entity.ChatMessage
	reply    string
	err      error
}

func (f *fakeAgent) Generate(_ context.Context, msgs []entity.ChatMessage) (string, error) {
	f.messages = append([]entity.ChatMessage(nil), msgs...)
	return f.reply, f.err
}

func userMsg(text string) entity.ChatMessage {
	return entity.ChatMessage{Role: entity.RoleUser, Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: text}}}
}

// GenerateReply 为纯透传：组装好的消息列表（含 system，P6-001 由 service/memory BuildMessages
// 组装）原样交给 Agent，回复透传。
func TestGenerateReplyPassthrough(t *testing.T) {
	fake := &fakeAgent{reply: "测试回复"}
	svc := NewAgentService(fake)

	want := []entity.ChatMessage{userMsg("第一条"), userMsg("第二条")}
	got, err := svc.GenerateReply(context.Background(), want)
	if err != nil {
		t.Fatalf("GenerateReply 失败: %v", err)
	}
	if got != "测试回复" {
		t.Errorf("回复 = %q，期望透传 %q", got, "测试回复")
	}

	if len(fake.messages) != 2 {
		t.Fatalf("agent 应收到 2 条消息（原样透传），实际 %d 条", len(fake.messages))
	}
	for i, w := range want {
		gotMsg := fake.messages[i]
		if gotMsg.Role != w.Role || len(gotMsg.Parts) != 1 || gotMsg.Parts[0].Text != w.Parts[0].Text {
			t.Errorf("第 %d 条消息 = %+v，期望 %+v（原样透传）", i+1, gotMsg, w)
		}
	}
}

// 空消息列表时透传 nil（不注入任何 system 消息，人设由 infra 层负责）。
func TestGenerateReplyEmptyMessages(t *testing.T) {
	fake := &fakeAgent{}
	svc := NewAgentService(fake)

	if _, err := svc.GenerateReply(context.Background(), nil); err != nil {
		t.Fatalf("GenerateReply 失败: %v", err)
	}
	if fake.messages != nil {
		t.Fatalf("消息列表 = %+v，期望 nil（无 system 注入）", fake.messages)
	}
}

// agent 返回错误时应原样透传。
func TestGenerateReplyPropagatesAgentError(t *testing.T) {
	fake := &fakeAgent{err: context.DeadlineExceeded}
	svc := NewAgentService(fake)

	if _, err := svc.GenerateReply(context.Background(), nil); err != context.DeadlineExceeded {
		t.Errorf("错误 = %v，期望透传 DeadlineExceeded", err)
	}
}
