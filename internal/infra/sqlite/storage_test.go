package sqlite

import (
	"context"
	"errors"
	"testing"

	"plumebot/internal/domain"
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

// TestGetPersonaByAgent 校验按 agent 名查询人格模板（命中返回字段 / 未命中 ErrNotFound）。
func TestGetPersonaByAgent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if _, err := s.InsertPersona(ctx, entity.Persona{Agent: "PlumeBot", Name: "默认", SystemPrompt: "你是 PlumeBot，一个赛博群友。"}); err != nil {
		t.Fatalf("InsertPersona 失败: %v", err)
	}

	got, err := s.GetPersonaByAgent(ctx, "PlumeBot")
	if err != nil {
		t.Fatalf("GetPersonaByAgent 失败: %v", err)
	}
	if got.Agent != "PlumeBot" || got.Name != "默认" || got.SystemPrompt != "你是 PlumeBot，一个赛博群友。" {
		t.Errorf("字段不符: %+v", got)
	}

	// 未命中 → ErrNotFound。
	if _, err := s.GetPersonaByAgent(ctx, "不存在"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("应返回 ErrNotFound, 实际: %v", err)
	}
}

// TestMessagesRoundTrip 校验 messages 表 Parts 序列化/反序列化往返（多段 + 空段）。
func TestMessagesRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	msg := entity.Message{
		MessageID: "m1",
		GroupID:   "g1",
		UserID:    "u1",
		Parts: []entity.ContentPart{
			{Type: entity.PartTypeText, Text: "看看"},
			{Type: entity.PartTypeAt, Text: "[@123]"},
			{Type: entity.PartTypeImage, URL: "https://example.com/cat.png"},
		},
		Timestamp:   1700000000,
		MessageType: "group",
	}
	if err := s.SaveMessage(ctx, msg); err != nil {
		t.Fatalf("SaveMessage 失败: %v", err)
	}

	got, err := s.GetMessages(ctx, "g1", 10, 0)
	if err != nil {
		t.Fatalf("GetMessages 失败: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("应返回 1 条, 实际 %d", len(got))
	}
	if got[0].MessageID != "m1" || got[0].GroupID != "g1" || got[0].UserID != "u1" ||
		got[0].Timestamp != 1700000000 || got[0].MessageType != "group" {
		t.Errorf("消息元数据不符: %+v", got[0])
	}
	if len(got[0].Parts) != 3 || got[0].Parts[0] != msg.Parts[0] ||
		got[0].Parts[1] != msg.Parts[1] || got[0].Parts[2] != msg.Parts[2] {
		t.Errorf("Parts 往返不符: %+v", got[0].Parts)
	}

	// 空 Parts → 存 "[]"，读回空切片（非 nil）。
	if err := s.SaveMessage(ctx, entity.Message{MessageID: "m2", GroupID: "g1", UserID: "u1", MessageType: "group"}); err != nil {
		t.Fatalf("SaveMessage 空 Parts 失败: %v", err)
	}
	got2, _ := s.GetMessages(ctx, "g1", 10, 0)
	if len(got2) != 2 {
		t.Fatalf("应返回 2 条, 实际 %d", len(got2))
	}
	// 时间倒序：m1(timestamp=1700000000) 在前，m2(timestamp=0) 在后。
	if got2[1].Parts == nil || len(got2[1].Parts) != 0 {
		t.Errorf("空 Parts 应读回空切片, 实际: %#v", got2[1].Parts)
	}
}

// TestGroupConfigRoundTrip 校验 group_config 表 Upsert→Get 往返、覆盖与未命中（P5-001）。
func TestGroupConfigRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// 未命中 → ErrNotFound。
	if _, err := s.GetGroupConfig(ctx, "g1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("未配置群应返回 ErrNotFound, 实际: %v", err)
	}

	// Upsert → Get 往返。
	if err := s.UpsertGroupConfig(ctx, entity.GroupConfig{GroupID: "g1", Mode: "auto"}); err != nil {
		t.Fatalf("UpsertGroupConfig 失败: %v", err)
	}
	got, err := s.GetGroupConfig(ctx, "g1")
	if err != nil {
		t.Fatalf("GetGroupConfig 失败: %v", err)
	}
	if got.GroupID != "g1" || got.Mode != "auto" {
		t.Errorf("group_config 往返不符: %+v", got)
	}

	// 同 group_id 再 Upsert 覆盖 mode。
	if err := s.UpsertGroupConfig(ctx, entity.GroupConfig{GroupID: "g1", Mode: "mention"}); err != nil {
		t.Fatalf("UpsertGroupConfig 覆盖失败: %v", err)
	}
	got, _ = s.GetGroupConfig(ctx, "g1")
	if got.Mode != "mention" {
		t.Errorf("覆盖后 mode 应更新为 mention, 实际 %q", got.Mode)
	}

	// 群间隔离。
	if err := s.UpsertGroupConfig(ctx, entity.GroupConfig{GroupID: "g2", Mode: "auto"}); err != nil {
		t.Fatalf("UpsertGroupConfig 失败: %v", err)
	}
	got, _ = s.GetGroupConfig(ctx, "g1")
	if got.Mode != "mention" {
		t.Errorf("g1 配置不应被 g2 影响, 实际 %+v", got)
	}
}
