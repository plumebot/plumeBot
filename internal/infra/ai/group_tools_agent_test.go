package ai

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	gmtools "plumebot/internal/infra/ai/tools"
)

// fakeGM 记录收到的动作，验证 eino 把注入的 GroupManager ctx 透传到工具。
type fakeGM struct {
	executed []entity.GroupAction
}

func (f *fakeGM) Execute(_ context.Context, a entity.GroupAction) error {
	f.executed = append(f.executed, a)
	return nil
}

// TestGroupToolsAgentLoop 验证 tool 自动循环中 group_mute 被调用，且经 ctx 注入的
// domain.GroupManager 被透传到工具（未透传则工具报错、Generate 失败）。
func TestGroupToolsAgentLoop(t *testing.T) {
	gm := &fakeGM{}
	gt := gmtools.NewGroupTools()

	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{
			{ID: "call-1", Function: schema.FunctionCall{Name: "group_mute", Arguments: `{"user_id":"u2","duration":60}`}},
		}),
		schema.AssistantMessage("已将 u2 禁言 60 秒", nil),
	}}

	agent, err := NewEinoAgent(context.Background(), fake, []tool.BaseTool{gt.GroupMute()}, agentCfg(""))
	if err != nil {
		t.Fatalf("NewEinoAgent 失败: %v", err)
	}

	ctx := entity.WithSession(domain.WithGroupManager(context.Background(), gm),
		entity.Session{GroupID: "g1", UserID: "u1"})
	got, err := agent.Generate(ctx, []entity.ChatMessage{usrMsg("把 u2 禁言一分钟")})
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if got != "已将 u2 禁言 60 秒" {
		t.Errorf("回复 = %q", got)
	}
	if len(gm.executed) != 1 || gm.executed[0] != (entity.GroupAction{Op: entity.GroupOpMute, Target: "u2", Duration: 60}) {
		t.Errorf("GroupManager 应收到禁言动作，实际 %+v", gm.executed)
	}
}
