# 多 API 风格（按子域前缀）规范 / API Dialects (Subdomain-Based Routing)

本项目需要同时支持多种 API 风格（兼容层），例如：

- `duck`：DuckMail / mail.tm 风格（当前 `mailapi` 的主风格）
- `cfworker`：`cloudflare_temp_email`（Cloudflare Worker）风格

为避免“同一套服务端口上堆叠多套路径前缀”带来的复杂度与冲突，建议采用 **按 Hostname 子域前缀选择 API 风格（dialect）** 的全局机制。

本文档定义该机制的规则、推荐默认值、以及生产部署所需的 DNS/TLS/反向代理要点。

---

## 1. 术语 / Terminology

- **API 风格 / Dialect**：一组“路径 + 鉴权头 + 请求/响应 JSON 结构”的组合规范。例如 DuckMail 与 cfworker 风格属于不同 dialect。
- **默认风格 / Default Dialect**：当 Hostname 不包含 dialect 前缀时（例如 `api.mailapi.com`），服务端采用的风格。
- **显式风格 / Explicit Dialect**：当 Hostname 以 `<dialect>.` 前缀开头时（例如 `cfworker.api.mailapi.com`），服务端强制采用该 dialect。

---

## 2. Hostname 规则 / Hostname Rules

以一个“API 基础域名”作为入口域名（示例：`api.mailapi.com`），并约定：

- `api.mailapi.com`：使用 **默认风格**（可配置）
- `<dialect>.api.mailapi.com`：使用 **显式风格**（由子域前缀指定）

示例（与你的目标一致）：

- `cfworker.api.mailapi.com`：cfworker 格式（Cloudflare Worker 风格）
- `duck.api.mailapi.com`：duckmail 格式（DuckMail 风格）
- `api.mailapi.com`：默认格式（由配置决定，建议默认 `duck` 以保持向后兼容）

实现要点（面向服务端）：

- 使用请求的 `Host`（去掉端口，统一小写）进行判断。
- 仅把 **最左侧 label** 作为 dialect 候选：`<dialect>.<baseHost>`。
- 若 `Host` 不匹配 `baseHost` 或 `*.baseHost`（例如直接访问 IP、localhost），使用默认风格（或仅在开发环境允许覆盖，见后文建议）。

---

## 3. 选择优先级 / Selection Priority

推荐优先级如下：

1. `Host` 显式指定 dialect（例如 `cfworker.api.mailapi.com`）时，**显式 dialect 优先生效**。
2. `Host` 不含 dialect 前缀（例如 `api.mailapi.com`）时，使用 **默认 dialect**。

对于未知 dialect（例如 `typo.api.mailapi.com`），推荐默认行为：

- **拒绝**（返回 404 或 400），避免误路由、避免“拼写错误却命中默认 API”导致排障困难。
- 可通过配置切换为“回退到默认 dialect”，用于容错或灰度期。

---

## 4. Dialect 定义（范围与链接）/ Dialect Definitions

本文件只定义“如何路由到 dialect”，不重复写每个 dialect 的完整 API 细节。建议：

- `duck`：以 [USAGE_GUIDE.md](USAGE_GUIDE.md) 与 [PROJECT_DOCUMENTATION.md](PROJECT_DOCUMENTATION.md) 为主文档。
- `cfworker`：以 [cfworker.md](cfworker.md) 为集成方案文档，并以 `cloudflare_temp_email` 上游文档为准。

---

## 5. 配置（已实现）/ Configuration (Implemented)

本机制已在服务端实现，对应配置位于 `config.yaml` 的 `server.api` 下，用于声明：

- API 基础域名（`baseHost`）
- 默认 dialect（`defaultDialect`）
- 允许路由到的 dialect 白名单（`enabledDialects`，可选）
- 未知 dialect 的处理策略（`unknownDialect`：`reject` 或 `fallback`）

示例：

```yaml
server:
  api:
    # API 基础域名。用于从 Host 中解析 <dialect>.<baseHost>
    baseHost: "api.mailapi.com"

    # api.mailapi.com 默认使用的风格（建议默认 duck，保证老客户端不受影响）
    defaultDialect: "duck"

    # 已启用的 dialect 列表（只允许这些值被路由）。为空表示不限制。
    enabledDialects:
      - "duck"
      - "cfworker"

    # 未知 dialect 处理：reject（推荐）或 fallback
    unknownDialect: "reject"
```

注意：

- `baseHost` 为空表示禁用该机制：所有请求都使用 `defaultDialect`（保持向后兼容）。
- 即便配置了 `baseHost`，当请求 `Host` 不匹配 `baseHost` 或 `*.baseHost`（例如直接访问 `localhost` / IP）时，也会强制走 `defaultDialect`，避免开发与直连场景被“子域路由”影响。
- 默认 dialect 建议保持为当前主风格（`duck`），否则会破坏现有客户端与文档示例。
- 生产环境建议启用 `reject`，开发/灰度阶段可用 `fallback` 提升容错与迁移体验。

---

## 6. DNS 与 TLS / DNS and TLS

要让 `*.api.mailapi.com` 生效，需要同时满足 DNS 与证书覆盖：

DNS（建议）：

- `api.mailapi.com` -> 负载均衡/入口代理（A/AAAA/CNAME）
- `*.api.mailapi.com` -> 同一入口（通配符 A/AAAA/CNAME）

TLS（建议）：

- 证书需覆盖 `api.mailapi.com` 与 `*.api.mailapi.com`。
- 常见做法：申请一张同时包含 `api.mailapi.com` 与 `*.api.mailapi.com` 的 SAN 证书，或使用通配符证书并额外覆盖根 host。

---

## 7. 反向代理要求 / Reverse Proxy Requirements

如果 `mailapi` 运行在 Nginx/HAProxy/Caddy 等代理后面，必须确保上游能看到正确的 Host：

- 保留原始 Host（`Host`/`:authority`），否则 dialect 解析会失效。
- 明确区分“可信代理”与“非可信来源”：
  - 建议默认只信任原始 `Host`。
  - 如必须使用 `X-Forwarded-Host`，需要在代码层配置 `trustedProxies` 并仅对可信代理生效，避免被客户端伪造。

Nginx 示例（概念）：

```nginx
proxy_set_header Host $host;
proxy_set_header X-Forwarded-Host $host;
proxy_set_header X-Forwarded-Proto $scheme;
```

---

## 8. 可观测性与排障 / Observability & Troubleshooting

建议在访问日志与指标中记录最终生效的 dialect，便于排查：

- 记录字段：`dialect`, `host`, `path`, `status`, `latency`
- 若命中未知 dialect（reject），返回体应包含可读的错误信息（例如提示可用的 dialect 列表）。

---

## 9. 安全与正确性注意事项 / Security & Correctness Notes

- dialect 切换是“路由层能力”，不能成为绕过鉴权/域名隔离的手段。不同 dialect 应遵守同等强度的鉴权与隔离策略。
- 浏览器场景可能涉及 CORS：不同子域是不同 Origin，需要在网关层做一致的 CORS 策略规划（若有 Web 前端需求）。
- 若启用 `*.api.mailapi.com` 通配符解析，未知子域流量会增加，默认 `unknownDialect: reject` 有助于减少误用与攻击面。
