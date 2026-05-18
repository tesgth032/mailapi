# MailAPI

高并发、可分布式部署的临时邮箱后端服务。MailAPI 由 API、SMTP、Worker 三个独立进程组成，通过 MongoDB、Redis、NATS JetStream 与 MinIO 协同完成账号创建、入站收信、异步解析、附件存储、实时推送和自动过期清理。

项目默认提供 DuckMail/mail.tm 风格的主 API，同时支持按 Host 子域切换的 API dialect，包括 `duck`、`cfworker` 和 `yyds`。

## 特性

- 多进程架构：API、SMTP、Worker 解耦，便于独立扩容。
- 事件驱动收信：SMTP 只做收件人校验与入队，Worker 异步解析 MIME。
- 多域名与多 IP：每个域名可绑定不同监听 IP，适合多节点 SMTP 部署。
- 统一 Bearer 鉴权：同一个 `Authorization: Bearer` 头同时支持 `sk_`/`dk_` API Key 与 JWT。
- 域名级隔离：API Key 可限制可访问域名，私有域名不会被 wildcard key 隐式放开。
- 三层限流：全局 IP、单 API Key、单 API Key + 单域名均可配置 RPM。
- 实时推送：基于 Redis Pub/Sub 的 SSE，可跨 API 节点推送新邮件。
- 自动过期：账号和邮件通过 TTL 策略自动清理，消息可按配置允许长期保留。
- 附件存储：附件落入 MinIO/S3 兼容对象存储。
- 多 API dialect：支持默认 Duck 风格、cfworker 代理风格、YYDS Mail `/v1` 风格。
- 运维脚本：`mailapi.sh` 覆盖构建、配置、基础设施、服务启停、健康检查和冒烟测试。

## 架构

```text
外部 SMTP 发件方
        |
        | TCP :25
        v
+------------------+       publish        +------------------+
| mailapi-smtp     | -------------------> | NATS JetStream   |
| RCPT 校验/入队    |                      | emails.incoming  |
+------------------+                      +------------------+
        |                                          |
        | Redis 地址缓存                            | consume
        v                                          v
+------------------+       store          +------------------+
| Redis            | <------------------- | mailapi-worker   |
| 缓存/限流/PubSub |                      | MIME 解析/附件处理 |
+------------------+                      +------------------+
        ^                                          |
        | SSE Pub/Sub                              | MongoDB + MinIO
        |                                          v
+------------------+       REST/SSE       +------------------+
| mailapi-api      | <------------------  | MongoDB / MinIO  |
| 账号/消息/API Key |                      | 元数据 / 附件      |
+------------------+                      +------------------+
```

## 技术栈

| 类别 | 技术 |
| --- | --- |
| 语言 | Go 1.26.1 |
| HTTP | Gin |
| SMTP | emersion/go-smtp |
| 邮件解析 | emersion/go-message |
| 数据库 | MongoDB |
| 缓存与事件 | Redis |
| 队列 | NATS JetStream |
| 对象存储 | MinIO/S3 |
| 认证 | JWT HS256、API Key |
| 指标 | Prometheus |

## 快速开始

### 前置依赖

- Go 1.26.1 或更高版本。
- Docker 与 Docker Compose，用于快速启动 MongoDB、Redis、NATS、MinIO。
- Linux 生产环境建议使用 systemd；SMTP 监听 25 端口需要 root、`CAP_NET_BIND_SERVICE` 或端口转发。

### Docker Compose 快速启动

仓库提供 `compose.yaml`，会同时启动 MongoDB、Redis、NATS、MinIO、API、SMTP、Worker。默认配置文件为 `docker/config.docker.yaml`，只适合本地体验，生产环境务必替换密钥和域名。

```bash
# 使用本地 Dockerfile 构建并启动全部服务
docker compose up -d --build

# 查看服务状态
docker compose ps

# 查看日志
docker compose logs -f api worker smtp

# 停止服务
docker compose down
```

默认访问地址：

