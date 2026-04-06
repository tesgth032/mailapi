# MailAPI — 高并发分布式临时邮件后端服务
# MailAPI — High-Concurrency Distributed Temporary Email Backend

---

## 目录 / Table of Contents

1. [项目概述 / Project Overview](#1-项目概述--project-overview)
2. [技术栈 / Technology Stack](#2-技术栈--technology-stack)
3. [项目结构 / Project Structure](#3-项目结构--project-structure)
4. [系统架构 / System Architecture](#4-系统架构--system-architecture)
5. [数据模型 / Data Models](#5-数据模型--data-models)
6. [配置系统 / Configuration System](#6-配置系统--configuration-system)
7. [API Key 鉴权体系 / API Key Authentication](#7-api-key-鉴权体系--api-key-authentication)
8. [API 接口 / API Endpoints](#8-api-接口--api-endpoints)
9. [SMTP 服务 / SMTP Service](#9-smtp-服务--smtp-service)
10. [Worker 服务 / Worker Service](#10-worker-服务--worker-service)
11. [JWT 认证与安全 / JWT Authentication & Security](#11-jwt-认证与安全--jwt-authentication--security)
12. [缓存层 / Cache Layer](#12-缓存层--cache-layer)
13. [消息队列 / Message Queue](#13-消息队列--message-queue)
14. [对象存储 / Object Storage](#14-对象存储--object-storage)
15. [数据库持久化 / Database Persistence](#15-数据库持久化--database-persistence)
16. [实时推送 / Real-time Push (SSE)](#16-实时推送--real-time-push-sse)
17. [邮箱前缀生成器 / Email Prefix Generator](#17-邮箱前缀生成器--email-prefix-generator)
18. [多 IP 与分布式部署 / Multi-IP & Distributed Deployment](#18-多-ip-与分布式部署--multi-ip--distributed-deployment)
19. [管理脚本 / Management Script](#19-管理脚本--management-script)
20. [防御性工程 / Defense Engineering](#20-防御性工程--defense-engineering)
21. [测试体系 / Test Suite](#21-测试体系--test-suite)
22. [部署说明 / Deployment](#22-部署说明--deployment)

---

## 1. 项目概述 / Project Overview

**中文：**

MailAPI 是一个对标 `mail.tm` 的高并发、可分布式部署的临时邮件后端服务，兼容 [DuckMail API 标准](https://www.duckmail.sbs/zh/api-docs)。采用微服务架构与事件驱动设计（EDA），通过 SMTP 接收邮件、异步解析、实时推送的流水线处理模式，支撑千万级每日邮件吞吐量。

系统由三个独立可部署的无状态服务组成：
- **API 服务**：RESTful API，提供账号管理、邮件查询、实时推送（SSE），支持 API Key（默认 `sk_`，兼容 `dk_`）+ JWT 统一 Bearer 鉴权
- **SMTP 服务**：接收外部发来的邮件，支持多 IP 监听和按域名过滤，做快速校验后入队
- **Worker 服务**：从队列消费原始邮件，解析 MIME、存储附件、写入数据库

核心增强特性：
- **DuckMail 兼容 API Key**：默认使用 `sk_` 前缀（兼容 `dk_`），通过统一 `Authorization: Bearer` 头传递
- **三层速率限制**：全局 IP 限制 + 每 API Key 限制 + 每 API Key 每域名限制
- **人类化邮箱前缀生成**：智能生成仿真人类商用/昵称邮箱前缀，单域名超 50 亿种独立组合
- **配置化域名管理**：直接在 config.yaml 中定义多个域名，启动时自动同步到 MongoDB
- **多 IP 绑定**：不同域名可绑定到不同 IP 的 SMTP 监听器
- **多服务器分布式**：各节点自动检测本机 IP，仅启动自己负责的监听器
- **一键管理脚本**：安装、配置、启停、监控一站式管理

**English:**

MailAPI is a high-concurrency, distributed temporary email backend service benchmarked against `mail.tm`, compatible with the [DuckMail API standard](https://www.duckmail.sbs/zh/api-docs). It adopts a microservices architecture with Event-Driven Architecture (EDA), processing emails through a pipeline of SMTP reception, asynchronous parsing, and real-time push — capable of handling tens of millions of daily email throughput.

The system comprises three independently deployable, stateless services:
- **API Service**: RESTful API providing account management, message queries, and real-time push (SSE), with API keys (default `sk_`, `dk_` also accepted) + JWT unified Bearer authentication
- **SMTP Service**: Receives inbound emails with multi-IP listener support and per-domain filtering, performs fast validation and enqueues
- **Worker Service**: Consumes raw emails from the queue, parses MIME, stores attachments, and writes to the database

Core enhanced features:
- **DuckMail-Compatible API Keys**: Uses `sk_` prefix by default (`dk_` also accepted) via unified `Authorization: Bearer` header
- **Three-Layer Rate Limiting**: Global per-IP + per-API-key + per-API-key-per-domain limits
- **Human-Like Email Prefix Generation**: Intelligent realistic human-like email prefix generation, 5+ billion unique combinations per domain
- **Config-based Domain Management**: Define multiple domains directly in config.yaml, auto-synced to MongoDB on startup
- **Multi-IP Binding**: Different domains can bind to different IP-based SMTP listeners
- **Multi-Server Distributed**: Each node auto-detects its local IPs and only starts matching listeners
- **One-click Management Script**: Install, configure, start/stop, and monitor in one place

---

## 2. 技术栈 / Technology Stack

| 组件 / Component | 技术选型 / Technology | 版本 / Version | 用途说明 / Purpose |
|:---|:---|:---|:---|
| **编程语言 / Language** | Go | 1.26.1 | 高并发网络 I/O，Goroutine 处理海量长连接 / High-concurrency network I/O with goroutines |
| **Web 框架 / Web Framework** | Gin | v1.12.0 | REST API 服务器 / REST API server |
| **SMTP 库 / SMTP Library** | emersion/go-smtp | v0.24.0 | SMTP 协议实现，多监听器 / SMTP protocol, multi-listener |
| **MIME 解析 / MIME Parser** | emersion/go-message | v0.18.2 | RFC 822 邮件流式解析 / RFC 822 email stream parsing |
| **认证 / Authentication** | golang-jwt/jwt | v5.3.1 | JWT 令牌签发与验证 / JWT token generation & validation |
| **数据库 / Database** | MongoDB | v2.5.0 (driver) | 文档存储，TTL 索引自动过期 / Document storage with TTL auto-expiry |
| **缓存 / Cache** | Redis | v9.18.0 (driver) | 地址缓存、Pub/Sub、三层速率限制 / Address cache, Pub/Sub, three-layer rate limiting |
| **消息队列 / Message Queue** | NATS JetStream | v1.50.0 | 异步邮件处理解耦 / Async email processing decoupling |
| **对象存储 / Object Storage** | MinIO (S3 兼容) | v7.0.99 | 附件分布式存储 / Distributed attachment storage |
| **密码加密 / Password Crypto** | golang.org/x/crypto | v0.49.0 | Bcrypt 密码哈希 / Bcrypt password hashing |
| **配置解析 / Config Parser** | gopkg.in/yaml.v3 | v3.0.1 | YAML 配置文件解析 / YAML config file parsing |

---

## 3. 项目结构 / Project Structure

```
mailapi/
├── cmd/                                  # 入口文件 / Entry points
│   ├── api/main.go                       # REST API 服务入口（含域名同步）/ API entry (with domain sync)
│   ├── smtp/main.go                      # SMTP 服务入口（多监听器）/ SMTP entry (multi-listener)
│   └── worker/main.go                    # Worker 服务入口 / Worker entry
│
├── internal/                             # 内部包 / Internal packages
│   ├── model/model.go                    # 数据模型定义 / Data model definitions
│   │
│   ├── config/                           # 配置管理 / Configuration management
│   │   ├── config.go                     #   域名·sk_/dk_ API Key·多IP·速率限制 / Domains, sk_/dk_ keys, IPs, rate limits
│   │   └── config_test.go               #   配置测试 / Config tests
│   │
│   ├── auth/                             # JWT 认证 / JWT authentication
│   │   ├── auth.go                       #   令牌签发与验证 / Token generation & validation
│   │   └── auth_test.go                 #   认证测试 / Auth tests
│   │
│   ├── cache/                            # Redis 缓存层 / Redis cache layer
│   │   ├── cache.go                      #   缓存实现（含三层限流）/ Cache (with 3-layer rate limiting)
│   │   └── interface.go                 #   接口定义 / Interface definition
│   │
│   ├── queue/                            # NATS 消息队列 / NATS message queue
│   │   ├── queue.go                      #   队列实现 / Queue implementation
│   │   └── interface.go                 #   接口定义 / Interface definition
│   │
│   ├── storage/                          # MinIO 对象存储 / MinIO object storage
│   │   ├── storage.go                    #   存储实现 / Storage implementation
│   │   └── interface.go                 #   接口定义 / Interface definition
│   │
│   ├── store/                            # MongoDB 数据访问层 / MongoDB data access layer
│   │   ├── store.go                      #   数据操作（含 SyncDomains）/ CRUD (with SyncDomains)
│   │   └── interface.go                 #   接口定义 / Interface definition
│   │
│   ├── handler/                          # HTTP 请求处理器 / HTTP request handlers
│   │   ├── handler.go                    #   路由注册（统一 Bearer 鉴权）/ Route setup (unified Bearer auth)
│   │   ├── account.go                    #   账号增删查（域名隔离·自动前缀）/ Account CRUD (domain-scoped, auto-prefix)
│   │   ├── token.go                      #   登录认证 / Login auth
│   │   ├── domain.go                     #   域名查询（按 Key 过滤）/ Domains (filtered by key)
│   │   ├── message.go                    #   邮件增删改查 / Message CRUD & download
│   │   ├── sse.go                        #   SSE 实时推送 / SSE real-time push
│   │   └── *_test.go                     #   各处理器测试 / Handler tests
│   │
│   ├── smtp/                             # SMTP 多监听器实现 / Multi-listener SMTP
│   │   ├── server.go                     #   多IP绑定·域名过滤·本机IP检测 / Multi-IP, domain filter, local IP detection
│   │   └── server_test.go               #   SMTP 测试 / SMTP tests
│   │
│   ├── worker/                           # 邮件解析工作者 / Email parsing worker
│   │   ├── worker.go                     #   MIME 解析与持久化 / MIME parsing & persistence
│   │   └── worker_test.go               #   Worker 测试 / Worker tests
│   │
│   ├── prefix/                           # 人类化邮箱前缀生成器 / Human-like email prefix generator
│   │   ├── generator.go                  #   生成器核心逻辑（三大类别·15种模式）/ Generator core (3 categories, 15 patterns)
│   │   ├── data.go                       #   国际化姓名数据库 / International name database
│   │   └── generator_test.go            #   生成器测试 / Generator tests
│   │
│   └── middleware/                        # HTTP 中间件 / HTTP middleware
│       ├── middleware.go                 #   统一 Bearer 鉴权 + 域名隔离 + 三层限流
│       └── middleware_test.go            #   中间件测试 / Middleware tests
│
├── config.yaml                           # 配置文件（域名·sk_/dk_ Key·多IP·限流）/ Config
├── mailapi.sh                            # 一键管理脚本 / One-click management script
├── go.mod                                # Go 模块定义 / Go module definition
└── go.sum                                # 依赖校验和 / Dependency checksums
```

---

## 4. 系统架构 / System Architecture

### 架构拓扑 / Architecture Topology

```
[外部发件方 / External Senders]                [前端/API 调用方 / Frontend/API Clients]
  (Gmail, QQ Mail, etc.)                               (Web, Mobile)
         │                                                    │
    TCP Port 25                                         HTTPS Port 8080
    (多 IP 监听 / Multi-IP)                                   │
         │                                             ┌──────┴──────┐
         ▼                                             ▼             ▼
  +--------------+                            +-------------------+
  | SMTP 多监听器 |                            | API Service       |
  | Multi-listener|                            | Bearer 统一鉴权    |
  | 域名过滤      |                            | sk_/dk_ Key + JWT |
  +--------------+                            +-------------------+
         │                                        │         ▲
         │ 原始 EML / Raw EML                    │ 读写    │ Pub/Sub
         ▼                                        ▼         │
  +------------------+                    +-------------------+
  | NATS JetStream   |                    | Redis             |
  | 消息队列          |                    | 缓存 / 事件 / 限流 |
  +------------------+                    +-------------------+
         │                                          ▲
         │ 消费 / Consume                          │
         ▼                                          │
  +------------------+                    +-------------------+
  | Worker Cluster   | ───────────────▶  | MongoDB            |
  | MIME 解析         |    写入 / Write   | 账号/邮件/域名数据  |
  +------------------+                    +-------------------+
         │
         │ 上传 / Upload
         ▼
  +------------------+
  | MinIO (S3)       |
  | 附件/原始 .eml 存储 |
  +------------------+
```

### 核心数据流 / Core Data Flow

**中文：**

1. **邮件接收**：外部邮件服务器通过 SMTP 协议将邮件发送到 SMTP 服务。多监听器模式下，不同 IP 接收不同域名的邮件
2. **域名过滤**：每个 SMTP 监听器知道自己负责哪些域名，在 `RCPT TO` 阶段拒绝不属于本监听器的域名
3. **地址校验**：通过 Redis 微秒级验证收件地址是否存在，拒绝无效地址
4. **异步入队**：校验通过后，原始邮件数据入 NATS JetStream 队列，SMTP 连接立即释放
5. **MIME 解析**：Worker 从队列消费消息，解析 MIME 结构，提取正文和附件
6. **对象存储**：原始 `.eml`（RFC 822）与附件上传到 MinIO（raw 上传失败则回退写入 Mongo `rawMessage`）
7. **数据写入**：邮件元数据与正文（text/html）写入 MongoDB
8. **实时通知**：Worker 通过 Redis Pub/Sub 发布新邮件事件
9. **SSE 推送**：API 服务监听 Redis 频道，通过 SSE 长连接推送到客户端

**English:**

1. **Email Reception**: External mail servers send emails to the SMTP service. In multi-listener mode, different IPs receive emails for different domains
2. **Domain Filtering**: Each SMTP listener knows which domains it serves, rejecting domains not assigned to it during the `RCPT TO` phase
3. **Address Validation**: Microsecond Redis validation checks if the recipient address exists, rejecting invalid addresses
4. **Async Enqueue**: After validation, raw email data is pushed to the NATS JetStream queue, SMTP connection released immediately
5. **MIME Parsing**: Workers consume messages from the queue, parse MIME structures, extract body and attachments
6. **Object Storage**: Raw `.eml` (RFC 822) and attachments are uploaded to MinIO (fallback to Mongo `rawMessage` if raw upload fails)
7. **Data Writing**: Email metadata and body content (text/html) are written to MongoDB
8. **Real-time Notification**: Workers publish new email events via Redis Pub/Sub
9. **SSE Push**: The API service listens on Redis channels and pushes events to clients via SSE

---

## 5. 数据模型 / Data Models

> 源码位置 / Source: `internal/model/model.go`

### 5.1 Domain — 可用域名 / Available Domain

| 字段 / Field | 类型 / Type | 说明 / Description |
|:---|:---|:---|
| `id` | ObjectID | MongoDB 文档 ID / MongoDB document ID |
| `domain` | string | 域名（如 `example.com`）/ Domain name |
| `isActive` | bool | 是否启用 / Whether active |
| `isPrivate` | bool | 是否私有 / Whether private |
| `createdAt` | time.Time | 创建时间 / Creation timestamp |
| `updatedAt` | time.Time | 更新时间 / Update timestamp |

**配置化管理 / Config-based Management:**

域名可直接在 `config.yaml` 的 `domains` 节定义，API 启动时自动同步到 MongoDB（upsert 模式，不会删除手动添加的域名）。

Domains can be defined in the `domains` section of `config.yaml`. On API startup, they are auto-synced to MongoDB (upsert mode — manually added domains are not removed).

### 5.2 Account — 临时邮箱账号 / Temporary Email Account

| 字段 / Field | 类型 / Type | 说明 / Description |
|:---|:---|:---|
| `id` | ObjectID | MongoDB 文档 ID |
| `address` | string | 唯一邮箱地址（可自动生成）/ Unique email address (can be auto-generated) |
| `password` | string | Bcrypt 哈希密码（JSON 不返回）/ Bcrypt hash (omitted from JSON) |
| `quota` | int64 | 存储配额（默认 40MB）/ Storage quota (default 40MB) |
| `used` | int64 | 已使用存储量 / Used storage |
| `isDeleted` | bool | 软删除标记 / Soft delete flag |
| `createdAt` | time.Time | 创建时间（TTL 基准）/ Creation time (TTL base) |
| `updatedAt` | time.Time | 更新时间 / Update timestamp |

### 5.3 Message — 邮件消息 / Email Message

| 字段 / Field | 类型 / Type | 说明 / Description |
|:---|:---|:---|
| `id` | ObjectID | MongoDB 文档 ID |
| `accountId` | ObjectID | 所属账号 ID / Owning account ID |
| `msgid` | string | 邮件 Message-ID 头 / Email Message-ID header |
| `from` | Address | 发件人 / Sender |
| `to` | []Address | 收件人列表 / Recipient list |
| `cc` | []Address | 抄送列表 / CC list |
| `bcc` | []Address | 密送列表 / BCC list |
| `subject` | string | 邮件主题 / Subject |
| `intro` | string | 正文前 100 字符摘要 / First 100 chars of body |
| `text` | string | 纯文本正文 / Plain text body |
| `html` | []string | HTML 正文（可多段）/ HTML body (multiple parts) |
| `hasAttachments` | bool | 是否有附件 / Has attachments flag |
| `attachments` | []Attachment | 附件元数据列表 / Attachment metadata list |
| `size` | int64 | 原始邮件大小 / Raw message size |
| `seen` | bool | 是否已读 / Read status |
| `isDeleted` | bool | 软删除标记 / Soft delete flag |
| `keep` | bool | 长期保留（不受 message TTL 自动过期影响；默认关闭，需 API 进程启用）/ Long-term retention (excluded from message TTL; gated by API env; disabled by default) |
| `rawMessage` | []byte | 原始 RFC 822 邮件（JSON 不返回）/ Raw RFC 822 (omitted from JSON) |
| `createdAt` | time.Time | 创建时间 / Creation timestamp |
| `updatedAt` | time.Time | 更新时间 / Update timestamp |

### 5.4 Attachment — 附件元数据 / Attachment Metadata

| 字段 / Field | 类型 / Type | 说明 / Description |
|:---|:---|:---|
| `id` | string | 附件唯一标识 / Unique attachment ID |
| `filename` | string | 文件名 / Filename |
| `contentType` | string | MIME 类型 / MIME content type |
| `disposition` | string | 内容处置方式 / Content disposition |
| `transferEncoding` | string | 传输编码 / Transfer encoding |
| `size` | int64 | 文件大小（字节）/ File size in bytes |

### 5.5 配置模型 / Configuration Models

**DomainConfig** — 域名配置 / Domain configuration:

| 字段 / Field | 类型 / Type | 说明 / Description |
|:---|:---|:---|
| `domain` | string | 域名 / Domain name |
| `isActive` | bool | 是否启用 / Whether active |
| `isPrivate` | bool | 是否私有 / Whether private |
| `ips` | []string | SMTP 监听 IP 列表（空=所有接口）/ SMTP listener IPs (empty = all interfaces) |

**APIKeyConfig** — API Key 配置 / API key configuration:

| 字段 / Field | 类型 / Type | 说明 / Description |
|:---|:---|:---|
| `key` | string | API 密钥（默认 `sk_` 前缀，兼容 `dk_`）/ API key string (default `sk_`, `dk_` also accepted) |
| `name` | string | 名称 / Descriptive name |
| `domains` | []string | 可访问域名（`["*"]` 为全部）/ Allowed domains (`["*"]` for all) |
| `rpmLimit` | int64 | 每分钟请求数限制（0=不限）/ Requests per minute limit (0=unlimited) |
| `domainLimits` | map[string]int64 | 每域名每分钟请求数限制 / Per-domain RPM overrides |

**RateLimitConfig** — 全局速率限制 / Global rate limiting:

| 字段 / Field | 类型 / Type | 说明 / Description |
|:---|:---|:---|
| `global` | int64 | 全局每 IP 每分钟请求数（0=默认100）/ Global RPM per IP (0=default 100) |

**CreateAccountRequest** — 创建账号请求 / Create account request:

| 字段 / Field | 类型 / Type | 说明 / Description |
|:---|:---|:---|
| `address` | string | 邮箱地址（可选，为空时需提供 domain）/ Email address (optional if domain provided) |
| `domain` | string | 目标域名（提供时自动生成前缀）/ Target domain (auto-generates prefix when provided) |
| `password` | string | 密码（最少 6 位）/ Password (min 6 chars, required) |

---

## 6. 配置系统 / Configuration System

> 源码位置 / Source: `internal/config/config.go`, `config.yaml`

### 配置优先级 / Configuration Priority

1. **硬编码默认值 / Hardcoded Defaults**: `config.go` 中定义的安全默认值
2. **YAML 配置文件 / YAML Config File**: `config.yaml` 覆盖默认值
3. **容错机制 / Fail-Safe**: 配置文件不存在时使用默认值，系统仍可启动

### 完整配置文件（与仓库根目录 config.yaml 一致）/ Full Configuration (same as repo root config.yaml)

```yaml
# =============================================================================
# MailAPI Configuration
# =============================================================================

# -----------------------------------------------------------------------------
# Domains — define email domains directly in config (synced to MongoDB on start)
# Each domain can optionally bind to specific IPs for multi-IP / distributed
# deployment. If 'ips' is empty, SMTP listens on 0.0.0.0 (all interfaces).
# -----------------------------------------------------------------------------
domains:
  - domain: "example.com"
    isActive: true
    isPrivate: false
    ips: []               # listen on all interfaces
#  - domain: "example.org"
#    isActive: true
#    isPrivate: false
#    ips: ["192.168.1.100"] # bind to specific IP
#  - domain: "example.net"
#    isActive: true
#    isPrivate: true
#    ips: ["192.168.1.101"] # different IP for this domain

# -----------------------------------------------------------------------------
# API Keys — default "sk_" prefix (legacy "dk_" also accepted), domain-level auth and isolation
#
# Each key grants access to the listed domains. Use ["*"] for all domains.
# If no API keys are defined, the API runs without key authentication.
# Clients pass the key via: Authorization: Bearer sk_xxx (or dk_xxx)
#
# Rate limiting:
#   rpmLimit       — total requests per minute for this key (0 = unlimited)
#   domainLimits   — per-domain RPM overrides for this key
# -----------------------------------------------------------------------------
apiKeys:
  - key: "sk_change_me_to_a_random_key"
    name: "Admin"
    domains: ["*"]        # access to all domains
    rpmLimit: 0           # unlimited (uses global limit only)
#  - key: "sk_partner_key_here"
#    name: "Partner"
#    domains: ["example.com"]      # access restricted to one domain
#    rpmLimit: 200                 # 200 requests per minute for this key
#    domainLimits:
#      "example.com": 100          # further limit: 100 RPM on example.com

# -----------------------------------------------------------------------------
# Rate Limiting — global limits applied per client IP
# -----------------------------------------------------------------------------
rateLimit:
  global: 100  # requests per minute per IP (0 = default 100, -1 = disable)

# -----------------------------------------------------------------------------
# Server
# -----------------------------------------------------------------------------
server:
  api:
    host: "0.0.0.0"
    port: 8080
    # 可信代理（影响 ClientIP 解析与 IP 限流）。生产环境强烈建议显式配置，避免被伪造 X-Forwarded-For 绕过。
    # 为空表示不覆盖 Gin 默认行为（保持向后兼容）。示例：
    #trustedProxies: ["127.0.0.1", "10.0.0.0/8"]
    # 多 API 风格（dialect）路由：通过 `<dialect>.<baseHost>` 的子域前缀选择不同 API 兼容层。
    # 例如：`cfworker.api.mailapi.com` 使用 cfworker 风格；`duck.api.mailapi.com` 使用 duck 风格。
    # baseHost 为空表示禁用（保持向后兼容：所有请求使用单一默认风格）。
    #baseHost: "api.mailapi.com"
    #defaultDialect: "duck"      # api.mailapi.com 与 localhost/IP 访问时采用的默认风格
    #enabledDialects: ["duck", "cfworker"] # 为空表示不限制（允许所有已注册 dialect）
    #unknownDialect: "reject"    # reject(推荐) 或 fallback（未知 dialect 回退到默认）
    # bcrypt 并发闸门：创建账号/登录会触发 bcrypt（CPU 密集），高并发时建议限制并发避免 CPU 打满导致整体雪崩
    maxConcurrentBcrypt: 0 # 0=自动（按 GOMAXPROCS）；建议 4/8/16 视机器与压测结果调整
    # 访问日志（高并发强烈建议开启采样/仅错误/仅慢请求，否则日志格式化与 IO 会成为 CPU 热点）
    accessLog:
      enabled: true
      sampleEvery: 100     # 1=全量；100=百分之一；建议在压测/生产场景保持 >= 10
      slowThreshold: 500ms # 慢请求阈值（>= 阈值将强制记录）；0s 表示禁用
      errorsOnly: false    # true=仅记录 status>=400 的请求
    # Debug HTTP server（健康检查/指标/pprof）。默认关闭；建议仅绑定 127.0.0.1 并置于防火墙/LB 后。
    # 注意：debug server 不做鉴权，若暴露到公网可能泄露运行时信息。
    debug:
      enabled: false
      host: "127.0.0.1"
      port: 6060
      metrics: true  # GET /metrics（Prometheus）
      pprof: false   # GET /debug/pprof/（强烈建议不要公网暴露）
  smtp:
    port: 25                  # default port for all SMTP listeners
    host: "0.0.0.0"          # fallback host (used only when no domains configured)
    domain: "mail.example.com" # EHLO banner domain
    maxMessageBytes: 20971520 # 20MB
    maxRecipients: 50
    readTimeout: 60s
    writeTimeout: 60s
    # Debug HTTP server（健康检查/指标/pprof）。默认关闭；建议仅绑定 127.0.0.1。
    debug:
      enabled: false
      host: "127.0.0.1"
      port: 6061
      metrics: true
      pprof: false
  # Worker 没有业务监听端口，但可以开启 debug HTTP server 用于 ready/metrics/pprof。
  worker:
    debug:
      enabled: false
      host: "127.0.0.1"
      port: 6062
      metrics: true
      pprof: false

# -----------------------------------------------------------------------------
# Dialects（可选）— 各 API 风格的专属配置
#
# 注意：这里不是“如何选择 dialect”（那在 server.api.baseHost 下配置），而是每个 dialect 自己的参数。
# 未配置 cfworker.upstream 时，访问 `cfworker.<baseHost>` 将返回 503（表示上游未就绪）。
# -----------------------------------------------------------------------------
dialects:
  cfworker:
    # 上游 Worker 的 Base URL（示例： https://temp-mail.example.workers.dev 或 http://127.0.0.1:8787 ）
    upstream: ""
    # 代理超时（包含请求与响应体传输）；0 表示不额外设置（仅受请求 ctx 影响）
    timeout: 15s

# -----------------------------------------------------------------------------
# Infrastructure
# -----------------------------------------------------------------------------
mongodb:
  uri: "mongodb://localhost:27017"
  database: "mailapi"
  # MongoDB 驱动调参（建议按压测逐步调整）
  # maxPoolSize:
  #   -1: 不覆盖驱动默认（通常为 100）
  #    0: 自动（按 CPU 规模估算，上限保护，适合高并发）
  #   >0: 固定值
  maxPoolSize: 0
  # minPoolSize:
  #   -1: 不覆盖驱动默认
  #   >=0: 固定值（通常 0 即可；需要预热连接可提高）
  minPoolSize: -1
  # maxConnecting:
  #   -1: 不覆盖驱动默认
  #    0: 自动（适当提高“建连并发”以降低突发流量抖动）
  #   >0: 固定值
  maxConnecting: 0
  # 连接/选主超时（0 表示使用驱动默认）
  connectTimeout: 10s
  serverSelectionTimeout: 10s
  # 空闲连接回收（<=0 表示不设置，使用驱动默认）
  maxConnIdleTime: 5m
  # 便于在 Mongo 端定位来源连接（可选）
  appName: "mailapi"

redis:
  addr: "localhost:6379"
  password: ""
  db: 0

nats:
  url: "nats://localhost:4222"
  stream: "EMAILS"
  subject: "emails.incoming"
  consumerAckWait: 30s
  consumerMaxDeliver: 3

minio:
  endpoint: "localhost:9000"
  accessKey: "minioadmin"
  secretKey: "minioadmin"
  bucket: "attachments"
  useSSL: false

# -----------------------------------------------------------------------------
# Auth & TTL
# -----------------------------------------------------------------------------
jwt:
  secret: "change-me-to-a-random-secret"
  expiry: 1h

account:
  ttl: 168h # 7 days

message:
  ttl: 168h # 7 days
```

补充 / Note:

- `message.ttl` 控制默认的自动过期删除（TTL）。若需要对单条邮件长期保留，可通过 `PATCH /messages/:id` 设置 `keep=true`（该字段会让 TTL 索引跳过该文档）。
- `keep` 参数默认禁用，需要在 **API 服务进程** 显式开启环境变量 `MAILAPI_API_ALLOW_MESSAGE_KEEP=1`（或 `MAILAPI_ALLOW_MESSAGE_KEEP=1`）。

### 域名同步机制 / Domain Sync Mechanism

**中文：**

API 服务启动时，会将 `config.yaml` 中定义的域名 **upsert** 到 MongoDB 的 `domains` 集合。已存在的域名会更新 `isActive` 和 `isPrivate` 字段；不存在的域名会新建。配置文件中未列出的域名（如手动通过 MongoDB 添加的）不会被删除。

**English:**

On API service startup, domains defined in `config.yaml` are **upserted** to MongoDB's `domains` collection. Existing domains have their `isActive` and `isPrivate` fields updated; missing domains are created. Domains not listed in the config (e.g., manually added via MongoDB) are not removed.

---

## 7. API Key 鉴权体系 / API Key Authentication

> 源码位置 / Source: `internal/middleware/middleware.go`

### 设计理念 / Design Philosophy

**中文：**

系统默认使用 `sk_` 前缀 API Key（兼容 DuckMail 标准 `dk_`），通过统一的 `Authorization: Bearer` 头传递。API Key 和 JWT 令牌共享同一个 Authorization 头，系统通过 `sk_`/`dk_` 前缀自动区分二者。API Key 提供**域名级别**的访问控制和可配置的速率限制，实现完整的多租户隔离。

**English:**

API keys use `sk_` prefix by default (`dk_` also accepted), passed through a unified `Authorization: Bearer` header. API keys and JWT tokens share the same Authorization header; the system auto-detects them via the `sk_`/`dk_` prefix. API keys provide **domain-level** access control and configurable rate limiting, achieving full multi-tenant isolation.

### 统一 Bearer 认证架构 / Unified Bearer Auth Architecture

```
Authorization: Bearer <token>
    │
    ├─▶ sk_/dk_ 前缀？ / sk_/dk_ prefix?
    │   ├─ 是 / Yes → API Key 认证 / API Key auth
    │   │   └─ 设置域名范围 + Key 信息 + 速率限制配置
    │   │     Sets domain scope + key info + rate limit config
    │   └─ 否 / No → JWT 认证 / JWT auth
    │       └─ 设置用户身份 + 自动限定邮箱域名
    │         Sets user identity + auto-scope to email domain
    │
    ▼
┌─────────────────────────────┐
│ Layer 1: 全局 IP 速率限制    │   Redis INCR + EXPIRE
│ Global per-IP rate limit    │   Key: rl:<ip>
└─────────────────────────────┘
    │
    ▼
┌─────────────────────────────┐
│ Layer 2: API Key 速率限制    │   Redis INCR + EXPIRE
│ Per-key rate limit          │   Key: rl:key:<apiKey>
│ （仅当 rpmLimit > 0）        │
└─────────────────────────────┘
    │
    ▼
┌─────────────────────────────┐
│ Layer 3: Key+域名 速率限制   │   由 handler 在操作域名时检查
│ Per-key-per-domain limit    │   Key: rl:key:<apiKey>:d:<domain>
│ （仅当 domainLimits 已配置） │   Checked by handler at domain operations
└─────────────────────────────┘
    │
    ▼
  处理器 / Handler
```

### 中间件详解 / Middleware Details

| 中间件 / Middleware | 作用范围 / Scope | 说明 / Description |
|:---|:---|:---|
| `BearerAuth` | 全部路由 / All routes | 解析 `Authorization: Bearer` 头，区分 sk_/dk_ API Key 和 JWT。设置域名范围和用户身份 / Parses Bearer header, distinguishes sk_/dk_ keys from JWT. Sets domain scope and identity |
| `APIKeyRateLimit` | 全部路由 / All routes | 检查每 API Key 的 RPM 限制（当 `rpmLimit > 0` 时生效）/ Checks per-key RPM (active when `rpmLimit > 0`) |
| `RequireAPIKey` | 公开路由 / Public routes | 确保存在 API Key 或 JWT（当系统配置了 API Key 时）/ Ensures key or JWT present (when keys configured) |
| `AuthRequired` | 认证路由 / Auth routes | 验证请求包含有效 JWT 身份 / Validates JWT identity is present |
| `DomainScopeCheck` | 认证路由 / Auth routes | 验证 JWT 用户的域名在允许范围内 / Verifies JWT user's domain is permitted |
| `RateLimit` | 全部路由 / All routes | 全局 IP 速率限制 / Global per-IP rate limiting |

### Context 键 / Context Keys

| 键 / Key | 类型 / Type | 说明 / Description |
|:---|:---|:---|
| `apiKeyDomains` | `[]string` | 当前 Key/JWT 的允许域名列表 / Allowed domains for current key/JWT |
| `apiKeyName` | `string` | API Key 名称 / API key name |
| `apiKey` | `string` | API Key 字符串 / API key string |
| `apiKeyInfo` | `*APIKeyInfo` | API Key 完整信息（含速率限制配置）/ Full key info (with rate limit config) |
| `accountId` | `string` | JWT 用户账号 ID / JWT user account ID |
| `address` | `string` | JWT 用户邮箱地址 / JWT user email address |

### 向后兼容 / Backward Compatibility

如果 `config.yaml` 中未配置任何 `apiKeys`，系统以通配符 `["*"]` 运行——所有域名对所有请求开放，与引入 API Key 之前的行为完全一致。

If no `apiKeys` are configured in `config.yaml`, the system runs with wildcard `["*"]` — all domains are open to all requests, identical to the behavior before API keys were introduced.

### 辅助函数 / Helper Functions

| 函数 / Function | 说明 / Description |
|:---|:---|
| `DomainFromAddress(address)` | 从邮箱地址提取域名部分 / Extracts domain from email address |
| `IsDomainAllowed(ctx, domain)` | 检查域名是否在当前 Key 允许范围内 / Checks if domain is allowed by current key |
| `GetAllowedDomains(ctx)` | 获取当前 Key/JWT 的允许域名列表 / Gets allowed domain list for current key/JWT |
| `HasWildcardAccess(ctx)` | 当前 Key 是否有通配符权限 / Whether current key has wildcard access |
| `GetAPIKeyInfo(ctx)` | 获取完整 API Key 信息（含限流配置）/ Gets full API key info (with rate limit config) |
| `CheckDomainRateLimit(ctx, ca, domain)` | 检查 Key+域名维度的速率限制 / Checks per-key-per-domain rate limit |

---

## 8. API 接口 / API Endpoints

> 源码位置 / Source: `internal/handler/`

所有认证统一通过 `Authorization: Bearer` 头传递。当配置了 API Key 时，公开接口需要 `sk_`/`dk_` API Key 或 JWT；认证接口需要 JWT。

All authentication goes through the `Authorization: Bearer` header. When API keys are configured, public routes require a `sk_`/`dk_` API key or JWT; authenticated routes require JWT.

### 多 API 风格（按子域前缀） / API Dialects (Subdomain-Based)

MailAPI 支持通过 API 域名前缀（子域）选择不同的 API 风格（兼容层），例如：

- `cfworker.api.mailapi.com`：cfworker 风格（Cloudflare Worker / cloudflare_temp_email 兼容）
- `duck.api.mailapi.com`：DuckMail 风格
- `api.mailapi.com`：默认风格（可配置，建议默认 `duck` 以保持向后兼容）

注意：上面的 “统一 `Authorization: Bearer`” 约定针对 `duck` 风格成立；其他风格可能有不同鉴权头（如 `x-admin-auth` 等），请以对应 dialect 文档为准。

具体规则、DNS/TLS 与反向代理部署要点参见：[API_DIALECTS.md](API_DIALECTS.md)。

### 8.1 公开接口 / Public Routes

#### `GET /domains` — 获取可用域名 / List Available Domains

返回当前 API Key/JWT 有权访问的所有已启用域名。

Returns all active domains the current API key/JWT has access to.

#### `POST /accounts` — 创建临时邮箱 / Create Temporary Account

创建临时邮箱。支持两种模式：
1. **指定地址**：`{"address": "user@example.com", "password": "..."}` — 直接指定完整邮箱地址
2. **自动生成**：`{"domain": "example.com", "password": "..."}` — 系统自动生成人类化前缀

校验域名在 API Key/JWT 允许范围内，且域名在 MongoDB 中处于 active 状态。自动生成模式下如果发生地址冲突，会自动重试最多 5 次。

Creates a temporary email account. Two modes:
1. **Specified address**: `{"address": "user@example.com", "password": "..."}` — specify full address
2. **Auto-generated**: `{"domain": "example.com", "password": "..."}` — system auto-generates human-like prefix

Validates domain is within API key/JWT scope and active in MongoDB. In auto-generate mode, retries up to 5 times on address collision.

#### `POST /token` — 登录获取 JWT / Login for JWT Token

使用邮箱地址和密码登录，返回 JWT 令牌。

Login with email and password, returns JWT token.

#### `GET /addresses/random` — 生成随机地址 / Generate Random Addresses

生成随机人类化邮箱地址（不创建账号）。查询参数：`domain`（必填）、`count`（可选，1-50，默认 5）。

Generate random human-like email addresses (without creating accounts). Query params: `domain` (required), `count` (optional, 1-50, default 5).

### 8.2 认证接口 / Authenticated Routes (JWT Required)

| 方法 / Method | 路径 / Path | 说明 / Description |
|:---|:---|:---|
| `GET` | `/me` | 获取当前用户 / Get current user |
| `GET` | `/accounts/:id` | 获取账号信息 / Get account |
| `DELETE` | `/accounts/:id` | 删除账号（级联删除邮件和附件）/ Delete account (cascades) |
| `GET` | `/messages` | 分页邮件列表 / Paginated message list |
| `GET` | `/messages/:id` | 邮件详情（含完整正文）/ Message detail (with full body) |
| `PATCH` | `/messages/:id` | 更新邮件状态 / Update message status |
| `DELETE` | `/messages/:id` | 删除邮件 / Delete message |
| `GET` | `/messages/:id/download` | 下载原始 .eml / Download raw .eml |
| `GET` | `/messages/:id/attachments/:attachmentId` | 下载附件 / Download attachment |
| `GET` | `/sse` | 实时推送 / Real-time SSE |

**域名隔离说明 / Domain Isolation:**

所有认证接口都经过 `DomainScopeCheck` 中间件，确保 JWT 中用户的邮箱域名在允许范围内。JWT 用户被自动限定在其邮箱地址的域名范围内。

All authenticated routes pass through the `DomainScopeCheck` middleware, ensuring the JWT user's email domain is permitted. JWT users are automatically scoped to their email address's domain.

---

## 9. SMTP 服务 / SMTP Service

> 源码位置 / Source: `internal/smtp/server.go`, `cmd/smtp/main.go`

### 多监听器架构 / Multi-Listener Architecture

**中文：**

SMTP 服务支持同时在多个 IP 地址上监听。每个监听器知道自己负责哪些域名，在 `RCPT TO` 阶段进行域名过滤——不属于该监听器的域名直接返回 550 拒绝。

**English:**

The SMTP service supports listening on multiple IP addresses simultaneously. Each listener knows which domains it serves, performing domain filtering at the `RCPT TO` phase — domains not assigned to that listener are rejected with 550.

### 监听器构建流程 / Listener Build Process

```
config.yaml 中的域名配置 / Domain config in config.yaml
    │
    │  提取每个域名的 IP 列表
    │  Extract IP list per domain
    ▼
┌─────────────────────────────────┐
│ BuildListeners()                 │
│ 按 IP 分组域名                    │   example.com → 192.168.1.100
│ Group domains by IP              │   example.org → 192.168.1.100
│                                  │   example.net → 192.168.1.101
└─────────────────────────────────┘
    │
    │  检测本机 IP / Detect local IPs
    │  IsLocalIP() → net.InterfaceAddrs()
    ▼
┌─────────────────────────────────┐
│ 过滤非本机 IP                     │   10.0.0.2 不是本机 → 跳过
│ Filter non-local IPs            │   10.0.0.2 not local → skip
└─────────────────────────────────┘
    │
    ▼
监听器 1: 192.168.1.100:25 → [example.com, example.org]
监听器 2: 192.168.1.101:25 → [example.net]
```

### RCPT TO 校验流程 / RCPT TO Validation Flow

```
RCPT TO: <user@example.com>
    │
    ├──▶ 域名在监听器允许列表中？/ Domain in listener's allowed list?
    │    ├─ 否 → 550 "Domain not accepted on this server"
    │    └─ 是 → 继续 / Yes → continue
    │
    ├──▶ Redis 地址校验 / Redis address check
    │    ├─ Redis 错误 → 451 临时错误（安全拒绝）/ Redis error → 451 (safe reject)
    │    ├─ 不存在 → 550 "User does not exist"
    │    └─ 存在 → 250 OK
    │
    ▼
  接收邮件数据 → 入队 NATS / Receive DATA → enqueue to NATS
```

### 向后兼容 / Backward Compatibility

如果 `config.yaml` 中未定义 `domains`，SMTP 服务退回到单监听器模式，使用 `server.smtp.host:port` 配置，接受所有域名——与引入多监听器之前的行为一致。

If no `domains` are defined in `config.yaml`, the SMTP service falls back to single-listener mode using `server.smtp.host:port`, accepting all domains — identical to the behavior before multi-listener was introduced.

---

## 10. Worker 服务 / Worker Service

> 源码位置 / Source: `internal/worker/worker.go`, `cmd/worker/main.go`

### 概述 / Overview

Worker 是邮件处理的核心引擎。它从 NATS JetStream 队列中消费原始邮件，执行完整的 MIME 解析，将原始 `.eml`（RFC 822）与附件上传到 MinIO，MongoDB 保存邮件元数据与正文（text/html），并通过 Redis Pub/Sub 发布实时通知。如果对象存储不可用，rawMessage 会回退写入 Mongo 以保证正确性。Worker 不受域名配置或 API Key 的影响——如果邮件通过了 SMTP 的验证，Worker 就会处理它。

The Worker is the core email processing engine. It consumes raw emails from the NATS JetStream queue, performs full MIME parsing, uploads the raw `.eml` (RFC 822) and attachments to MinIO, stores metadata and body content (text/html) in MongoDB, and publishes real-time notifications via Redis Pub/Sub. If object storage is unavailable, rawMessage falls back to Mongo to preserve correctness. Workers are not affected by domain configuration or API keys — if an email passed SMTP validation, the Worker processes it.

### 安全防护 / Security Protections

| 防护措施 / Protection | 限制值 / Limit |
|:---|:---|
| MIME 嵌套深度 / MIME Nesting Depth | 50 层 / 50 levels |
| 内联部分大小 / Inline Part Size | 10MB |
| 附件大小 / Attachment Size | 20MB |
| 内存管理 / Memory | 流式 `io.LimitReader` / Stream-based |

---

## 11. JWT 认证与安全 / JWT Authentication & Security

> 源码位置 / Source: `internal/auth/auth.go`, `internal/middleware/middleware.go`

### JWT 令牌 / JWT Token

| 属性 / Property | 值 / Value |
|:---|:---|
| 签名算法 / Algorithm | HMAC-SHA256 |
| 令牌载荷 / Claims | `accountId`, `address`, `exp`, `iat`, `nbf` |
| 默认有效期 / Default Expiry | 1 小时 / 1 hour |

### 三层速率限制 / Three-Layer Rate Limiting

| 层级 / Layer | 限制 / Limit | Redis Key | 实现 / Implementation |
|:---|:---|:---|:---|
| 全局 IP / Global per-IP | 可配置（默认 100/min）/ Configurable (default 100/min) | `rl:<ip>` | Redis INCR + EXPIRE |
| API Key / Per-key | 每 Key 可配置 / Per-key configurable | `rl:key:<apiKey>` | Redis INCR + EXPIRE |
| Key+域名 / Per-key-per-domain | 每 Key 每域名可配置 / Per-key-per-domain | `rl:key:<apiKey>:d:<domain>` | Redis INCR + EXPIRE |
| SMTP / SMTP Layer | 100 连接/min/IP | `smtp_rl:<ip>` | Redis INCR + EXPIRE |

### 安全措施汇总 / Security Summary

| 措施 / Measure | 说明 / Description |
|:---|:---|
| **统一 Bearer 鉴权 / Unified Bearer Auth** | sk_/dk_ API Key（域名级）+ JWT（用户级）统一通过 Bearer 头 / sk_/dk_ Key (domain) + JWT (user) via Bearer header |
| 密码哈希 / Password | Bcrypt（10 轮）/ Bcrypt (10 rounds) |
| 域名隔离 / Domain Isolation | API Key + JWT 域名自动限定 / API Key + JWT domain auto-scoping |
| 所有权校验 / Ownership | 每个接口校验资源归属 / Per-endpoint resource checks |
| 三层限流 / Three-Layer Rate Limit | 全局 IP + 单 Key + 单 Key 单域名 / Global IP + per-key + per-key-per-domain |
| MIME 防护 / MIME Protection | 深度限制 50 + LimitReader / Depth 50 + LimitReader |
| 邮件限制 / Size Limit | 20MB |

---

## 12. 缓存层 / Cache Layer

> 源码位置 / Source: `internal/cache/cache.go`, `internal/cache/interface.go`

### Redis 键设计 / Redis Key Design

| 键模式 / Key Pattern | 用途 / Purpose | TTL |
|:---|:---|:---|
| `addr:<email>` | 邮件地址存在性缓存 / Address existence cache | 与账号 TTL 相同 / Same as account TTL |
| `rl:<ip>` | 全局 IP 速率限制 / Global IP rate limit counter | 1 分钟 / 1 minute |
| `rl:key:<apiKey>` | API Key 速率限制 / Per-key rate limit counter | 1 分钟 / 1 minute |
| `rl:key:<apiKey>:d:<domain>` | Key+域名速率限制 / Per-key-per-domain rate limit counter | 1 分钟 / 1 minute |
| `smtp_rl:<ip>` | SMTP 速率限制计数器 / SMTP rate limit counter | 1 分钟 / 1 minute |
| `email:events:<accountId>` | SSE Pub/Sub 频道 / SSE Pub/Sub channel | N/A |

### 缓存接口 / Cache Interface

```go
type Interface interface {
    SetAddress(ctx, addr, ttl)              // 设置地址缓存 / Set address cache
    HasAddress(ctx, addr)                   // 检查地址是否存在 / Check if address exists
    RemoveAddress(ctx, addr)                // 移除地址缓存 / Remove address cache
    Publish(ctx, accountID, payload)        // 发布 SSE 事件 / Publish SSE event
    Subscribe(ctx, accountID)               // 订阅 SSE 频道 / Subscribe SSE channel
    CheckRateLimit(ctx, ip, limit, window)  // 全局 IP 限流 / Global IP rate limit
    CheckSMTPRateLimit(ctx, ip, limit, window)      // SMTP 限流 / SMTP rate limit
    CheckKeyRateLimit(ctx, apiKey, limit, window)    // Key 限流 / Per-key rate limit
    CheckKeyDomainRateLimit(ctx, apiKey, domain, limit, window) // Key+域名限流 / Per-key-per-domain rate limit
    Client()                                // 获取 Redis 客户端 / Get Redis client
    Close()                                 // 关闭连接 / Close connection
}
```

---

## 13. 消息队列 / Message Queue

> 源码位置 / Source: `internal/queue/queue.go`

| 参数 / Parameter | 值 / Value |
|:---|:---|
| Stream 名称 | `EMAILS` |
| Subject | `emails.incoming` |
| 持久化 / Persistence | JetStream (at-least-once) |
| 最大存储 / Max Storage | 1GB |
| 消息保留 / Retention | 24 小时 / 24 hours |
| 消费者 / Consumer | `email-worker` (durable) |
| AckWait | `nats.consumerAckWait`（默认 30s，可配置）/ default 30s, configurable |
| MaxDeliver | `nats.consumerMaxDeliver`（默认 3，可配置）/ default 3, configurable |

---

## 14. 对象存储 / Object Storage

> 源码位置 / Source: `internal/storage/storage.go`

| 属性 / Property | 值 / Value |
|:---|:---|
| 存储桶 / Bucket | `attachments`（自动创建 / auto-created） |
| 对象键 / Object Key | `messages/{messageID}/{attachmentID}/{filename}`（附件 / attachments）；原始 `.eml` 使用固定键 `messages/{messageID}/_raw/message.eml` |
| 删除策略 / Deletion | 按邮件 ID 前缀批量删除（附件 + 原始 `.eml`）；Worker 还会后台扫描并清理“Mongo 文档已过期/删除但对象仍存在”的孤儿对象 / Batch delete by message ID prefix (attachments + raw `.eml`), plus background GC for orphaned objects |

---

## 15. 数据库持久化 / Database Persistence

> 源码位置 / Source: `internal/store/store.go`

### MongoDB 集合与索引 / Collections & Indexes

| 集合 / Collection | 索引 / Indexes | TTL |
|:---|:---|:---|
| `domains` | `domain` (unique), `isActive` | — |
| `accounts` | `address` (unique), `createdAt` (TTL) | 7 天 / 7 days |
| `messages` | `{accountId, isDeleted, createdAt: -1, _id: -1}` (compound), `createdAt` (TTL, partial keep!=true), `{accountId, ingestStream, ingestSeq}` (unique partial) | 7 天 / 7 days（keep=true 的文档不受 TTL 影响 / keep=true excluded） |

### SyncDomains 操作 / SyncDomains Operation

API 启动时调用 `store.SyncDomains()`，对 config 中每个域名执行 MongoDB `UpdateOne` with `upsert: true`：
- `$set`: 更新 `domain`, `isActive`, `isPrivate`, `updatedAt`
- `$setOnInsert`: 仅在新建时设置 `createdAt`

On API startup, `store.SyncDomains()` executes MongoDB `UpdateOne` with `upsert: true` for each config domain:
- `$set`: Updates `domain`, `isActive`, `isPrivate`, `updatedAt`
- `$setOnInsert`: Sets `createdAt` only on creation

---

## 16. 实时推送 / Real-time Push (SSE)

> 源码位置 / Source: `internal/handler/sse.go`, `internal/handler/sse_hub.go`

### 事件流转 / Event Flow

```
Worker 解析完邮件 / Worker finishes parsing
    │
    │  Redis PUBLISH "email:events:<accountId>" JSON
    ▼
Redis Pub/Sub ──▶ 所有 API 节点 / All API nodes
    │
    │  SSE: data: {"@type":"Message","id":"...","subject":"..."}
    ▼
客户端 / Client
```

SSE 连接同样受 JWT 认证保护，`DomainScopeCheck` 确保用户只能监听自己域名下的事件。

SSE connections are also protected by JWT authentication. `DomainScopeCheck` ensures users can only listen for events in their own domain.

高并发优化要点 / High-concurrency notes:

- Redis Pub/Sub 订阅连接做了进程内复用：从“每个 SSE 连接一个订阅”降为“每个 account 一个订阅 + 本地 fan-out”。这能显著降低 Redis 连接数与 goroutine 数量。
- SSE 会定期发送注释行心跳（`: ping`），帮助代理/LB 保持连接并更快发现断链。

---

## 17. 邮箱前缀生成器 / Email Prefix Generator

> 源码位置 / Source: `internal/prefix/`

### 概述 / Overview

**中文：**

内置智能邮箱前缀生成器，生成高度仿真的人类商用或昵称邮箱前缀。所有前缀使用小写 ASCII 字符（`a-z`、`0-9`、`.`、`_`、`-`），最短 3 个字符，适用于标准邮件地址。

**English:**

Built-in intelligent email prefix generator producing highly realistic human-like commercial or nickname email prefixes. All prefixes use lowercase ASCII characters (`a-z`, `0-9`, `.`, `_`, `-`), minimum 3 characters, suitable for standard email addresses.

### 生成类别 / Generation Categories

| 类别 / Category | 权重 / Weight | 模式数 / Pattern Count | 说明 / Description |
|:---|:---|:---|:---|
| 个人姓名 / Personal | 55% | 15 种 | `first.last`, `firstlast`, `first_last`, `flast`, `f.last`, `last.first` 等 / etc. |
| 昵称 / Nickname | 30% | 6 种 | `nick+num`, `adj+noun`, `adj_noun`, `adj.noun`, `pre+nick`, `nick.tag` |
| 商务 / Business | 15% | 4 种 | `biz`, `biz.dept`, `biz_dept`, `biz+num` |

### 个人姓名模式 / Personal Name Patterns

| 模式 / Pattern | 权重 / Weight | 示例 / Example |
|:---|:---|:---|
| `first.last` | ~27% | `john.smith`, `maria.garcia` |
| `firstlast` | ~13% | `johnsmith`, `zhangwei` |
| `first_last` | ~7% | `john_smith` |
| `first-last` | ~7% | `john-smith` |
| `flast` (initial+last) | ~13% | `jsmith`, `zweig` |
| `f.last` | ~7% | `j.smith` |
| `first.l` | ~7% | `john.s` |
| `last.first` | ~7% | `smith.john` |
| `lastfirst` | ~7% | `smithjohn` |
| `first+suffix` | ~7% | `john456` |

### 后缀系统 / Suffix System

个人姓名模式支持 6 种后缀类型：

| 后缀 / Suffix | 概率 / Probability | 范围 / Range | 示例 / Example |
|:---|:---|:---|:---|
| 无 / None | 30% | — | `john.smith` |
| 1-2 位 / 1-2 digits | 20% | 0-99 | `john.smith42` |
| 2 位年份 / 2-digit year | 15% | 50-09 | `john.smith92` |
| 4 位年份 / 4-digit year | 10% | 1960-2026 | `john.smith1992` |
| 3 位 / 3 digits | 13% | 100-999 | `john.smith456` |
| 4 位 / 4 digits | 12% | 1000-9999 | `john.smith6789` |

### 名称数据库 / Name Database

国际化多元姓名数据库（全部小写 ASCII）：

Internationally diverse name database (all lowercase ASCII):

| 数据集 / Dataset | 数量 / Count | 语言 / Languages |
|:---|:---|:---|
| 名字 / First Names | ~420 | 英、中拼音、西、法、德、意、日、韩、阿拉伯、印度等 / English, Chinese pinyin, Spanish, French, German, Italian, Japanese, Korean, Arabic, Indian, etc. |
| 姓氏 / Last Names | ~420 | 同上 / Same |
| 昵称 / Nicknames | ~170 | 英语网络昵称 / English internet nicknames |
| 形容词 / Adjectives | ~120 | 英语 / English |
| 名词 / Nouns | ~120 | 英语 / English |
| 商务前缀 / Business | ~50 | 英语 / English |
| 部门 / Departments | ~35 | 英语 / English |

### 唯一性容量 / Uniqueness Capacity

- 个人姓名：~420 × ~420 × 15 模式 × ~1200 后缀 ≈ **32 亿** / ≈ **3.2 billion**
- 昵称：~170 × 8 模式 × ~1200 后缀 + ~120 × ~120 × 3 模式 × ~1200 ≈ **18 亿** / ≈ **1.8 billion**
- 商务：~50 × ~35 × 4 模式 × ~100 ≈ **70 万** / ≈ **700 thousand**
- **总计 / Total: > 50 亿 / > 5 billion** 种独立前缀每域名

### API 接口 / API Endpoints

| 接口 / Endpoint | 说明 / Description |
|:---|:---|
| `GET /addresses/random?domain=xxx&count=N` | 生成 N 个随机地址（不创建账号，最多 50）/ Generate N random addresses (max 50) |
| `POST /accounts` with `{"domain": "xxx"}` | 自动生成前缀并创建账号（碰撞重试 5 次）/ Auto-generate prefix and create account (5 retries on collision) |

---

## 18. 多 IP 与分布式部署 / Multi-IP & Distributed Deployment

### 单服务器多 IP / Single Server, Multiple IPs

**中文：**

一台服务器拥有多个 IP 地址（如 192.168.1.100 和 192.168.1.101），通过在域名配置中指定 `ips` 字段，可以让不同域名监听在不同 IP 上。MX 记录将各域名指向对应的 IP 地址。

**English:**

A single server with multiple IPs (e.g., 192.168.1.100 and 192.168.1.101) can bind different domains to different IPs via the `ips` field in domain config. MX records point each domain to its corresponding IP.

```yaml
domains:
  - domain: "a.com"
    isActive: true
    ips: ["192.168.1.100"]
  - domain: "b.com"
    isActive: true
    ips: ["192.168.1.101"]
```

### 多服务器分布式 / Multiple Servers, Distributed

**中文：**

多台服务器使用**完全相同**的配置文件。每台服务器启动 SMTP 服务时，`BuildListeners()` 会调用 `IsLocalIP()` 检测本机网卡上绑定的 IP 地址，只有匹配的监听器会被启动。所有服务器共享同一套 MongoDB / Redis / NATS / MinIO 后端。速率限制状态存储在共享的 Redis 中，因此限流在所有节点上一致。

**English:**

Multiple servers use the **exact same** config file. When each server starts the SMTP service, `BuildListeners()` calls `IsLocalIP()` to detect IPs bound to local network interfaces — only matching listeners are started. All servers share the same MongoDB / Redis / NATS / MinIO backends. Rate limit state is stored in shared Redis, so rate limiting is consistent across all nodes.

```
Server A (IP: 10.0.0.1)                   Server B (IP: 10.0.0.2)
┌──────────────────────┐                   ┌──────────────────────┐
│ SMTP: 10.0.0.1:25    │                   │ SMTP: 10.0.0.2:25    │
│ → a.com, shared.com  │                   │ → b.com, shared.com  │
│                      │                   │                      │
│ API: 0.0.0.0:8080    │                   │ API: 0.0.0.0:8080    │
│ Worker               │                   │ Worker               │
└──────────┬───────────┘                   └──────────┬───────────┘
           │                                          │
           └──────────┬───────────────────────────────┘
                      ▼
              共享后端 / Shared Backends
              MongoDB / Redis / NATS / MinIO
              (速率限制状态在 Redis 中共享 / Rate limit state shared in Redis)
```

### 本机 IP 检测 / Local IP Detection

`IsLocalIP()` 通过 `net.InterfaceAddrs()` 枚举所有网络接口的 IP 地址，判断给定 IP 是否为本机地址。特殊地址 `0.0.0.0` 和 `::` 总是被视为本地。

`IsLocalIP()` enumerates all network interface IPs via `net.InterfaceAddrs()` to determine if a given IP is local. Special addresses `0.0.0.0` and `::` are always considered local.

---

## 19. 管理脚本 / Management Script

> 文件位置 / File: `mailapi.sh`

### 功能列表 / Command List

| 命令 / Command | 说明 / Description |
|:---|:---|
| `install` | 完整安装：检查依赖、编译、Docker 基础设施、配置生成、systemd 服务 / Full install |
| `build` | 编译三个 Go 二进制 / Build three Go binaries |
| `config` | 配置生成（交互/环境变量，生成域名、API Key、JWT Secret）/ Config generation (interactive/env) |
| `infra-up` | 启动 Docker Compose 基础设施 / Start Docker infra |
| `infra-down` | 停止 Docker Compose 基础设施 / Stop Docker infra |
| `infra-clean` | 停止基础设施并删除数据卷（危险）/ Stop infra and remove volumes (DANGEROUS) |
| `compose <args...>` | 透传 docker compose 子命令（便于排查 infra）/ Pass-through docker compose (for infra troubleshooting) |
| `doctor` | 环境/依赖/配置自检（也可用 `check` 作为别名）/ Doctor check (`check` alias) |
| `upgrade [pull]` | 编译并重启服务（可选拉取 infra 镜像）/ Build and restart (optional pull images) |
| `uninstall [purge]` | 卸载服务（可选清理数据/目录，危险）/ Uninstall (optional purge, dangerous) |
| `setcap-smtp` | 为 SMTP 设置 setcap，允许非 root 绑定 25（Linux）/ setcap for SMTP (Linux) |
| `smoke` | 冒烟测试：部署后快速验证 API/依赖可用性 / Smoke test |
| `start [service]` | 启动服务（api/smtp/worker/all）/ Start services |
| `stop [service]` | 停止服务 / Stop services |
| `restart [service]` | 重启服务 / Restart services |
| `status [service]` | 显示服务和基础设施状态 / Show status |
| `logs [service] [lines]` | 查看日志（默认 200 行）/ View logs (default 200 lines) |
| `genkey [name] [prefix]` | 生成 API Key（默认 sk_，也可生成 dk_）/ Generate API key (default sk_, dk_ optional) |
| `list-domains` | 列出配置中的域名 / List configured domains |
| `list-apikeys` | 列出配置中的 API Key（密钥已脱敏）/ List API keys (redacted) |

### 基础设施 / Infrastructure

脚本自动生成 Docker Compose 文件并管理 MongoDB、Redis、NATS、MinIO 四个容器。默认所有端口仅绑定到 `127.0.0.1`（可通过环境变量 `MAILAPI_INFRA_BIND_ADDR` 调整，或通过 `MAILAPI_SKIP_INFRA=1` 完全跳过）。

The script auto-generates a Docker Compose file managing MongoDB, Redis, NATS, and MinIO containers. By default ports bind to `127.0.0.1` (tune via `MAILAPI_INFRA_BIND_ADDR`, or set `MAILAPI_SKIP_INFRA=1` to skip infra management).

### 服务管理 / Service Management

- **root 用户 / Root**: 自动安装 systemd 服务文件（`mailapi-api`、`mailapi-smtp`、`mailapi-worker`）
- **非 root / Non-root**: 使用 PID 文件 + nohup 管理后台进程

### 健康检查与调试端口 / Health & Debug

三个服务（API / SMTP / Worker）都可以按需开启独立的 debug HTTP server（默认关闭，建议仅绑定 `127.0.0.1` 并置于防火墙/LB 后）。debug server 提供：

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

---

## 20. 防御性工程 / Defense Engineering

### DDoS 与连接洪流 / DDoS & Flood Defense

| 层级 / Layer | 防御 / Defense |
|:---|:---|
| SMTP 协议层 | RCPT TO 域名过滤 + Redis 地址校验 / Domain filtering + address validation |
| SMTP 连接层 | 100 连接/分钟/IP 速率限制 / 100 conn/min/IP rate limit |
| API 全局层 | 可配置 RPM/IP 速率限制（默认 100）/ Configurable RPM/IP rate limit (default 100) |
| API Key 层 | 每 Key 可配置 RPM 限制 / Per-key configurable RPM limit |
| API Key+域名层 | 每 Key 每域名可配置 RPM 限制 / Per-key-per-domain configurable RPM limit |
| API 认证层 | sk_/dk_ API Key + JWT 域名隔离 / sk_/dk_ API Key + JWT domain isolation |

### MIME 炸弹防御 / MIME Bomb Defense

| 防护 / Protection | 限制 / Limit |
|:---|:---|
| SMTP MaxMessageBytes | 20MB |
| MIME 嵌套深度 | 50 层 / 50 levels |
| 内联部分 / Inline Part | 10MB |
| 附件 / Attachment | 20MB |

---

## 21. 测试体系 / Test Suite

### 运行测试 / Running Tests

```bash
go test ./...           # 运行所有测试 / Run all tests
go test -cover ./...    # 带覆盖率 / With coverage
go test -v ./...        # 详细输出 / Verbose
```

### 测试方法 / Methodology

项目采用接口驱动的 Mock 测试模式。所有外部依赖（数据库、缓存、存储、队列）都定义了接口（`interface.go`），测试中使用函数指针注入来模拟各种行为。

The project uses an interface-driven mock testing pattern. All external dependencies have defined interfaces (`interface.go`), and tests use function pointer injection to simulate various behaviors.

### 测试覆盖 / Test Coverage

| 模块 / Module | 测试重点 / Focus |
|:---|:---|
| `auth` | JWT 生成与验证、过期、错误 / JWT generation, validation, expiry, errors |
| `config` | YAML 解析、默认值、速率限制配置 / YAML parsing, defaults, rate limit config |
| `handler` | HTTP 处理器、域名隔离、自动前缀生成、随机地址 / HTTP handlers, domain isolation, auto-prefix, random addresses |
| `middleware` | Bearer 统一鉴权（sk_/dk_ Key + JWT）、域名限定、三层限流 / Unified Bearer auth, domain scoping, 3-layer rate limiting |
| `prefix` | 最小长度、字符合法性、唯一性、分布、域名生成 / Min length, valid chars, uniqueness, distribution, domain generation |
| `smtp` | 会话、RCPT TO 校验、数据大小、队列错误 / Session, RCPT TO validation, data size, queue errors |
| `worker` | MIME 解析、附件处理、错误恢复 / MIME parsing, attachment handling, error recovery |

---

## 22. 部署说明 / Deployment

### 快速部署 / Quick Deploy

```bash
# 一键安装 / One-click install
sudo ./mailapi.sh install

# 启动 / Start
sudo ./mailapi.sh start

# 状态 / Status
./mailapi.sh status
```

### 生产环境清单 / Production Checklist

| 项目 / Item | 说明 / Description |
|:---|:---|
| `jwt.secret` | **必须修改**为强随机字符串 / **MUST change** to strong random |
| `apiKeys[].key` | **必须修改**，默认使用 `sk_` 前缀（兼容 `dk_`），用 `./mailapi.sh genkey` 生成 / **MUST change**, default `sk_` prefix (`dk_` also accepted), use `genkey` |
| `minio.accessKey/secretKey` | **必须修改** / **MUST change** |
| `rateLimit.global` | 根据业务调整全局限速 / Adjust global rate limit per business needs |
| `apiKeys[].rpmLimit` | 为每个 Key 设置合适的限速 / Set appropriate RPM per key |
| SMTP 端口 25 | 云环境需开放 / Must be open in cloud |
| TLS/HTTPS | 在反向代理终止 / Terminate at reverse proxy |
| MongoDB | 建议副本集 / Recommend replica set |
| Redis | 建议集群（共享速率限制状态）/ Recommend cluster (shared rate limit state) |
| 监控 / Monitoring | Prometheus + Grafana |
| DNS | 每个域名配置 MX 记录指向对应 IP / MX records for each domain to its IP |

---

## 附录 / Appendix

### A. API 快速参考 / API Quick Reference

| 方法 | 路径 | 认证 / Auth | 说明 |
|:---|:---|:---|:---|
| `GET` | `/domains` | Bearer sk_* / dk_* or JWT | 域名列表（按 Key/JWT 过滤）/ List domains |
| `POST` | `/accounts` | Bearer sk_* / dk_* or JWT | 创建账号（支持自动前缀）/ Create account (auto-prefix) |
| `POST` | `/token` | Bearer sk_* / dk_* (optional) | 登录获取 JWT / Login for JWT |
| `GET` | `/addresses/random` | Bearer sk_* / dk_* or JWT | 生成随机地址 / Generate random addresses |
| `GET` | `/me` | Bearer JWT | 当前用户 / Current user |
| `GET` | `/accounts/:id` | Bearer JWT | 账号信息 / Account info |
| `DELETE` | `/accounts/:id` | Bearer JWT | 删除账号 / Delete account |
| `GET` | `/messages` | Bearer JWT | 邮件列表 / List messages |
| `GET` | `/messages/:id` | Bearer JWT | 邮件详情 / Message detail |
| `PATCH` | `/messages/:id` | Bearer JWT | 更新邮件 / Update message |
| `DELETE` | `/messages/:id` | Bearer JWT | 删除邮件 / Delete message |
| `GET` | `/messages/:id/download` | Bearer JWT | 下载 .eml / Download .eml |
| `GET` | `/messages/:id/attachments/:aid` | Bearer JWT | 下载附件 / Download attachment |
| `GET` | `/sse` | Bearer JWT | 实时推送 / SSE stream |

> **认证说明 / Auth Note**: 所有认证通过 `Authorization: Bearer <token>` 头传递。`sk_`/`dk_` 前缀令牌被识别为 API Key，其他被视为 JWT。未配置 `apiKeys` 时，公开接口无需认证。
>
> All auth goes through `Authorization: Bearer <token>`. `sk_`/`dk_`-prefixed tokens are identified as API keys; others as JWT. When `apiKeys` is not configured, public routes require no auth.

### B. 关键安全参数 / Key Security Parameters

| 参数 / Parameter | 值 / Value |
|:---|:---|
| 密码最小长度 / Min Password Length | 6 字符 / 6 characters |
| 用户名最小长度 / Min Username Length | 3 字符 / 3 characters |
| Bcrypt Cost | 10 |
| JWT 有效期 / JWT Expiry | 1 小时 / 1 hour |
| 账号 TTL / Account TTL | 7 天 / 7 days |
| 邮件 TTL / Message TTL | 7 天 / 7 days |
| 全局 IP 限速 / Global IP Rate Limit | 可配置（默认 100/min）/ Configurable (default 100/min) |
| API Key 限速 / Per-Key Rate Limit | 可配置（默认不限）/ Configurable (default unlimited) |
| Key+域名限速 / Per-Key-Per-Domain | 可配置 / Configurable |
| SMTP 速率限制 / SMTP Rate Limit | 100/min/IP |
| 最大邮件大小 / Max Email Size | 20MB |
| MIME 最大深度 / Max MIME Depth | 50 |
| 默认配额 / Default Quota | 40MB |
| 自动前缀碰撞重试 / Auto-prefix Collision Retries | 5 次 / 5 times |
| 随机地址最大生成数 / Max Random Addresses | 50 |
| 前缀唯一组合数 / Prefix Unique Combinations | > 50 亿/域名 / > 5 billion/domain |
