package sqlite

// 管理后端扩展（P7-001）DB 层单测：新迁移 003、10 个新增 Storage 方法。

import (
	"context"
	"errors"
	"testing"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
)

// TestMigrateAdminUser 校验 003_admin_user.sql 迁移重放幂等：
// 新库建表 → 再 Open 不重复执行（版本记录），admin_user 可正常读写。
func TestMigrateAdminUser(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	applied, err := s.appliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("读取已执行迁移失败: %v", err)
	}
	if !applied["003_admin_user.sql"] {
		t.Fatalf("003_admin_user.sql 应已执行, 实际: %v", applied)
	}
	s.Close()

	// 幂等重放：再次打开不因 duplicate table 报错。
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("重复打开应幂等, 实际: %v", err)
	}
	defer s2.Close()

	if _, err := s2.CreateAdminUser(context.Background(), entity.AdminUser{Username: "admin", PasswordHash: "h"}); err != nil {
		t.Fatalf("003 后 admin_user 应可写: %v", err)
	}
}

// TestListDeleteGroupConfig 覆盖管理面 group_config 列表与删除（删除恢复全局兜底）。
func TestListDeleteGroupConfig(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// 空列表。
	got, err := s.ListGroupConfigs(ctx)
	if err != nil {
		t.Fatalf("ListGroupConfigs 失败: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空库应返回空列表, 实际: %+v", got)
	}

	// upsert 两群 → 按 group_id 排序返回。
	if err := s.UpsertGroupConfig(ctx, entity.GroupConfig{GroupID: "g2", Mode: "auto"}); err != nil {
		t.Fatalf("UpsertGroupConfig 失败: %v", err)
	}
	if err := s.UpsertGroupConfig(ctx, entity.GroupConfig{GroupID: "g1", Mode: "mention", EnergyMax: 100}); err != nil {
		t.Fatalf("UpsertGroupConfig 失败: %v", err)
	}
	got, err = s.ListGroupConfigs(ctx)
	if err != nil {
		t.Fatalf("ListGroupConfigs 失败: %v", err)
	}
	if len(got) != 2 || got[0].GroupID != "g1" || got[0].Mode != "mention" || got[0].EnergyMax != 100 || got[1].GroupID != "g2" {
		t.Fatalf("应按 group_id 排序返回两群配置, 实际: %+v", got)
	}

	// 删除存在 → 单群配置查不到了。
	if err := s.DeleteGroupConfig(ctx, "g1"); err != nil {
		t.Fatalf("DeleteGroupConfig 失败: %v", err)
	}
	if _, err := s.GetGroupConfig(ctx, "g1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("删除后应报 ErrNotFound, 实际: %v", err)
	}
	// 删除不存在 → ErrNotFound。
	if err := s.DeleteGroupConfig(ctx, "g-nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("删除不存在应报 ErrNotFound, 实际: %v", err)
	}
}

// TestListUpsertPersona 覆盖管理面人格列表与按 agent 幂等 upsert。
func TestListUpsertPersona(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// 插入 → 列表含。
	if err := s.UpsertPersona(ctx, entity.Persona{Agent: "PlumeBot", Name: "默认", SystemPrompt: "你是赛博群友。"}); err != nil {
		t.Fatalf("UpsertPersona 失败: %v", err)
	}
	got, err := s.ListPersonas(ctx)
	if err != nil {
		t.Fatalf("ListPersonas 失败: %v", err)
	}
	if len(got) != 1 || got[0].Agent != "PlumeBot" || got[0].Name != "默认" || got[0].SystemPrompt != "你是赛博群友。" {
		t.Fatalf("列表应含插入的人格, 实际: %+v", got)
	}

	// 同 agent 更新 → 不新增行、值被覆盖。
	if err := s.UpsertPersona(ctx, entity.Persona{Agent: "PlumeBot", Name: "新名", SystemPrompt: "改版人设。"}); err != nil {
		t.Fatalf("UpsertPersona 覆盖失败: %v", err)
	}
	got, err = s.ListPersonas(ctx)
	if err != nil {
		t.Fatalf("ListPersonas 失败: %v", err)
	}
	if len(got) != 1 || got[0].Name != "新名" || got[0].SystemPrompt != "改版人设。" {
		t.Fatalf("同 agent upsert 应覆盖不新增, 实际: %+v", got)
	}
	byAgent, err := s.GetPersonaByAgent(ctx, "PlumeBot")
	if err != nil {
		t.Fatalf("GetPersonaByAgent 失败: %v", err)
	}
	if byAgent.ID != got[0].ID {
		t.Fatalf("upsert 应保留原记录 ID, 实际: %d vs %d", byAgent.ID, got[0].ID)
	}
}