| 服务 | 地址 |
| --- | --- |
| API | `http://127.0.0.1:8080` |
| SMTP | `0.0.0.0:25` |
| MinIO API | `http://127.0.0.1:9000` |
| MinIO Console | `http://127.0.0.1:9001` |

Debug server 按项目安全约束只绑定容器内部 loopback，默认不发布到宿主机；Compose healthcheck 会在容器内部访问 `/healthz`。

可复制 `docker/env.example` 为 `.env` 覆盖端口绑定、镜像名和本地 MinIO 凭据：

```bash
cp docker/env.example .env
docker compose up -d --build
```

只启动部分服务也可以直接指定服务名。Compose 会自动带起 `depends_on` 中的基础设施：

```bash
docker compose up -d api worker
docker compose up -d smtp
```

### 使用已发布镜像

CD 会将镜像发布到 GitHub Container Registry：

```text
ghcr.io/tesgth032/mailapi:latest
ghcr.io/tesgth032/mailapi:main
ghcr.io/tesgth032/mailapi:<tag>
ghcr.io/tesgth032/mailapi:sha-<commit>
```

如果不想在目标机器构建镜像，可以直接拉取 GHCR 镜像：

```bash
MAILAPI_IMAGE=ghcr.io/tesgth032/mailapi:latest docker compose up -d
```

如果依赖服务由外部托管，只想分别部署 API、SMTP、Worker，可使用 `compose.app.yaml` 并挂载自己的生产配置：

```bash
MAILAPI_CONFIG_FILE=/opt/mailapi/config.yaml \
MAILAPI_IMAGE=ghcr.io/tesgth032/mailapi:latest \
docker compose -f compose.app.yaml up -d api worker

MAILAPI_CONFIG_FILE=/opt/mailapi/config.yaml \
docker compose -f compose.app.yaml up -d smtp
```

单镜像内包含三个二进制，通过 command 选择服务：

```bash
docker run --rm -v "$PWD/docker/config.docker.yaml:/etc/mailapi/config.yaml:ro" ghcr.io/tesgth032/mailapi:latest api --check-config
docker run --rm -v "$PWD/docker/config.docker.yaml:/etc/mailapi/config.yaml:ro" ghcr.io/tesgth032/mailapi:latest smtp --check-config
docker run --rm -v "$PWD/docker/config.docker.yaml:/etc/mailapi/config.yaml:ro" ghcr.io/tesgth032/mailapi:latest worker --check-config
```

### 使用管理脚本

```bash
# 构建二进制、生成配置，并按提示准备基础设施
./mailapi.sh install

# 启动 MongoDB、Redis、NATS、MinIO
./mailapi.sh infra-up

# 启动 API、SMTP、Worker
./mailapi.sh start

# 查看状态
./mailapi.sh status

# 执行环境与服务诊断
./mailapi.sh doctor

# 执行冒烟测试
./mailapi.sh smoke
```

### 手动构建

```bash
mkdir -p bin
go build -o bin/mailapi-api ./cmd/api
go build -o bin/mailapi-smtp ./cmd/smtp
go build -o bin/mailapi-worker ./cmd/worker
```

### 手动运行

```bash
./bin/mailapi-api --config config.yaml
./bin/mailapi-worker --config config.yaml

# SMTP 默认监听 :25。Linux 下可使用 sudo，或先执行 ./mailapi.sh setcap-smtp。
sudo ./bin/mailapi-smtp --config config.yaml
```

所有进程也支持把配置文件作为位置参数：

```bash
./bin/mailapi-api config.yaml
```

## 配置

主配置文件是 `config.yaml`。仓库中的配置只包含示例域名、占位 API Key 和本地默认依赖地址，生产部署前必须替换所有敏感值。

### 域名

```yaml
domains:
  - domain: "example.com"
    isActive: true
    isPrivate: false
    ips: []

  - domain: "private.example.com"
    isActive: true
    isPrivate: true
    ips: ["10.0.0.10"]
```

