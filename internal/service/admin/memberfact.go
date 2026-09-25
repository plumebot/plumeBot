package admin

// member_facts 域：成员事实（管理面纠错用；正常写者仍是 Agent 的 store_fact 工具）。

import (
	"context"
	"slices"
	"strings"

	"plumebot/internal/domain/entity"
)

// ListMemberFacts 列出指定群/用户的事实（私聊 group_id 空 = 该用户私聊事实）。
func (s *Service) ListMemberFacts(ctx context.Context, groupID, userID string) ([]string, error) {
	return s.store.ListMemberFacts(ctx, groupID, userID)
}

// AddMemberFact 管理面补记/纠错一条事实。
func (s *Service) AddMemberFact(ctx context.Context, by, groupID, userID, fact string) error {
	if err := validateMemberFact(userID, fact); err != nil {
		return err
	}
	fact = strings.TrimSpace(fact)
	if err := s.store.AddMemberFact(ctx, groupID, userID, fact); err != nil {
		return err
	}
	s.audit(ctx, by, "member_facts", groupID+"/"+userID+"/"+fact)
	return nil
}

// DeleteMemberFact 删除单条事实（纠错主场景）；先查证存在（决策 D7），不存在返回 entity.ErrNotFound。
func (s *Service) DeleteMemberFact(ctx context.Context, by, groupID, userID, fact string) error {
	items, err := s.store.ListMemberFacts(ctx, groupID, userID)
	if err != nil {
		return err
	}
	if !slices.Contains(items, fact) {
		return entity.ErrNotFound
	}
	if err := s.store.DeleteMemberFact(ctx, groupID, userID, fact); err != nil {
		return err
	}
	s.audit(ctx, by, "member_facts", groupID+"/"+userID+"/"+fact)
	return nil
}