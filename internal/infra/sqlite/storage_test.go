package sqlite

import (
	"context"
	"testing"

	"plumebot/internal/domain/entity"
)

func TestConversationSummaryArchive(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// 保存 3 条归档摘要（seq 1/2/3）+ 另一会话 1 条（验证隔离）。
	for seq, text := range []string{"一", "二", "三"} {
		sum := entity.Summary{ChatID: "g1", Seq: int64(seq + 1), Text: text,
			Keywords: []string{"k"}, Decisions: []string{"d"}, CreatedAt: 1}
		if err := s.SaveSummary(ctx, sum); err != nil {
			t.Fatalf("SaveSummary 失败: %v", err)
		}
	}
	if err := s.SaveSummary(ctx, entity.Summary{ChatID: "g2", Seq: 1, Text: "其他群", CreatedAt: 1}); err != nil {
		t.Fatalf("SaveSummary 失败: %v", err)
	}

	got, err := s.ListSummaries(ctx, "g1", 5)
	if err != nil {
		t.Fatalf("ListSummaries 失败: %v", err)
	}
	if len(got) != 3 || got[0].Text != "一" || got[1].Text != "二" || got[2].Text != "三" {
		t.Errorf("应按 seq 正序返回 3 条, 实际: %+v", got)
	}
	if len(got[0].Keywords) != 1 || got[0].Keywords[0] != "k" || got[0].Decisions[0] != "d" {
		t.Errorf("keywords/decisions 应反序列化, 实际: %+v", got[0])
	}

	// limit 生效：取最新 2 条并保持正序。
	limited, err := s.ListSummaries(ctx, "g1", 2)
	if err != nil {
		t.Fatalf("ListSummaries 失败: %v", err)
	}
	if len(limited) != 2 || limited[0].Text != "二" || limited[1].Text != "三" {
		t.Errorf("limit 应取最新 N 条正序, 实际: %+v", limited)
	}

	// upsert 幂等：重存同 (chat_id, seq) 应覆盖而非新增行。
	if err := s.SaveSummary(ctx, entity.Summary{ChatID: "g1", Seq: 3, Text: "三改", CreatedAt: 2}); err != nil {
		t.Fatalf("SaveSummary 失败: %v", err)
	}
	after, _ := s.ListSummaries(ctx, "g1", 5)
	if len(after) != 3 || after[2].Text != "三改" {
		t.Errorf("同 seq 重存应覆盖不新增, 实际: %+v", after)
	}
}

// TestPersonaSchema 校验 persona 新 schema（agent/name/system_prompt，agent UNIQUE）。
func TestPersonaSchema(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	id, err := s.InsertPersona(ctx, entity.Persona{Agent: "PlumeBot", Name: "默认", SystemPrompt: "你是 PlumeBot，一个赛博群友。"})
	if err != nil {
		t.Fatalf("InsertPersona 失败: %v", err)
	}
	got, err := s.GetPersona(ctx, id)
	if err != nil {
		t.Fatalf("GetPersona 失败: %v", err)
	}
	if got.Agent != "PlumeBot" || got.Name != "默认" || got.SystemPrompt != "你是 PlumeBot，一个赛博群友。" {
		t.Errorf("persona 字段不符: %+v", got)
	}

	// Update 生效。
	if err := s.UpdatePersona(ctx, entity.Persona{ID: id, Agent: "PlumeBot", Name: "默认", SystemPrompt: "新的人设"}); err != nil {
		t.Fatalf("UpdatePersona 失败: %v", err)
	}
	got, _ = s.GetPersona(ctx, id)
	if got.SystemPrompt != "新的人设" {
		t.Errorf("Update 后 system_prompt 应更新, 实际 %q", got.SystemPrompt)
	}

	// UNIQUE(agent)：同 agent 重复插入报错。
	if _, err := s.InsertPersona(ctx, entity.Persona{Agent: "PlumeBot", Name: "另一个", SystemPrompt: "..."}); err == nil {
		t.Error("同 agent 重复插入应触发 UNIQUE 错误")
	}
}
