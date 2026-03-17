# AskCodi Token Pool Manager (Go)

## 项目概述

AskCodi Token 池管理器 + OpenAI/Anthropic 兼容 API 转发服务。自动注册 AskCodi 账号、管理 Token 池，提供兼容 OpenAI 和 Anthropic 的 API 接口。

## 技术栈

- Go 1.26+
- SQLite (modernc.org/sqlite, 纯 Go 无 CGO)
- chi 路由器 (github.com/go-chi/chi/v5)
- sqlx (github.com/jmoiron/sqlx)
- 标准库 net/http

## 项目结构

```
askcodi-go/
├── main.go                        # 入口: DB 初始化, 路由注册, Worker 启动, 优雅关闭
├── static/                        # 前端 UI (HTML/CSS/JS)
├── internal/
│   ├── config/config.go           # 环境变量配置 (LISTEN_ADDR, DATABASE_PATH)
│   ├── database/
│   │   ├── db.go                  # SQLite 连接 (WAL mode, MaxOpenConns=1)
│   │   ├── models.go              # Account, Proxy, SystemConfig 结构体
│   │   └── migrations.go          # 建表 + 默认配置 + 迁移
│   ├── handler/
│   │   ├── chat.go                # /v1/models, /v1/chat/completions, /v1/messages
│   │   ├── dashboard.go           # /api/ 全部 CRUD (账号/代理/配置/日志)
│   │   └── health.go              # /api/health
│   ├── service/
│   │   ├── askcodi_client.go      # 核心: API 转发 + Anthropic↔OpenAI 协议翻译
│   │   ├── registration.go        # 自动注册 (GPTMail + Supabase PKCE + AskCodi)
│   │   ├── account_manager.go     # 账号池 (按 tokens_remaining DESC 优先选取)
│   │   ├── proxy_manager.go       # 代理管理 (含 proxy_enabled 开关)
│   │   ├── worker.go              # 后台定时: 余额检查(2s间隔) + 自动注册
│   │   └── logger.go              # 环形内存日志 (200条)
│   ├── middleware/cors.go          # CORS
│   └── util/
│       ├── httputil.go            # HTTP client 工厂 (HTTP/SOCKS5 代理)
│       ├── pkce.go                # PKCE code_verifier/challenge
│       └── password.go            # 随机密码生成
```

## 构建与运行

```bash
# 构建
go build -o askcodi-go .

# 运行 (默认端口 8000, 数据库 ./data.db)
./askcodi-go

# 自定义配置
LISTEN_ADDR=:8080 DATABASE_PATH=/path/to/data.db ./askcodi-go
```

## API 端点

### 转发 API (前缀 /v1)
- `GET  /v1/models` — 模型列表
- `POST /v1/chat/completions` — OpenAI 兼容 (流式/非流式)
- `POST /v1/messages` — Anthropic 兼容 (SSE)

### 管理 API (前缀 /api)
- `GET  /api/health` — 健康检查
- `GET  /api/dashboard/stats` — 仪表盘统计
- `GET  /api/accounts` — 账号列表
- `POST /api/accounts/register` — 触发批量注册
- `POST /api/accounts/{id}/refresh` — 刷新单个余额
- `POST /api/accounts/refresh_all` — 批量刷新余额
- `POST /api/accounts/{id}/disable` — 停用账号
- `DELETE /api/accounts/{id}` — 删除账号
- `GET  /api/proxies` — 代理列表
- `POST /api/proxies` — 添加代理
- `DELETE /api/proxies/{id}` — 删除代理
- `GET  /api/config` — 获取配置
- `PUT  /api/config` — 更新配置
- `GET  /api/registration/logs` — 注册日志

### UI
- `http://localhost:8000/ui/` — 管理面板

## 关键设计

- **账号优先级**: 按 `tokens_remaining DESC` 选取余额最多的账号
- **代理开关**: SystemConfig.proxy_enabled, 关闭后所有请求直连
- **Worker 稳定性**: 每个账号余额检查间隔 2s, 避免 Supabase 限频
- **注册稳定性**: create_trial 后等待 2s 再查余额
- **错误一致性**: 401/429/402 统一标记账号为 Exhausted
- **数据库兼容**: 与 Python 版 data.db 无缝迁移

## 开发规范

- 修改代码后先 `go build ./...` 确认编译通过
- 不要修改 static/ 下的文件除非涉及 UI 功能变更
- 所有数据库操作通过 sqlx, 不使用 ORM
- 新增 API 端点需要在 main.go 注册路由
- 保持 internal/ 包的分层: handler → service → database
