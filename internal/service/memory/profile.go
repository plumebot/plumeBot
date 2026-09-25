package memory

import (
	"context"
	"errors"
	"sync"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// ProfileCache 管理群画像的按需加载 + 内存缓存（架构 §4.3）。
// 群画像在群首次出现时加载一次，之后走内存不重复查库；私聊消息不参与。
// （成员画像 member_profile 已于 P3-005 移除，成员上下文由 member_facts 承担。）
type ProfileCache struct {
	store  domain.Storage
	mu     sync.Mutex
	groups map[string]*entity.GroupProfile // groupID -> 群画像（nil = 查询过但无画像）
}

// NewProfileCache 创建群画像缓存，注入 domain.Storage 用于按需加载。
func NewProfileCache(store domain.Storage) *ProfileCache {
	return &ProfileCache{
		store:  store,
		groups: make(map[string]*entity.GroupProfile),
	}
}

// TouchMessage 在每条群聊消息到达时加载群画像（群首次出现时加载一次）。私聊消息直接忽略。
func (p *ProfileCache) TouchMessage(ctx context.Context, msg entity.Message) {
	if msg.MessageType != "group" {
		return
	}
	gid := msg.GroupID

	p.mu.Lock()
	defer p.mu.Unlock()

	// 群画像：群首次出现时加载一次；加载失败（非 NotFound）不缓存，下条消息重试。
	if _, ok := p.groups[gid]; !ok {
		if prof, cached := p.loadGroupProfile(ctx, gid); cached {
			p.groups[gid] = prof
			logger.Debug("群画像已加载（缓存 miss）",
				logger.S("group_id", gid), logger.B("has_profile", prof != nil))
		}
	}
}

// GetGroupProfile 返回缓存的群画像；ok=false 表示尚未缓存。ok=true 时可能为 nil（已确认无画像）。
func (p *ProfileCache) GetGroupProfile(groupID string) (*entity.GroupProfile, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	prof, ok := p.groups[groupID]
	return prof, ok
}

// Invalidate 删除指定群的缓存条目（含「已确认无画像」的 nil 占位）。
// P7-001 管理面写/删 group_profile 后调用，下一条群消息触达时重新加载。
func (p *ProfileCache) Invalidate(groupID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.groups, groupID)
	logger.Debug("群画像缓存失效", logger.S("group_id", groupID))
}

// loadGroupProfile 查询群画像；确认无画像（ErrNotFound）返回 nil 并标记可缓存。
// 查询失败（非 ErrNotFound）不缓存，返回 cached=false 以便下条消息重试。
func (p *ProfileCache) loadGroupProfile(ctx context.Context, groupID string) (*entity.GroupProfile, bool) {
	prof, err := p.store.GetGroupProfile(ctx, groupID)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return nil, true
		}
		logger.Warn("加载群画像失败",
			logger.S("group_id", groupID), logger.Err(err))
		return nil, false
	}
	return prof, true
}
