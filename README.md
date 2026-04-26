# MailAPI

**高并发分布式临时邮件后端 | High-Concurrency Distributed Temporary Email Backend**

---

对标 [mail.tm](https://mail.tm) 的临时邮件后端服务，兼容 [DuckMail](https://www.duckmail.sbs/zh/api-docs) API 标准。采用微服务架构与事件驱动设计，三个独立服务（API / SMTP / Worker）协同工作，支持多域名配置、多 IP 绑定、多服务器分布式部署、API Key 鉴权体系（默认 `sk_` 前缀，兼容 DuckMail `dk_`）、三层速率限制和人类化邮箱前缀生成。

A temporary email backend service benchmarked against [mail.tm](https://mail.tm), compatible with the [DuckMail](https://www.duckmail.sbs/zh/api-docs) API standard. Built with a microservices architecture and event-driven design. Three independent services (API / SMTP / Worker) work in concert, supporting multi-domain configuration, multi-IP binding, multi-server distributed deployment, API key authentication (default `sk_`, `dk_` also accepted for compatibility), three-layer rate limiting, and human-like email prefix generation.

额外支持多种按子域切换的 API dialect，包括 `duck`、`cfworker` 与 `yyds`。其中 `yyds` 对齐 YYDS Mail 的公共临时邮箱 `/v1` 风格。

## 架构概览 / Architecture Overview

```
[外部发件方 / External Senders]                [客户端 / Clients]
         │                                           │
    TCP :25 (多IP绑定)                         HTTP :8080
    (Multi-IP binding)                               │
         ▼                                           ▼
+---------------------+                    +-------------------+
| SMTP Service        |                    | API Service       |
| 多监听器·域名过滤    |                    | REST API + SSE    |
| Multi-listener      |                    | Bearer 统一鉴权    |
| Domain filtering    |                    | sk_/dk_ Key + JWT |
+---------------------+                    +-------------------+
         │                                       │        ▲
         ▼                                       ▼        │ Pub/Sub
+---------------------+                    +-------------------+
| NATS JetStream      |                    | Redis             |
| 消息队列             |                    | 缓存 / 事件 / 限流 |
+---------------------+                    +-------------------+
         │                                          ▲
         ▼                                          │
+---------------------+                    +-------------------+
| Worker Service      | ─────写入────▶    | MongoDB            |
| 解析 MIME·存附件     |                    | 账号 / 邮件 / 域名 |
+---------------------+                    +-------------------+
         │
         ▼
+---------------------+
| MinIO (S3)          |
| 附件存储             |
+---------------------+
```

## 核心特性 / Key Features

- **DuckMail 兼容 API Key** — 默认使用 `sk_` 前缀（兼容 `dk_`），通过统一的 `Authorization: Bearer` 头传递，同时支持 API Key 和 JWT 令牌
- **DuckMail-Compatible API Keys** — `sk_`-prefixed API keys by default (`dk_` also accepted) via unified `Authorization: Bearer` header, supporting both API keys and JWT tokens

- **三层速率限制** — 全局 IP 限制 + 每 API Key 限制 + 每 API Key 每域名限制，全部可配置
- **Three-Layer Rate Limiting** — Global per-IP + per-API-key + per-API-key-per-domain limits, all configurable

- **人类化邮箱前缀生成** — 智能生成仿真人类商用/昵称邮箱前缀，单域名超 50 亿种独立组合
- **Human-Like Email Prefix Generation** — AI-inspired realistic human-like email prefixes, 5+ billion unique combinations per domain

- **配置化域名管理** — 直接在 `config.yaml` 中定义多个域名，启动时自动同步到数据库
- **Config-based Domain Management** — Define multiple domains in `config.yaml`, auto-synced to database on startup

- **多 IP 绑定** — 不同域名可绑定到不同 IP 地址的 SMTP 监听器，按域名路由收件
- **Multi-IP Binding** — Different domains can bind to different IP-based SMTP listeners, routing by domain

- **SMTP STARTTLS（可选）** — 可通过 `server.smtp.tls.enabled=true` 启用 STARTTLS（RFC 3207），并支持 `requireTLS` 强制先加密再投递
- **SMTP STARTTLS (optional)** — Enable STARTTLS (RFC 3207) via `server.smtp.tls.enabled=true`, with optional `requireTLS` enforcement

- **多服务器分布式** — 多台服务器共享同一配置，各节点自动检测本机 IP 并启动对应监听器
- **Multi-Server Distributed** — Multiple servers share one config; each node auto-detects local IPs and starts matching listeners

- **域名级 API Key 鉴权** — 不同 API Key 拥有不同域名访问权限，实现完整的域名级隔离
- **Domain-level API Key Auth** — Different API keys have access to different domains, achieving full domain-level isolation

- **一键管理脚本** — `mailapi.sh` 提供安装、配置、启停、日志查看、密钥生成等全套运维功能
- **One-click Management** — `mailapi.sh` provides install, config, start/stop, logs, key generation and more

## 技术栈 / Tech Stack

| 组件 / Component | 技术 / Technology | 用途 / Purpose |
|:---|:---|:---|
| Language | **Go 1.26** | 高并发网络 I/O / High-concurrency I/O |
| Web Framework | **Gin** | REST API |
| SMTP | **go-smtp** | 入站邮件接收 / Inbound email reception |
| MIME Parser | **go-message** | RFC 822 流式解析 / Stream parsing |
| Database | **MongoDB** | 文档存储 + TTL 自动过期 / Document store + TTL |
| Cache | **Redis** | 地址缓存 / Pub/Sub / 限流 / Cache, Pub/Sub, Rate limiting |
| Queue | **NATS JetStream** | 异步解耦 / Async decoupling |
| Storage | **MinIO** | S3 兼容附件存储 / S3-compatible attachments |
| Auth | **JWT (HS256) + sk_/dk_ API Key** | 统一 Bearer 鉴权 / Unified Bearer authentication |
| Crypto | **Bcrypt** | 密码哈希 / Password hashing |

## 快速开始 / Quick Start

### 方式一：一键安装 / Option 1: One-click Install

```bash
# 安装（需要 Go 和 Docker）/ Install (requires Go and Docker)
sudo ./mailapi.sh install

# 启动所有服务 / Start all services
sudo ./mailapi.sh start

# 查看状态 / Check status
./mailapi.sh status
```

### 方式二：手动部署 / Option 2: Manual Setup

#### 前置依赖 / Prerequisites

| 服务 / Service | 默认地址 / Default Address |
|:---|:---|
| MongoDB | `localhost:27017` |
| Redis | `localhost:6379` |
| NATS | `localhost:4222` |
| MinIO | `localhost:9000` |

```bash
# Docker Compose 快速启动依赖 / Quick start dependencies
./mailapi.sh infra-up
```

#### 编译 / Build

```bash
go build -o bin/api    ./cmd/api
go build -o bin/smtp   ./cmd/smtp
go build -o bin/worker ./cmd/worker
```

#### 启动 / Run

```bash
./bin/api              # REST API on :8080
sudo ./bin/smtp        # SMTP on :25 (需要 root / needs root)
./bin/worker           # Worker 后台处理 / background processing
```

## 配置 / Configuration

配置文件 `config.yaml` 支持所有功能。详细说明参见 [USAGE_GUIDE.md](USAGE_GUIDE.md)。

The `config.yaml` file controls all features. See [USAGE_GUIDE.md](USAGE_GUIDE.md) for details.

### 域名配置 / Domain Configuration

直接在配置文件中定义域名，无需手动操作数据库：

Define domains directly in the config file — no manual database operations needed:

```yaml
domains:
  - domain: "example.com"
    isActive: true
    isPrivate: false
    ips: []                    # 监听所有接口 / listen on all interfaces
  - domain: "example.org"
    isActive: true
    isPrivate: false
    ips: ["192.168.1.100"]     # 绑定到指定 IP / bind to specific IP
  - domain: "example.net"
    isActive: true
    isPrivate: true
    ips: ["192.168.1.101"]     # 另一个 IP / another IP
```

> Note: `isPrivate: true` 的域名不会被 wildcard（`domains: ["*"]`）API Key 隐式放开，必须显式在该 Key 的 `domains` 列表中列出（例如 `["*", "example.net"]`）。
>
> A domain with `isPrivate: true` will NOT be implicitly included by wildcard API keys (`domains: ["*"]`). You must explicitly list it in the key's `domains` (e.g. `["*", "example.net"]`).

### API Key 鉴权 / API Key Authentication

API Key 默认使用 `sk_` 前缀（兼容 DuckMail 标准 `dk_`）。所有认证统一通过 `Authorization: Bearer` 头传递——API Key 和 JWT 共享同一个头，系统通过 `sk_`/`dk_` 前缀自动区分。

API keys use `sk_` prefix by default (`dk_` is also accepted). All authentication goes through a unified `Authorization: Bearer` header — API keys and JWT tokens share the same header, the system auto-detects via the `sk_`/`dk_` prefix.

```yaml
apiKeys:
  - key: "sk_your_admin_key_here"
    name: "Admin"
    domains: ["*"]              # 通配符：全部公开域名 / wildcard: all public domains (private domains still require explicit listing)
    rpmLimit: 0                 # 不限速（仅受全局限速）/ unlimited (global limit only)
  - key: "sk_your_partner_key_here"
    name: "Partner"
    domains: ["example.com"]    # 仅限一个域名 / one domain only
    rpmLimit: 200               # 每分钟 200 次请求 / 200 requests per minute
    domainLimits:
      "example.com": 100        # 对 example.com 每分钟 100 次 / 100 RPM for example.com

rateLimit:
  global: 100                   # 全局每 IP 每分钟请求数 / global RPM per IP
```

客户端通过 `Authorization: Bearer` 头传递 Key：

Clients pass the key via the `Authorization: Bearer` header:

```bash
# API Key 方式 / API Key authentication
curl -H "Authorization: Bearer sk_your_admin_key_here" http://localhost:8080/domains

# JWT 方式 / JWT authentication
curl -H "Authorization: Bearer eyJhbGciOi..." http://localhost:8080/me
```

### 多 IP / 分布式部署 / Multi-IP / Distributed Deployment

每个域名可绑定到一个或多个 IP 地址。部署多台服务器时，所有服务器使用相同配置，各节点自动检测本机 IP，仅启动自己负责的监听器。

Each domain can bind to one or more IPs. When deploying multiple servers, all servers share the same config — each node auto-detects its local IPs and only starts the listeners it owns.

```yaml
domains:
  # 服务器 A (IP: 10.0.0.1) 负责 / Server A handles:
  - domain: "a.example.com"
    isActive: true
    ips: ["10.0.0.1"]

  # 服务器 B (IP: 10.0.0.2) 负责 / Server B handles:
  - domain: "b.example.com"
    isActive: true
    ips: ["10.0.0.2"]

  # 两台服务器都处理 / Both servers handle:
  - domain: "shared.example.com"
    isActive: true
    ips: ["10.0.0.1", "10.0.0.2"]
```

## API 接口 / API Endpoints

### 公开接口 / Public (API Key Required if configured)

| 方法 / Method | 路径 / Path | 说明 / Description |
|:---|:---|:---|
| `GET` | `/domains` | 获取可用域名列表（按 API Key 过滤）/ List domains (filtered by API key) |
| `POST` | `/accounts` | 创建临时邮箱（支持自动生成人类化前缀）/ Create account (supports auto-generated human-like prefix) |
| `POST` | `/token` | 登录获取 JWT / Login for JWT token |
| `GET` | `/addresses/random` | 生成随机人类化邮箱地址 / Generate random human-like email addresses |

### 认证接口 / Authenticated (JWT Required)

| 方法 / Method | 路径 / Path | 说明 / Description |
|:---|:---|:---|
| `GET` | `/me` | 获取当前用户 / Get current user |
| `GET` | `/accounts/:id` | 获取账号信息 / Get account |
| `DELETE` | `/accounts/:id` | 删除账号（级联删除邮件和附件）/ Delete account (cascades) |
| `GET` | `/messages` | 分页邮件列表 / Paginated message list |
| `PATCH` | `/messages` | 批量更新 flags（seen/keep）/ Bulk update flags (seen/keep) |
| `DELETE` | `/messages` | 按账号批量软删（可按 seen 过滤）/ Bulk soft delete by account (optional seen filter) |
| `POST` | `/messages/bulk-delete` | 按 ids 批量软删 / Bulk soft delete by ids |
| `GET` | `/messages/:id` | 邮件详情 / Message detail |
| `PATCH` | `/messages/:id` | 更新邮件状态（seen/keep）/ Update flags (seen/keep) |
| `DELETE` | `/messages/:id` | 删除邮件 / Delete message |
| `GET` | `/messages/:id/download` | 下载原始 .eml 文件 / Download raw .eml |
| `GET` | `/messages/:id/attachments/:attachmentId` | 下载附件 / Download attachment |
| `GET` | `/sse` | 实时推送新邮件 / Real-time new email push |

消息分页与保留说明 / Notes:

- `/messages` 同时支持传统分页（`page/itemsPerPage`）与高性能 cursor 分页（`cursor`/`after`，响应包含 `nextCursor`）。
- `/messages` 支持 `seen=true/false` 过滤，并为该查询模式建立了复合索引（适合高并发列表页）。
- 邮件默认按 `message.ttl` 自动过期删除（TTL）。如需长期保留某封邮件，可 `PATCH /messages/:id` 传 `{"keep": true}`。该能力默认关闭，需要在 API 服务进程显式设置环境变量：`MAILAPI_API_ALLOW_MESSAGE_KEEP=1`（或 `MAILAPI_ALLOW_MESSAGE_KEEP=1`）。

OpenAPI:

- 见仓库根目录的 `openapi.yaml`（DuckMail 风格主 API）。

### 速率限制 / Rate Limiting

三层速率限制体系，全部基于 Redis 固定窗口计数器（原子 INCR + 首次设置 TTL）：

Three-layer rate limiting system, all based on Redis fixed-window counters (atomic INCR + set TTL on first increment):

| 层级 / Layer | 说明 / Description | 配置 / Config |
|:---|:---|:---|
| 全局 IP 限制 / Global per-IP | 每个 IP 每分钟请求数上限 / Max requests per minute per IP | `rateLimit.global` |
| API Key 限制 / Per-API-Key | 每个 Key 每分钟总请求数 / Total RPM for each key | `apiKeys[].rpmLimit` |
| Key+域名限制 / Per-Key-Per-Domain | 每个 Key 对特定域名的请求数 / RPM for a key on a specific domain | `apiKeys[].domainLimits` |

## 邮箱前缀生成器 / Email Prefix Generator

系统内置智能邮箱前缀生成器，生成的前缀高度仿真真实人类邮箱，支持三大类别：

Built-in intelligent email prefix generator producing highly realistic human-like email prefixes across three categories:

| 类别 / Category | 权重 / Weight | 示例 / Examples |
|:---|:---|:---|
| 个人姓名 / Personal Names | 55% | `john.smith`, `maria_garcia92`, `jdoe`, `zhang.wei2001` |
| 昵称 / Nicknames | 30% | `cooldragon`, `happy_panda88`, `night.wolf`, `starlight2023` |
| 商务 / Business | 15% | `techcorp.sales`, `support_hr`, `marketing` |

- 单域名超 **50 亿**种独立组合 / Over **5 billion** unique combinations per domain
- 15 种个人姓名模式 + 6 种后缀类型 / 15 personal name patterns + 6 suffix types
- 国际化姓名（英语、中文拼音、欧洲、日韩等）/ International names (English, Chinese pinyin, European, Japanese, Korean, etc.)
- 全部使用小写 ASCII 字符 / All lowercase ASCII characters

```bash
# 生成随机地址 / Generate random addresses
curl -H "Authorization: Bearer sk_xxx" \
  "http://localhost:8080/addresses/random?domain=example.com&count=5"

# 自动生成前缀创建账号 / Auto-generate prefix when creating account
curl -X POST http://localhost:8080/accounts \
  -H "Authorization: Bearer sk_xxx" \
  -H "Content-Type: application/json" \
  -d '{"domain": "example.com", "password": "secret123"}'
```

## 管理脚本 / Management Script

`mailapi.sh` 提供全套运维功能：

`mailapi.sh` provides complete operations:

```bash
./mailapi.sh install           # 完整安装 / Full installation
./mailapi.sh build             # 编译二进制 / Build binaries
./mailapi.sh config            # 配置生成（交互/环境变量）/ Config generation (interactive/env)
./mailapi.sh infra-up          # 启动基础设施 / Start infrastructure
./mailapi.sh infra-down        # 停止基础设施 / Stop infrastructure
./mailapi.sh infra-clean       # 清理基础设施数据（危险）/ Clean infra data (DANGEROUS)
./mailapi.sh doctor            # 环境与配置自检 / Doctor check
./mailapi.sh upgrade [pull]    # 编译并重启（可选拉取镜像）/ Build and restart (optional pull images)
./mailapi.sh uninstall [purge] # 卸载（可选清理数据）/ Uninstall (optional purge)
./mailapi.sh compose <args...> # 透传 docker compose 命令 / Pass-through docker compose
./mailapi.sh setcap-smtp       # 允许非 root 绑定 25 端口（Linux）/ Allow non-root bind to 25 (Linux)
./mailapi.sh smoke             # 冒烟测试 / Smoke test
./mailapi.sh start [service]   # 启动服务 / Start services
./mailapi.sh stop [service]    # 停止服务 / Stop services
./mailapi.sh restart [service] # 重启服务 / Restart services
./mailapi.sh status [service]  # 查看状态 / Show status
./mailapi.sh logs [service] [lines] # 查看日志 / View logs
./mailapi.sh genkey [name] [prefix] # 生成 API Key（sk/dk）/ Generate API key (sk/dk)
./mailapi.sh list-domains      # 列出已配置域名 / List configured domains
./mailapi.sh list-apikeys      # 列出已配置 Key / List configured API keys
```

## 健康检查与调试端口 / Health & Debug

每个服务（API / SMTP / Worker）都可以按需开启**独立的 debug HTTP server**（默认关闭，建议仅绑定 `127.0.0.1` 并置于防火墙/LB 后），用于运维探活与性能诊断：

- `GET /healthz`：进程存活检查 / Liveness
- `GET /readyz`：依赖就绪检查（会探测后端连通性）/ Readiness
- `GET /metrics`：Prometheus 指标（可选）/ Prometheus metrics (optional)
- `GET /debug/pprof/`：pprof（可选，**不要公网暴露**）/ pprof (optional, do not expose publicly)

通过 `config.yaml` 的 `server.api.debug` / `server.smtp.debug` / `server.worker.debug` 开启并设置监听地址（建议仅绑定 `127.0.0.1`）。

运维便捷：也可以用 `--debug-addr host:port` 临时覆盖 debug server 地址并隐式启用（仅影响当前进程，不会改配置文件）。

```yaml
server:
  api:
    debug:
      enabled: true
      host: "127.0.0.1"
      port: 6060
      metrics: true
      pprof: false
```

CLI（所有服务通用）/ CLI (all services):

```bash
# 检查配置 / Validate config
./bin/mailapi-api --check-config ./config.yaml
./bin/mailapi-smtp --check-config ./config.yaml
./bin/mailapi-worker --check-config ./config.yaml

# 打印生效配置（默认脱敏）/ Print effective config (redacted by default)
./bin/mailapi-api --print-effective-config ./config.yaml
./bin/mailapi-smtp --print-effective-config ./config.yaml
./bin/mailapi-worker --print-effective-config ./config.yaml

# 输出未脱敏配置（谨慎）/ Print unredacted config (be careful)
./bin/mailapi-api --print-effective-config --full ./config.yaml
./bin/mailapi-smtp --print-effective-config --full ./config.yaml
./bin/mailapi-worker --print-effective-config --full ./config.yaml
```

## 项目结构 / Project Structure

```
mailapi/
├── cmd/
│   ├── api/main.go               # REST API 服务入口 / API entry point
│   ├── smtp/main.go              # SMTP 服务入口（多监听器）/ SMTP entry (multi-listener)
│   └── worker/main.go            # Worker 服务入口 / Worker entry point
├── internal/
│   ├── model/model.go            # 数据模型 / Data models
│   ├── config/                   # 配置管理（域名·API Key·多IP·速率限制）/ Config (domains, keys, IPs, rate limits)
│   ├── auth/                     # JWT 认证 / JWT auth
│   ├── cache/                    # Redis 缓存层（含三层限流）/ Redis cache (with 3-layer rate limiting)
│   ├── queue/                    # NATS 消息队列 / NATS queue
│   ├── storage/                  # MinIO 对象存储 / MinIO storage
│   ├── store/                    # MongoDB 数据访问（含域名同步）/ MongoDB (with domain sync)
│   ├── handler/                  # HTTP 处理器（域名隔离·前缀生成）/ HTTP handlers (domain isolation, prefix generation)
│   ├── smtp/                     # SMTP 多监听器实现 / SMTP multi-listener
│   ├── worker/                   # MIME 解析工作者 / MIME parser
│   ├── prefix/                   # 人类化邮箱前缀生成器 / Human-like email prefix generator
│   └── middleware/               # Bearer 统一鉴权 + 域名隔离 + 三层限流 / Unified Bearer auth + isolation + 3-layer rate limit
├── config.yaml                   # 配置文件 / Configuration
├── mailapi.sh                    # 一键管理脚本 / Management script
└── go.mod
```

## 测试 / Testing

```bash
go test ./...           # 运行所有测试 / Run all tests
go test -cover ./...    # 带覆盖率 / With coverage
go test -v ./...        # 详细输出 / Verbose
```

## 核心设计理念 / Design Principles

- **SMTP 不解析** — SMTP 收到邮件只做地址校验和入队，不阻塞在 MIME 解析上 / SMTP does not parse — only validates and enqueues
- **RCPT TO 防洪** — Redis 微秒级地址校验 + 域名过滤，拒绝不属于本监听器的邮件 / Redis microsecond validation + domain filtering rejects non-matching mail
- **按 IP 路由域名** — 不同域名绑定不同 IP，SMTP 监听器自动按域名过滤 / Domain-to-IP routing with per-listener domain filtering
- **自动节点发现** — 分布式部署时各节点自动检测本机 IP，仅启动对应监听器 / Auto-detection of local IPs for distributed deployment
- **域名级隔离** — API Key 实现域名级别的访问控制 / API keys enforce domain-level access control
- **统一 Bearer 鉴权** — sk_/dk_ API Key 和 JWT 共享同一个 Authorization 头，系统自动区分 / Unified Bearer auth: sk_/dk_ keys and JWT share the same header
- **三层限流** — 全局 IP + 单 Key + 单 Key 单域名三级精细限流 / Three-layer rate limiting: global IP + per-key + per-key-per-domain
- **人类化前缀** — 50 亿+ 仿真组合，自动重试防碰撞 / 5B+ realistic combinations with auto-retry on collision
- **TTL 自动清理** — MongoDB TTL 索引自动删除过期数据 / MongoDB TTL auto-deletes expired data
- **跨节点 SSE** — Redis Pub/Sub 确保任意 API 节点都能推送到客户端 / Redis Pub/Sub enables SSE push from any API node

## 安全特性 / Security

- **统一 Bearer 鉴权** — sk_/dk_ API Key（域名级）+ JWT（用户级）统一通过 `Authorization: Bearer` 头 / Unified Bearer: sk_/dk_ API Key (domain-level) + JWT (user-level) via `Authorization: Bearer`
- Bcrypt 密码哈希 / Bcrypt password hashing
- API Key 域名隔离，不同 Key 不同权限 / API key domain isolation
- 每个接口的资源所有权校验 / Per-endpoint resource ownership checks
- 三层速率限制（全局 IP + 单 Key + 单 Key 单域名）/ Three-layer rate limiting (global IP + per-key + per-key-per-domain)
- SMTP 层独立速率限制 / SMTP-layer independent rate limiting
- MIME 炸弹防护（深度限制 50 层）/ MIME bomb protection (depth limit 50)
- 邮件大小限制 20MB / Message size limit 20MB
- 流式 `io.LimitReader` 防 OOM / Stream `io.LimitReader` prevents OOM

## 文档 / Documentation

详细的项目文档请参阅 / For detailed documentation see:
- [PROJECT_DOCUMENTATION.md](PROJECT_DOCUMENTATION.md) — 完整项目文档（中英双语）/ Full project documentation (bilingual)
- [USAGE_GUIDE.md](USAGE_GUIDE.md) — 使用指南（中英双语）/ Usage guide (bilingual)
- [API_DIALECTS.md](API_DIALECTS.md) — 多 API 风格（按子域前缀）规范 / API dialect routing by subdomain
- [cfworker.md](cfworker.md) — Cloudflare Worker 对接方案 / Cloudflare Worker integration plan
- [yyds.md](yyds.md) — YYDS Mail 公共临时邮箱 dialect 说明 / YYDS Mail public temporary-inbox dialect

## License

MIT
