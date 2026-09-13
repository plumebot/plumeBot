package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
)

// schemaFS 内嵌 migrations/ 目录下的全部 DDL 迁移文件（文件名序执行）。
//
//go:embed migrations/*.sql
var schemaFS embed.FS

// 编译期校验：Storage 实现 domain.Storage。
var _ domain.Storage = (*Storage)(nil)

// Storage 是 domain.Storage 的 SQLite 实现。
type Storage struct {
	db *sql.DB
}

// Open 打开（或创建）dataDir/plumebot.db，自动建表（migrate 执行全部 DDL）。
// DSN 必须带 _busy_timeout + _journal_mode=WAL：消息管线与管理面 web 共用同一 *sql.DB，
// 裸 DSN 下 SQLite 默认 rollback journal + busy_timeout=0，任一连接持写锁时其余连接
// 立即 SQLITE_BUSY（database is locked）——表现为管理页请求 500、群配置保存不落库、
// 消息落库失败导致回复静默丢弃（回归测试见 concurrent_test.go）。
func Open(dataDir string) (*Storage, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("sqlite: 创建数据目录失败: %w", err)
	}

	dbPath := filepath.Join(dataDir, "plumebot.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("sqlite: 打开数据库失败: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: 数据库连接失败: %w", err)
	}

	s := &Storage{db: db}

	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: 建表失败: %w", err)
	}

	return s, nil
}

// Close 关闭数据库连接。
func (s *Storage) Close() error {
	return s.db.Close()
}

// ──────────────────────────── migration ────────────────────────────

