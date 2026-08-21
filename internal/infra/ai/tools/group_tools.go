// Package tools 提供 Agent 可调用的群管理动作工具（B-015 AI 自主群管理）。
//
// 设计原则（与 B-015 设计定案一致）：
//   - 动作经 agent tool 触发，不走回复通道；能力经 ctx 注入的 domain.GroupManager
//     执行（per-event 实例，绑定当次事件 *zero.Ctx），与记忆工具经 entity.Session
//     取会话身份同构，本工具集自身为无依赖共享单例；
//   - 护栏全部集中在 GroupManager.Execute（per-group 开关 + 触发者/bot 管理员校验
//     + mute 时长钳制），本层不重复校验，只做：群聊守卫 → 取执行器 → 构造动作；
//   - 工具 Desc 写清触发边界（高危动作、仅群聊、开关与管理员前置条件）。
package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
)

// GroupTools 是 AI 群管理动作工具的构造集合（B-015）。
// 与 MemoryTools 不同，本工具集不持任何依赖：动作执行能力经 ctx 注入
// （domain.GroupManagerFrom），护栏在 onebot 实现（botGroupManager.Execute）集中把关，
// 工具只做参数映射与中文反馈。
type GroupTools struct{}

// NewGroupTools 创建群管理工具集（无依赖共享单例，由 main.go 注册进 ai.ToolsRegistry）。
func NewGroupTools() *GroupTools {
	return &GroupTools{}
}

// groupMuteArgs 是 group_mute 工具的参数。
type groupMuteArgs struct {
	UserID   string `json:"user_id" jsonschema:"required,description=目标群成员QQ号"`
	Duration int    `json:"duration" jsonschema:"required,description=禁言时长（秒），1~2592000（30 天上限）"`
}

// groupUnmuteArgs 是 group_unmute 工具的参数。
type groupUnmuteArgs struct {
	UserID string `json:"user_id" jsonschema:"required,description=目标群成员QQ号"`
}

// groupKickArgs 是 group_kick 工具的参数。
type groupKickArgs struct {
	UserID string `json:"user_id" jsonschema:"required,description=目标群成员QQ号"`
}

// groupSetCardArgs 是 group_set_card 工具的参数。
type groupSetCardArgs struct {
	UserID string `json:"user_id" jsonschema:"required,description=目标群成员QQ号"`
	Card   string `json:"card" jsonschema:"required,description=新的群名片内容"`
}

// GroupMute 返回 group_mute 工具：将某位群成员禁言指定时长（高危动作，B-015）。
func (g *GroupTools) GroupMute() tool.BaseTool {
	params, err := toolutils.GoStruct2ParamsOneOf[groupMuteArgs]()
	if err != nil {
		panic(err) // 参数结构体定义错误属编译期问题
	}
	return toolutils.NewTool(&schema.ToolInfo{
		Name:        "group_mute",
		Desc:        "将某位群成员禁言指定时长。禁言属高危动作，仅当成员明显违规（刷屏、辱骂等）时调用；本群未显式关闭群管理开关且你和 bot 都是群主/管理员，任一不满足调用失败。仅群聊可用。",
		ParamsOneOf: params,
	}, g.groupMute)
}

// GroupUnmute 返回 group_unmute 工具：解除某位群成员的禁言（高危动作，B-015）。
func (g *GroupTools) GroupUnmute() tool.BaseTool {
	params, err := toolutils.GoStruct2ParamsOneOf[groupUnmuteArgs]()
	if err != nil {
		panic(err)
	}
	return toolutils.NewTool(&schema.ToolInfo{
		Name:        "group_unmute",
		Desc:        "解除某位群成员的禁言。执行前校验本群未显式关闭群管理开关且你和 bot 都是群主/管理员，任一不满足调用失败。仅群聊可用。",
		ParamsOneOf: params,
	}, g.groupUnmute)
}

// GroupKick 返回 group_kick 工具：将某位群成员移出本群（最高危动作，B-015）。
func (g *GroupTools) GroupKick() tool.BaseTool {
	params, err := toolutils.GoStruct2ParamsOneOf[groupKickArgs]()
	if err != nil {
		panic(err)
	}
	return toolutils.NewTool(&schema.ToolInfo{
		Name:        "group_kick",
		Desc:        "将某位群成员移出本群。踢人属最高危动作，仅当成员严重违规（广告、人身攻击）时调用；本群未显式关闭群管理开关且你和 bot 都是群主/管理员，任一不满足调用失败。仅群聊可用。",
		ParamsOneOf: params,
	}, g.groupKick)
}

