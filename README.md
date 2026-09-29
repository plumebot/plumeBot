[简体中文](README.zh-CN.md) |
[Documentation](docs/README.md) |
[Contributing](CONTRIBUTING.md) |
[Architecture](docs/architecture.md) |
[Roadmap](docs/roadmap.md)

![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white) ![License](https://img.shields.io/badge/license-MIT-blue.svg) ![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux-blue)

# PlumeBot

```markdown
NapCat (QQ 登录)
   │ OneBot WebSocket
   ▼
ZeroBot 连接层
   │
   ▼
中间件链：日志 → 限流 → 敏感词 → 持久化(窗口+SQLite)
   │                    │
   ▼                    ▼
插件分发(/命令)      触发判断(mention/auto + 状态规则)
                        │
                        ▼
                 Prompt 五段组装 → eino Agent 推理 ⇄ 记忆工具
                        │
                        ▼
                 Sender 发送 → 窗口追加 + OnReplied 记账
```

```markdown
cmd ──→ handler ──→ service ──→ domain（接口）
                      │
                      └──→ infra（编译时注入）

Written in Go. Ships as a single static binary with no cgo — no service dependencies other than NapCat.

## Features

| 组件 | 说明 |
| :------ | :------ |
| Go 1.21+ | 单二进制，无外部服务依赖（除 NapCat） |
| [ZeroBot](https://github.com/wdvxdr1123/ZeroBot) | OneBot v11 连接层 |
| [eino](https://github.com/cloudwego/eino) (CloudWeGo) | AI Agent 引擎，ChatModelAgent + tool calling |
| modernc.org/sqlite | SQLite 驱动，纯 Go 无 cgo |
| HashiCorp go-plugin | 插件动态加载（net/rpc 变体，免 protoc） |
| uber/zap + lumberjack | 结构化日志 |

## Quick start

Requires Docker (for NapCat), a QQ account and an OpenAI-compatible LLM endpoint.

```bash
# 1. Start NapCat (QQ login); scan the QR code shown in the log
cp .env.example .env                                  # edit: PLUMEBOT_SELFID, WEBUI_TOKEN
docker compose up -d && docker compose logs -f napcat

# 2. First run generates config.yaml — fill in your model API key
go build -o bot.exe ./cmd/bot/ && ./bot.exe
```

Then just **@ the bot**. Full walkthrough and troubleshooting: [Quick Start](docs/en/quickstart.md).

## Documentation

- [Documentation home](docs/README.md) — quick start, full configuration reference, features guide, plugin development, admin backend
- [简体中文](README.zh-CN.md)
- [Architecture design](docs/architecture.md) · [Roadmap](docs/roadmap.md) · [Admin API plan](docs/admin-web-api-plan.md)

## License

MIT — see [LICENSE](LICENSE).