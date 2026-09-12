package memory

import (
	"context"
	"testing"

	"plumebot/internal/domain/entity"
)

// groupMsgBy 构造带发送者的群聊消息。
func groupMsgBy(groupID, userID, content string) entity.Message {
	return entity.Message{GroupID: groupID, UserID: userID, MessageType: "group", Parts: textParts(content)}
}

func TestProfileCacheLoadsGroupOnFirstAppearance(t *testing.T) {
	store := &fakeStorage{}
	store.groupProf = map[string]*entity.GroupProfile{
		"g1": {GroupID: "g1", Culture: "轻松"},
	}
	p := NewProfileCache(store)

	p.TouchMessage(context.Background(), groupMsgBy("g1", "u1", "hi"))

	gp, ok := p.GetGroupProfile("g1")
	if !ok || gp == nil || gp.Culture != "轻松" {
		t.Errorf("群画像未加载: ok=%v gp=%+v", ok, gp)
	}
}

func TestProfileCacheNoReloadOnReappear(t *testing.T) {
	store := &fakeStorage{}
	store.groupProf = map[string]*entity.GroupProfile{"g1": {GroupID: "g1"}}
	p := NewProfileCache(store)

	p.TouchMessage(context.Background(), groupMsgBy("g1", "u1", "a"))
	p.TouchMessage(context.Background(), groupMsgBy("g1", "u2", "b"))

	if store.groupGets != 1 {
		t.Errorf("群画像应只查一次，实际查询 %d 次", store.groupGets)
	}
}

func TestProfileCacheCachesAbsentGroup(t *testing.T) {
	store := &fakeStorage{} // 无任何画像
	p := NewProfileCache(store)

	p.TouchMessage(context.Background(), groupMsgBy("g1", "u1", "a"))
	p.TouchMessage(context.Background(), groupMsgBy("g1", "u2", "b"))

	if store.groupGets != 1 {
		t.Errorf("确认无画像后不应重复查库，实际查询 %d 次", store.groupGets)
	}
	gp, ok := p.GetGroupProfile("g1")
	if !ok || gp != nil {
		t.Errorf("应缓存为「无画像」: ok=%v gp=%+v", ok, gp)
	}
}

func TestProfileCachePrivateMessageIgnored(t *testing.T) {
	store := &fakeStorage{}
	p := NewProfileCache(store)

	p.TouchMessage(context.Background(), entity.Message{UserID: "u1", MessageType: "private", Parts: textParts("hi")})

	if store.groupGets != 0 {
		t.Errorf("私聊消息不应触发群画像加载: group=%d", store.groupGets)
	}
}

// TestProfileCacheInvalidate 校验 P7-001 管理面缓存失效：Invalidate 后下一条群消息触发重新加载
//（groupGets 增长，且读到的是新值）。
func TestProfileCacheInvalidate(t *testing.T) {
	store := &fakeStorage{}
	store.groupProf = map[string]*entity.GroupProfile{"g1": {GroupID: "g1", Culture: "旧"}}
	p := NewProfileCache(store)

	p.TouchMessage(context.Background(), groupMsgBy("g1", "u1", "a"))
	if _, ok := p.GetGroupProfile("g1"); !ok {
		t.Fatal("首条消息应加载群画像")
	}

	// 管理面改库 + 失效缓存。
	store.groupProf["g1"] = &entity.GroupProfile{GroupID: "g1", Culture: "新"}
	p.Invalidate("g1")
	if _, ok := p.GetGroupProfile("g1"); ok {
		t.Fatal("Invalidate 后应无缓存")
	}

	// 下一条群消息重新加载新值。
	p.TouchMessage(context.Background(), groupMsgBy("g1", "u2", "b"))
	gp, ok := p.GetGroupProfile("g1")
	if !ok || gp.Culture != "新" {
		t.Fatalf("失效后应重新加载新值: ok=%v gp=%+v", ok, gp)
	}
	if store.groupGets != 2 {
		t.Fatalf("失效后应触发再次查库, 实际 %d 次", store.groupGets)
	}
}
