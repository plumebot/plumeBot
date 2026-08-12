# PlumeBot

基于 OneBot 协议的 QQ 机器人，对接 NapCat，AI 驱动的赛博群友。

## 技术栈

| 组件 | 说明 |
|------|------|
| Go | 1.21+ |
| ZeroBot | OneBot v11 连接层 |
| eino | AI Agent 引擎 (CloudWeGo) |
| turso/sqlite | SQLite 驱动，纯 Go 无 cgo |
| uber/zap | 结构化日志 |
| go-plugin | 插件动态加载（子进程 stdio，自定义指令集协议） |
| Go testing | 标准库测试 |

## 快速启动

### 前置条件

- Go 1.21+
- NapCat（QQ 登录端，单独运行）

### 配置

```bash
cp config.yaml.example config.yaml
# 编辑 config.yaml，填写 NapCat WebSocket 地址、模型 API Key 等
```

### 编译 & 启动

```bash
go build -o plumebot ./cmd/bot/
./plumebot
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

## 当前阶段

第一阶段：项目骨架。详见 [CLAUDE.md](CLAUDE.md) 和 [roadmap](docs/roadmap.md)。
