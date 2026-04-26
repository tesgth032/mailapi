# YYDS Dialect

本文档说明 `mailapi` 中新增的 `yyds` dialect。它对齐 [YYDS Mail 文档页](https://vip.215.im/docs) 中开发者可直接接入的临时邮箱 / 消息 / 实时收件接口风格，重点兼容：

- `/v1` 路径前缀
- `X-API-Key` 与 `Authorization: Bearer <temp_token>` 鉴权方式
- 统一的 `{ "success": true, "data": ... }` / `{ "success": false, "error": "...", "errorCode": "..." }` JSON 包裹

注意：当前实现**不**包含 YYDS Mail 站内控制台、支付结算、Webhook 管理、DNS 自动化等完整站内产品能力；这些不属于 `mailapi` 当前数据模型与服务边界。

---

## 1. 路由方式

推荐通过子域切换到该 dialect：

- `https://yyds.api.example.com`

示例：

```yaml
server:
  api:
    baseHost: "api.example.com"
    defaultDialect: "duck"
    enabledDialects: ["duck", "cfworker", "yyds"]
    unknownDialect: "reject"
```

---

## 2. 已实现接口

### 2.1 公共元数据

- `GET /v1/domains`
- `GET /v1/me/domains`
- `GET /v1/me/wildcard-rules`
- `GET /v1/me/quota`
- `GET /v1/plans`
- `GET /v1/pricing`
- `GET /v1/domain-reward/config`
- `GET /v1/stats`
- `GET /v1/llms.txt`

说明：

- `/v1/domains` 会返回当前可见的活动域名。匿名请求默认只看到公开域名；私有域名需要显式授权后才会出现在结果中。
- `/v1/me/domains` 是带鉴权版本的别名，便于与 YYDS 文档页对齐。
- `/v1/me/wildcard-rules` 会把当前可见父域名派生为“可用泛子域规则”列表，其中 `id` 直接返回父域名字符串，方便兼容旧脚本里的 `wildcardRuleId`。
- `/v1/me/quota` 返回当前生效套餐快照与维度上限；这是运行时权益视图，不只是套餐目录。
- 其余几个公共元数据端点由 `config.yaml -> dialects.yyds` 提供内容；不影响临时邮箱/消息主流程。

### 2.2 临时邮箱

- `POST /v1/accounts`
- `POST /v1/accounts/wildcard`
- `POST /v1/token`
- `GET /v1/accounts/me`
- `GET /v1/accounts/{id}`
- `DELETE /v1/accounts/{id}`

说明：

- `POST /v1/accounts` 支持 `localPart`、`address`、`domain`、`subdomain`、`autoDomainStrategy`。
- 兼容旧字段：`wildcardRuleId`、`subdomainLabel`。
- `localPart` 是推荐字段；`address` 仍保留兼容。
- 现在推荐把请求理解成“固定域名 + 可选真实子域”：
  - 固定域名：传 `localPart + domain`
  - 泛子域名：继续传 `localPart + domain`，若要固定真实子域，再额外传 `subdomain`
- 若未传本地部分，服务端会复用 `mailapi` 的人类化前缀生成器自动生成。
- 若未传 `domain`：
  - 先尝试 API key 配置里的 `defaultDomain`
  - 若仍未命中，则按 `autoDomainStrategy` 从当前可见域名里选父域
- `POST /v1/token` 采用 YYDS 风格：按 `address` 刷新临时 token，而不是使用密码登录。
- 匿名刷新 token 仅对公开域名开放；私有域名仍需显式授权。
- `POST /v1/accounts/wildcard` 会强制按泛子域模式创建：
  - 若显式传了 `subdomain` / `subdomainLabel`，则固定使用该真实子域
  - 若省略 `subdomain`，先尝试 API key 配置里的 `defaultSubdomain`
  - 若仍为空，则自动生成随机真实子域
  - 若同时省略父域名，则会从当前可见父域里随机选择一个

### 2.3 消息

- `GET /v1/messages`
- `POST /v1/messages/mark-read`
- `GET /v1/messages/{id}`
- `GET /v1/messages/{id}/source`
- `PATCH /v1/messages/{id}`
- `DELETE /v1/messages/{id}`
- `GET /v1/sources/{id}`
- `GET /v1/messages/{id}/attachments/{attachmentId}`

说明：

- `GET /v1/messages` 的 `limit` 默认 `50`，上限 `200`。
- `GET /v1/messages` 与 `POST /v1/messages/mark-read` 都支持 query `address=...`。
- 列表响应包含 `messages`、`total`、`unreadCount`。
- 详情响应会同时返回 `inbox_id` 与 `inboxId` 两种字段名，方便兼容历史调用方。
- `GET /v1/messages/{id}/source` 与 `GET /v1/sources/{id}` 都会把原始 RFC 822 文本包在 JSON 里返回。
- 若邮箱是自动分配的真实子域（例如 `demo@wxxxx.example.com`），后续查信必须使用接口返回的最终 `address`，不能只传父域或规则域名。

### 2.4 实时收件

- `GET /v1/auth/ws-ticket`
- `GET /v1/ws?token=...`

说明：

- 先用 API key 或 temp token 调 `GET /v1/auth/ws-ticket` 获取短期票据。
- 然后用该票据连接 `/v1/ws?token=...`。
- 当收到新邮件时，服务端会推送 `message.new` 事件，格式兼容文档页约定的 `type + mailbox + data` 结构。

---

## 3. 鉴权语义

### 3.1 API Key

`yyds` dialect 优先读取：

```http
X-API-Key: your-key
```

兼容说明：

- 只要 key 已配置在 `config.yaml -> apiKeys` 中即可，不强制要求 `AC-` 前缀。
- 仍兼容 `Authorization: Bearer <api-key>` 的传法，方便与现有 `mailapi` key 体系共存。

### 3.2 Temp Token

临时 token 仍复用 `mailapi` 当前 JWT 实现：

```http
Authorization: Bearer <temp_token>
```

`GET /v1/accounts/me` 与“当前邮箱上下文”相关的消息操作优先使用 temp token。

---

## 4. 与 YYDS 官方站点的差异

当前实现有两个刻意保留的边界：

1. `mailapi` 没有站内“用户/套餐/支付/Webhook/DNS 自动化”模型，因此只实现公共临时邮箱子集与公共元数据端点。
2. `mailapi` 不负责自动替你创建 DNS / MX 记录；这些仍需要由域名持有方在外部 DNS 中完成。

这意味着：

- 只要父域（例如 `example.com`）已经作为活动接收域配置到 `mailapi`，并且 DNS / MX 泛解析已经把 `*.example.com` 指到当前 SMTP 服务，`POST /v1/accounts/wildcard` 就可以在该父域下动态创建并接收 child-domain 邮箱。
- 若请求体中提供 `subdomain`，服务端会创建 `subdomain + "." + domain`；若省略 `subdomain`，服务端会自动生成随机 child-domain。
- API key 若配置了 `defaultDomain` / `defaultSubdomain`，则会优先使用这些默认值。
- SMTP listener 已支持“父域配置覆盖其子域接收”，因此不需要把每一个 child-domain 逐条写入 `domains`。

---

## 5. 配置示例

```yaml
dialects:
  yyds:
    publicBaseURL: "https://yyds.api.example.com/v1"
    plans: []
    pricing:
      currency:
        code: "CNY"
        suffix: ""
        symbol: "¥"
      packages: []
      rateLimits: []
    domainReward:
      creditExpireDays: 0
      creditsPerCycle: 0
      runHour: 0
      usagePerCredit: 0
    stats:
      totalUsers: 0
      totalDomains: 0
      verifiedDomains: 0
      publicDomains: 0
      totalInboxes: 0
      anonInboxes: 0
      totalStoredMessages: 0
      totalHistoricalMessages: 0
      totalCreatedInboxes: 0
      totalMessages: 0
      todayApiCalls: 0
      topDomains: []
      hourlyActivity: []
      dailyTrend: []
```

如果这些元数据不重要，可以全部留空数组/零值；不会影响邮箱与消息接口本身。
