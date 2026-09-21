// group_manager.go — B-015 群管理动作执行的 OneBot 实现。
// 与 domain.Sender（botSender）同构：matcher 闭包构造 per-event 实例（持有当次
// 事件 *zero.Ctx，动作绑定当次事件）经 WithGroupManager 注入 ctx。
// 三道护栏统一在此集中把关（工具层与插件 Actions 共用本执行路径，护栏不重复）：
//   - per-group 开关：group_config.group_mgmt_enabled（默认 1 开；无配置行同样视为开，
//     0 = 显式关闭）；
//   - 管理员校验：触发者与 bot 都须为群主/管理员（get_group_member_info 查 role，fail-closed）；
//   - 动作映射：mute 时长钳制到 QQ 上限，未知动作拒绝。
// 动作调用直接用 ctx.CallAction 检查 APIResponse（SetGroupBan 等封装吞掉响应，
// 动作失败无法向调用方反馈，高危能力静默失败不可接受）。
package onebot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/tidwall/gjson"
	zero "github.com/wdvxdr1123/ZeroBot"
	"go.uber.org/zap"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// maxMuteDurationSeconds 是 QQ 单次禁言时长上限（30 天），超限腾讯侧/NapCat 会拒绝。
const maxMuteDurationSeconds = 30 * 24 * 3600

// botGroupManager 是 domain.GroupManager 的 OneBot 实现（B-015）。
// per-event 实例：持当次事件 *zero.Ctx（动作绑定当次事件）+ domain.Storage
// （查 per-group 开关）+ botID（bot 管理员校验，空/非法在构造时归 0 → fail-closed）。
type botGroupManager struct {
	ctx   *zero.Ctx
	store domain.Storage
	botID int64
}

// newGroupManager 构造 per-event 的 GroupManager（matcher 闭包调用）。
func newGroupManager(ctx *zero.Ctx, store domain.Storage, botID int64) domain.GroupManager {
	return &botGroupManager{ctx: ctx, store: store, botID: botID}
}

// Execute 执行一个群管理动作，护栏链：群聊守卫 → per-group 开关 → 管理员校验
// → 动作映射（含时长钳制）→ CallAction 响应检查。
// 审计（架构 §17.5，防重复规则 3）：本方法对群管理动作做**唯一**审计——AI 工具层
//（group_tools.go）与插件 Actions 层（command.go executeActions）均不重复记录；
// 成功 Info、护栏拒绝/API 失败 Warn，均带 group_id/actor/op/target。
func (m *botGroupManager) Execute(ctx context.Context, action entity.GroupAction) error {
	// 审计经 ctx 内派生 logger（携带 trace_id=会话键，架构 §17.6）：群管理动作可在
	// 日志浏览页与会话内其它行（消息入口/结局/模型调用）串联。
	l := logger.From(ctx)
	groupID := m.ctx.Event.GroupID
	actor := m.ctx.Event.UserID // 触发者（per-event）即动作执行者
	baseFields := []zap.Field{
		logger.I64("group_id", groupID),
		logger.I64("actor", actor),
		logger.S("op", string(action.Op)),
		logger.S("target", action.Target),
	}
	if action.Duration > 0 {
		baseFields = append(baseFields, logger.I("duration", action.Duration))
	}

	if groupID == 0 {
		l.Warn("群管理动作被拒绝", append(baseFields, logger.Err(errors.New("仅支持群聊事件")))...)
		return errors.New("群管理动作仅支持群聊事件")
	}
	// 护栏 1：per-group 开关（默认开；无配置行 = 开，0 = 显式关闭，
	// 见 entity.GroupConfig.GroupMgmtEnabled 注释）。
	cfg, err := m.store.GetGroupConfig(ctx, strconv.FormatInt(groupID, 10))
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		l.Warn("群管理动作失败", append(baseFields, logger.Err(err))...)
		return fmt.Errorf("查询群配置失败: %w", err)
	}
	if cfg != nil && cfg.GroupMgmtEnabled == 0 {
		l.Warn("群管理动作被拒绝", append(baseFields, logger.S("reason", "group_mgmt_enabled=0"))...)
		return errors.New("本群未开启群管理功能（group_mgmt_enabled=0），拒绝执行")
	}
	// 护栏 2：触发者与 bot 都须为群主/管理员（fail-closed：查询失败/无 role 一律拒绝）。
	triggerer := m.ctx.Event.UserID
	if !memberInfoIsAdmin(m.ctx.GetGroupMemberInfo(groupID, triggerer, true)) {
		l.Warn("群管理动作被拒绝", append(baseFields, logger.S("reason", "triggerer_not_admin"))...)
		return errors.New("仅群主/管理员可触发群管理动作")
	}
	if !memberInfoIsAdmin(m.ctx.GetGroupMemberInfo(groupID, m.botID, true)) {
		l.Warn("群管理动作被拒绝", append(baseFields, logger.S("reason", "bot_not_admin"))...)
		return errors.New("bot 非本群主/管理员，无法执行（请确认 bot 已设为管理员且 bot.self_id 已配置）")
	}
	// 护栏 3：动作映射（含 mute 时长钳制）+ API 响应检查。
	actionName, params, err := onebotAction(groupID, action)
	if err != nil {
		l.Warn("群管理动作被拒绝", append(baseFields, logger.S("reason", "invalid_action"), logger.Err(err))...)
		return err
	}
	start := time.Now()
	rsp := m.ctx.CallAction(actionName, params)
	if rsp.Status != "ok" || rsp.RetCode != 0 {
		fields := append(baseFields, logger.S("api_action", actionName),
			logger.I64("retcode", rsp.RetCode), logger.S("message", rsp.Message))
		l.Warn("群管理动作失败", append(fields, logger.Err(errors.New(rsp.Wording)))...)
		return fmt.Errorf("动作 %s 执行失败: retcode=%d, message=%s, wording=%s",
			actionName, rsp.RetCode, rsp.Message, rsp.Wording)
	}
	l.Info("群管理动作", append(baseFields,
		logger.S("api_action", actionName),
		logger.I64("latency_ms", time.Since(start).Milliseconds()))...)
	return nil
}