// sqlCreateSchemaMigrations 建迁移版本记录表（B-015 起 migrate 升级为版本记录式：
// 每个迁移文件在单事务内执行一次并记录，旧库 001 重放靠 IF NOT EXISTS 幂等，
// 新增列类迁移（ALTER TABLE）不再与全量重放冲突）。
const sqlCreateSchemaMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,                       -- 迁移文件名，如 "001_initial_schema.sql"
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
)`

// migrate 执行未记录的迁移文件（文件名序）。每个文件在单事务内逐语句执行：
// 任一语句失败整体回滚、不记录版本，下次启动重试（避免「ALTER 成功但未记录
// → 重启 duplicate column」）；成功则写入 schema_migrations。
func (s *Storage) migrate(ctx context.Context) error {
	names, err := fs.Glob(schemaFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("列举迁移文件失败: %w", err)
	}
	sort.Strings(names)

	if _, err := s.db.ExecContext(ctx, sqlCreateSchemaMigrations); err != nil {
		return fmt.Errorf("创建 schema_migrations 失败: %w", err)
	}
	applied, err := s.appliedMigrations(ctx)
	if err != nil {
		return err
	}

	for _, name := range names {
		version := filepath.Base(name)
		if applied[version] {
			continue
		}
		b, err := schemaFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("读取迁移文件 %s 失败: %w", name, err)
		}
		var sb strings.Builder
		sb.Write(b)
		sb.WriteString(";")
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("开启迁移事务 %s 失败: %w", version, err)
		}
		for _, stmt := range strings.Split(sb.String(), ";") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("执行迁移 %s 失败: %w\n%s", version, err, stmt)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			tx.Rollback()
			return fmt.Errorf("记录迁移 %s 失败: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("提交迁移 %s 失败: %w", version, err)
		}
	}
	return nil
}

// appliedMigrations 读取 schema_migrations 中已执行的迁移文件名集合。
func (s *Storage) appliedMigrations(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("读取已执行迁移失败: %w", err)
	}
	defer rows.Close()
	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("扫描已执行迁移失败: %w", err)
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// ──────────────────────────── messages ────────────────────────────

// SaveMessage 保存一条聊天消息到 messages 表（Parts 序列化为 JSON）。
func (s *Storage) SaveMessage(ctx context.Context, msg entity.Message) error {
	_, err := s.db.ExecContext(ctx, sqlSaveMessage,
		msg.MessageID, msg.GroupID, msg.UserID, marshalParts(msg.Parts), msg.Timestamp, msg.MessageType)
	return err
}

// GetMessages 按群 ID 获取最近消息，按时间倒序，支持分页（limit/offset）。
func (s *Storage) GetMessages(ctx context.Context, groupID string, limit, offset int) ([]entity.Message, error) {
	rows, err := s.db.QueryContext(ctx, sqlGetMessages, groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []entity.Message
	for rows.Next() {
		var m entity.Message
		var parts string
		if err := rows.Scan(&m.MessageID, &m.GroupID, &m.UserID, &parts, &m.Timestamp, &m.MessageType); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(parts), &m.Parts)
		out = append(out, m)
	}
	return out, rows.Err()
}

// marshalParts 将内容段序列化为 JSON；空段返回 "[]"（避免 null）。
func marshalParts(parts []entity.ContentPart) string {
	if len(parts) == 0 {
		return "[]"
	}
	b, err := json.Marshal(parts)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// UpdateMessageParts 覆盖一条消息的内容段（P6-001 B-025：图片描述写回 messages.parts）。
// message_id 不存在时 UPDATE 影响 0 行，幂等不报错。
func (s *Storage) UpdateMessageParts(ctx context.Context, messageID string, parts []entity.ContentPart) error {
	_, err := s.db.ExecContext(ctx, sqlUpdateMessageParts, marshalParts(parts), messageID)
	return err
}

// ──────────────────────────── conversation_summary ────────────────────────────

// SaveSummary 归档一条摘要到 conversation_summary 表。
// (chat_id, seq) 冲突时覆盖 —— 回灌的旧摘要再次被淘汰时重复归档是幂等的。
func (s *Storage) SaveSummary(ctx context.Context, sum entity.Summary) error {
	keywords, _ := json.Marshal(sum.Keywords)
	decisions, _ := json.Marshal(sum.Decisions)
	_, err := s.db.ExecContext(ctx, sqlSaveSummary,
		sum.ChatID, sum.Seq, sum.Text, string(keywords), string(decisions), sum.CreatedAt)
	return err
}

// ListSummaries 返回指定会话最新的 limit 条归档摘要（按 seq 时间正序）。
func (s *Storage) ListSummaries(ctx context.Context, chatID string, limit int) ([]entity.Summary, error) {
	rows, err := s.db.QueryContext(ctx, sqlListSummaries, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// SQL 按 seq 倒序取最新 limit 条，Go 侧反转回正序（旧→新）。
	var out []entity.Summary
	for rows.Next() {
		var sum entity.Summary
		var keywords, decisions string
		if err := rows.Scan(&sum.ChatID, &sum.Seq, &sum.Text, &keywords, &decisions, &sum.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(keywords), &sum.Keywords)
		json.Unmarshal([]byte(decisions), &sum.Decisions)
		out = append(out, sum)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ──────────────────────────── group_profile ────────────────────────────

// UpsertGroupProfile 插入或更新群聊画像（group_id 冲突时覆盖）。
func (s *Storage) UpsertGroupProfile(ctx context.Context, p entity.GroupProfile) error {
	topics, _ := json.Marshal(p.Topics)
	rules, _ := json.Marshal(p.Rules)
	atmosphere, _ := json.Marshal(p.Atmosphere)

	_, err := s.db.ExecContext(ctx, sqlUpsertGroupProfile,
		p.GroupID, p.Culture, string(topics), p.ActiveHours, string(rules), string(atmosphere))
	return err
}

// GetGroupProfile 按群 ID 查询群聊画像。不存在时返回 domain.ErrNotFound。
func (s *Storage) GetGroupProfile(ctx context.Context, groupID string) (*entity.GroupProfile, error) {
	row := s.db.QueryRowContext(ctx, sqlGetGroupProfile, groupID)

	var p entity.GroupProfile
	var topics, rules, atmosphere string
	if err := row.Scan(&p.GroupID, &p.Culture, &topics, &p.ActiveHours, &rules, &atmosphere); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	json.Unmarshal([]byte(topics), &p.Topics)
	json.Unmarshal([]byte(rules), &p.Rules)
	json.Unmarshal([]byte(atmosphere), &p.Atmosphere)
	return &p, nil
}

// ──────────────────────────── group_jargon ────────────────────────────

// AddJargon 添加一条群黑话。已存在的 (group_id, jargon) 组合会被忽略。
func (s *Storage) AddJargon(ctx context.Context, groupID, jargon string) error {
	_, err := s.db.ExecContext(ctx, sqlAddJargon, groupID, jargon)
	return err
}

// ListJargon 列出指定群的全部黑话。
func (s *Storage) ListJargon(ctx context.Context, groupID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, sqlListJargon, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// DeleteJargon 删除指定群的一条黑话。
func (s *Storage) DeleteJargon(ctx context.Context, groupID, jargon string) error {
	_, err := s.db.ExecContext(ctx, sqlDeleteJargon, groupID, jargon)
	return err
}

// ListConfirmedJargon 列出指定群内已确认（status='confirmed'）的黑话。
// learn_jargon 写入的黑话默认 pending，经 ConfirmJargon 转为 confirmed 后才会被列到。
func (s *Storage) ListConfirmedJargon(ctx context.Context, groupID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, sqlListConfirmedJargon, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ConfirmJargon 把一条黑话置为 confirmed（待确认 → 已确认）。黑话不存在时返回 domain.ErrNotFound。
func (s *Storage) ConfirmJargon(ctx context.Context, groupID, jargon string) error {
	res, err := s.db.ExecContext(ctx, sqlConfirmJargon, groupID, jargon)
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

// ──────────────────────────── member_facts ────────────────────────────

// AddMemberFact 添加一条个人事实记忆。已存在的 (group_id, user_id, fact) 组合会被忽略。
func (s *Storage) AddMemberFact(ctx context.Context, groupID, userID, fact string) error {
	_, err := s.db.ExecContext(ctx, sqlAddMemberFact, groupID, userID, fact)
	return err
}

// ListMemberFacts 列出指定用户在指定群内的全部事实记忆。
func (s *Storage) ListMemberFacts(ctx context.Context, groupID, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, sqlListMemberFacts, groupID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteMemberFact 删除指定用户的一条事实记忆。
func (s *Storage) DeleteMemberFact(ctx context.Context, groupID, userID, fact string) error {
	_, err := s.db.ExecContext(ctx, sqlDeleteMemberFact, groupID, userID, fact)
	return err
}

// ──────────────────────────── persona ────────────────────────────

// InsertPersona 插入一条人格模板，返回新记录的 ID。
// agent 字段 UNIQUE：同一 agent 重复插入返回错误。
func (s *Storage) InsertPersona(ctx context.Context, p entity.Persona) (int64, error) {
	res, err := s.db.ExecContext(ctx, sqlInsertPersona, p.Agent, p.Name, p.SystemPrompt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePersona 更新指定 ID 的人格模板。
func (s *Storage) UpdatePersona(ctx context.Context, p entity.Persona) error {
	_, err := s.db.ExecContext(ctx, sqlUpdatePersona, p.Agent, p.Name, p.SystemPrompt, p.ID)
	return err
}

// GetPersona 按 ID 查询人格模板。不存在时返回 domain.ErrNotFound。
func (s *Storage) GetPersona(ctx context.Context, id int64) (*entity.Persona, error) {
	row := s.db.QueryRowContext(ctx, sqlGetPersona, id)

	var p entity.Persona
	if err := row.Scan(&p.ID, &p.Agent, &p.Name, &p.SystemPrompt); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// GetPersonaByAgent 按绑定的 agent 名查询人格模板（人格选择 agent，agent 字段 UNIQUE）。
// 不存在时返回 domain.ErrNotFound。
func (s *Storage) GetPersonaByAgent(ctx context.Context, agent string) (*entity.Persona, error) {
	row := s.db.QueryRowContext(ctx, sqlGetPersonaByAgent, agent)

	var p entity.Persona
	if err := row.Scan(&p.ID, &p.Agent, &p.Name, &p.SystemPrompt); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// ──────────────────────────── bot_state ────────────────────────────

// UpsertBotState 插入或更新 bot 在指定群的运行时状态（group_id 冲突时覆盖）。
func (s *Storage) UpsertBotState(ctx context.Context, st entity.BotState) error {
	_, err := s.db.ExecContext(ctx, sqlUpsertBotState, st.GroupID, st.State)
	return err
}

// GetBotState 按群 ID 查询 bot 状态。不存在时返回 domain.ErrNotFound。
func (s *Storage) GetBotState(ctx context.Context, groupID string) (*entity.BotState, error) {
	row := s.db.QueryRowContext(ctx, sqlGetBotState, groupID)

	var st entity.BotState
	if err := row.Scan(&st.GroupID, &st.State); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &st, nil
}

// ──────────────────────────── group_config ────────────────────────────

// UpsertGroupConfig 插入或更新群的静态配置（group_id 冲突时覆盖）。
func (s *Storage) UpsertGroupConfig(ctx context.Context, cfg entity.GroupConfig) error {
	_, err := s.db.ExecContext(ctx, sqlUpsertGroupConfig,
		cfg.GroupID, cfg.Mode, cfg.EnergyMax, cfg.EnergyCost, cfg.EnergyRecover,
		cfg.EnergyThreshold, cfg.CooldownSeconds, cfg.ConsecutiveLimit, cfg.RestSeconds,
		cfg.QuietHoursStart, cfg.QuietHoursEnd, cfg.ShortMessageChars, cfg.GroupMgmtEnabled)
	return err
}

// GetGroupConfig 按群 ID 查询静态配置。不存在时返回 domain.ErrNotFound
//（群管理开关语义：无配置行 = 默认关，见 entity.GroupConfig.GroupMgmtEnabled 注释）。
func (s *Storage) GetGroupConfig(ctx context.Context, groupID string) (*entity.GroupConfig, error) {
	row := s.db.QueryRowContext(ctx, sqlGetGroupConfig, groupID)

	var c entity.GroupConfig
	if err := row.Scan(&c.GroupID, &c.Mode, &c.EnergyMax, &c.EnergyCost, &c.EnergyRecover,
		&c.EnergyThreshold, &c.CooldownSeconds, &c.ConsecutiveLimit, &c.RestSeconds,
		&c.QuietHoursStart, &c.QuietHoursEnd, &c.ShortMessageChars, &c.GroupMgmtEnabled); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}
