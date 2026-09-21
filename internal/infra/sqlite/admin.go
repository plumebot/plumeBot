package sqlite

// 管理后端扩展实现（P7-001，见 docs/admin-web-api-plan.md §5）。
// 全部 SQL 常量在 queries.go「admin 管理后端」节，方法体不内嵌 SQL。

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
)

// ──────────────────────────── group_config (admin) ────────────────────────────

// ListGroupConfigs 列出全部已配置群的静态配置（按群 ID 排序）。
func (s *Storage) ListGroupConfigs(ctx context.Context) ([]entity.GroupConfig, error) {
	rows, err := s.db.QueryContext(ctx, sqlListGroupConfigs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []entity.GroupConfig
	for rows.Next() {
		var c entity.GroupConfig
		if err := rows.Scan(&c.GroupID, &c.Mode, &c.EnergyMax, &c.EnergyCost, &c.EnergyRecover,
			&c.EnergyThreshold, &c.CooldownSeconds, &c.ConsecutiveLimit, &c.RestSeconds,
			&c.QuietHoursStart, &c.QuietHoursEnd, &c.ShortMessageChars, &c.GroupMgmtEnabled); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteGroupConfig 删除单群配置行（恢复全局兜底）；不存在时返回 domain.ErrNotFound。
func (s *Storage) DeleteGroupConfig(ctx context.Context, groupID string) error {
	res, err := s.db.ExecContext(ctx, sqlDeleteGroupConfig, groupID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ──────────────────────────── persona (admin) ────────────────────────────

// ListPersonas 列出全部人格模板。
func (s *Storage) ListPersonas(ctx context.Context) ([]entity.Persona, error) {
	rows, err := s.db.QueryContext(ctx, sqlListPersonas)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []entity.Persona
	for rows.Next() {
		var p entity.Persona
		if err := rows.Scan(&p.ID, &p.Agent, &p.Name, &p.SystemPrompt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpsertPersona 按 agent 幂等写人格模板（存在更新、不存在插入，免两段查 id）。
func (s *Storage) UpsertPersona(ctx context.Context, p entity.Persona) error {
	_, err := s.db.ExecContext(ctx, sqlUpsertPersona, p.Agent, p.Name, p.SystemPrompt)
	return err
}

// ──────────────────────────── group_jargon (admin) ────────────────────────────

// ListJargonWithStatus 列出指定群全部黑话（含审核状态，按插入序）。
func (s *Storage) ListJargonWithStatus(ctx context.Context, groupID string) ([]entity.Jargon, error) {
	rows, err := s.db.QueryContext(ctx, sqlListJargonWithStatus, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []entity.Jargon
	for rows.Next() {
		var j entity.Jargon
		if err := rows.Scan(&j.GroupID, &j.Jargon, &j.Status); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ──────────────────────────── group_profile (admin) ────────────────────────────

// DeleteGroupProfile 删除单群画像（恢复「无画像」态）；不存在时返回 domain.ErrNotFound。
func (s *Storage) DeleteGroupProfile(ctx context.Context, groupID string) error {
	res, err := s.db.ExecContext(ctx, sqlDeleteGroupProfile, groupID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ──────────────────────────── admin_user ────────────────────────────

// GetAdminUserByName 按用户名查询管理员账号；不存在时返回 domain.ErrNotFound。
func (s *Storage) GetAdminUserByName(ctx context.Context, username string) (*entity.AdminUser, error) {
	row := s.db.QueryRowContext(ctx, sqlGetAdminUserByName, username)

	var u entity.AdminUser
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// CreateAdminUser 创建管理员账号；username 冲突（UNIQUE）时返回 domain.ErrConflict。
func (s *Storage) CreateAdminUser(ctx context.Context, u entity.AdminUser) (int64, error) {
	res, err := s.db.ExecContext(ctx, sqlCreateAdminUser, u.Username, u.PasswordHash, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		if isUniqueConstraint(err) {
			return 0, domain.ErrConflict
		}
		return 0, err
	}
	return res.LastInsertId()
}

// ListAdminUsers 列出全部管理员账号（注册门控用，按 ID 序）。
func (s *Storage) ListAdminUsers(ctx context.Context) ([]entity.AdminUser, error) {
	rows, err := s.db.QueryContext(ctx, sqlListAdminUsers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []entity.AdminUser
	for rows.Next() {
		var u entity.AdminUser
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateAdminUserPassword 更新指定用户的密码散列；用户名不存在时返回 domain.ErrNotFound。
// updated_at 由 SQL 内 strftime('%s','now') 落库。
func (s *Storage) UpdateAdminUserPassword(ctx context.Context, username, hash string) error {
	res, err := s.db.ExecContext(ctx, sqlUpdateAdminUserPassword, hash, username)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// isUniqueConstraint 判断 modernc 驱动的 UNIQUE 约束冲突：主/扩展码 + 消息双重校验
// （驱动 Error.Code() 为主码 SQLITE_CONSTRAINT=19 或扩展码 SQLITE_CONSTRAINT_UNIQUE=2067，
// 消息含 "UNIQUE constraint failed"，见 modernc.org/sqlite v1.55.0）。
func isUniqueConstraint(err error) bool {
	var se *sqlitedrv.Error
	if !errors.As(err, &se) {
		return false
	}
	if se.Code() != sqlite3.SQLITE_CONSTRAINT && se.Code() != sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return false
	}
	return strings.Contains(se.Error(), "UNIQUE constraint failed")
}
