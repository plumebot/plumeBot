package plugin

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/plumebot/plugin-sdk/entity"
)

// echoImpl 是测试插件实现：回显命令名。
type echoImpl struct{}

func (e *echoImpl) Execute(_ context.Context, req entity.PluginRequest) (entity.PluginResult, error) {
	return entity.PluginResult{
		Reply: &entity.Reply{Segments: []entity.Segment{{Kind: entity.SegmentKindText, Text: "echo: " + req.Command}}},
	}, nil
}

// TestHelperProcess 不是普通测试：GO_WANT_HELPER_PROCESS=1 时以插件身份 Serve 并阻塞。
// 由 TestRoundTrip 以子进程方式（-test.run=TestHelperProcess）拉起。
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	Serve(&echoImpl{})
	os.Exit(0)
}

// TestRoundTrip 验证 go-plugin net/rpc 往返：host 拉起插件子进程 → Execute → 结果经 gob 还原。
func TestRoundTrip(t *testing.T) {
	os.Setenv("GO_WANT_HELPER_PROCESS", "1")
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	client, err := newClient(cmd)
	if err != nil {
		t.Fatalf("拉起插件失败: %v", err)
	}
	defer client.Close()

	res, err := client.Execute(context.Background(), entity.PluginRequest{Proto: 1, Command: "ping"})
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if res.Reply == nil || len(res.Reply.Segments) != 1 || res.Reply.Segments[0].Text != "echo: ping" {
		t.Fatalf("返回结果不符合预期: %+v", res)
	}
}
