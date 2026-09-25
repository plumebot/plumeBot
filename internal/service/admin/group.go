package admin

// group_config 域：每群触发/状态配置（读即生效；0/空列 = 走全局，见计划书 §7.2）。

import (
	"context"

	"plumebot/internal/domain/entity"
)

// ListGroupConfigs 列出全部已配置群的静态配置。
func (s *Service) ListGroupConfigs(ctx context.Context) ([]entity.GroupConfig, error) {
	return s.store.ListGroupConfigs(ctx)
}

// GetGroupConfig 查询单群配置；不存在时返回 entity.ErrNotFound。
func (s *Service) GetGroupConfig(ctx context.Context, groupID string) (*entity.GroupConfig, error) {
	return s.store.GetGroupConfig(ctx, groupID)
}

// UpsertGroupConfig 整行 upsert 单群配置（校验通过才写库）；返回校验后的最新配置。
func (s *Service) UpsertGroupConfig(ctx context.Context, by string, cfg entity.GroupConfig) (*entity.GroupConfig, error) {
	if err := validateGroupConfig(&cfg); err != nil {
		return nil, err
	}
	if err := s.store.UpsertGroupConfig(ctx, cfg); err != nil {
		return nil, err
	}
	s.audit(ctx, by, "group_config", cfg.GroupID)
	return &cfg, nil
}

// DeleteGroupConfig 删除单群配置（恢复全局兜底）；不存在时返回 entity.ErrNotFound。
func (s *Service) DeleteGroupConfig(ctx context.Context, by, groupID string) error {
	if err := s.store.DeleteGroupConfig(ctx, groupID); err != nil {
		return err
	}
	s.audit(ctx, by, "group_config", groupID)
	return nil
}