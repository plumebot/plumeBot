package event

import (
	"reflect"
	"strings"
	"testing"

	"plumebot/internal/domain/entity"
)

// TestParseCommand 表驱动：命令解析（/cmd arg1 arg2 → cmd + args）。
func TestParseCommand(t *testing.T) {
	tests := []struct {
		name    string
		content string
		cmd     string
		args    []string
		ok      bool
	}{
		{"普通消息", "今天天气不错", "", nil, false},
		{"仅斜杠", "/", "", nil, false},
		{"斜杠加空格", "/ ", "", nil, false},
		{"仅命令", "/echo", "echo", nil, true},
		{"命令加参数", "/echo 你好 世界", "echo", []string{"你好", "世界"}, true},
		{"多空格", "/weather   北京  上海", "weather", []string{"北京", "上海"}, true},
		{"前导空格", "  /ping", "ping", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args, ok := parseCommand(tt.content)
			if ok != tt.ok || cmd != tt.cmd || !reflect.DeepEqual(args, tt.args) {
				t.Fatalf("parseCommand(%q) = (%q, %v, %v)，期望 (%q, %v, %v)",
					tt.content, cmd, args, ok, tt.cmd, tt.args, tt.ok)
			}
		})
	}
}

// helpMsg 构造 /help 命令消息。
func helpMsg(content string) entity.Message {
	return entity.Message{
		MessageID:   "m-help",
		GroupID:     "g1",
		UserID:      "u1",
		MessageType: "group",
		Parts:       []entity.ContentPart{{Type: entity.PartTypeText, Text: content}},
	}
}

// TestDispatchHelpListsPlugins /help 应 handled、经 Sender 发送「可用插件」文本。
func TestDispatchHelpListsPlugins(t *testing.T) {
	f := newTailFixture()
	handled, err := f.svc.dispatchCommand(withSender(f.sender), helpMsg("/help"))
	if err != nil {
		t.Fatalf("dispatchCommand 不应报错: %v", err)
	}
	if !handled {
		t.Fatal("/help 应 handled")
	}
	if f.sender.calls != 1 {
		t.Fatalf("应发送 1 次回复, 实际 %d", f.sender.calls)
	}
	if got := f.sender.last.Segments[0].Text; !strings.Contains(got, "可用插件") {
		t.Fatalf("help 文本缺少「可用插件」: %q", got)
	}
}

// TestDispatchHelpUnknownPlugin /help <未知> 应返回未找到提示。
func TestDispatchHelpUnknownPlugin(t *testing.T) {
	f := newTailFixture()
	handled, err := f.svc.dispatchCommand(withSender(f.sender), helpMsg("/help nope"))
	if err != nil {
		t.Fatalf("dispatchCommand 不应报错: %v", err)
	}
	if !handled {
		t.Fatal("/help nope 应 handled")
	}
	if got := f.sender.last.Segments[0].Text; !strings.Contains(got, "未找到插件：nope") {
		t.Fatalf("未知插件提示错误: %q", got)
	}
}

// TestTailHelpBypassesJudge /help 走 tail 时应短路触发判断（不调 respond）。
func TestTailHelpBypassesJudge(t *testing.T) {
	f := newTailFixture()
	f.control.dec = entity.Decision{Reply: true}
	if err := f.svc.tail(withSender(f.sender), helpMsg("/help")); err != nil {
		t.Fatalf("tail 不应报错: %v", err)
	}
	if f.control.calls != 0 {
		t.Fatalf("/help 不应触发触发判断, 实际 %d", f.control.calls)
	}
	if f.sender.calls != 1 {
		t.Fatalf("/help 应发送帮助文本, 实际 %d", f.sender.calls)
	}
}
