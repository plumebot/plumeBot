package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	memtools "plumebot/internal/infra/ai/tools"
	"plumebot/internal/infra/sqlite"
)

// openTestStorage 打开临时测试数据库，返回存储与记忆工具集。
func openTestStorage(t *testing.T) (*sqlite.Storage, *memtools.MemoryTools) {
	t.Helper()
	s, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, memtools.NewMemoryTools(s)
}

// TestMemoryToolsAgentStoreFactLoop 验证 tool 自动循环中 store_fact 被调用并落库，
// 同时验证 eino 把注入的会话身份 ctx 透传到工具 InvokableRun（未透传则工具报错、Generate 失败）。
func TestMemoryToolsAgentStoreFactLoop(t *testing.T) {
	s, mt := openTestStorage(t)

	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{
			{ID: "call-1", Function: schema.FunctionCall{Name: "store_fact", Arguments: `{"fact":"喜欢猫"}`}},
		}),
		schema.AssistantMessage("已记住：u1 的事实「喜欢猫」", nil),
	}}

	agent, err := NewEinoAgent(context.Background(), fake, []tool.BaseTool{mt.StoreFact(), mt.LearnJargon()}, agentCfg(""))
	if err != nil {
		t.Fatalf("NewEinoAgent 失败: %v", err)
	}

	ctx := domain.WithSession(context.Background(), domain.Session{GroupID: "g1", UserID: "u1"})
	got, err := agent.Generate(ctx, []entity.ChatMessage{usrMsg("记住我喜欢猫")})
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if got != "已记住：u1 的事实「喜欢猫」" {
		t.Errorf("回复 = %q", got)
	}

	// 落库验证 + ctx 透传验证（工具能读到会话身份，才写到 g1/u1）。
	facts, err := s.ListMemberFacts(ctx, "g1", "u1")
	if err != nil {
		t.Fatalf("ListMemberFacts 失败: %v", err)
	}
	if len(facts) != 1 || facts[0] != "喜欢猫" {
		t.Errorf("store_fact 应落库到 g1/u1，实际 %v", facts)
	}
}

// TestMemoryToolsAgentLearnJargonLoop learn_jargon 在 tool 循环中落库且为 pending（不在 confirmed 列表）。
func TestMemoryToolsAgentLearnJargonLoop(t *testing.T) {
	s, mt := openTestStorage(t)

	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{
			{ID: "call-1", Function: schema.FunctionCall{Name: "learn_jargon", Arguments: `{"jargon":"kwi"}`}},
		}),
		schema.AssistantMessage("已学习黑话「kwi」，状态：待确认", nil),
	}}

	agent, err := NewEinoAgent(context.Background(), fake, []tool.BaseTool{mt.StoreFact(), mt.LearnJargon()}, agentCfg(""))
	if err != nil {
		t.Fatalf("NewEinoAgent 失败: %v", err)
	}

	ctx := domain.WithSession(context.Background(), domain.Session{GroupID: "g1", UserID: "u1"})
	if _, err := agent.Generate(ctx, []entity.ChatMessage{usrMsg("群里都说 kwi")}); err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}

	all, _ := s.ListJargon(ctx, "g1")
	if len(all) != 1 || all[0] != "kwi" {
		t.Fatalf("learn_jargon 应落库，实际 %v", all)
	}
	confirmed, _ := s.ListConfirmedJargon(ctx, "g1")
	if len(confirmed) != 0 {
		t.Errorf("新学黑话应为 pending，实际 %v", confirmed)
	}
}

// TestMemoryToolsAgentNoSession 未注入会话身份时工具报错，Generate 透传该错误（不写错误归属）。
func TestMemoryToolsAgentNoSession(t *testing.T) {
	s, mt := openTestStorage(t)

	fake := &fakeChatModel{script: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{
			{ID: "call-1", Function: schema.FunctionCall{Name: "store_fact", Arguments: `{"fact":"不该落库"}`}},
		}),
	}}

	agent, err := NewEinoAgent(context.Background(), fake, []tool.BaseTool{mt.StoreFact()}, agentCfg(""))
	if err != nil {
		t.Fatalf("NewEinoAgent 失败: %v", err)
	}

	_, err = agent.Generate(context.Background(), []entity.ChatMessage{usrMsg("记住某某")})
	if err == nil || !strings.Contains(err.Error(), "会话上下文") {
		t.Fatalf("无会话身份应报错，实际 %v", err)
	}
	facts, _ := s.ListMemberFacts(context.Background(), "g1", "u1")
	if len(facts) != 0 {
		t.Errorf("无会话身份不应落库，实际 %v", facts)
	}
}
