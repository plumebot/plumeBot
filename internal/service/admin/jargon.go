package admin

// group_jargon 域：黑话审核流（管理面添加默认 confirmed；pending 待人工审核，confirmed 才进 prompt）。

import (
	"context"
	"errors"

	"plumebot/internal/domain/entity"
)

// ListJargonWithStatus 列黑话；status 为空或 all 时返回全部，否则过滤 pending/confirmed。
func (s *Service) ListJargonWithStatus(ctx context.Context, groupID, status string) ([]entity.Jargon, error) {
	items, err := s.store.ListJargonWithStatus(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if status == "" || status == "all" {
		return items, nil
	}
	out := items[:0]
	for _, it := range items {
		if it.Status == status {
			out = append(out, it)
		}
	}
	return out, nil
}

// AddJargon 管理面添加黑话（定案 F：默认 confirmed——人工添加即人工认可，下条消息注入 prompt）。
// 复用现有 AddJargon（新行 pending）+ ConfirmJargon（置 confirmed）组合，不新增 Storage 方法。
func (s *Service) AddJargon(ctx context.Context, by, groupID, jargon string) (*entity.Jargon, error) {
	if err := validateJargon(jargon); err != nil {
		return nil, err
	}
	items, err := s.store.ListJargonWithStatus(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if len(items) >= maxJargonPerGroup {
		return nil, entity.ValidationErrorf("该群黑话已达上限 %d 条", maxJargonPerGroup)
	}
	if err := s.store.AddJargon(ctx, groupID, jargon); err != nil {
		return nil, err
	}
	// 已是 confirmed 的重复添加 → ConfirmJargon 影响 0 行 → ErrNotFound，视为成功（决策 D8）。
	if err := s.store.ConfirmJargon(ctx, groupID, jargon); err != nil && !errors.Is(err, entity.ErrNotFound) {
		return nil, err
	}
	s.audit(ctx, by, "group_jargon", groupID+"/"+jargon)
	return &entity.Jargon{GroupID: groupID, Jargon: jargon, Status: "confirmed"}, nil
}

// DeleteJargon 删除黑话；先查证存在（决策 D7：不改共享幂等删语义，避免 Agent 的「删不存在」变错误），
// 不存在返回 entity.ErrNotFound。
func (s *Service) DeleteJargon(ctx context.Context, by, groupID, jargon string) error {
	items, err := s.store.ListJargonWithStatus(ctx, groupID)
	if err != nil {
		return err
	}
	if !containsJargon(items, jargon) {
		return entity.ErrNotFound
	}
	if err := s.store.DeleteJargon(ctx, groupID, jargon); err != nil {
		return err
	}
	s.audit(ctx, by, "group_jargon", groupID+"/"+jargon)
	return nil
}

// ConfirmJargon 审核确认 pending → confirmed；黑话不存在时返回 entity.ErrNotFound。
func (s *Service) ConfirmJargon(ctx context.Context, by, groupID, jargon string) (*entity.Jargon, error) {
	if err := s.store.ConfirmJargon(ctx, groupID, jargon); err != nil {
		return nil, err
	}
	s.audit(ctx, by, "group_jargon", groupID+"/"+jargon)
	return &entity.Jargon{GroupID: groupID, Jargon: jargon, Status: "confirmed"}, nil
}

// containsJargon 判断列表是否包含指定黑话。
func containsJargon(items []entity.Jargon, jargon string) bool {
	for _, it := range items {
		if it.Jargon == jargon {
			return true
		}
	}
	return false
}