// memberInfoIsAdmin 从 get_group_member_info 的 data 判断成员是否为群主/管理员
// （纯函数，便于单测）。fail-closed：缺 role / 查询失败返回空 role → 非管理员。
func memberInfoIsAdmin(info gjson.Result) bool {
	role := info.Get("role").String()
	return role == "owner" || role == "admin"
}

// onebotAction 将领域动作映射为 OneBot API 调用（纯函数，便于单测）。
// 直接返回 action 名 + 参数（调用方经 ctx.CallAction 执行并检查响应）：
// mute → set_group_ban(duration>0)；unmute → set_group_ban(duration=0，OneBot v11
// 规范 0 = 取消禁言)；kick → set_group_kick；set_card → set_group_card。
func onebotAction(groupID int64, action entity.GroupAction) (string, zero.Params, error) {
	target, err := strconv.ParseInt(action.Target, 10, 64)
	if err != nil {
		return "", nil, fmt.Errorf("无效的目标用户 %q", action.Target)
	}
	switch action.Op {
	case entity.GroupOpMute:
		if action.Duration <= 0 {
			return "", nil, errors.New("禁言时长必须 > 0")
		}
		d := int64(action.Duration)
		if d > maxMuteDurationSeconds {
			d = maxMuteDurationSeconds // 钳制到 QQ 上限，避免平台拒绝
		}
		return "set_group_ban", zero.Params{"group_id": groupID, "user_id": target, "duration": d}, nil
	case entity.GroupOpUnmute:
		return "set_group_ban", zero.Params{"group_id": groupID, "user_id": target, "duration": int64(0)}, nil
	case entity.GroupOpKick:
		return "set_group_kick", zero.Params{"group_id": groupID, "user_id": target, "reject_add_request": false}, nil
	case entity.GroupOpSetCard:
		return "set_group_card", zero.Params{"group_id": groupID, "user_id": target, "card": action.Card}, nil
	default:
		return "", nil, fmt.Errorf("未知群管理动作 %q", action.Op)
	}
}