- `ips: []` 表示 SMTP 监听所有接口。
- `ips` 非空时，SMTP 节点只会为本机拥有的 IP 启动对应监听器。
- `isPrivate: true` 的域名必须被 API Key 显式授权，`domains: ["*"]` 不会自动包含它。

生产收信还需要为域名配置 MX 记录，指向运行 `mailapi-smtp` 的主机。

### API Key

```yaml
apiKeys:
  - key: "sk_change_me_to_a_random_key"
    name: "Admin"
    domains: ["*"]
    rpmLimit: 0

  - key: "sk_partner_key_here"
    name: "Partner"
    domains: ["example.com"]
    rpmLimit: 200
    domainLimits:
      "example.com": 100
```

客户端统一通过 Bearer 头传递凭据：

```bash
curl -H "Authorization: Bearer sk_your_key_here" http://localhost:8080/domains
curl -H "Authorization: Bearer eyJhbGciOi..." http://localhost:8080/me
```

生成 API Key：

```bash
./mailapi.sh genkey admin sk
./mailapi.sh genkey partner dk
```

### 基础设施

默认本地地址：

| 服务 | 默认地址 |
| --- | --- |
| MongoDB | `mongodb://localhost:27017` |
| Redis | `localhost:6379` |
| NATS | `nats://localhost:4222` |
| MinIO | `localhost:9000` |

生产建议：

- 修改 `jwt.secret`、MinIO 凭据、Redis 密码、MongoDB 连接串。
- 将基础设施端口绑定在内网或 `127.0.0.1`，不要直接暴露到公网。
- 为 MongoDB、Redis、NATS、MinIO 配置持久化、备份和访问控制。

### Debug Server

API、SMTP、Worker 都可以开启独立 debug HTTP server：

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

可用端点：

- `GET /healthz`：存活检查。
- `GET /readyz`：依赖就绪检查。
- `GET /metrics`：Prometheus 指标。
- `GET /debug/pprof/`：pprof，默认建议关闭。

不要把 debug server 暴露到公网。

## API 概览

默认主 API 使用 DuckMail/mail.tm 风格，完整定义见 `openapi.yaml`。

### 公共接口

如果配置了 `apiKeys`，以下接口需要 API Key；如果未配置 API Key，则按向后兼容模式开放。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/domains` | 获取可用域名列表 |
| `POST` | `/accounts` | 创建临时邮箱账号 |
| `POST` | `/token` | 使用地址和密码换取 JWT |
| `GET` | `/addresses/random` | 生成随机邮箱地址候选 |

创建账号：

```bash
curl -X POST http://localhost:8080/accounts \
  -H "Authorization: Bearer sk_your_key_here" \
  -H "Content-Type: application/json" \
  -d '{"domain":"example.com","password":"secret123"}'
```

登录获取 JWT：

```bash
curl -X POST http://localhost:8080/token \
  -H "Content-Type: application/json" \
  -d '{"address":"user@example.com","password":"secret123"}'
```

### 认证接口

以下接口需要 JWT，或者需要在对应 dialect 中满足等价认证要求。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/me` | 当前账号信息 |
| `GET` | `/accounts/:id` | 账号详情 |
| `DELETE` | `/accounts/:id` | 删除账号并清理相关数据 |
| `GET` | `/messages` | 邮件列表，支持传统分页和 cursor 分页 |
| `PATCH` | `/messages` | 批量更新邮件状态 |
| `DELETE` | `/messages` | 批量软删除邮件 |
| `POST` | `/messages/bulk-delete` | 按 ID 批量软删除 |
| `GET` | `/messages/:id` | 邮件详情 |
| `PATCH` | `/messages/:id` | 更新 `seen`、`keep` 等状态 |
| `DELETE` | `/messages/:id` | 删除邮件 |
| `GET` | `/messages/:id/download` | 下载原始 `.eml` |
| `GET` | `/messages/:id/attachments/:attachmentId` | 下载附件 |
| `GET` | `/sse` | 实时收信 SSE |

