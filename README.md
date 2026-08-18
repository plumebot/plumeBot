# PlumeBot

基于 OneBot-11 协议的 QQ 机器人，对接 NapCat，轻量化，采用三级记忆压缩，更友好的多模态处理，简洁的插件配置以及双模式对话（mention|auto）。

## 技术栈

| 组件 | 说明 |
|------|------|
| Go | 1.21+ |
| ZeroBot | OneBot v11 连接层 |
| eino | AI Agent 引擎 (CloudWeGo) |
| modernc.org/sqlite | SQLite 驱动，纯 Go 无 cgo |
| uber/zap | 结构化日志 |
| go-plugin | 插件动态加载（子进程 stdio，自定义指令集协议） |
| Go testing | 标准库测试 |

## 快速启动

### 前置条件

- Go 1.21+
- NapCat（QQ 登录端，单独运行）

### 配置

首次运行前编辑 `config.yaml`：缺失时由 `pkg/config` 自动写入嵌入默认模板
（`config.default.yaml`），再填写 NapCat WebSocket 地址、模型 API Key 等。

> 人格注意：bot 人格由 SQLite `persona` 表定义（首次启动 seed 默认模板），改人格请改
> `persona` 表（即时生效，无需重启）；`config.yaml` 的 `agent.system_prompt` 仅作 persona
> 未配置时的兜底，不覆盖 persona 表。

### 编译 & 启动

```bash
go build -o bot.exe ./cmd/bot/   # Windows 开发机
./bot.exe
```

## 模块结构

```
plumebot/
├── cmd/bot/              # 入口
├── internal/
│   ├── domain/           # 领域层：纯接口 + 实体
│   ├── service/          # 业务编排层
│   ├── handler/          # 事件处理入口
│   └── infra/            # 基础设施实现
├── pkg/                  # 可复用工具
├── plugins/              # 插件目录
├── data/                 # SQLite 运行时生成
├── docs/                 # 文档
└── config.yaml           # 配置文件
```

## 架构

详见：

- [架构设计文档](docs/architecture.md)
- [开发阶段规划](docs/roadmap.md)
- [开发执行规范](CLAUDE.md)