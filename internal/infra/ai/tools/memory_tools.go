// Package tools 提供 Agent 可调用的记忆更新工具（P3-004 记忆更新闭环）。
//
// 设计原则（与用户讨论定案）：
//   - 读在组装、写在 tool：事实/黑话的读取在 P6-001 prompt 组装时现查注入，
//     这里只实现写入（store_fact/forget_fact）与学习（learn_jargon），不做查询型 tool；
//   - 无界写入、有界注入：本层写入不做数量上限（小行 + 有索引，与 messages 全量落库同性质），
//     注入 prompt 的上限归 P6-001 消费方决定；
//   - 会话身份走 ctx：工具为共享单例，群/用户经 entity.Session 注入 Generate 的 ctx
//     贯穿 eino 到 InvokableRun，避免构造期绑定或每消息重建 agent。
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

// MemoryTools 是记忆更新工具的构造集合，持有 domain.Storage 用于写 SQLite。
// 同一实例可并发使用（database/sql 并发安全），由 main.go 注册进 ai.ToolsRegistry。
type MemoryTools struct {
	store domain.Storage
}

// NewMemoryTools 创建记忆工具集，注入 domain.Storage。
func NewMemoryTools(store domain.Storage) *MemoryTools {
	return &MemoryTools{store: store}
}

// storeFactArgs 是 store_fact 工具的参数。
type storeFactArgs struct {
	UserID string `json:"user_id" jsonschema:"description=目标用户QQ号；缺省为当前说话人"`
	Fact   string `json:"fact" jsonschema:"required,description=要记住的事实，一句完整的陈述"`
}

// learnJargonArgs 是 learn_jargon 工具的参数。
type learnJargonArgs struct {
	Jargon string `json:"jargon" jsonschema:"required,description=新学到的群黑话/梗"`
}

// forgetFactArgs 是 forget_fact 工具的参数。
type forgetFactArgs struct {
	UserID string `json:"user_id" jsonschema:"description=目标用户QQ号；缺省为当前说话人"`
	Fact   string `json:"fact" jsonschema:"required,description=要删除的事实，与存储时保持一致"`
}

// StoreFact 返回 store_fact 工具：记住一条关于某群成员的长期事实（member_facts）。
// 完全相同的事实幂等去重；发现旧事实过时先用 forget_fact 删除、再 store_fact 存新（覆盖语义）。
func (m *MemoryTools) StoreFact() tool.BaseTool {
	params, err := toolutils.GoStruct2ParamsOneOf[storeFactArgs]()
	if err != nil {
		panic(err) // 参数结构体定义错误属编译期问题
	}
	return toolutils.NewTool(&schema.ToolInfo{
		Name:        "store_fact",
		Desc:        "记住一条关于某位群成员的长期事实（如爱好、习惯、个人信息），写入该成员的个人记忆。重复的相同事实会被自动去重；若已有旧事实需要更正，请先用 forget_fact 删除旧事实再调用本工具。",
		ParamsOneOf: params,
	}, m.storeFact)
}

// LearnJargon 返回 learn_jargon 工具：学习一条群黑话/梗（group_jargon，状态 pending 待确认）。
func (m *MemoryTools) LearnJargon() tool.BaseTool {
	params, err := toolutils.GoStruct2ParamsOneOf[learnJargonArgs]()
	if err != nil {
		panic(err)
	}
	return toolutils.NewTool(&schema.ToolInfo{
		Name:        "learn_jargon",
		Desc:        "学习一条群聊特有的黑话、梗或专用称呼，写入群黑话词典（状态为待确认，经人工审核后生效）。仅在群聊中调用。",
		ParamsOneOf: params,
	}, m.learnJargon)
}

// ForgetFact 返回 forget_fact 工具：删除一条关于某群成员的已存事实（member_facts）。
func (m *MemoryTools) ForgetFact() tool.BaseTool {
	params, err := toolutils.GoStruct2ParamsOneOf[forgetFactArgs]()
	if err != nil {
		panic(err)
	}
	return toolutils.NewTool(&schema.ToolInfo{
		Name:        "forget_fact",
		Desc:        "删除一条之前记住的关于某群成员的事实（事实过期、被更正或不再适用时调用）。",
		ParamsOneOf: params,
	}, m.forgetFact)
}

// sessionFrom 读取会话身份；未注入时返回错误（提示模型无法确定记忆归属，而非写入错误归属）。
func sessionFrom(ctx context.Context) (entity.Session, error) {
	session, ok := entity.SessionFrom(ctx)
	if !ok {
		return entity.Session{}, errors.New("缺少会话上下文（group_id/user_id），无法写入记忆")
	}
	return session, nil
}

// storeFact 执行事实写入。
func (m *MemoryTools) storeFact(ctx context.Context, args storeFactArgs) (string, error) {
	session, err := sessionFrom(ctx)
	if err != nil {
		return "", err
	}
	uid := args.UserID
	if uid == "" {
		uid = session.UserID
	}
	if err := m.store.AddMemberFact(ctx, session.GroupID, uid, args.Fact); err != nil {
		return "", fmt.Errorf("写入事实失败: %w", err)
	}
	return fmt.Sprintf("已记住：%s 的事实「%s」", uid, args.Fact), nil
}

// learnJargon 执行黑话学习（仅群聊；写入状态为 pending 待确认）。
func (m *MemoryTools) learnJargon(ctx context.Context, args learnJargonArgs) (string, error) {
	session, err := sessionFrom(ctx)
	if err != nil {
		return "", err
	}
	if session.GroupID == "" {
		return "", errors.New("仅群聊可学习黑话（当前会话无群 ID）")
	}
	if err := m.store.AddJargon(ctx, session.GroupID, args.Jargon); err != nil {
		return "", fmt.Errorf("学习黑话失败: %w", err)
	}
	return fmt.Sprintf("已学习黑话「%s」，状态：待确认", args.Jargon), nil
}

// forgetFact 执行事实删除。
func (m *MemoryTools) forgetFact(ctx context.Context, args forgetFactArgs) (string, error) {
	session, err := sessionFrom(ctx)
	if err != nil {
		return "", err
	}
	uid := args.UserID
	if uid == "" {
		uid = session.UserID
	}
	if err := m.store.DeleteMemberFact(ctx, session.GroupID, uid, args.Fact); err != nil {
		return "", fmt.Errorf("删除事实失败: %w", err)
	}
	return fmt.Sprintf("已删除事实「%s」", args.Fact), nil
}
