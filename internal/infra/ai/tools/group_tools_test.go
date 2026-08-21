package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
)

// errGroupMgmtDisabled 模拟 GroupManager 护栏拒绝（真实实现返回「未开启群管理功能」等）。
var errGroupMgmtDisabled = errors.New("本群未开启群管理功能")

// fakeGroupManager 记录收到的动作并按脚本返回错误，用于工具层单测
// （动作执行与护栏在真实实现内，本层只验证参数构造与错误透传）。
type fakeGroupManager struct {
	executed []entity.GroupAction
	err      error
}

func (f *fakeGroupManager) Execute(_ context.Context, a entity.GroupAction) error {
	f.executed = append(f.executed, a)
	return f.err
}

// TestGroupToolsActions 4 个工具的参数构造与中文反馈：会话身份 + 注入 fake
// GroupManager → 断言收到的动作载荷。
func TestGroupToolsActions(t *testing.T) {
	cases := []struct {
		name    string
		tool    func(g *GroupTools) tool.BaseTool
		args    map[string]any
		wantAct entity.GroupAction
		wantOut string
	}{
		{
			name:    "group_mute",
			tool:    func(g *GroupTools) tool.BaseTool { return g.GroupMute() },
			args:    map[string]any{"user_id": "u2", "duration": 60},
			wantAct: entity.GroupAction{Op: entity.GroupOpMute, Target: "u2", Duration: 60},
			wantOut: "已将 u2 禁言 60 秒",
		},
		{
			name:    "group_unmute",
			tool:    func(g *GroupTools) tool.BaseTool { return g.GroupUnmute() },
			args:    map[string]any{"user_id": "u2"},
			wantAct: entity.GroupAction{Op: entity.GroupOpUnmute, Target: "u2"},
			wantOut: "已解除 u2 的禁言",
		},
		{
			name:    "group_kick",
			tool:    func(g *GroupTools) tool.BaseTool { return g.GroupKick() },
			args:    map[string]any{"user_id": "u2"},
			wantAct: entity.GroupAction{Op: entity.GroupOpKick, Target: "u2"},
			wantOut: "已将 u2 移出本群",
		},
		{
			name:    "group_set_card",
			tool:    func(g *GroupTools) tool.BaseTool { return g.GroupSetCard() },
			args:    map[string]any{"user_id": "u2", "card": "新名片"},
			wantAct: entity.GroupAction{Op: entity.GroupOpSetCard, Target: "u2", Card: "新名片"},
			wantOut: "已将 u2 的名片改为「新名片」",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeGroupManager{}
			ctx := entity.WithSession(domain.WithGroupManager(context.Background(), fake),
				entity.Session{GroupID: "g1", UserID: "u1"})
			out, err := invoke(t, c.tool(NewGroupTools()), ctx, c.args)
			if err != nil {
				t.Fatalf("%s 执行失败: %v", c.name, err)
			}
			if out != c.wantOut {
				t.Errorf("反馈文案应为 %q, 实际 %q", c.wantOut, out)
			}
			if len(fake.executed) != 1 || fake.executed[0] != c.wantAct {
				t.Errorf("GroupManager 收到的动作应为 %+v, 实际 %+v", c.wantAct, fake.executed)
			}
		})
	}
}

// TestGroupToolsPrivate 私聊（会话无群 ID）应报「仅群聊」，GroupManager 不被调用。
func TestGroupToolsPrivate(t *testing.T) {
	fake := &fakeGroupManager{}
	ctx := entity.WithSession(domain.WithGroupManager(context.Background(), fake),
		entity.Session{UserID: "u1"})

	_, err := invoke(t, NewGroupTools().GroupMute(), ctx, map[string]any{"user_id": "u2", "duration": 60})
	if err == nil || !strings.Contains(err.Error(), "仅群聊") {
		t.Fatalf("私聊应报「仅群聊」，实际 %v", err)
	}
	if len(fake.executed) != 0 {
		t.Errorf("私聊不应调用 GroupManager，实际 %+v", fake.executed)
	}
}

// TestGroupToolsNoSession 无会话身份（未注入 entity.Session）应报错。
func TestGroupToolsNoSession(t *testing.T) {
	ctx := domain.WithGroupManager(context.Background(), &fakeGroupManager{})
	_, err := invoke(t, NewGroupTools().GroupMute(), ctx, map[string]any{"user_id": "u2", "duration": 60})
	if err == nil || !strings.Contains(err.Error(), "会话上下文") {
		t.Fatalf("无会话上下文应报错，实际 %v", err)
	}
}

// TestGroupToolsNoManager 有会话身份但未注入 GroupManager 应报错（护栏缺失宁可失败）。
func TestGroupToolsNoManager(t *testing.T) {
	ctx := entity.WithSession(context.Background(), entity.Session{GroupID: "g1", UserID: "u1"})
	_, err := invoke(t, NewGroupTools().GroupMute(), ctx, map[string]any{"user_id": "u2", "duration": 60})
	if err == nil || !strings.Contains(err.Error(), "GroupManager 未注入") {
		t.Fatalf("未注入 GroupManager 应报错，实际 %v", err)
	}
}

// TestGroupToolsExecuteError GroupManager 执行失败 → 工具返回带前缀的包装错误。
func TestGroupToolsExecuteError(t *testing.T) {
	fake := &fakeGroupManager{err: errGroupMgmtDisabled}
	ctx := entity.WithSession(domain.WithGroupManager(context.Background(), fake),
		entity.Session{GroupID: "g1", UserID: "u1"})

	_, err := invoke(t, NewGroupTools().GroupMute(), ctx, map[string]any{"user_id": "u2", "duration": 60})
	if err == nil || !strings.Contains(err.Error(), "禁言失败") {
		t.Fatalf("应返回带「禁言失败」前缀的错误，实际 %v", err)
	}
}
