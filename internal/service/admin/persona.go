package admin

// persona 域：人格模板（改即生效——组装每次现查 persona 表，无需缓存失效，见计划书 §2.3）。

import (
	"context"

	"plumebot/internal/domain/entity"
)

// ListPersonas 列出全部人格模板。
func (s *Service) ListPersonas(ctx context.Context) ([]entity.Persona, error) {
	return s.store.ListPersonas(ctx)
}

// GetPersonaByAgent 按 agent 查询人格模板；不存在时返回 domain.ErrNotFound。
func (s *Service) GetPersonaByAgent(ctx context.Context, agent string) (*entity.Persona, error) {
	return s.store.GetPersonaByAgent(ctx, agent)
}

// UpsertPersona 按 agent 幂等写人格模板；返回校验后的最新模板。
func (s *Service) UpsertPersona(ctx context.Context, by string, p entity.Persona) (*entity.Persona, error) {
	if err := validatePersona(&p); err != nil {
		return nil, err
	}
	if err := s.store.UpsertPersona(ctx, p); err != nil {
		return nil, err
	}
	s.audit(ctx, by, "persona", p.Agent)
	return &p, nil
}