package admin

// 会话窗口域单测（P7-003）：注入假窗口读取器，断言概览组装与消息展示视图转换。
// Service 直构（store/mgr 本组方法不消费，传 nil/接口假实现即可，与既有 service_test 同风格）。

import (
	"context"
	"sort"
	"testing"

	"plumebot/internal/domain/entity"
)

// fakeSessionMem 实现 domain.SessionWindowReader（GetWindow/ListSessions），并补 InvalidateGroupProfile
// 同时满足 domain.GroupProfileInvalidator（Service 两个消费面共用注入面）。
type fakeSessionMem struct {
	sessions map[string][]entity.Message
}

func newFakeSessionMem() *fakeSessionMem {
	return &fakeSessionMem{sessions: map[string][]entity.Message{}}
}

func (f *fakeSessionMem) InvalidateGroupProfile(string) {}

func (f *fakeSessionMem) GetWindow(_ context.Context, key string) ([]entity.Message, error) {
	return f.sessions[key], nil
}

func (f *fakeSessionMem) ListSessions() []string {
	keys := make([]string, 0, len(f.sessions))
	for k := range f.sessions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func textMsg(groupID, userID, senderName, content string) entity.Message {
	return entity.Message{
		GroupID: groupID, UserID: userID, SenderName: senderName, MessageType: "group",
		Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: content}},
	}
}

func TestListSessionsOverviews(t *testing.T) {
	fk := newFakeSessionMem()
	fk.sessions["g1"] = []entity.Message{
		textMsg("g1", "u1", "小明", "早"),
		{MessageID: "self:1", GroupID: "g1", UserID: "bot", SenderName: "PlumeBot",
			MessageType: "group", Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: "你好"}}, Timestamp: 100},
	}
	fk.sessions["private:u2"] = []entity.Message{
		{UserID: "u2", MessageType: "private",
			Parts: []entity.ContentPart{{Type: entity.PartTypeText, Text: "hi"}}, Timestamp: 200},
	}
	svc := &Service{win: fk}

	ovs := svc.ListSessions(context.Background())
	if len(ovs) != 2 || ovs[0].Key != "g1" || ovs[1].Key != "private:u2" {
		t.Fatalf("概览应按键升序输出 2 个, 实际 %+v", ovs)
	}
	if ovs[0].Count != 2 || ovs[0].LastTS != 100 || ovs[0].LastRender != "你好" {
		t.Fatalf("g1 概览字段错误: %+v", ovs[0])
	}
	if ovs[1].Count != 1 || ovs[1].LastTS != 200 || ovs[1].LastRender != "hi" {
		t.Fatalf("private 概览字段错误: %+v", ovs[1])
	}
}

func TestGetSessionWindowView(t *testing.T) {
	fk := newFakeSessionMem()
	fk.sessions["g1"] = []entity.Message{
		textMsg("g1", "111", "小明", "早"),
		{MessageID: "self:9", GroupID: "g1", UserID: "999", SenderName: "PlumeBot",
			MessageType: "group", Timestamp: 100,
			Parts: []entity.ContentPart{{Type: entity.PartTypeImage, Description: "一只橘猫在窗台"}}},
		{MessageID: "m3", GroupID: "g1", UserID: "222", SenderName: "小红",
			MessageType: "group", Timestamp: 101,
			Parts: []entity.ContentPart{{Type: entity.PartTypeImage}}},
	}
	svc := &Service{win: fk}

	msgs := svc.GetSessionWindow(context.Background(), "g1")
	if len(msgs) != 3 {
		t.Fatalf("应返回 3 条, 实际 %d", len(msgs))
	}
	if msgs[0].Self || msgs[0].Sender != "小明" || msgs[0].Render != "早" {
		t.Fatalf("首条（非 bot）视图错误: %+v", msgs[0])
	}
	// bot 回复：self 前缀 → is_self=true，SenderName=botName；已回填描述的图片露出描述。
	if !msgs[1].Self || msgs[1].Sender != "PlumeBot" || msgs[1].Render != "（图片：一只橘猫在窗台）" {
		t.Fatalf("bot 回复（含描述）视图错误: %+v", msgs[1])
	}
	// 未描述图片（从未被组装触发）回落 [图片] 占位。
	if msgs[2].Self || msgs[2].Render != "[图片]" {
		t.Fatalf("未描述图片应回落 [图片], 实际 %+v", msgs[2])
	}
}

func TestGetSessionWindowEmptyAndFallback(t *testing.T) {
	fk := newFakeSessionMem()
	svc := &Service{win: fk}

	// 未知会话 → 空切片（非错误）。
	if msgs := svc.GetSessionWindow(context.Background(), "nosuch"); len(msgs) != 0 {
		t.Fatalf("未知会话应返回空, 实际 %+v", msgs)
	}
	// SenderName 为空回落 QQ 号。
	fk.sessions["g1"] = []entity.Message{textMsg("g1", "123456", "", "hi")}
	if msgs := svc.GetSessionWindow(context.Background(), "g1"); msgs[0].Sender != "123456" {
		t.Fatalf("SenderName 空应回落 QQ 号, 实际 %q", msgs[0].Sender)
	}
	// 会话存在但窗口空 → 空切片。
	fk.sessions["g2"] = []entity.Message{}
	if msgs := svc.GetSessionWindow(context.Background(), "g2"); len(msgs) != 0 {
		t.Fatalf("空窗口应返回空, 实际 %+v", msgs)
	}
}