// GroupSetCard 返回 group_set_card 工具：修改某位群成员的群名片（高危动作，B-015）。
func (g *GroupTools) GroupSetCard() tool.BaseTool {
	params, err := toolutils.GoStruct2ParamsOneOf[groupSetCardArgs]()
	if err != nil {
		panic(err)
	}
	return toolutils.NewTool(&schema.ToolInfo{
		Name:        "group_set_card",
		Desc:        "修改某位群成员的群名片。仅当用户明确要求改名片时调用；本群未显式关闭群管理开关且你和 bot 都是群主/管理员，任一不满足调用失败。仅群聊可用。",
		ParamsOneOf: params,
	}, g.groupSetCard)
}

// groupManagerFrom 读取 ctx 内的群管理执行器；未注入时返回错误
// （护栏缺失宁可失败，不静默跳过）。
func groupManagerFrom(ctx context.Context) (domain.GroupManager, error) {
	gm, ok := domain.GroupManagerFrom(ctx)
	if !ok {
		return nil, errors.New("缺少群管理执行能力（GroupManager 未注入）")
	}
	return gm, nil
}

// groupMute 执行禁言：群聊守卫 → 取执行器 → 构造 GroupAction → Execute（护栏在内）。
func (g *GroupTools) groupMute(ctx context.Context, args groupMuteArgs) (string, error) {
	session, err := sessionFrom(ctx)
	if err != nil {
		return "", err
	}
	if session.GroupID == "" {
		return "", errors.New("仅群聊可执行群管理动作（当前会话无群 ID）")
	}
	gm, err := groupManagerFrom(ctx)
	if err != nil {
		return "", err
	}
	if err := gm.Execute(ctx, entity.GroupAction{Op: entity.GroupOpMute, Target: args.UserID, Duration: args.Duration}); err != nil {
		return "", fmt.Errorf("禁言失败: %w", err)
	}
	return fmt.Sprintf("已将 %s 禁言 %d 秒", args.UserID, args.Duration), nil
}

// groupUnmute 执行解除禁言。
func (g *GroupTools) groupUnmute(ctx context.Context, args groupUnmuteArgs) (string, error) {
	session, err := sessionFrom(ctx)
	if err != nil {
		return "", err
	}
	if session.GroupID == "" {
		return "", errors.New("仅群聊可执行群管理动作（当前会话无群 ID）")
	}
	gm, err := groupManagerFrom(ctx)
	if err != nil {
		return "", err
	}
	if err := gm.Execute(ctx, entity.GroupAction{Op: entity.GroupOpUnmute, Target: args.UserID}); err != nil {
		return "", fmt.Errorf("解除禁言失败: %w", err)
	}
	return fmt.Sprintf("已解除 %s 的禁言", args.UserID), nil
}

// groupKick 执行移出群聊。
func (g *GroupTools) groupKick(ctx context.Context, args groupKickArgs) (string, error) {
	session, err := sessionFrom(ctx)
	if err != nil {
		return "", err
	}
	if session.GroupID == "" {
		return "", errors.New("仅群聊可执行群管理动作（当前会话无群 ID）")
	}
	gm, err := groupManagerFrom(ctx)
	if err != nil {
		return "", err
	}
	if err := gm.Execute(ctx, entity.GroupAction{Op: entity.GroupOpKick, Target: args.UserID}); err != nil {
		return "", fmt.Errorf("踢人失败: %w", err)
	}
	return fmt.Sprintf("已将 %s 移出本群", args.UserID), nil
}

// groupSetCard 执行修改群名片。
func (g *GroupTools) groupSetCard(ctx context.Context, args groupSetCardArgs) (string, error) {
	session, err := sessionFrom(ctx)
	if err != nil {
		return "", err
	}
	if session.GroupID == "" {
		return "", errors.New("仅群聊可执行群管理动作（当前会话无群 ID）")
	}
	gm, err := groupManagerFrom(ctx)
	if err != nil {
		return "", err
	}
	if err := gm.Execute(ctx, entity.GroupAction{Op: entity.GroupOpSetCard, Target: args.UserID, Card: args.Card}); err != nil {
		return "", fmt.Errorf("修改名片失败: %w", err)
	}
	return fmt.Sprintf("已将 %s 的名片改为「%s」", args.UserID, args.Card), nil
}