获取邮件列表：

```bash
curl "http://localhost:8080/messages?itemsPerPage=20" \
  -H "Authorization: Bearer <jwt_token>"
```

监听 SSE：

```bash
curl -N http://localhost:8080/sse \
  -H "Authorization: Bearer <jwt_token>"
```

## API Dialect

`server.api.baseHost` 启用后，服务会按 Host 最左侧子域选择 API 风格：

```yaml
server:
  api:
    baseHost: "api.example.com"
    defaultDialect: "duck"
    enabledDialects: ["duck", "cfworker", "yyds"]
    unknownDialect: "reject"
```

示例：

- `api.example.com`：默认 `duck` 风格。
- `duck.api.example.com`：DuckMail/mail.tm 风格。
- `cfworker.api.example.com`：Cloudflare Worker 兼容代理风格。
- `yyds.api.example.com`：YYDS Mail `/v1` 风格。

相关文档：

- `API_DIALECTS.md`：dialect 路由规则。
- `cfworker.md`：cfworker 集成说明。
- `yyds.md`：YYDS Mail 兼容接口说明。

## 运维脚本

`mailapi.sh` 主要命令：

```bash
./mailapi.sh install
./mailapi.sh build
./mailapi.sh config
./mailapi.sh infra-up
./mailapi.sh infra-down
./mailapi.sh infra-clean
./mailapi.sh start [api|smtp|worker|all]
./mailapi.sh stop [api|smtp|worker|all]
./mailapi.sh restart [api|smtp|worker|all]
./mailapi.sh status [api|smtp|worker|all]
./mailapi.sh logs [api|smtp|worker|all] [lines]
./mailapi.sh doctor
./mailapi.sh smoke
./mailapi.sh upgrade [--pull]
./mailapi.sh uninstall [--purge]
./mailapi.sh compose <args...>
./mailapi.sh setcap-smtp
./mailapi.sh genkey [name] [sk|dk]
./mailapi.sh list-domains
./mailapi.sh list-apikeys
```

常用环境变量：

| 变量 | 说明 |
| --- | --- |
| `MAILAPI_CONFIG` | 配置文件路径 |
| `MAILAPI_DIR` | 安装目录 |
| `MAILAPI_BIN_DIR` | 二进制目录 |
| `MAILAPI_LOG_DIR` | 日志目录 |
| `MAILAPI_NONINTERACTIVE=1` | 禁用交互提示 |
| `MAILAPI_SKIP_INFRA=1` | 不启动 Docker 基础设施 |
| `MAILAPI_DOMAINS` | 非交互配置时的域名 CSV |
| `MAILAPI_API_PORT` | API 监听端口 |
| `MAILAPI_SMTP_PORT` | SMTP 监听端口 |
| `MAILAPI_ENABLE_DIALECTS=1` | 生成配置时启用 dialect 路由 |
| `MAILAPI_SMOKE_URL` | 冒烟测试 API 地址 |
| `MAILAPI_SMOKE_HOST` | 冒烟测试 Host 头 |

## GitHub Actions

仓库内置两条工作流：

- `.github/workflows/ci.yml`：在 push、pull request 和手动触发时运行 `go test ./...`、构建三个二进制，并构建 Docker 镜像验证入口脚本和配置检查命令。
- `.github/workflows/docker.yml`：在 `main`、`v*.*.*` tag 和手动触发时构建 `linux/amd64`、`linux/arm64` 多架构镜像并推送到 GHCR。

发布规则：

| 触发 | 镜像标签 |
| --- | --- |
| push 到 `main` | `latest`、`main`、`sha-<commit>` |
| push tag `v1.2.3` | `v1.2.3`、`1.2.3`、`1.2`、`sha-<commit>` |
| 手动触发 | 当前 ref 对应标签 |

