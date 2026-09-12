package admin

// 边界矩阵（P7-001 收口）：长度/条数上限、HH:MM 边界、密码长度边界、
// 黑话每群条数上限、群画像数组条数上限。

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"plumebot/internal/domain/entity"
)

func TestPasswordLengthBoundary(t *testing.T) {
	svc := newTestSvc()
	ctx := context.Background()

	// 7 位（ASCII）→ 拒绝；8 位 → 通过（首账号）。
	if _, err := svc.Register(ctx, "admin", "1234567"); !isValidation(err) {
		t.Fatalf("7 位密码应拒绝: %v", err)
	}
	if _, err := svc.Register(ctx, "admin", "12345678"); err != nil {
		t.Fatalf("8 位密码应通过: %v", err)
	}
	// 中文按字符计：8 个汉字 → 通过（改密路径校验）。
	if err := svc.ChangePassword(ctx, "admin", "12345678", "密码密码密码密码"); err != nil {
		t.Fatalf("8 个汉字应通过: %v", err)
	}
	// 7 个汉字（密,码 ×3 + 密）→ 拒绝；8 个汉字 → 已在上方通过。
	if err := svc.ChangePassword(ctx, "admin", "密码密码密码密码", "密码密码密码密"); !isValidation(err) {
		t.Fatalf("7 个汉字应拒绝: %v", err)
	}
}

func TestSystemPromptLengthBoundary(t *testing.T) {
	svc, _, _ := newTestAdminSvc(t)
	ctx := context.Background()

	if _, err := svc.UpsertPersona(ctx, "admin", entity.Persona{Agent: "a", SystemPrompt: strings.Repeat("字", maxSystemPromptLen)}); err != nil {
		t.Fatalf("恰好上限应通过: %v", err)
	}
	if _, err := svc.UpsertPersona(ctx, "admin", entity.Persona{Agent: "a", SystemPrompt: strings.Repeat("字", maxSystemPromptLen+1)}); !isValidation(err) {
		t.Fatalf("超上限应拒绝: %v", err)
	}
}

func TestJargonLengthAndCountBoundary(t *testing.T) {
	svc, _, store := newTestAdminSvc(t)
	ctx := context.Background()

	// 长度：100 通过、101 拒绝。
	if _, err := svc.AddJargon(ctx, "admin", "g1", strings.Repeat("a", maxJargonLen)); err != nil {
		t.Fatalf("恰好 %d 字符应通过: %v", maxJargonLen, err)
	}
	if _, err := svc.AddJargon(ctx, "admin", "g1", strings.Repeat("a", maxJargonLen+1)); !isValidation(err) {
		t.Fatalf("超 %d 字符应拒绝: %v", maxJargonLen, err)
	}

	// 条数：预置满 maxJargonPerGroup 条（store 直写，绕过 service 计数）→ 新增拒绝。
	for i := 0; i < maxJargonPerGroup; i++ {
		if err := store.AddJargon(ctx, "g2", "jargon-"+strconv.Itoa(i)); err != nil {
			t.Fatalf("预置黑话失败: %v", err)
		}
	}
	items, err := svc.ListJargonWithStatus(ctx, "g2", "all")
	if err != nil || len(items) != maxJargonPerGroup {
		t.Fatalf("预置应达上限条数 %d, 实际 %d (%v)", maxJargonPerGroup, len(items), err)
	}
	if _, err := svc.AddJargon(ctx, "admin", "g2", "overflow"); !isValidation(err) {
		t.Fatalf("达上限后新增应拒绝: %v", err)
	}
}

func TestProfileItemsBoundary(t *testing.T) {
	svc, _, _ := newTestAdminSvc(t)
	ctx := context.Background()

	mk := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "topic-" + strconv.Itoa(i)
		}
		return out
	}
	// 恰好上限通过。
	if _, err := svc.UpsertGroupProfile(ctx, "admin", entity.GroupProfile{GroupID: "g1", Topics: mk(maxProfileItems)}); err != nil {
		t.Fatalf("恰好 %d 条应通过: %v", maxProfileItems, err)
	}
	// 超上限拒绝。
	if _, err := svc.UpsertGroupProfile(ctx, "admin", entity.GroupProfile{GroupID: "g1", Rules: mk(maxProfileItems + 1)}); !isValidation(err) {
		t.Fatalf("超 %d 条应拒绝: %v", maxProfileItems, err)
	}
}

func TestMemberFactLengthBoundary(t *testing.T) {
	svc, _, _ := newTestAdminSvc(t)
	ctx := context.Background()

	if err := svc.AddMemberFact(ctx, "admin", "g1", "u1", strings.Repeat("事", maxFactLen)); err != nil {
		t.Fatalf("恰好 %d 字符应通过: %v", maxFactLen, err)
	}
	if err := svc.AddMemberFact(ctx, "admin", "g1", "u2", strings.Repeat("事", maxFactLen+1)); !isValidation(err) {
		t.Fatalf("超 %d 字符应拒绝: %v", maxFactLen, err)
	}
}

func TestQuietHoursBoundary(t *testing.T) {
	svc, _, _ := newTestAdminSvc(t)
	ctx := context.Background()

	// 注："7:00" 亦合法——Go time.Parse("15:04") 接受一位小时，与运行期 parseHM 解析一致。
	valid := []string{"00:00", "23:59", "07:30", "7:00"}
	for _, hm := range valid {
		if _, err := svc.UpsertGroupConfig(ctx, "admin", entity.GroupConfig{
			GroupID: "g-" + hm, Mode: "auto", QuietHoursStart: hm, QuietHoursEnd: hm,
		}); err != nil {
			t.Errorf("合法时段 %q 应通过: %v", hm, err)
		}
	}
	invalid := []string{"24:00", "23:60", "2300", "aa:bb"}
	for _, hm := range invalid {
		if _, err := svc.UpsertGroupConfig(ctx, "admin", entity.GroupConfig{
			GroupID: "gb", Mode: "auto", QuietHoursStart: hm,
		}); !isValidation(err) {
			t.Errorf("非法时段 %q 应拒绝: %v", hm, err)
		}
	}
	// 空串 = 走全局（合法）。
	if _, err := svc.UpsertGroupConfig(ctx, "admin", entity.GroupConfig{GroupID: "g-empty", Mode: "auto"}); err != nil {
		t.Errorf("空时段（走全局）应通过: %v", err)
	}
}