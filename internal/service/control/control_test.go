package control

import (
	"context"
	"errors"
	"testing"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
)

// fakeStore 嵌入 domain.Storage，只覆写 GetGroupConfig。
// cfgs 中缺失的群 → ErrNotFound；getErr 模拟 DB 故障。
type fakeStore struct {
	domain.Storage
	cfgs   map[string]*entity.GroupConfig
	getErr error
}

func (f *fakeStore) GetGroupConfig(_ context.Context, groupID string) (*entity.GroupConfig, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if c, ok := f.cfgs[groupID]; ok {
		return c, nil
	}
	return nil, domain.ErrNotFound
}

func groupMsg(mentioned bool) entity.Message {
	return entity.Message{MessageID: "m1", GroupID: "g1", UserID: "u1", MessageType: "group", Mentioned: mentioned}
}

func privateMsg() entity.Message {
	return entity.Message{MessageID: "m1", UserID: "u1", MessageType: "private", Mentioned: true}
}

// TestShouldReplyModeTable 表驱动：模式 × 群@/群非@/私聊 × 有/无 per-group 配置。
func TestShouldReplyModeTable(t *testing.T) {
	cases := []struct {
		name   string
		global string                         // cfg.Control.Mode
		cfgs   map[string]*entity.GroupConfig // per-group 配置（nil = 无）
		msg    entity.Message
		want   bool
		reason entity.DecisionReason
	}{
		{"mention+群非@", "mention", nil, groupMsg(false), false, entity.DecisionReasonNotTriggered},
		{"mention+群@", "mention", nil, groupMsg(true), true, entity.DecisionReasonMentionForced},
		{"mention+私聊", "mention", nil, privateMsg(), true, entity.DecisionReasonMentionForced},
		{"auto+群非@", "auto", nil, groupMsg(false), true, entity.DecisionReasonAutoPass},
		{"auto+群@", "auto", nil, groupMsg(true), true, entity.DecisionReasonAutoPass},
		{"auto+私聊", "auto", nil, privateMsg(), true, entity.DecisionReasonAutoPass},
		{"全局mention+per-group auto", "mention",
			map[string]*entity.GroupConfig{"g1": &entity.GroupConfig{GroupID: "g1", Mode: "auto"}},
			groupMsg(false), true, entity.DecisionReasonAutoPass},
		{"全局auto+per-group mention", "auto",
			map[string]*entity.GroupConfig{"g1": &entity.GroupConfig{GroupID: "g1", Mode: "mention"}},
			groupMsg(true), true, entity.DecisionReasonMentionForced},
		{"per-group mode空→落全局", "mention",
			map[string]*entity.GroupConfig{"g1": &entity.GroupConfig{GroupID: "g1"}},
			groupMsg(false), false, entity.DecisionReasonNotTriggered},
		{"per-group 隔离：非配置群落全局", "auto",
			map[string]*entity.GroupConfig{"g2": &entity.GroupConfig{GroupID: "g2", Mode: "mention"}},
			groupMsg(false), true, entity.DecisionReasonAutoPass},
		{"全局mode空→mention兜底", "", nil, groupMsg(false), false, entity.DecisionReasonNotTriggered},
		{"全局mode非法→mention兜底", "foo", nil, groupMsg(false), false, entity.DecisionReasonNotTriggered},
		{"per-group mode非法→mention兜底", "auto",
			map[string]*entity.GroupConfig{"g1": &entity.GroupConfig{GroupID: "g1", Mode: "foo"}},
			groupMsg(false), false, entity.DecisionReasonNotTriggered},
		{"私聊忽略per-group配置", "mention",
			map[string]*entity.GroupConfig{"g1": &entity.GroupConfig{GroupID: "g1", Mode: "auto"}},
			privateMsg(), true, entity.DecisionReasonMentionForced},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewControlService(config.ControlConfig{Mode: tc.global}, &fakeStore{cfgs: tc.cfgs})
			got, err := svc.ShouldReply(context.Background(), tc.msg)
			if err != nil {
				t.Fatalf("ShouldReply 报错: %v", err)
			}
			if got.Reply != tc.want || got.Reason != tc.reason {
				t.Errorf("ShouldReply = {Reply:%v Reason:%q}, want {Reply:%v Reason:%q}",
					got.Reply, got.Reason, tc.want, tc.reason)
			}
		})
	}
}

// TestShouldReplyStoreError 存储故障时应透传 error，不吞错误。
func TestShouldReplyStoreError(t *testing.T) {
	svc := NewControlService(config.ControlConfig{Mode: "mention"}, &fakeStore{getErr: errors.New("db down")})
	_, err := svc.ShouldReply(context.Background(), groupMsg(false))
	if err == nil {
		t.Fatal("GetGroupConfig 故障时应返回 error")
	}
}

// TestOnRepliedNoop P5-001 空实现返回 nil。
func TestOnRepliedNoop(t *testing.T) {
	svc := NewControlService(config.ControlConfig{}, &fakeStore{})
	if err := svc.OnReplied(context.Background(), groupMsg(true)); err != nil {
		t.Fatalf("OnReplied 应返回 nil, 实际 %v", err)
	}
}
