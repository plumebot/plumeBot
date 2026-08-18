// Package plugin 是子进程 stdio 插件机制（P4-002 主方案，go-plugin net/rpc 变体）的 SDK。
//
// 宿主与插件**共用**本包：插件侧调用 Serve 把自己注册为插件服务端；宿主侧调用
// NewClient 拉起插件进程并得到可调用的客户端。协议 wire 类型定义在
// github.com/plumebot/plugin-sdk/entity（单一事实来源，见架构 §8.6），本包只做
// go-plugin net/rpc 的接口映射 shim（net/rpc 无接口生成工具，方法签名为
// (args, *reply) error 且无 ctx，需手写翻译层——方案甲）。
//
// 第三方插件只需依赖本 module（entity + plugin），无需 import 宿主 internal 包；
// 宿主在其 internal/domain/entity 以类型别名复用协议类型，gob 类型名保持一致。
package plugin

import (
	"context"
	"fmt"
	"net/rpc"
	"os/exec"
	"time"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/plumebot/plugin-sdk/entity"
)

// pluginName 是 go-plugin 插件注册名。go-plugin 会把 RPC 服务端注册为固定名 "Plugin"，
// 客户端统一调用 "Plugin.<Method>"。
const pluginName = "pb"

// Handshake 是宿主与插件之间的握手配置（防误执行非插件程序；UX 特性非安全特性）。
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "PLUMEBOT_PLUGIN",
	MagicCookieValue: "pb1",
}

// defaultCallTimeout 是单次插件调用默认超时（net/rpc 无内置超时，宿主侧兜底）。
// 5s → 30s（csstats 插件真实 HTTP 查询 + LLM 点评需要；超时宿主不返回任何回复）。
// 宿主 B-019 per-plugin 超时策略仍后置。
const defaultCallTimeout = 30 * time.Second

// Plugin 是插件需实现的协议接口：收到一次命令调用，返回指令集（Reply + Actions）。
// 签名与宿主 domain.Plugin 一致（宿主 entity 为 SDK entity 的类型别名），
// 宿主侧 go-plugin 客户端代理（Client/RPCClient）结构化满足本接口与宿主 domain.Plugin。
type Plugin interface {
	Execute(ctx context.Context, req entity.PluginRequest) (entity.PluginResult, error)
}

// API 实现 go-plugin 的 goplugin.Plugin 接口（net/rpc 变体）。
// 插件侧 Impl 为真实实现；宿主侧 Impl 为空，仅用于 Dispense 出客户端代理。
type API struct {
	Impl Plugin
}

// Server 返回 RPC 服务端翻译层：把 net/rpc 调用翻译成真实方法。
func (a *API) Server(*goplugin.MuxBroker) (interface{}, error) {
	return &RPCServer{Impl: a.Impl}, nil
}

// Client 返回 RPC 客户端代理：把惯用签名调用包装成 net/rpc 消息。
func (a *API) Client(_ *goplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &RPCClient{client: c}, nil
}

// RPCServer 是 RPC 服务端翻译层（net/rpc 方法签名，无 ctx）。
type RPCServer struct {
	Impl Plugin
}

// Execute 实现 net/rpc 方法签名：请求指针 + 结果指针 + error。
func (s *RPCServer) Execute(req *entity.PluginRequest, res *entity.PluginResult) error {
	out, err := s.Impl.Execute(context.Background(), *req)
	if err != nil {
		return err
	}
	*res = out
	return nil
}

// RPCClient 是 RPC 客户端代理（内部类型）：把带 ctx 的惯用调用包装成 net/rpc 消息，
// ctx 转超时。
type RPCClient struct {
	client *rpc.Client
}

// Execute 的 net/rpc 不支持 ctx，用 goroutine + select 模拟超时/取消。
func (c *RPCClient) Execute(ctx context.Context, req entity.PluginRequest) (entity.PluginResult, error) {
	done := make(chan error, 1)
	var res entity.PluginResult
	go func() {
		done <- c.client.Call("Plugin.Execute", req, &res)
	}()
	select {
	case err := <-done:
		return res, err
	case <-ctx.Done():
		return entity.PluginResult{}, ctx.Err()
	}
}

// Serve 由插件程序（package main）调用：启动 go-plugin 服务端并阻塞，直到宿主断开。
func Serve(impl Plugin) {
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins: map[string]goplugin.Plugin{
			pluginName: &API{Impl: impl},
		},
	})
}

// Client 是宿主侧对单个插件进程的客户端句柄：实现 Plugin + Close。
type Client struct {
	raw  *goplugin.Client
	impl *RPCClient
}

// NewClient 按 exePath 拉起插件子进程，返回可调用的插件客户端。
func NewClient(exePath string) (*Client, error) {
	return newClient(exec.Command(exePath))
}

// newClient 供测试以自定义 exec.Cmd 拉起（helper 进程模式）。
func newClient(cmd *exec.Cmd) (*Client, error) {
	raw := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: Handshake,
		Plugins: map[string]goplugin.Plugin{
			pluginName: &API{},
		},
		Cmd: cmd,
	})
	rpcClient, err := raw.Client()
	if err != nil {
		raw.Kill()
		return nil, err
	}
	rawImpl, err := rpcClient.Dispense(pluginName)
	if err != nil {
		raw.Kill()
		return nil, err
	}
	impl, ok := rawImpl.(*RPCClient)
	if !ok {
		raw.Kill()
		return nil, fmt.Errorf("插件代理类型不符: %T", rawImpl)
	}
	return &Client{raw: raw, impl: impl}, nil
}

// Execute 委托给插件进程，带默认超时兜底（调用方 ctx 更紧则优先）。
func (c *Client) Execute(ctx context.Context, req entity.PluginRequest) (entity.PluginResult, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	return c.impl.Execute(ctx, req)
}

// Close 停止插件子进程。
func (c *Client) Close() {
	c.raw.Kill()
}

// 编译期校验：Client 实现 Plugin。
var _ Plugin = (*Client)(nil)