工作流使用 `GITHUB_TOKEN` 写入 GHCR，不需要额外配置 Docker Hub 密钥。若 GHCR package 未自动公开，可在 GitHub Packages 页面将 package visibility 改为 public。

## 测试

```bash
go test ./...
go test -cover ./...
```

配置检查：

```bash
./bin/mailapi-api --check-config --config config.yaml
./bin/mailapi-smtp --check-config --config config.yaml
./bin/mailapi-worker --check-config --config config.yaml
```

打印脱敏后的生效配置：

```bash
./bin/mailapi-api --print-effective-config --config config.yaml
```

`--full` 会输出未脱敏配置，仅在本地排障时谨慎使用。

## 项目结构

```text
.
├── cmd/
│   ├── api/              # REST API 服务入口
│   ├── smtp/             # SMTP 服务入口
│   └── worker/           # Worker 服务入口
├── internal/
│   ├── auth/             # JWT 与 token 缓存
│   ├── cache/            # Redis 缓存、Pub/Sub、限流
│   ├── config/           # 配置加载、校验、脱敏
│   ├── debugserver/      # healthz、readyz、metrics、pprof
│   ├── dialect/          # 多 API dialect 路由与兼容层
│   ├── domainutil/       # 域名工具
│   ├── handler/          # HTTP 处理器
│   ├── health/           # 依赖健康检查
│   ├── metrics/          # Prometheus 指标
│   ├── middleware/       # 鉴权、域名隔离、限流、访问日志
│   ├── model/            # 数据模型
│   ├── prefix/           # 邮箱前缀生成器
│   ├── queue/            # NATS JetStream 队列
│   ├── smtp/             # SMTP 会话与多监听器逻辑
│   ├── storage/          # MinIO/S3 附件存储
│   ├── store/            # MongoDB 数据访问
│   └── worker/           # MIME 解析与邮件入库
├── API_DIALECTS.md
├── PROJECT_DOCUMENTATION.md
├── USAGE_GUIDE.md
├── cfworker.md
├── yyds.md
├── openapi.yaml
├── Dockerfile
├── compose.yaml
├── compose.app.yaml
├── docker/
├── config.yaml
├── mailapi.sh
└── go.mod
```

## 安全建议

- 生产环境必须替换 `jwt.secret`、API Key、MinIO 凭据、数据库连接串和 Redis 密码。
- 不要把真实 `.env`、证书私钥、生产 `config.yaml` 变体提交到仓库。
- 只将 debug server 绑定到 `127.0.0.1` 或内网可信地址。
- 在反向代理后运行时，显式配置可信代理，避免伪造 `X-Forwarded-For` 绕过 IP 限流。
- 私有域名使用 `isPrivate: true`，并在 API Key 中显式授权。
- 对外公开仓库前运行敏感信息扫描，确认没有真实 token、域名、账号或远程地址残留。

## 生产部署清单

- 配置域名 MX 记录指向 SMTP 入口。
- 为 API 入口和 dialect 通配子域配置 DNS 与 TLS。
- 调整 MongoDB/Redis/NATS/MinIO 的认证、持久化和备份。
- 设置 `trustedProxies`、访问日志采样、全局限流与 API Key 限流。
- 根据吞吐量调整 MongoDB 连接池、NATS consumer、SMTP `maxMessageBytes` 和 Worker 并发环境。
- 开启 Prometheus 指标采集，并为 `/readyz` 配置负载均衡健康检查。
- 运行 `./mailapi.sh doctor` 和 `./mailapi.sh smoke`。

## 文档

- `PROJECT_DOCUMENTATION.md`：完整项目说明。
- `USAGE_GUIDE.md`：部署、配置和 API 使用指南。
- `API_DIALECTS.md`：按 Host 子域切换 API dialect 的规则。
- `cfworker.md`：Cloudflare Worker 兼容方案。
- `yyds.md`：YYDS Mail 风格接口说明。
- `openapi.yaml`：默认主 API 的 OpenAPI 描述。

## License

MIT。详见 `LICENSE`。
