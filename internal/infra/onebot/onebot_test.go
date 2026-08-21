package onebot

import (
	"testing"

	"plumebot/pkg/config"
)

// New 只存储 handler，不解析事件，因此可用 nil 注入测试构造期行为。
func TestNewDefaultsWsURL(t *testing.T) {
	c := New(config.OnebotConfig{}, "info", nil, nil, nil, nil, "")
	if c.cfg.WsURL != config.DefaultWsURL {
		t.Errorf("WsURL 空值应回落为 %q，实际 %q", config.DefaultWsURL, c.cfg.WsURL)
	}
}

func TestNewPreservesWsURL(t *testing.T) {
	c := New(config.OnebotConfig{WsURL: "ws://example.com:8080"}, "info", nil, nil, nil, nil, "")
	if c.cfg.WsURL != "ws://example.com:8080" {
		t.Errorf("WsURL 合法值被改写: %q", c.cfg.WsURL)
	}
}
