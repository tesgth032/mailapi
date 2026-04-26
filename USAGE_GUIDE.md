# MailAPI 使用指南 / Usage Guide

本指南面向 API 调用方（前端开发者、第三方集成者）和运维人员，提供完整的安装部署、接口使用示例和最佳实践。

This guide is for API consumers (frontend developers, third-party integrators) and operators, providing complete installation, endpoint usage examples, and best practices.

OpenAPI（duck 主风格）/ OpenAPI (duck dialect): `openapi.yaml`

---

## 目录 / Table of Contents

1. [环境准备 / Setup](#1-环境准备--setup)
2. [管理脚本 / Management Script](#2-管理脚本--management-script)
3. [配置详解 / Configuration](#3-配置详解--configuration)
4. [基本工作流程 / Basic Workflow](#4-基本工作流程--basic-workflow)
5. [域名管理 / Domains](#5-域名管理--domains)
6. [API Key 鉴权 / API Key Authentication](#6-api-key-鉴权--api-key-authentication)
7. [账号管理 / Accounts](#7-账号管理--accounts)
8. [JWT 认证 / Authentication](#8-jwt-认证--authentication)
9. [邮件操作 / Messages](#9-邮件操作--messages)
10. [附件下载 / Attachments](#10-附件下载--attachments)
11. [原始邮件下载 / Raw Email Download](#11-原始邮件下载--raw-email-download)
12. [实时推送 / Real-time SSE](#12-实时推送--real-time-sse)
13. [分页 / Pagination](#13-分页--pagination)
14. [错误处理 / Error Handling](#14-错误处理--error-handling)
15. [速率限制 / Rate Limiting](#15-速率限制--rate-limiting)
16. [邮箱前缀生成 / Email Prefix Generation](#16-邮箱前缀生成--email-prefix-generation)
17. [多 IP 配置 / Multi-IP Setup](#17-多-ip-配置--multi-ip-setup)
18. [分布式部署 / Distributed Deployment](#18-分布式部署--distributed-deployment)
19. [完整使用示例 / Complete Examples](#19-完整使用示例--complete-examples)
20. [生产部署 / Production Deployment](#20-生产部署--production-deployment)

---

## 1. 环境准备 / Setup

### 1.1 一键安装（推荐）/ One-click Install (Recommended)

```bash
# 完整安装：检查依赖、编译、启动基础设施、生成配置、安装服务
# Full install: check deps, build, start infra, generate config, install services
sudo ./mailapi.sh install
```

脚本会自动完成 / The script automatically:
- 检查 Go 和 Docker 是否安装 / Checks Go and Docker are installed
- 编译三个二进制文件到 `/opt/mailapi/bin/` / Builds binaries to `/opt/mailapi/bin/`
- 使用 Docker Compose 启动 MongoDB、Redis、NATS、MinIO / Starts infra via Docker Compose
- 交互式生成配置文件（域名、API Key、端口等）/ Interactively generates config
- 安装 systemd 服务（需要 root）/ Installs systemd services (needs root)

### 1.2 手动安装 / Manual Setup

```bash
# 1. 启动基础设施 / Start infrastructure
./mailapi.sh infra-up

# 2. 编译 / Build
go build -o bin/api    ./cmd/api
go build -o bin/smtp   ./cmd/smtp
go build -o bin/worker ./cmd/worker

# 3. 编辑配置文件 / Edit config
vim config.yaml

# 4. 启动服务 / Start services
./bin/api              # REST API on :8080
sudo ./bin/smtp        # SMTP on :25
./bin/worker           # Background worker
```

### 1.3 DNS 配置 / DNS Configuration

要接收外部邮件，需要为每个域名配置 MX 记录：

To receive external emails, configure MX records for each domain:

```
example.com.    IN  MX  10  mail.example.com.
mail.example.com.  IN  A  YOUR_SERVER_IP
```

多 IP 部署时，每个域名指向其配置的 IP：

For multi-IP deployments, point each domain to its configured IP:

```
a.example.com.  IN  MX  10  smtp-a.example.com.
smtp-a.example.com.  IN  A  192.168.1.100

b.example.com.  IN  MX  10  smtp-b.example.com.
smtp-b.example.com.  IN  A  192.168.1.101
```

### 1.4 按子域选择 API 风格 / API Dialects by Subdomain

MailAPI 支持通过 API 域名前缀（子域）区分不同的 API 风格（兼容层），并允许设置 `api.mailapi.com` 的默认风格。例如：

- `https://api.mailapi.com`：默认风格（可配置）
- `https://duck.api.mailapi.com`：DuckMail 风格
- `https://cfworker.api.mailapi.com`：Cloudflare Worker（cloudflare_temp_email）风格
- `https://yyds.api.mailapi.com`：YYDS Mail 公共临时邮箱风格

注意：`cfworker` 风格在当前实现中是“代理转发”到你部署的 Cloudflare Worker，上游地址需要在 `config.yaml` 中配置：

```yaml
dialects:
  cfworker:
    upstream: "https://temp-mail.example.workers.dev"
    timeout: 15s
```

未配置 `dialects.cfworker.upstream` 时，访问 `cfworker.<baseHost>` 将返回 503（表示上游未就绪）。

`yyds` 风格不依赖额外上游；它直接复用本地 `mailapi` 的邮箱/消息存储能力，并把接口转换为 YYDS Mail 的 `/v1` 风格。支持的范围包括：

- 公共临时邮箱接口：`/v1/accounts*`、`/v1/token`
- 邮件接口：`/v1/messages*`、`/v1/sources/{id}`
- 公共元数据接口：`/v1/domains`、`/v1/plans`、`/v1/pricing`、`/v1/domain-reward/config`、`/v1/stats`、`/v1/llms.txt`

补充说明：

- `yyds` 当前实现的是 YYDS Mail 文档中的“公共临时邮箱/公共元数据”子集；其站内控制台、计费、Webhook、DNS 自动化等功能不在本仓库内实现。
- `/v1/accounts/wildcard` 仅支持“目标子域已预先配置为接收域名”的场景；`mailapi` 不会自动创建 wildcard 子域的 DNS/MX/SMTP 接收能力。

完整说明见：[yyds.md](yyds.md)。

生产部署时建议同时配置：

- DNS：`api.mailapi.com` 与 `*.api.mailapi.com` 指向同一入口（LB/反向代理/API 服务）
- TLS：证书覆盖 `api.mailapi.com` 与 `*.api.mailapi.com`

完整规则与配置建议见：[API_DIALECTS.md](API_DIALECTS.md)。

---

## 2. 管理脚本 / Management Script

`mailapi.sh` 提供全套运维功能：

```bash
# 安装与编译 / Install & Build
./mailapi.sh install              # 完整安装 / Full install
./mailapi.sh build                # 仅编译 / Build only
./mailapi.sh config               # 配置生成（交互/环境变量）/ Generate config (interactive/env)

# 基础设施 / Infrastructure
./mailapi.sh infra-up             # 启动 Docker 容器 / Start containers
./mailapi.sh infra-down           # 停止 Docker 容器 / Stop containers
./mailapi.sh infra-clean          # 停止并清空数据卷（危险）/ Stop and wipe volumes (DANGEROUS)
./mailapi.sh compose <args...>    # 透传 docker compose 命令 / Pass-through docker compose

# 服务管理 / Service management
./mailapi.sh start [service]      # 启动服务（api|smtp|worker|all）/ Start services
./mailapi.sh stop [service]       # 停止服务 / Stop services
./mailapi.sh restart [service]    # 重启服务 / Restart services
./mailapi.sh status [service]     # 查看状态 / Show status
./mailapi.sh logs [service] [n]   # 查看日志（默认 200 行）/ View logs (default 200 lines)
./mailapi.sh logs api 200         # 查看 API 日志（最近 200 行）/ View API logs (last 200 lines)

# 运维辅助 / Ops helpers
./mailapi.sh doctor               # 环境与配置自检 / Doctor check
./mailapi.sh upgrade [pull]       # 编译并重启（可选拉取镜像）/ Build and restart (optional pull images)
./mailapi.sh uninstall [purge]    # 卸载（可选清理数据）/ Uninstall (optional purge)
./mailapi.sh setcap-smtp          # 允许非 root 绑定 25 端口（Linux）/ Allow non-root bind to 25 (Linux)
./mailapi.sh smoke                # 冒烟测试 / Smoke test

# 密钥与域名 / Keys & Domains
./mailapi.sh genkey "My Key"      # 生成 API Key（默认 sk_）/ Generate API key (default sk_)
./mailapi.sh genkey "Duck Key" dk # 生成 dk_ 前缀 Key（兼容 DuckMail）/ Generate dk_ key (DuckMail)
./mailapi.sh list-domains         # 列出域名 / List domains
./mailapi.sh list-apikeys         # 列出 Key（脱敏）/ List keys (redacted)
```

非交互模式（适合 CI/脚本）示例 / Non-interactive examples:

```bash
# 生成配置（不弹交互提示）/ Generate config without prompts
MAILAPI_NONINTERACTIVE=1 MAILAPI_DOMAINS=example.com MAILAPI_API_PORT=8080 MAILAPI_SMTP_PORT=2525 ./mailapi.sh config

# 危险操作需要显式确认 / Dangerous ops need explicit confirmation
MAILAPI_NONINTERACTIVE=1 MAILAPI_CONFIRM=1 ./mailapi.sh infra-clean
```

补充 / Note:

- 脚本会在安装目录下生成 `infra.env`（默认路径：`$MAILAPI_DIR/infra.env`），用于持久化 Docker Compose 需要的基础设施变量（尤其是 MinIO root 凭证），避免跨会话时 config 与容器凭证不一致。
- 可通过环境变量 `MAILAPI_ENV_FILE` 覆盖该文件路径。

### 2.1 健康检查与调试端口 / Health & Debug

三个服务（API / SMTP / Worker）都可以按需启用独立的 debug HTTP server（默认关闭，建议仅绑定 `127.0.0.1` 并置于防火墙/LB 后）。debug server 提供：

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

典型用法 / Examples:

```bash
curl -sS http://127.0.0.1:6060/healthz
curl -sS http://127.0.0.1:6060/readyz
curl -sS http://127.0.0.1:6060/metrics | head
go tool pprof -http=:0 http://127.0.0.1:6060/debug/pprof/profile?seconds=10
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

---

## 3. 配置详解 / Configuration

### 3.1 域名配置 / Domain Configuration

直接在 `config.yaml` 中定义域名，**无需手动操作 MongoDB**：

Define domains directly in `config.yaml` — **no manual MongoDB operations needed**:

```yaml
domains:
  - domain: "example.com"
    isActive: true         # 是否启用 / Whether active
    isPrivate: false       # 是否私有 / Whether private
    ips: []                # 空=监听所有接口 / Empty = listen on all interfaces
  - domain: "vip.example.com"
    isActive: true
    isPrivate: true
    ips: ["10.0.0.1"]     # 绑定到指定 IP / Bind to specific IP
```

API 服务启动时会自动将这些域名同步到 MongoDB。

The API service automatically syncs these domains to MongoDB on startup.

补充说明 / Notes:

- `isPrivate: true` 的域名会被视为“私有域名”：不会被 wildcard（`domains: ["*"]`）API Key 隐式放开，必须显式在该 Key 的 `domains` 列表中列出该域名（例如 `["*", "vip.example.com"]`），否则该私有域名对该 Key 不可见/不可用。
- A domain with `isPrivate: true` is treated as "private": it will NOT be implicitly included by wildcard API keys (`domains: ["*"]`). You must explicitly list it in the API key's `domains` (e.g. `["*", "vip.example.com"]`) to make it visible/usable.

### 3.2 API Key 配置 / API Key Configuration

API Key 默认使用 `sk_` 前缀（兼容 DuckMail `dk_`），通过 `Authorization: Bearer sk_xxx` 头传递。

API keys use `sk_` prefix by default (`dk_` also accepted), passed via the `Authorization: Bearer sk_xxx` header.

```yaml
apiKeys:
  - key: "sk_abc123def456ghi789"      # sk_ 前缀密钥 / sk_ prefixed key
    name: "Admin"                      # 描述性名称 / Descriptive name
    domains: ["*"]                     # ["*"] = 所有公开域名 / all public domains (private domains still require explicit listing)
    rpmLimit: 0                        # 0 = 不限速 / unlimited
  - key: "sk_partner_key_here"
    name: "Partner"
    domains: ["example.com"]           # 仅限指定域名 / restricted
    rpmLimit: 200                      # 每分钟 200 次请求 / 200 RPM
    domainLimits:
      "example.com": 100               # 对该域名每分钟 100 次 / 100 RPM for this domain
```

生成 API Key / Generate API key:

```bash
./mailapi.sh genkey "Partner"
# 输出 / Output:
#   Key:  sk_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4
#   Add to config.yaml:
#     - key: "sk_a1b2c3..."
#       name: "Partner"
#       domains: ["*"]
```

### 3.3 速率限制配置 / Rate Limit Configuration

三层速率限制可独立配置：

Three-layer rate limits are independently configurable:

```yaml
# 全局 IP 限制 / Global per-IP limit
rateLimit:
  global: 100              # 每 IP 每分钟 100 次 / 100 RPM per IP (default)

# 每 Key 限制 / Per-key limit (in apiKeys section)
apiKeys:
  - key: "sk_xxx"
    rpmLimit: 200          # 该 Key 总共每分钟 200 次 / 200 total RPM for this key

    # 每 Key 每域名限制 / Per-key-per-domain limit
    domainLimits:
      "example.com": 100   # 该 Key 对 example.com 每分钟 100 次 / 100 RPM on example.com
      "other.com": 50      # 该 Key 对 other.com 每分钟 50 次 / 50 RPM on other.com
```

### 3.4 不配置 API Key / Without API Keys

如果省略 `apiKeys` 节，系统以向后兼容模式运行——不需要 API Key，所有域名对所有请求开放。

If the `apiKeys` section is omitted, the system runs in backward-compatible mode — no API key required, all domains open to all requests.

---

## 4. 基本工作流程 / Basic Workflow

```
查询可用域名             创建临时账号             登录获取 JWT
GET /domains  ──▶  POST /accounts  ──▶  POST /token
(Bearer sk_xxx)    (Bearer sk_xxx)       (Bearer sk_xxx)
                                              │
      ┌──── 可选：自动生成前缀 ────┐           │
      │ POST /accounts             │           │
      │ {"domain":"x.com",         │           ▼
      │  "password":"..."}         │     查看邮件列表
      └────────────────────────────┘  GET /messages
                                      (Bearer JWT)
                                           │
                                           ▼
                                     下载附件 / 下载 .eml / 实时监听 SSE
```

> **统一认证说明 / Unified Auth Note**: API Key（`sk_xxx` / `dk_xxx`）和 JWT 令牌都通过同一个 `Authorization: Bearer` 头传递。系统通过 `sk_`/`dk_` 前缀自动识别 API Key；其他 Bearer 令牌被视为 JWT。JWT 用户被自动限定在其邮箱域名范围内。
>
> Both API keys (`sk_xxx` / `dk_xxx`) and JWT tokens are passed via the same `Authorization: Bearer` header. The system auto-detects API keys by the `sk_`/`dk_` prefix; other Bearer tokens are treated as JWT. JWT users are automatically scoped to their email domain.

---

## 5. 域名管理 / Domains

### 获取可用域名列表 / List Available Domains

```bash
curl -s http://localhost:8080/domains \
  -H "Authorization: Bearer sk_your_key_here" | jq
```

**响应 / Response** `200 OK`:

```json
{
  "@context": "/contexts/Domain",
  "@id": "/domains",
  "@type": "hydra:Collection",
  "hydra:totalItems": 1,
  "hydra:member": [
    {
      "id": "6789abcdef012345abcdef01",
      "domain": "example.com",
      "isActive": true,
      "isPrivate": false,
      "createdAt": "2026-04-01T10:00:00Z",
      "updatedAt": "2026-04-01T10:00:00Z"
    }
  ]
}
```

**说明 / Notes:**
- 只返回 `isActive: true` 的域名 / Only returns active domains
- 结果按当前 API Key 的域名权限过滤 / Filtered by current API key's domain permissions
- 通配符 Key（`["*"]`）默认返回所有**公开**域名；私有域名（`isPrivate=true`）必须显式授权才会返回 / Wildcard key returns all *public* domains by default; private domains require explicit allowlist

---

## 6. API Key 鉴权 / API Key Authentication

### 6.1 使用方式 / Usage

所有认证统一通过 `Authorization: Bearer` 头传递。API Key 默认使用 `sk_` 前缀（兼容 `dk_`），其他 Bearer 令牌被视为 JWT。

All authentication goes through the `Authorization: Bearer` header. API keys use `sk_` prefix by default (`dk_` also accepted); other Bearer tokens are treated as JWT.

```bash
# API Key 鉴权 / API key auth
curl -H "Authorization: Bearer sk_your_key_here" http://localhost:8080/domains

# JWT 鉴权 / JWT auth
curl -H "Authorization: Bearer eyJhbGciOi..." http://localhost:8080/me
```

### 6.2 域名隔离 / Domain Isolation

不同 Key 看到不同的域名和数据：

Different keys see different domains and data:

```bash
# Admin Key (domains: ["*", "vip.example.com"]) — 看到所有公开域名 + 显式授权的私有域名 / sees all public domains + explicitly allowed private domains
curl -H "Authorization: Bearer sk_admin_key" http://localhost:8080/domains
# → ["example.com", "vip.example.com"]

# Partner Key (domains: ["example.com"]) — 只看到授权域名 / sees only authorized
curl -H "Authorization: Bearer sk_partner_key" http://localhost:8080/domains
# → ["example.com"]
```

### 6.3 JWT 用户的域名限定 / JWT User Domain Scoping

使用 JWT 登录的用户会被自动限定在其邮箱地址的域名范围内。例如，`user@example.com` 的 JWT 只能访问 `example.com` 的资源，无需额外的 API Key。

JWT-authenticated users are automatically scoped to their email address's domain. For example, a JWT for `user@example.com` can only access `example.com` resources, without needing a separate API key.

### 6.4 错误场景 / Error Scenarios

```bash
# 缺少认证 / Missing auth (when API keys configured)
401 {"code": 401, "message": "API key required (Authorization: Bearer sk_... or dk_...)"}

# 无效 API Key / Invalid API key
401 {"code": 401, "message": "invalid API key"}

# 无效 JWT / Invalid JWT
401 {"code": 401, "message": "invalid or expired token"}

# 域名不在 Key 允许范围 / Domain not in key scope
403 {"code": 403, "message": "domain not allowed for this API key"}
```

---

## 7. 账号管理 / Accounts

### 7.1 创建临时邮箱（指定地址）/ Create Account (Specified Address)

```bash
curl -s -X POST http://localhost:8080/accounts \
  -H "Authorization: Bearer sk_your_key_here" \
  -H "Content-Type: application/json" \
  -d '{
    "address": "testuser@example.com",
    "password": "mypassword123"
  }' | jq
```

### 7.2 创建临时邮箱（自动生成人类化前缀）/ Create Account (Auto-Generated Prefix)

只需提供域名和密码，系统自动生成仿真人类邮箱前缀：

Just provide domain and password, the system auto-generates a realistic human-like email prefix:

```bash
curl -s -X POST http://localhost:8080/accounts \
  -H "Authorization: Bearer sk_your_key_here" \
  -H "Content-Type: application/json" \
  -d '{
    "domain": "example.com",
    "password": "mypassword123"
  }' | jq
```

系统会自动生成类似 `john.smith92@example.com`、`cooldragon@example.com` 等人类化地址。如果发生地址冲突，会自动重试最多 5 次。

The system auto-generates addresses like `john.smith92@example.com`, `cooldragon@example.com`, etc. If a collision occurs, it automatically retries up to 5 times.

**响应 / Response** `201 Created`:

```json
{
  "id": "6789abcdef012345abcdef02",
  "address": "sarah.chen88@example.com",
  "quota": 41943040,
  "used": 0,
  "isDeleted": false,
  "createdAt": "2026-04-01T10:05:00Z",
  "updatedAt": "2026-04-01T10:05:00Z"
}
```

**约束 / Constraints:**

| 约束 / Constraint | 说明 / Description |
|:---|:---|
| 域名权限 / Domain permission | 域名必须在 API Key 允许范围内 / Domain must be in API key scope |
| 域名状态 / Domain status | 域名必须 `isActive: true` / Domain must be active |
| 密码长度 / Password length | 最少 6 个字符 / Minimum 6 characters |
| 地址唯一性 / Uniqueness | 地址不可重复（409 Conflict）/ Must be unique |
| 用户名长度 / Username length | 至少 3 个字符 / Minimum 3 characters |
| 存活时间 / TTL | 7 天后自动过期删除 / Auto-expires after 7 days |
| Key+域名限速 / Key+Domain RPM | 受 `domainLimits` 速率限制 / Subject to `domainLimits` rate limit |

### 7.3 生成随机地址（不创建账号）/ Generate Random Addresses (Without Creating)

```bash
curl -s "http://localhost:8080/addresses/random?domain=example.com&count=5" \
  -H "Authorization: Bearer sk_your_key_here" | jq
```

**响应 / Response** `200 OK`:

```json
{
  "addresses": [
    "michael.zhang92@example.com",
    "nightwolf_77@example.com",
    "li.wei@example.com",
    "coolpanda2001@example.com",
    "techcorp.sales@example.com"
  ]
}
```

| 参数 / Parameter | 默认 / Default | 范围 / Range | 说明 / Description |
|:---|:---|:---|:---|
| `domain` | — (必填 / required) | — | 目标域名 / Target domain |
| `count` | 5 | 1-50 | 生成数量 / Number to generate |

### 7.4 获取账号信息 / Get Account

```bash
curl -s http://localhost:8080/accounts/6789abcdef012345abcdef02 \
  -H "Authorization: Bearer <jwt_token>" | jq
```

### 7.5 获取当前用户 / Get Current User

```bash
curl -s http://localhost:8080/me \
  -H "Authorization: Bearer <jwt_token>" | jq
```

### 7.6 删除账号 / Delete Account

```bash
curl -s -X DELETE http://localhost:8080/accounts/6789abcdef012345abcdef02 \
  -H "Authorization: Bearer <jwt_token>"
```

**响应 / Response:** `204 No Content`

**级联操作 / Cascade:**
1. 从 Redis 移除地址缓存 / Remove address from Redis
2. 硬删除所有邮件 / Hard-delete all messages
3. 删除所有附件 / Delete all attachments from MinIO
4. 删除账号记录 / Delete account record

---

## 8. JWT 认证 / Authentication

### 8.1 获取 JWT 令牌 / Get JWT Token

```bash
curl -s -X POST http://localhost:8080/token \
  -H "Authorization: Bearer sk_your_key_here" \
  -H "Content-Type: application/json" \
  -d '{
    "address": "testuser@example.com",
    "password": "mypassword123"
  }' | jq
```

**响应 / Response** `200 OK`:

```json
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "id": "6789abcdef012345abcdef02"
}
```

### 8.2 使用令牌 / Using the Token

获取 JWT 后，认证接口只需携带 JWT 即可（JWT 用户自动限定域名范围）：

After obtaining a JWT, authenticated endpoints only need the JWT (JWT users are auto-scoped to their domain):

```bash
# JWT 认证（已自动限定域名范围）/ JWT auth (auto-scoped to domain)
Authorization: Bearer eyJhbGciOiJIUzI1NiIs...
```

> **注意 / Note**: 使用 JWT 认证时不需要同时传递 API Key。JWT 中的邮箱域名会自动成为访问范围。API Key 和 JWT 不能同时在一个请求中使用（它们共享同一个 Authorization 头）。
>
> When using JWT authentication, you don't need to also pass an API key. The JWT's email domain automatically becomes the access scope. API Key and JWT cannot be used simultaneously in one request (they share the same Authorization header).

### 8.3 令牌属性 / Token Properties

| 属性 / Property | 值 / Value |
|:---|:---|
| 算法 / Algorithm | HMAC-SHA256 |
| 有效期 / Expiry | 1 小时（可配置）/ 1 hour (configurable) |
| 载荷 / Claims | `accountId`, `address`, `exp`, `iat`, `nbf` |

---

## 9. 邮件操作 / Messages

### 9.1 获取邮件列表 / List Messages

```bash
curl -s "http://localhost:8080/messages?page=1&itemsPerPage=20" \
  -H "Authorization: Bearer <jwt_token>" | jq
```

列表响应不包含 `text`、`html`、`rawMessage` 等大字段以提升性能。

List responses exclude `text`, `html`, `rawMessage` for performance.

#### 高性能 cursor 分页（推荐）/ High-Performance Cursor Pagination (Recommended)

除了 `page/itemsPerPage` 的传统分页，MailAPI 还支持 **seek（cursor）分页** 来避免深分页 `skip` 的性能问题：

- 请求参数：`cursor`（推荐）或兼容别名 `after`
- 响应字段：`nextCursor`（服务端生成的下一页游标，推荐直接使用）

`nextCursor` 形如：`<unix_ms>.<messageId>`（例如 `1710000000123.507f1f77bcf86cd799439011`）。

```bash
# 第一页（不带 cursor）/ First page
curl -s "http://localhost:8080/messages?itemsPerPage=20" \
  -H "Authorization: Bearer <jwt_token>" | jq

# 下一页：把上一页响应的 nextCursor 作为 cursor 传回 / Next page
curl -s "http://localhost:8080/messages?cursor=<nextCursor>&itemsPerPage=20" \
  -H "Authorization: Bearer <jwt_token>" | jq
```

兼容说明 / Compatibility:

- 仍支持旧写法：`after=<messageId>`（仅传 messageId）。但这种写法服务端需要额外查询一次 `createdAt` 来构造 seek 条件，性能略差。
- 推荐使用服务端返回的 `nextCursor`，以减少一次 MongoDB 查询。

#### seen 过滤 / Filter by seen

```bash
# 仅看已读 / Seen only
curl -s "http://localhost:8080/messages?seen=true&itemsPerPage=20" \
  -H "Authorization: Bearer <jwt_token>" | jq

# 仅看未读 / Unseen only（也可与 cursor 分页组合使用）
curl -s "http://localhost:8080/messages?seen=false&cursor=<nextCursor>&itemsPerPage=20" \
  -H "Authorization: Bearer <jwt_token>" | jq
```

### 9.2 获取邮件详情 / Get Message Detail

```bash
curl -s http://localhost:8080/messages/6789abcdef012345abcdef10 \
  -H "Authorization: Bearer <jwt_token>" | jq
```

包含完整的 `text` 和 `html` 正文 / Includes full `text` and `html` body.

### 9.3 标记已读 / Mark as Read

```bash
curl -s -X PATCH http://localhost:8080/messages/6789abcdef012345abcdef10 \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"seen": true}' | jq
```

### 9.4 长期保留（keep）/ Long-term Retention (keep)

默认情况下邮件会按 `message.ttl` 自动过期删除（TTL）。如需让某封邮件长期保留，可设置 `keep=true`：

```bash
curl -s -X PATCH http://localhost:8080/messages/6789abcdef012345abcdef10 \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"keep": true}' | jq
```

恢复默认 TTL 行为（允许其过期）：

```bash
curl -s -X PATCH http://localhost:8080/messages/6789abcdef012345abcdef10 \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"keep": false}' | jq
```

注意 / Note:

- `keep` 参数默认不允许使用（防止被滥用导致无限存储增长）。
- 需要在 **API 服务进程** 显式开启环境变量：
  - `MAILAPI_API_ALLOW_MESSAGE_KEEP=1`（优先）
  - 或 `MAILAPI_ALLOW_MESSAGE_KEEP=1`
- 若未开启，带 `keep` 的请求会返回 `403`。

### 9.5 删除邮件 / Delete Message

```bash
curl -s -X DELETE http://localhost:8080/messages/6789abcdef012345abcdef10 \
  -H "Authorization: Bearer <jwt_token>"
```

### 9.6 批量更新 flags（seen/keep）/ Bulk Update Flags (seen/keep)

按 ids 批量更新 / Update by IDs:

```bash
curl -s -X PATCH http://localhost:8080/messages \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"ids":["6789abcdef012345abcdef10","6789abcdef012345abcdef11"],"seen":true}' | jq
```

按账号全量更新 / Update all messages of the account:

```bash
curl -s -X PATCH http://localhost:8080/messages \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"all":true,"seen":false}' | jq
```

返回 / Response:

```json
{"modified": 2}
```

注意 / Note:

- `keep` 同样支持批量更新，但受 `MAILAPI_API_ALLOW_MESSAGE_KEEP=1` 控制，未开启会返回 `403`。
- `ids` 模式下最多 2000 个 id；`all=true` 时必须不传 `ids`。

### 9.7 按账号批量删除（软删）/ Bulk Delete by Account (Soft delete)

删除该账号下的消息（可选按 seen 过滤），适合“一键清空收件箱”。默认上限 5000 条，可用 `limit` 调整（最大 20000）。删除后会尝试回收 `used` 配额，并后台纠偏一次。

```bash
curl -s -X DELETE "http://localhost:8080/messages?seen=true&limit=5000" \
  -H "Authorization: Bearer <jwt_token>" | jq
```

返回 / Response:

```json
{"deleted": 3, "totalSize": 12345, "truncated": false, "seenFilter": true}
```

### 9.8 按 ids 批量删除（软删）/ Bulk Delete by IDs (Soft delete)

```bash
curl -s -X POST http://localhost:8080/messages/bulk-delete \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Content-Type: application/json" \
  -d '{"ids":["6789abcdef012345abcdef10","6789abcdef012345abcdef11"]}' | jq
```

返回 / Response:

```json
{"deleted": 2, "totalSize": 12345}
```

---

## 10. 附件下载 / Attachments

```bash
curl -s -O -J \
  http://localhost:8080/messages/MSG_ID/attachments/ATT_ID \
  -H "Authorization: Bearer <jwt_token>"
```

---

## 11. 原始邮件下载 / Raw Email Download

```bash
curl -s -O -J \
  http://localhost:8080/messages/MSG_ID/download \
  -H "Authorization: Bearer <jwt_token>"
```

下载 RFC 822 格式的 `.eml` 文件，可用 Thunderbird / Outlook 打开。

Downloads the raw `.eml` file (RFC 822), viewable in Thunderbird / Outlook.

---

## 12. 实时推送 / Real-time SSE

```bash
curl -s -N http://localhost:8080/sse \
  -H "Authorization: Bearer <jwt_token>" \
  -H "Accept: text/event-stream"
```

**事件格式 / Event Format:**

```
event: message
data: {"@type":"Message","id":"...","subject":"New Email","from":{"name":"Bob","address":"bob@gmail.com"},"intro":"Hey...","seen":false}
```

### JavaScript 示例 / JavaScript Example

```javascript
// 使用 fetch API（原生 EventSource 不支持自定义 Header）
// Using fetch API (native EventSource doesn't support custom headers)
async function listenSSE(jwtToken) {
  const response = await fetch("http://localhost:8080/sse", {
    headers: {
      "Authorization": `Bearer ${jwtToken}`,
      "Accept": "text/event-stream"
    }
  });

  const reader = response.body.getReader();
  const decoder = new TextDecoder();

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    const text = decoder.decode(value);
    for (const line of text.split("\n")) {
      if (line.startsWith("data: ")) {
        const data = JSON.parse(line.slice(6));
        console.log("New email:", data.subject, "from", data.from.address);
      }
    }
  }
}
```

---

## 13. 分页 / Pagination

| 参数 / Parameter | 默认值 / Default | 说明 / Notes |
|:---|:---|:---|
| `itemsPerPage` | 30 | 1 - 100 |
| `page` | 1 | 传统分页：>= 1（不推荐深分页） |
| `cursor` | (empty) | seek 分页（推荐）：使用响应的 `nextCursor` |
| `after` | (empty) | `cursor` 的兼容别名（建议改用 `cursor`） |

```bash
curl -s "http://localhost:8080/messages?page=2&itemsPerPage=10" \
  -H "Authorization: Bearer <jwt_token>" | jq
```

seek（cursor）分页示例 / seek (cursor) example:

```bash
curl -s "http://localhost:8080/messages?cursor=<nextCursor>&itemsPerPage=10" \
  -H "Authorization: Bearer <jwt_token>" | jq
```

说明 / Notes:

- 当提供 `cursor/after` 时，服务端会使用 seek 分页（不会使用 `skip`），更适合高并发与大数据量。
- 推荐直接使用服务端返回的 `nextCursor`，避免额外数据库查询。

---

## 14. 错误处理 / Error Handling

### 统一格式 / Unified Format

```json
{"code": 400, "message": "human-readable description"}
```

### 状态码速查 / Status Code Reference

| 码 / Code | 含义 / Meaning | 场景 / Scenario |
|:---|:---|:---|
| `200` | OK | 查询/更新成功 / Query/update success |
| `201` | Created | 账号创建成功 / Account created |
| `204` | No Content | 删除成功 / Deletion success |
| `400` | Bad Request | 参数错误、域名不可用 / Bad params, domain unavailable |
| `401` | Unauthorized | API Key 缺失/无效、JWT 过期 / Missing/invalid key, expired JWT |
| `403` | Forbidden | 域名不在 Key 范围、访问他人资源 / Domain not in scope, other's resource |
| `404` | Not Found | 资源不存在 / Resource not found |
| `409` | Conflict | 地址已注册 / Address already registered |
| `429` | Too Many Requests | 速率限制（三层中任一层触发）/ Rate limited (any of the 3 layers) |
| `500` | Internal Error | 后端异常 / Backend failure |

---

## 15. 速率限制 / Rate Limiting

### 三层限制体系 / Three-Layer System

| 层级 / Layer | 限制 / Limit | 配置项 / Config | Redis Key 模式 |
|:---|:---|:---|:---|
| 全局 IP / Global per-IP | 可配置（默认 100/min）/ Configurable (default 100/min) | `rateLimit.global` | `rl:<ip>` |
| API Key / Per-API-Key | 每 Key 可配置 / Per-key configurable | `apiKeys[].rpmLimit` | `rl:key:<apiKey>` |
| Key+域名 / Per-Key-Per-Domain | 每 Key 每域名可配置 / Per-key-per-domain configurable | `apiKeys[].domainLimits` | `rl:key:<apiKey>:d:<domain>` |
| SMTP / SMTP Layer | 100 连接/分钟/IP / 100 conn/min/IP | — | `smtp_rl:<ip>` |

### 限制触发顺序 / Evaluation Order

```
请求到达 / Request arrives
    │
    ├─▶ 全局 IP 限制 / Global IP limit
    │   └─ 429 "rate limit exceeded"
    │
    ├─▶ API Key 限制（如配置了 rpmLimit）/ Per-key limit (if rpmLimit configured)
    │   └─ 429 "API key rate limit exceeded"
    │
    └─▶ Key+域名限制（由 handler 在操作域名时检查）/ Per-key-per-domain limit (checked by handler)
        └─ 429 "domain rate limit exceeded for this API key"
```

### 最佳实践 / Best Practices

- 使用 SSE 替代轮询 / Use SSE instead of polling
- 缓存 `/domains` 响应 / Cache `/domains` responses
- 避免高频调用列表接口 / Avoid high-frequency list calls
- 对高频 Key 设置合理的 `rpmLimit` / Set reasonable `rpmLimit` for high-traffic keys
- 对敏感域名设置 `domainLimits` 精细限流 / Use `domainLimits` for fine-grained control on sensitive domains

---

## 16. 邮箱前缀生成 / Email Prefix Generation

### 概述 / Overview

系统内置智能邮箱前缀生成器（`internal/prefix/`），生成高度仿真的人类邮箱前缀，支持三大类别：

Built-in intelligent email prefix generator (`internal/prefix/`), producing highly realistic human-like email prefixes across three categories:

### 生成类别 / Categories

| 类别 / Category | 权重 / Weight | 模式 / Patterns | 示例 / Examples |
|:---|:---|:---|:---|
| 个人姓名 / Personal | 55% | `first.last`, `flast`, `first_last`, `last.first`, etc. (15 种模式) | `john.smith`, `jdoe92`, `zhang_wei`, `garcia.m` |
| 昵称 / Nickname | 30% | `nick+suffix`, `adj+noun`, `adj_noun`, `adj.noun`, etc. (6 种模式) | `cooldragon88`, `happy_panda`, `night.wolf2001` |
| 商务 / Business | 15% | `biz`, `biz.dept`, `biz_dept`, `biz+num` (4 种模式) | `techcorp.sales`, `support_hr`, `marketing42` |

### 后缀类型 / Suffix Types

个人姓名模式支持 6 种后缀：

Personal name patterns support 6 suffix types:

| 后缀 / Suffix | 概率 / Probability | 示例 / Example |
|:---|:---|:---|
| 无后缀 / None | 30% | `john.smith` |
| 1-2 位数字 / 1-2 digits | 20% | `john.smith42` |
| 2 位出生年 / 2-digit birth year | 15% | `john.smith92` |
| 4 位出生年 / 4-digit birth year | 10% | `john.smith1992` |
| 3 位数字 / 3 digits | 13% | `john.smith456` |
| 4 位数字 / 4 digits | 12% | `john.smith6789` |

### 唯一性容量 / Uniqueness Capacity

- 个人姓名：~420 名 × ~420 姓 × 15 模式 × ~1200 后缀 ≈ **32 亿** / ~420 first × ~420 last × 15 patterns × ~1200 suffixes ≈ **3.2 billion**
- 昵称：~170 昵称 × 8 模式 × ~1200 后缀 + 形容词×名词组合 ≈ **18 亿** / nicknames × patterns × suffixes + adj×noun combos ≈ **1.8 billion**
- 总计：单域名超 **50 亿** 种独立前缀 / Total: Over **5 billion** unique prefixes per domain

### 名称数据 / Name Data

国际化多元姓名数据库，全部小写 ASCII：

Internationally diverse name database, all lowercase ASCII:

- 英语、中文拼音、西班牙语、法语、德语、日语罗马字、韩语罗马字等
- English, Chinese pinyin, Spanish, French, German, Japanese romaji, Korean romanization, etc.

### API 使用 / API Usage

```bash
# 生成 5 个随机地址（不创建账号）/ Generate 5 random addresses (no account creation)
curl "http://localhost:8080/addresses/random?domain=example.com&count=5" \
  -H "Authorization: Bearer sk_xxx"

# 自动生成前缀并创建账号 / Auto-generate prefix and create account
curl -X POST http://localhost:8080/accounts \
  -H "Authorization: Bearer sk_xxx" \
  -H "Content-Type: application/json" \
  -d '{"domain": "example.com", "password": "secret123"}'
```

---

## 17. 多 IP 配置 / Multi-IP Setup

### 场景 / Scenario

一台服务器有多个 IP 地址，希望不同域名监听在不同 IP 上：

A server with multiple IPs, where different domains listen on different IPs:

```yaml
domains:
  - domain: "a.example.com"
    isActive: true
    ips: ["192.168.1.100"]
  - domain: "b.example.com"
    isActive: true
    ips: ["192.168.1.101"]
  - domain: "shared.example.com"
    isActive: true
    ips: ["192.168.1.100", "192.168.1.101"]  # 两个 IP 都接收 / both IPs
```

### 效果 / Result

SMTP 服务启动时自动创建两个监听器：
- `192.168.1.100:25` → 接收 `a.example.com` 和 `shared.example.com` 的邮件
- `192.168.1.101:25` → 接收 `b.example.com` 和 `shared.example.com` 的邮件

The SMTP service auto-creates two listeners:
- `192.168.1.100:25` → accepts mail for `a.example.com` and `shared.example.com`
- `192.168.1.101:25` → accepts mail for `b.example.com` and `shared.example.com`

---

## 18. 分布式部署 / Distributed Deployment

### 架构 / Architecture

多台服务器共享同一份配置文件和同一套后端基础设施：

Multiple servers share one config file and the same backend infrastructure:

```yaml
domains:
  - domain: "a.example.com"
    isActive: true
    ips: ["10.0.0.1"]        # 服务器 A / Server A
  - domain: "b.example.com"
    isActive: true
    ips: ["10.0.0.2"]        # 服务器 B / Server B
```

### 部署步骤 / Deploy Steps

1. 所有服务器安装相同的 MailAPI 二进制 / Install same binaries on all servers
2. 分发相同的 `config.yaml` / Distribute the same `config.yaml`
3. 确保所有服务器能访问共享的 MongoDB / Redis / NATS / MinIO
4. 在每台服务器上启动 SMTP + API + Worker

Each server's SMTP process will auto-detect its local IPs and only start matching listeners.

### 配合 API Key / Combined with API Keys

每台服务器的 API 服务都会应用相同的 API Key 规则和速率限制。客户端可以连接到任意 API 节点，域名隔离和限流在所有节点上一致（因为速率限制状态存储在共享的 Redis 中）。

Every server's API service enforces the same API key rules and rate limits. Clients can connect to any API node — domain isolation and rate limiting are consistent across all nodes (since rate limit state is stored in shared Redis).

---

## 19. 完整使用示例 / Complete Examples

### Shell 脚本 / Shell Script

```bash
#!/bin/bash
API="http://localhost:8080"
KEY="sk_your_key_here"
KEY="sk_your_key_here"

# 1. 查看可用域名 / Check available domains
echo "=== Available Domains ==="
DOMAIN=$(curl -s "$API/domains" -H "Authorization: Bearer $KEY" | jq -r '.["hydra:member"][0].domain')
echo "Using domain: $DOMAIN"

# 2. 自动生成前缀创建账号 / Create account with auto-generated prefix
echo -e "\n=== Create Account (Auto-prefix) ==="
ACCOUNT=$(curl -s -X POST "$API/accounts" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d "{\"domain\": \"$DOMAIN\", \"password\": \"secret123\"}")
ACCOUNT_ID=$(echo "$ACCOUNT" | jq -r '.id')
ADDRESS=$(echo "$ACCOUNT" | jq -r '.address')
echo "Account ID: $ACCOUNT_ID"
echo "Address: $ADDRESS"

# 3. 登录 / Login
echo -e "\n=== Login ==="
TOKEN=$(curl -s -X POST "$API/token" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d "{\"address\": \"$ADDRESS\", \"password\": \"secret123\"}" | jq -r '.token')
echo "Token: ${TOKEN:0:50}..."

# 4. 查看当前用户 / View current user
echo -e "\n=== Current User ==="
curl -s "$API/me" -H "Authorization: Bearer $TOKEN" | jq

# 5. 获取邮件列表 / List messages
echo -e "\n=== Messages ==="
curl -s "$API/messages?page=1&itemsPerPage=10" \
  -H "Authorization: Bearer $TOKEN" | jq '.["hydra:member"]'

# 6. 生成随机地址（不创建账号）/ Generate random addresses
echo -e "\n=== Random Addresses ==="
curl -s "$API/addresses/random?domain=$DOMAIN&count=5" \
  -H "Authorization: Bearer $KEY" | jq

# 7. 删除账号 / Delete account
echo -e "\n=== Delete Account ==="
curl -s -X DELETE "$API/accounts/$ACCOUNT_ID" \
  -H "Authorization: Bearer $TOKEN" -w "HTTP %{http_code}\n"
```

### Python 示例 / Python Example

```python
import requests

API = "http://localhost:8080"
KEY = "sk_your_key_here"
AUTH = {"Authorization": f"Bearer {KEY}"}

# 1. 获取域名 / Get domains
domains = requests.get(f"{API}/domains", headers=AUTH).json()
domain = domains["hydra:member"][0]["domain"]
print(f"Using domain: {domain}")

# 2. 生成随机地址 / Generate random addresses
random = requests.get(f"{API}/addresses/random?domain={domain}&count=3", headers=AUTH).json()
print(f"Random addresses: {random['addresses']}")

# 3. 自动生成前缀创建账号 / Create account with auto-generated prefix
account = requests.post(f"{API}/accounts", headers=AUTH, json={
    "domain": domain,
    "password": "secret123"
}).json()
print(f"Account: {account['id']} ({account['address']})")

# 4. 获取 JWT / Get JWT
token = requests.post(f"{API}/token", headers=AUTH, json={
    "address": account["address"],
    "password": "secret123"
}).json()["token"]

jwt_auth = {"Authorization": f"Bearer {token}"}

# 5. 获取邮件 / Get messages
messages = requests.get(f"{API}/messages", headers=jwt_auth).json()
print(f"Messages: {messages['hydra:totalItems']}")

# 6. SSE 实时监听 / SSE real-time
import sseclient
response = requests.get(f"{API}/sse", headers=jwt_auth, stream=True)
client = sseclient.SSEClient(response)
for event in client.events():
    print(f"New email: {event.data}")
```

---

## 20. 生产部署 / Production Deployment

### 必须修改的配置 / Must-Change Settings

| 配置项 / Setting | 说明 / Description |
|:---|:---|
| `jwt.secret` | 使用强随机字符串 / Use strong random string |
| `apiKeys[].key` | 默认使用 `sk_` 前缀（兼容 `dk_`），用 `./mailapi.sh genkey` 生成 / Use `sk_` prefix by default (`dk_` also accepted), generate with `genkey` |
| `minio.accessKey/secretKey` | 修改为安全凭证 / Change to secure credentials |

### 基础设施建议 / Infrastructure Recommendations

| 组件 / Component | 建议 / Recommendation |
|:---|:---|
| MongoDB | 副本集或托管服务 / Replica set or managed |
| Redis | 集群或托管服务（共享速率限制状态）/ Cluster or managed (shared rate limit state) |
| NATS | 3 节点集群 / 3-node cluster |
| MinIO | 多节点纠删码 / Multi-node erasure coding |

### 网络 / Network

- SMTP 端口 25 需要云服务商开放 / Port 25 must be allowed by cloud provider
- SMTP 可选开启 STARTTLS（RFC 3207）：通过 `server.smtp.tls.enabled=true` 启用，并配置 `certFile/keyFile`。如需强制对方先加密再投递，可设置 `requireTLS=true`（不支持 STARTTLS 的客户端会被拒绝，返回 530）。
- API 层在 Nginx/HAProxy 后终止 TLS / Terminate TLS at reverse proxy
- 生产环境所有 Docker 端口应限制为内网访问 / Bind Docker ports to internal IPs

示例 / Example:

```yaml
server:
  smtp:
    tls:
      enabled: true
      certFile: "/opt/mailapi/certs/smtp.crt"
      keyFile: "/opt/mailapi/certs/smtp.key"
      requireTLS: false
      minVersion: "1.2"
```

验证 / Verify:

```bash
openssl s_client -starttls smtp -connect mail.example.com:25 -servername mail.example.com
```

### 监控 / Monitoring

- 接入 Prometheus + Grafana / Integrate Prometheus + Grafana
- 监控各服务进程存活 / Monitor service process health
- 监控队列积压 / Monitor NATS queue depth
- 监控 MongoDB 连接数和慢查询 / Monitor MongoDB connections and slow queries
- 监控 Redis 中速率限制 Key 数量 / Monitor rate limit key count in Redis
