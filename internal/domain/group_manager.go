package domain

import (
	"context"

	"plumebot/internal/domain/entity"
)

// GroupManager 是事件级群管理动作执行能力（B-015）。
// 与 Sender 分离：动作权限不注入整条消息链（禁言等为高危能力），仅暴露给工具层
// 与插件 Actions 执行路径（dispatchCommand），二者共用本接口唯一入口 Execute。
// 由连接层（infra/onebot）在 matcher 闭包构造 per-event 实例（持有 *zero.Ctx，
// 动作绑定当次事件）并经 WithGroupManager 注入 ctx；护栏（per-group 开关 +
// 管理员校验）统一在实现内把关，调用方不重复校验。
type GroupManager interface {
	Execute(ctx context.Context, action entity.GroupAction) error
}

// groupManagerCtxKey 是 GroupManager 在 context 中的键类型（私有类型，避免与其他包键冲突）。
type groupManagerCtxKey struct{}

// WithGroupManager 把事件级 GroupManager 注入 context（matcher 闭包调用，与 WithSender 同构）。
func WithGroupManager(ctx context.Context, gm GroupManager) context.Context {
	return context.WithValue(ctx, groupManagerCtxKey{}, gm)
}

// GroupManagerFrom 从 context 取出 GroupManager。未注入（ok=false）时调用方应告警跳过
//（不报错阻断管线）；工具场景视为护栏缺失宁可失败，不静默跳过。
func GroupManagerFrom(ctx context.Context) (GroupManager, bool) {
	gm, ok := ctx.Value(groupManagerCtxKey{}).(GroupManager)
	return gm, ok
}
