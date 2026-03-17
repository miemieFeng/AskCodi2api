# AskCodi Token Pool Manager (Go)

AskCodi Token 池管理器 + OpenAI/Anthropic 兼容 API 转发服务。

自动注册 AskCodi 账号、管理 Token 池，提供兼容 OpenAI 和 Anthropic 的 API 接口，支持 Claude Code 直连。

## 功能特性

- **多协议兼容** — 同时支持 OpenAI (`/v1/chat/completions`) 和 Anthropic (`/v1/messages`) 协议
- **真流式转发** — Anthropic SSE 实时流式输出，完美支持 Claude Code
- **Token 池管理** — 自动注册账号、余额检查、账号轮换
- **代理支持** — HTTP/HTTPS/SOCKS5 代理，带故障自动切换
- **Web 管理面板** — 账号/代理/配置一站式管理
- **模型动态获取** — 从上游实时获取可用模型列表并缓存

## 快速开始

### 本地运行

```bash
go build -o askcodi-go .
./askcodi-go
```

### Docker

```bash
docker build -t askcodi-go .
docker run -d --name askcodi -p 8000:8000 -v ./data:/app/data askcodi-go
```

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `LISTEN_ADDR` | `:8000` | 监听地址 |
| `DATABASE_PATH` | `./data.db` (本地) / `/app/data/data.db` (Docker) | 数据库路径 |

## 搭配 Claude Code 使用

```bash
export ANTHROPIC_BASE_URL=http://localhost:8000
claude
```

## API 端点

### 转发 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/v1/models` | 模型列表 |
| POST | `/v1/chat/completions` | OpenAI 兼容（流式/非流式） |
| POST | `/v1/messages` | Anthropic 兼容（SSE 流式/JSON） |

### 管理 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/health` | 健康检查 |
| GET | `/api/dashboard/stats` | 仪表盘统计 |
| GET | `/api/accounts` | 账号列表 |
| POST | `/api/accounts/register` | 触发批量注册 |
| POST | `/api/accounts/{id}/refresh` | 刷新单个余额 |
| POST | `/api/accounts/refresh_all` | 批量刷新余额 |
| POST | `/api/accounts/{id}/disable` | 停用账号 |
| DELETE | `/api/accounts/{id}` | 删除账号 |
| GET | `/api/proxies` | 代理列表 |
| POST | `/api/proxies` | 添加代理 |
| DELETE | `/api/proxies/{id}` | 删除代理 |
| GET | `/api/config` | 获取配置 |
| PUT | `/api/config` | 更新配置 |
| GET | `/api/registration/logs` | 注册日志 |

### 管理面板

```
http://localhost:8000/ui/
```

## 项目结构

```
askcodi-go/
├── main.go                        # 入口
├── Dockerfile
├── static/                        # 前端 UI
├── internal/
│   ├── config/config.go           # 环境变量配置
│   ├── database/
│   │   ├── db.go                  # SQLite 连接 (WAL mode)
│   │   ├── models.go              # 数据模型
│   │   └── migrations.go          # 建表与迁移
│   ├── handler/
│   │   ├── chat.go                # 转发 API 处理
│   │   ├── dashboard.go           # 管理 API 处理
│   │   └── health.go              # 健康检查
│   ├── service/
│   │   ├── askcodi_client.go      # API 转发 + 协议翻译
│   │   ├── registration.go        # 自动注册
│   │   ├── account_manager.go     # 账号池管理
│   │   ├── proxy_manager.go       # 代理管理
│   │   ├── worker.go              # 后台定时任务
│   │   └── logger.go              # 内存日志
│   ├── middleware/cors.go          # CORS
│   └── util/                      # 工具函数
```

## 技术栈

- Go 1.26+
- SQLite（纯 Go，无 CGO）
- chi 路由器
- sqlx
