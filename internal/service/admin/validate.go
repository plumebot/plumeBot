package admin

// 管理面 fail-fast 字段校验（与运行期 mergeOverrides「非法保留默认」的容错语义刻意区分——
// 管理面即时可见错误，杜绝脏数据入库）。校验均在 store 写之前执行。

import (
	"strings"
	"time"
	"unicode/utf8"

	"plumebot/internal/domain/entity"
)

// 长度/条数上限常量（决策记录 §13 建议默认值，实现可微调）。
const (
	minPasswordLen     = 8
	maxSystemPromptLen = 20000 // persona.system_prompt 字符上限
	maxJargonLen       = 100   // 单条黑话字符上限
	maxJargonPerGroup  = 500   // 每群黑话条数上限
	maxFactLen         = 500   // 成员事实字符上限
	maxProfileItems    = 50    // 群画像 topics/rules/atmosphere 条数上限
)

// 触发模式合法值（与 service/control 的 ModeMention/ModeAuto 一致；管理面拒绝空/未知）。
const (
	modeMention = "mention"
	modeAuto    = "auto"
)

// validateAuthInput 校验注册/登录入参：用户名非空 + 密码长度。
func validateAuthInput(username, password string) error {
	if strings.TrimSpace(username) == "" {
		return entity.ValidationErrorf("用户名不能为空")
	}
	return validatePassword(password)
}

// validatePassword 校验密码最小长度（中文按字符计）。
func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLen {
		return entity.ValidationErrorf("密码至少 %d 位", minPasswordLen)
	}
	return nil
}

// validateGroupConfig 校验 group_config 全字段（管理面 PUT）。
// 0/空列 = 走全局（合法）；mode/group_mgmt_enabled 为显式枚举；quiet_hours 严格 HH:MM。
func validateGroupConfig(c *entity.GroupConfig) error {
	if strings.TrimSpace(c.GroupID) == "" {
		return entity.ValidationErrorf("group_id 不能为空")
	}
	if c.Mode != modeMention && c.Mode != modeAuto {
		return entity.ValidationErrorf("mode 只能是 mention 或 auto")
	}
	if c.GroupMgmtEnabled != 0 && c.GroupMgmtEnabled != 1 {
		return entity.ValidationErrorf("group_mgmt_enabled 只能是 0 或 1")
	}
	for _, p := range []struct {
		name  string
		value int
	}{
		{"energy_max", c.EnergyMax}, {"energy_cost", c.EnergyCost}, {"energy_recover", c.EnergyRecover},
		{"energy_threshold", c.EnergyThreshold}, {"cooldown_seconds", c.CooldownSeconds},
		{"consecutive_limit", c.ConsecutiveLimit}, {"rest_seconds", c.RestSeconds},
		{"short_message_chars", c.ShortMessageChars},
	} {
		if p.value < 0 {
			return entity.ValidationErrorf("%s 不能为负数", p.name)
		}
	}
	if err := validateHM(c.QuietHoursStart); err != nil {
		return err
	}
	return validateHM(c.QuietHoursEnd)
}

// validatePersona 校验人格模板：agent 非空、system_prompt 非空且不超上限。
func validatePersona(p *entity.Persona) error {
	if strings.TrimSpace(p.Agent) == "" {
		return entity.ValidationErrorf("agent 不能为空")
	}
	n := utf8.RuneCountInString(p.SystemPrompt)
	if n == 0 {
		return entity.ValidationErrorf("system_prompt 不能为空")
	}
	if n > maxSystemPromptLen {
		return entity.ValidationErrorf("system_prompt 超过上限 %d 字符", maxSystemPromptLen)
	}
	return nil
}

// validateGroupProfile 校验群画像：数组元素 trim 去空 + 条数上限。
func validateGroupProfile(p *entity.GroupProfile) error {
	if strings.TrimSpace(p.GroupID) == "" {
		return entity.ValidationErrorf("group_id 不能为空")
	}
	p.Topics = sanitizeStrings(p.Topics)
	p.Rules = sanitizeStrings(p.Rules)
	p.Atmosphere = sanitizeStrings(p.Atmosphere)
	if len(p.Topics) > maxProfileItems || len(p.Rules) > maxProfileItems || len(p.Atmosphere) > maxProfileItems {
		return entity.ValidationErrorf("topics/rules/atmosphere 条数不能超过 %d", maxProfileItems)
	}
	return nil
}

// validateJargon 校验黑话文本：trim 非空、不超字符上限。
func validateJargon(jargon string) error {
	if strings.TrimSpace(jargon) == "" {
		return entity.ValidationErrorf("黑话内容不能为空")
	}
	if utf8.RuneCountInString(jargon) > maxJargonLen {
		return entity.ValidationErrorf("黑话超过上限 %d 字符", maxJargonLen)
	}
	return nil
}

// validateMemberFact 校验成员事实：user_id 非空、fact trim 非空且不超上限。
func validateMemberFact(userID, fact string) error {
	if strings.TrimSpace(userID) == "" {
		return entity.ValidationErrorf("user_id 不能为空")
	}
	if strings.TrimSpace(fact) == "" {
		return entity.ValidationErrorf("事实内容不能为空")
	}
	if utf8.RuneCountInString(fact) > maxFactLen {
		return entity.ValidationErrorf("事实超过上限 %d 字符", maxFactLen)
	}
	return nil
}

// validateHM 校验 "HH:MM"；空串合法（= 走全局兜底），非空必须严格 HH:MM。
func validateHM(s string) error {
	if s == "" {
		return nil
	}
	if _, err := time.Parse("15:04", s); err != nil {
		return entity.ValidationErrorf("时间需按 HH:MM 格式（如 23:00）")
	}
	return nil
}

// sanitizeStrings trim 元素并过滤空串。
func sanitizeStrings(items []string) []string {
	out := items[:0]
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	return out
}