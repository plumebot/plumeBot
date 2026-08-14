package sqlite

const (
	// ── messages ──

	sqlSaveMessage = `INSERT OR IGNORE INTO messages (message_id, group_id, user_id, parts, timestamp, message_type)
 VALUES (?, ?, ?, ?, ?, ?)`

	sqlGetMessages = `SELECT message_id, group_id, user_id, parts, timestamp, message_type
 FROM messages WHERE group_id = ? ORDER BY timestamp DESC LIMIT ? OFFSET ?`

	// ── conversation_summary ──

	sqlSaveSummary = `INSERT INTO conversation_summary (chat_id, seq, text, keywords, decisions, created_at)
 VALUES (?, ?, ?, ?, ?, ?)
 ON CONFLICT(chat_id, seq) DO UPDATE SET
 text=excluded.text, keywords=excluded.keywords, decisions=excluded.decisions, created_at=excluded.created_at`

	sqlListSummaries = `SELECT chat_id, seq, text, keywords, decisions, created_at
 FROM conversation_summary WHERE chat_id = ? ORDER BY seq DESC LIMIT ?`

	// ── group_profile ──

	sqlUpsertGroupProfile = `INSERT INTO group_profile (group_id, culture, topics, active_hours, rules, atmosphere)
 VALUES (?, ?, ?, ?, ?, ?)
 ON CONFLICT(group_id) DO UPDATE SET
 culture=excluded.culture, topics=excluded.topics, active_hours=excluded.active_hours,
 rules=excluded.rules, atmosphere=excluded.atmosphere`

	sqlGetGroupProfile = `SELECT group_id, culture, topics, active_hours, rules, atmosphere
 FROM group_profile WHERE group_id = ?`

	// ── group_jargon ──

	sqlAddJargon    = `INSERT OR IGNORE INTO group_jargon (group_id, jargon) VALUES (?, ?)`
	sqlListJargon   = `SELECT jargon FROM group_jargon WHERE group_id = ?`
	sqlDeleteJargon = `DELETE FROM group_jargon WHERE group_id = ? AND jargon = ?`

	sqlListConfirmedJargon = `SELECT jargon FROM group_jargon WHERE group_id = ? AND status = 'confirmed'`

	sqlConfirmJargon = `UPDATE group_jargon SET status = 'confirmed' WHERE group_id = ? AND jargon = ?`

	// ── member_facts ──

	sqlAddMemberFact    = `INSERT OR IGNORE INTO member_facts (group_id, user_id, fact) VALUES (?, ?, ?)`
	sqlListMemberFacts  = `SELECT fact FROM member_facts WHERE group_id = ? AND user_id = ?`
	sqlDeleteMemberFact = `DELETE FROM member_facts WHERE group_id = ? AND user_id = ? AND fact = ?`

	// ── persona ──

	sqlInsertPersona = `INSERT INTO persona (agent, name, system_prompt) VALUES (?, ?, ?)`
	sqlUpdatePersona = `UPDATE persona SET agent=?, name=?, system_prompt=? WHERE id=?`
	sqlGetPersona    = `SELECT id, agent, name, system_prompt FROM persona WHERE id = ?`

	sqlGetPersonaByAgent = `SELECT id, agent, name, system_prompt FROM persona WHERE agent = ?`

	// ── bot_state ──

	sqlUpsertBotState = `INSERT INTO bot_state (group_id, state) VALUES (?, ?)
 ON CONFLICT(group_id) DO UPDATE SET state=excluded.state`

	sqlGetBotState = `SELECT group_id, state FROM bot_state WHERE group_id = ?`

	// ── plugin_config ──

	sqlUpsertPluginConfig = `INSERT INTO plugin_config (group_id, plugin_name, config) VALUES (?, ?, ?)
 ON CONFLICT(group_id, plugin_name) DO UPDATE SET config=excluded.config`

	sqlGetPluginConfig    = `SELECT group_id, plugin_name, config FROM plugin_config WHERE group_id = ? AND plugin_name = ?`
	sqlListPluginConfigs  = `SELECT group_id, plugin_name, config FROM plugin_config WHERE group_id = ?`
	sqlDeletePluginConfig = `DELETE FROM plugin_config WHERE group_id = ? AND plugin_name = ?`

	// ── group_config ──

	sqlUpsertGroupConfig = `INSERT INTO group_config (group_id, mode, energy_max, energy_cost, energy_recover,
 energy_threshold, cooldown_seconds, consecutive_limit, rest_seconds, quiet_hours_start, quiet_hours_end, short_message_chars)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
 ON CONFLICT(group_id) DO UPDATE SET
 mode=excluded.mode, energy_max=excluded.energy_max, energy_cost=excluded.energy_cost,
 energy_recover=excluded.energy_recover, energy_threshold=excluded.energy_threshold,
 cooldown_seconds=excluded.cooldown_seconds, consecutive_limit=excluded.consecutive_limit,
 rest_seconds=excluded.rest_seconds, quiet_hours_start=excluded.quiet_hours_start,
 quiet_hours_end=excluded.quiet_hours_end, short_message_chars=excluded.short_message_chars`

	sqlGetGroupConfig = `SELECT group_id, mode, energy_max, energy_cost, energy_recover,
 energy_threshold, cooldown_seconds, consecutive_limit, rest_seconds, quiet_hours_start, quiet_hours_end, short_message_chars
 FROM group_config WHERE group_id = ?`
)