// TestListJargonWithStatus 覆盖管理面黑话列表（含审核状态）与 confirmed 流转。
func TestListJargonWithStatus(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.AddJargon(ctx, "g1", "yyds"); err != nil {
		t.Fatalf("AddJargon 失败: %v", err)
	}
	if err := s.AddJargon(ctx, "g1", "绝绝子"); err != nil {
		t.Fatalf("AddJargon 失败: %v", err)
	}
	if err := s.ConfirmJargon(ctx, "g1", "yyds"); err != nil {
		t.Fatalf("ConfirmJargon 失败: %v", err)
	}

	got, err := s.ListJargonWithStatus(ctx, "g1")
	if err != nil {
		t.Fatalf("ListJargonWithStatus 失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应返回 2 条黑话, 实际: %+v", got)
	}
	// 按插入序：yyds(confirmed) 在前，绝绝子(pending) 在后。
	if got[0].Jargon != "yyds" || got[0].Status != "confirmed" ||
		got[1].Jargon != "绝绝子" || got[1].Status != "pending" {
		t.Fatalf("应携带审核状态并区分 pending/confirmed, 实际: %+v", got)
	}

	// 其他群隔离。
	other, err := s.ListJargonWithStatus(ctx, "g2")
	if err != nil {
		t.Fatalf("ListJargonWithStatus 失败: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("其他群不应出现该群黑话, 实际: %+v", other)
	}
}

// TestDeleteGroupProfile 覆盖管理面删除群画像（恢复无画像态）。
func TestDeleteGroupProfile(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.UpsertGroupProfile(ctx, entity.GroupProfile{GroupID: "g1", Culture: "认真讨论"}); err != nil {
		t.Fatalf("UpsertGroupProfile 失败: %v", err)
	}
	if err := s.DeleteGroupProfile(ctx, "g1"); err != nil {
		t.Fatalf("DeleteGroupProfile 失败: %v", err)
	}
	if _, err := s.GetGroupProfile(ctx, "g1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("删除后应报 ErrNotFound, 实际: %v", err)
	}
	if err := s.DeleteGroupProfile(ctx, "g-nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("删除不存在应报 ErrNotFound, 实际: %v", err)
	}
}

// TestAdminUser 覆盖 admin_user 表 CRUD 与唯一约束冲突。
func TestAdminUser(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// 创建 + 按名查询。
	id, err := s.CreateAdminUser(ctx, entity.AdminUser{Username: "admin", PasswordHash: "hash1", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatalf("CreateAdminUser 失败: %v", err)
	}
	if id <= 0 {
		t.Fatalf("应返回自增 ID > 0, 实际: %d", id)
	}
	u, err := s.GetAdminUserByName(ctx, "admin")
	if err != nil {
		t.Fatalf("GetAdminUserByName 失败: %v", err)
	}
	if u.ID != id || u.Username != "admin" || u.PasswordHash != "hash1" || u.CreatedAt != 1 || u.UpdatedAt != 1 {
		t.Fatalf("查询结果不符, 实际: %+v", u)
	}
	// 不存在 → ErrNotFound。
	if _, err := s.GetAdminUserByName(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("不存在应报 ErrNotFound, 实际: %v", err)
	}

	// 同名冲突 → ErrConflict。
	if _, err := s.CreateAdminUser(ctx, entity.AdminUser{Username: "admin", PasswordHash: "hash2"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("用户名冲突应报 ErrConflict, 实际: %v", err)
	}

	// 列表（注册门控）。
	users, err := s.ListAdminUsers(ctx)
	if err != nil {
		t.Fatalf("ListAdminUsers 失败: %v", err)
	}
	if len(users) != 1 || users[0].Username != "admin" {
		t.Fatalf("列表应含唯一管理员, 实际: %+v", users)
	}
	// 第二个账号（仅用于验证列表多行 + updated_at 更新）。
	if _, err := s.CreateAdminUser(ctx, entity.AdminUser{Username: "op", PasswordHash: "hash", CreatedAt: 2, UpdatedAt: 2}); err != nil {
		t.Fatalf("CreateAdminUser 失败: %v", err)
	}
	users, _ = s.ListAdminUsers(ctx)
	if len(users) != 2 || users[0].Username != "admin" || users[1].Username != "op" {
		t.Fatalf("列表应按 ID 序返回 2 账号, 实际: %+v", users)
	}

	// 改密生效。
	if err := s.UpdateAdminUserPassword(ctx, "admin", "hash3"); err != nil {
		t.Fatalf("UpdateAdminUserPassword 失败: %v", err)
	}
	u, _ = s.GetAdminUserByName(ctx, "admin")
	if u.PasswordHash != "hash3" || u.UpdatedAt == 0 {
		t.Fatalf("密码散列应更新且 updated_at 落库, 实际: %+v", u)
	}
	// 改密目标不存在 → ErrNotFound。
	if err := s.UpdateAdminUserPassword(ctx, "nope", "hash"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("改密不存在账号应报 ErrNotFound, 实际: %v", err)
	}
}