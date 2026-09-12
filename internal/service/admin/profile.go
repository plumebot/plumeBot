package admin

// group_profile 域：群画像（有内存缓存 ProfileCache 且无淘汰出口——
// 写/删后必须经 mem 失效缓存，否则 prompt 仍走旧画像，计划书 §2.3/§7.4）。

import (
	"context"

	"plumebot/internal/domain/entity"
)

// GetGroupProfile 查询群画像；不存在时返回 domain.ErrNotFound。
func (s *Service) GetGroupProfile(ctx context.Context, groupID string) (*entity.GroupProfile, error) {
	return s.store.GetGroupProfile(ctx, groupID)
}

// UpsertGroupProfile 写群画像并失效内存缓存（先写库成功再失效）。
func (s *Service) UpsertGroupProfile(ctx context.Context, by string, p entity.GroupProfile) (*entity.GroupProfile, error) {
	if err := validateGroupProfile(&p); err != nil {
		return nil, err
	}
	if err := s.store.UpsertGroupProfile(ctx, p); err != nil {
		return nil, err
	}
	s.mem.InvalidateGroupProfile(p.GroupID) // 决策 D9：写库成功才失效
	s.audit(by, "group_profile", p.GroupID)
	return &p, nil
}

// DeleteGroupProfile 删除群画像（恢复无画像态）并失效内存缓存；不存在时返回 domain.ErrNotFound。
func (s *Service) DeleteGroupProfile(ctx context.Context, by, groupID string) error {
	if err := s.store.DeleteGroupProfile(ctx, groupID); err != nil {
		return err
	}
	s.mem.InvalidateGroupProfile(groupID)
	s.audit(by, "group_profile", groupID)
	return nil
}