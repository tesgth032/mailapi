# YYDS Dialect

本文档说明 `mailapi` 中新增的 `yyds` dialect。它对齐 [YYDS Mail 公共文档](https://vip.215.im/docs) 的“公共临时邮箱 / 公共元数据”接口风格，重点兼容：

- `/v1` 路径前缀
- `X-API-Key` 与 `Authorization: Bearer <temp_token>` 鉴权方式
- 统一的 `{ "success": true, "data": ... }` / `{ "success": false, "error": "...", "errorCode": "..." }` JSON 包裹

注意：当前实现**不**包含 YYDS Mail 站内控制台、计费、Webhook、DNS 自动化等产品能力；这些不属于 `mailapi` 当前数据模型与服务边界。

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
- `GET /v1/plans`
- `GET /v1/pricing`
- `GET /v1/domain-reward/config`
- `GET /v1/stats`
- `GET /v1/llms.txt`

说明：

- `/v1/domains` 会返回当前可见的活动域名。匿名请求默认只看到公开域名；私有域名需要显式授权后才会出现在结果中。
- 其余几个公共元数据端点由 `config.yaml -> dialects.yyds` 提供内容；不影响临时邮箱/消息主流程。

### 2.2 临时邮箱

- `POST /v1/accounts`
- `POST /v1/accounts/wildcard`
- `POST /v1/token`
- `GET /v1/accounts/me`
- `GET /v1/accounts/{id}`
- `DELETE /v1/accounts/{id}`

说明：

- `POST /v1/accounts` 支持 `localPart`、`address`、`domain`、`subdomain`。
- `localPart` 是推荐字段；`address` 仍保留兼容。
- 若未传本地部分，服务端会复用 `mailapi` 的人类化前缀生成器自动生成。
- `POST /v1/token` 采用 YYDS 风格：按 `address` 刷新临时 token，而不是使用密码登录。
- 匿名刷新 token 仅对公开域名开放；私有域名仍需显式授权。

### 2.3 消息

- `GET /v1/messages`
- `POST /v1/messages/mark-read`
- `GET /v1/messages/{id}`
- `PATCH /v1/messages/{id}`
- `DELETE /v1/messages/{id}`
- `GET /v1/sources/{id}`
- `GET /v1/messages/{id}/attachments/{attachmentId}`

说明：

- `GET /v1/messages` 的 `limit` 默认 `50`，上限 `200`。
- 列表响应包含 `messages`、`total`、`unreadCount`。
- 详情响应会同时返回 `inbox_id` 与 `inboxId` 两种字段名，方便兼容历史调用方。
- `GET /v1/sources/{id}` 会把原始 RFC 822 文本包在 JSON 里返回。

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
2. `POST /v1/accounts/wildcard` 不会自动创建 wildcard 子域的 DNS/MX/SMTP 接收能力。

这意味着：

- 若传了 `subdomain`，只有当最终子域已经作为活动接收域存在于 `domains` 中时，创建才会成功。
- 如果你需要“自动创建并接收任意 child-domain”的完整 YYDS wildcard 体验，需要另外扩展 `mailapi` 的域名监听与 DNS 管理能力。

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
