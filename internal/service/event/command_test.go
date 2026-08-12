package event

import (
	"reflect"
	"testing"
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
