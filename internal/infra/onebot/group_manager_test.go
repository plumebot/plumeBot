package onebot

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	zero "github.com/wdvxdr1123/ZeroBot"

	"plumebot/internal/domain/entity"
	"plumebot/internal/infra/sqlite"
)

// TestOnebotAction 动作映射：4 个 Op → action 名 + 参数；mute 时长钳制/非法参数报错。
func TestOnebotAction(t *testing.T) {
	cases := []struct {
		name   string
		action entity.GroupAction
		wantOp string
		check  func(t *testing.T, p zero.Params)
	}{
		{
			name:   "mute",
			action: entity.GroupAction{Op: entity.GroupOpMute, Target: "456", Duration: 60},
			wantOp: "set_group_ban",
			check: func(t *testing.T, p zero.Params) {
				if p["duration"] != int64(60) {
					t.Errorf("mute duration 应为 int64 60, 实际 %v (%T)", p["duration"], p["duration"])
				}
			},
		},
		{
			name:   "mute_duration_clamped",
			action: entity.GroupAction{Op: entity.GroupOpMute, Target: "456", Duration: maxMuteDurationSeconds + 100},
			wantOp: "set_group_ban",
			check: func(t *testing.T, p zero.Params) {
				if p["duration"] != int64(maxMuteDurationSeconds) {
					t.Errorf("超上限 duration 应钳制到 %d, 实际 %v", maxMuteDurationSeconds, p["duration"])
				}
			},
		},
		{
			name:   "unmute",
			action: entity.GroupAction{Op: entity.GroupOpUnmute, Target: "456"},
			wantOp: "set_group_ban",
			check: func(t *testing.T, p zero.Params) {
				if p["duration"] != int64(0) {
					t.Errorf("unmute duration 应为 0（取消禁言）, 实际 %v", p["duration"])
				}
			},
		},
		{
			name:   "kick",
			action: entity.GroupAction{Op: entity.GroupOpKick, Target: "456"},
			wantOp: "set_group_kick",
			check: func(t *testing.T, p zero.Params) {
				if p["reject_add_request"] != false {
					t.Errorf("kick reject_add_request 应为 false, 实际 %v", p["reject_add_request"])
				}
			},
		},
		{
			name:   "set_card",
			action: entity.GroupAction{Op: entity.GroupOpSetCard, Target: "456", Card: "新名片"},
			wantOp: "set_group_card",
			check: func(t *testing.T, p zero.Params) {
				if p["card"] != "新名片" {
					t.Errorf("set_card card 透传失败, 实际 %v", p["card"])
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, params, err := onebotAction(100, c.action)
			if err != nil {
				t.Fatalf("onebotAction 失败: %v", err)
			}
			if name != c.wantOp {
				t.Errorf("action 名应为 %q, 实际 %q", c.wantOp, name)
			}
			if params["group_id"] != int64(100) || params["user_id"] != int64(456) {
				t.Errorf("group_id/user_id 透传错误: %v", params)
			}
			c.check(t, params)
		})
	}

	// 非法参数：mute duration ≤ 0、Target 非数字、未知 Op。
	if _, _, err := onebotAction(100, entity.GroupAction{Op: entity.GroupOpMute, Target: "456", Duration: 0}); err == nil {
		t.Error("mute duration=0 应报错")
	}
	if _, _, err := onebotAction(100, entity.GroupAction{Op: entity.GroupOpMute, Target: "abc", Duration: 60}); err == nil {
		t.Error("非数字 Target 应报错")
	}
	if _, _, err := onebotAction(100, entity.GroupAction{Op: "ban_forever", Target: "456"}); err == nil {
		t.Error("未知 Op 应报错")
	}
}

// TestMemberInfoIsAdmin 管理员判定：owner/admin → 通过；member/缺 role → 拒绝（fail-closed）。
func TestMemberInfoIsAdmin(t *testing.T) {
	cases := []struct {
		role string
		want bool
	}{
		{"owner", true},
		{"admin", true},
		{"member", false},
		{"", false},     // 查询失败/缺 role
		{"manager", false}, // 非标准角色
	}
	for _, c := range cases {
		info := gjson.Parse(`{"role": "` + c.role + `"}`)
		if got := memberInfoIsAdmin(info); got != c.want {
			t.Errorf("role=%q 应为 %v, 实际 %v", c.role, c.want, got)
		}
	}
}

// TestExecuteSwitchGuard per-group 开关护栏：group_mgmt_enabled=0（显式关）拒绝，
// 且拒绝发生在管理员校验之前（此分支不触网；无配置行 = 默认开会放行至管理员校验，
// 属触网分支，由 memberInfoIsAdmin/onebotAction 纯函数测试覆盖，此处不构造真连接）。
func TestExecuteSwitchGuard(t *testing.T) {
	s, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer s.Close()
	gm := &botGroupManager{
		ctx:   &zero.Ctx{Event: &zero.Event{GroupID: 100, UserID: 200}},
		store: s,
		botID: 1,
	}
	action := entity.GroupAction{Op: entity.GroupOpMute, Target: "456", Duration: 60}

	// group_mgmt_enabled=0（显式关）拒绝。
	if err := s.UpsertGroupConfig(context.Background(), entity.GroupConfig{GroupID: "100", GroupMgmtEnabled: 0}); err != nil {
		t.Fatalf("UpsertGroupConfig 失败: %v", err)
	}
	err = gm.Execute(context.Background(), action)
	if err == nil || !strings.Contains(err.Error(), "未开启群管理功能") {
		t.Errorf("group_mgmt_enabled=0 应拒绝，实际 %v", err)
	}
}
