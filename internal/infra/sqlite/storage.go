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
func Open(dataDir string) (*Storage, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("sqlite: 创建数据目录失败: %w", err)
	}

	dbPath := filepath.Join(dataDir, "plumebot.db")
	db, err := sql.Open("sqlite", dbPath)
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

// migrate 执行嵌入的全部迁移文件 DDL（按文件名序拼接后逐条执行）。
func (s *Storage) migrate(ctx context.Context) error {
	names, err := fs.Glob(schemaFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("列举迁移文件失败: %w", err)
	}
	sort.Strings(names)

	var sb strings.Builder
	for _, name := range names {
		b, err := schemaFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("读取迁移文件 %s 失败: %w", name, err)
		}
		sb.Write(b)
		sb.WriteString(";")
	}
	for _, stmt := range strings.Split(sb.String(), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("执行 DDL 失败: %w\n%s", err, stmt)
		}
	}
	return nil
}

// ──────────────────────────── messages ────────────────────────────

// SaveMessage 保存一条聊天消息到 messages 表。
func (s *Storage) SaveMessage(ctx context.Context, msg entity.Message) error {
	_, err := s.db.ExecContext(ctx, sqlSaveMessage,
		msg.MessageID, msg.GroupID, msg.UserID, msg.Content, msg.Timestamp, msg.MessageType)
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
		if err := rows.Scan(&m.MessageID, &m.GroupID, &m.UserID, &m.Content, &m.Timestamp, &m.MessageType); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
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

// ──────────────────────────── plugin_config ────────────────────────────

// UpsertPluginConfig 插入或更新插件的群配置（(group_id, plugin_name) 冲突时覆盖）。
func (s *Storage) UpsertPluginConfig(ctx context.Context, cfg entity.PluginConfig) error {
	_, err := s.db.ExecContext(ctx, sqlUpsertPluginConfig, cfg.GroupID, cfg.PluginName, cfg.Config)
	return err
}

// GetPluginConfig 按群和插件名查询配置。不存在时返回 domain.ErrNotFound。
func (s *Storage) GetPluginConfig(ctx context.Context, groupID, pluginName string) (*entity.PluginConfig, error) {
	row := s.db.QueryRowContext(ctx, sqlGetPluginConfig, groupID, pluginName)

	var cfg entity.PluginConfig
	if err := row.Scan(&cfg.GroupID, &cfg.PluginName, &cfg.Config); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &cfg, nil
}

// ListPluginConfigs 列出指定群内全部插件配置。
func (s *Storage) ListPluginConfigs(ctx context.Context, groupID string) ([]entity.PluginConfig, error) {
	rows, err := s.db.QueryContext(ctx, sqlListPluginConfigs, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []entity.PluginConfig
	for rows.Next() {
		var cfg entity.PluginConfig
		if err := rows.Scan(&cfg.GroupID, &cfg.PluginName, &cfg.Config); err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// DeletePluginConfig 删除指定群中指定插件的配置。
func (s *Storage) DeletePluginConfig(ctx context.Context, groupID, pluginName string) error {
	_, err := s.db.ExecContext(ctx, sqlDeletePluginConfig, groupID, pluginName)
	return err
}
