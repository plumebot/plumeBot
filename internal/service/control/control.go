package control

import (
	"context"
	"errors"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
)

// 触发模式合法值（service 侧唯一；空/未知在归一化时兜底 mention，B-005）。
const (
	ModeMention = "mention"
	ModeAuto    = "auto"
)

var _ domain.Control = (*ControlService)(nil)

// ControlService 负责触发判断（P5-001：仅 mention/auto 模式判定，无状态规则；
// 精力/冷却/时段/短消息等状态规则归 P5-002）。
type ControlService struct {
	defaultMode string         // 全局默认模式（cfg.Control.Mode；空 → mention 兜底）
	store       domain.Storage // 读 per-group group_config（不触碰 bot_state，状态规则归 P5-002）
}

// NewControlService 创建 ControlService。
// mode 解析优先级：per-group group_config.mode（非空）→ 全局 cfg.Control.Mode → "mention"。
func NewControlService(cfg config.ControlConfig, store domain.Storage) *ControlService {
	return &ControlService{defaultMode: cfg.Mode, store: store}
}

// ShouldReply 判断 bot 在当前消息下是否应回复（P5-001 仅 mention/auto 模式判定）。
// auto 模式：任何消息均预检通过（自主回复与否由后续 Agent 判断，P6-002）。
// mention 模式：仅被 @ 或私聊回复（消息始终旁听缓存，触发只控制回复，架构 §9.1）。
func (s *ControlService) ShouldReply(ctx context.Context, msg entity.Message) (entity.Decision, error) {
	mode, err := s.resolveMode(ctx, msg.GroupID)
	if err != nil {
		return entity.Decision{}, err
	}
	switch mode {
	case ModeAuto:
		return entity.Decision{Reply: true, Reason: entity.DecisionReasonAutoPass}, nil
	default: // mention（含空/未知兜底，fail-closed）
		if msg.Mentioned {
			return entity.Decision{Reply: true, Reason: entity.DecisionReasonMentionForced}, nil
		}
		return entity.Decision{Reply: false, Reason: entity.DecisionReasonNotTriggered}, nil
	}
}

// OnReplied 回复后回调。P5-001 空实现；P5-002 在此维护 bot_state 精力/冷却规则。
func (s *ControlService) OnReplied(_ context.Context, _ entity.Message) error { return nil }

// resolveMode 解析消息应使用的触发模式。私聊 GroupID 为空 → 无 per-group 配置，直接落全局。
func (s *ControlService) resolveMode(ctx context.Context, groupID string) (string, error) {
	if groupID != "" {
		gc, err := s.store.GetGroupConfig(ctx, groupID)
		switch {
		case err == nil && gc.Mode != "":
			return normalizeMode(gc.Mode), nil
		case err == nil:
			// per-group 配置存在但 mode 为空 → 落全局
		case errors.Is(err, domain.ErrNotFound):
			// 无 per-group 配置 → 落全局
		default:
			return "", err
		}
	}
	return normalizeMode(s.defaultMode), nil
}

// normalizeMode 归一化模式：仅 "auto" 为 auto；空/未知 → mention（fail-closed，B-005）。
func normalizeMode(mode string) string {
	if mode == ModeAuto {
		return ModeAuto
	}
	return ModeMention
}
