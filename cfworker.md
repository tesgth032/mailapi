# Cloudflare Worker 集成方案（对接 cloudflare_temp_email）

本文档描述如何借助 `cloudflare_temp_email` 的优秀架构，为本项目（`mailapi`）提供以下能力：

- 支持将 `cloudflare_temp_email` 部署出来的 Cloudflare Worker 作为“邮件数据平面/邮件 worker”（收信入口、可选解析与存储）。
- 支持 `cloudflare_temp_email` 的 API 风格（路径、鉴权头、参数与返回结构），使其前端/脚本可直接对接到 `mailapi`。

说明：此文档是方案与落地路径，并标注 `mailapi` 当前已实现与仍待实现的部分。

## 当前实现状态（mailapi 已内置）

`mailapi` 已内置 **cfworker dialect 的 Proxy（模式 A）**：当启用“按 Host 子域前缀选择 dialect”后，访问 `cfworker.<baseHost>` 会将请求按**原路径/查询/方法**代理到 `dialects.cfworker.upstream` 指定的上游 Worker。

最小配置示例（以 [config.yaml](config.yaml) 为准）：

```yaml
server:
  api:
    # 启用按子域前缀选择 API 风格（dialect）
    baseHost: "api.mailapi.com"
    defaultDialect: "duck"
    enabledDialects: ["duck", "cfworker"]
    unknownDialect: "reject"

dialects:
  cfworker:
    upstream: "https://temp-mail.example.workers.dev"
    timeout: 15s
```

提示：如果你启用了“按子域前缀选择 API 风格（dialect）”的全局能力，推荐将 cfworker 风格入口暴露为 `https://cfworker.api.mailapi.com`（或 `cfworker.<baseHost>`）。规则与部署要点见 [API_DIALECTS.md](API_DIALECTS.md)。

## 相关仓库与资料

- `cloudflare_temp_email` 仓库：`https://github.com/dreamhunter2333/cloudflare_temp_email`
- `cloudflare_temp_email` 文档站：`https://temp-mail-docs.awsl.uk/zh/`

## 目标与非目标

### 目标

- 兼容其 API 风格：支持 `/api/*`、`/admin/*`、`/user_api/*`、`/external/api/*` 等端点的“同款”调用方式（含鉴权头约定）。
- 支持将收信入口迁移到 Cloudflare Email Routing + Worker，并在需要时把邮件镜像落库到 `mailapi`（Mongo/MinIO/SSE 等能力继续可用）。
- 可按域名或站点维度选择“本地 provider”或 “Cloudflare provider”，实现渐进迁移。

### 非目标（阶段 1 不做）

- 不追求对所有响应字段“字节级完全一致”，优先保证调用流程与关键字段一致；字段差异通过兼容层适配。
- 不强制把 Cloudflare 侧的所有数据结构映射成 Mongo 的同构结构；只做业务上必要的映射。

## 总体架构：分层 + Provider 可插拔（推荐）

核心思想：把 `mailapi` 定位为 **控制平面/统一网关**，把 `cloudflare_temp_email` 定位为 **邮件数据平面（Cloudflare Worker + D1/KV/R2）**。两者之间通过“provider 抽象”解耦。

### 核心抽象：MailProvider（按域名/站点选择）

在 `mailapi` 内部抽象出一层 `MailProvider`（概念接口），并通过配置选择实现：

- `CreateAddress(...)` / `DeleteAddress(...)`
- `ListMails(limit, offset, filters...)`（以 raw RFC822 为主）
- `DeleteMail(id)`（管理端）
- `SendMail(...)`（可选）
- `WebhookIngest(payload)`（可选，仅 Cloudflare -> mailapi 镜像模式）

这样可以同时支持：

- **本地实现**（现有：Mongo + MinIO + NATS/Worker + Redis SSE）
- **Cloudflare 实现**（调用 cloudflare_temp_email Worker API，或接收其 webhook）

## 两种接入模式（可并存，按配置切换）

### 模式 A：Proxy/Gateway（最快达成 API 兼容）

`mailapi` 对外暴露与 `cloudflare_temp_email` 相同风格的端点，但内部直接 **转发** 到你部署的 Cloudflare Worker（HTTP proxy），通常不落库。

优点：

- 最少改动即可让其前端/脚本跑通。
- 完全复用其 Email Routing/Worker/D1/解析/存储能力。

缺点：

- `mailapi` 现有的 Mongo/MinIO/SSE 能力用得少；如要 SSE/统计，需要额外实现或在模式 B 才实现。

适用场景：

- 你主要目标是“复用其架构”与“API 风格对齐”，而不是本地二次存储与增强。

### 模式 B：Mirror/Store（把 Cloudflare 当“收信 worker”，mailapi 仍为主存储/主 API）

Cloudflare Worker 收到邮件后，通过 webhook 把邮件数据（至少包含 `raw RFC822`，可选包含解析结果）POST 到 `mailapi`；`mailapi` 走现有 pipeline 存 Mongo/MinIO，并通过 Redis 发布 SSE。

在真实生产落地时，Mirror 模式建议按“传输成本与可靠性”再细分为三种子形态（可逐步演进）：

1. **B1：Webhook 直推 raw（最简单）**
   - webhook payload 直接携带 raw RFC822（或 base64）。
   - 优点：实现最快；落库逻辑最直观。
   - 缺点：带宽与 CPU 成本高；请求体大、失败重试代价大；对上游与下游的超时/限额更敏感。
2. **B2：Webhook 推“引用”，mailapi 拉取 raw（推荐）**
   - 上游把 raw 存在 R2/S3（或其对象存储），webhook 只传 `raw_url`（短期 presigned URL）或 `bucket/key` 引用。
   - `mailapi` 流式拉取 raw，再走既有解析与存储链路（raw/附件存对象存储，Mongo 只存元数据）。
   - 优点：显著降低 webhook 体积与失败重试成本；更利于高吞吐与稳定性。
   - 缺点：需要额外的“拉取凭证/URL 过期时间”设计与网络连通性保障。
3. **B3：定时 Pull 对账（作为补偿与兜底）**
   - `mailapi` 定时调用上游 admin API 拉取增量，确保 webhook 丢失/失败时仍可补齐。
   - 建议作为 B1/B2 的补偿机制而不是替代。

优点：

- 保留 `mailapi` 的存储、下载、SSE、配额等能力。
- 收信入口交给 Cloudflare（避免自建 25 端口与公网暴露 SMTP 的运维成本）。

缺点：

- 需要幂等、补偿拉取（防 webhook 丢失）、以及外部 ID 映射策略。

适用场景：

- 你希望 Cloudflare 负责收信入口，但依然希望 `mailapi` 提供统一 API、SSE、审计、配额与本地存储。

### 推荐落地顺序

1. 先上 **模式 A（Proxy）**：快速实现 API 风格兼容与对接验证。
2. 再加 **模式 B（Mirror）**：需要本地 SSE/存储/增强能力时再接入 webhook 与落库。
3. 最后补齐 **发送邮件**（如需要），优先保持字段与行为一致。

## API 风格兼容层（路径、鉴权头、参数、返回）

### 鉴权头对齐（必须兼容）

`cloudflare_temp_email` 体系中常见的鉴权方式如下（兼容层必须支持）：

- 地址 JWT：`Authorization: Bearer <jwt>`，用于 `/api/*`
- 用户 JWT：`x-user-token: <jwt>`，用于 `/user_api/*`
- 管理员：`x-admin-auth: <admin_password>`，用于 `/admin/*`
- 私有站点密码：`x-custom-auth: <password>`（可选，按其部署配置启用）

兼容策略：

- 模式 A（Proxy）：上述头字段 **原样透传** 到上游 Worker。
- 模式 B（Mirror）：
  - `/api/*` 的 `Authorization: Bearer` 可复用 `mailapi` 自身 JWT，也可兼容上游 JWT（取决于落地策略）。
  - `x-admin-auth/x-user-token/x-custom-auth` 可作为“兼容层鉴权”单独校验（建议与 `mailapi` 的 `sk_/dk_` API key 与现有 JWT 体系并行，不相互替代）。

### 关键设计问题：账号/地址的事实来源（Source of Truth）

在 Cloudflare Email Routing + Worker 架构下，“收件人是否存在”的校验通常发生在 Worker 内部（否则会产生大量无效存储与滥用风险）。因此需要明确：**地址列表由谁做事实来源**，并决定同步方式。

常见三种选择：

1. **上游（cfworker）作为事实来源（推荐起步）**
   - 地址创建/删除走上游 admin API（模式 A 直接转发；模式 B 也可以转发）。
   - Worker 使用 D1/KV/R2 中的地址数据做收件人校验。
   - `mailapi` 若做镜像落库，仅需要保存 `upstream_address_id / upstream_mail_id` 的映射与审计信息。
2. **mailapi 作为事实来源（更强控制，但需要同步）**
   - 地址创建/删除由 `mailapi` 决定（配额、域名隔离、策略都在 `mailapi` 生效）。
   - 需要把地址列表同步到 Worker（例如写入上游 KV/D1，或提供 Worker 可调用的“地址校验 API”）。
   - 这是“把 cfworker 当纯收信 worker”的更彻底形态，但实现复杂度更高，建议在模式 A 稳定后再推进。
3. **双写/最终一致（不推荐起步）**
   - 同时写入上游与下游，或靠定时任务对账；工程复杂度与一致性风险较高。

### Mirror 模式的读写策略（建议先 Proxy 读，再逐步本地化）

Mirror/Store 不一定要马上“本地提供 cfworker 风格查询”。更稳妥的演进路径是：

- **起步阶段（推荐）**：对外的 cfworker 风格查询仍走 Proxy（上游是权威读源），Mirror 仅用于“本地增强能力”（例如 SSE、审计、备份、跨风格统一索引等）。
- **后续增强**：当你希望 cfworker 风格查询也能从本地读取时，再引入：
  - 上游 token 的验证/解析策略（共享密钥或 token exchange）
  - `upstream_mail_id -> local_message_id` 的稳定映射
  - 与上游一致的排序/分页语义

### 端点兼容清单（最小可用集）

下表给出建议优先兼容的一组端点，以及两种模式下的实现路径（不要求一开始全部实现，按阶段推进）。

注意：上游项目可能随着版本迭代调整路径/参数/字段；本文表格用于对齐思路与范围，落地时以 `cloudflare_temp_email` 的实际接口与文档为准。

| 端点 | 认证方式 | 模式 A（Proxy） | 模式 B（Mirror/Store） |
|---|---|---|---|
| `POST /admin/new_address` body：`{enablePrefix,name,domain}` 返回：`{jwt,...}` | `x-admin-auth` | 转发到 Worker | 本地创建账号/地址，返回 `jwt`（字段名对齐上游） |
| `GET /api/mails?limit&offset` | `Authorization: Bearer <address_jwt>` | 转发 | 从 Mongo/MinIO 组装结果，尽量以 raw RFC822 为主 |
| `GET /admin/mails?limit&offset&address?` | `x-admin-auth` | 转发 | 本地查询（可按 address 过滤） |
| `DELETE /admin/mails/:mail_id` | `x-admin-auth` | 转发 | 删除 message 并清理对象存储 |
| `POST /api/send_mail` body：`{from_name,to_name,to_mail,subject,is_html,content}` | `Authorization` | 转发 | 对接本地发送通道（建议优先对接第三方发信 API） |
| `POST /external/api/send_mail` body 多一个 `token` | 无 `Authorization` | 转发 | 校验 `token` 后复用 send_mail |
| `DELETE /api/delete_address` | `Authorization` | 转发 | 映射为删除 account + 清理消息 |
| `DELETE /admin/delete_address/:id` | `x-admin-auth` | 转发 | 删除指定 address/account（注意 ID 形态差异） |

备注：

- 若仅需其前端/脚本可用，通常先实现 `/admin/new_address` + `/api/mails` +（可选）`/api/delete_address` 就足够跑通“收信 + 查信 + 删号”主链路。

### ID 形态差异与兼容策略

上游常见的 `id` 可能是数字型（例如 D1 自增），而 `mailapi` 现有对象是 Mongo `ObjectID(hex)`。

推荐策略：

- 模式 A（Proxy）：不做 ID 适配，直接沿用上游 `id`，`mailapi` 仅代理。
- 模式 B（Mirror）：
  - 对外的“兼容端点”返回上游 `id`（例如 `mail_id`）作为主 ID。
  - 内部落库时记录 `upstream_id -> local_message_id` 的映射（建议独立 collection 并建唯一索引，保证幂等）。
  - 不建议一开始就做“自增 ID 复刻”，复杂度与冲突风险较高，后期如确有需要再评估。

## 收信链路设计（把 Cloudflare 当 worker）

### 纯 Proxy（不落库）

链路：

1. Cloudflare Email Routing 接收邮件
2. Worker（cloudflare_temp_email）处理并存储（D1/KV/R2）
3. 客户端调用 `mailapi` 的兼容端点
4. `mailapi` 代理转发到 Worker，返回上游响应

关键点：

- `mailapi` 只承担统一入口、鉴权统一、域名隔离与审计（可选）的职责。
- 这条链路最简单，适合优先验证“API 风格兼容”与“架构复用”。

### Webhook 镜像（落库到 mailapi）

链路（推荐可选增强）：

1. Cloudflare Worker 收到邮件后，按其 webhook 能力推送邮件数据到 `mailapi`
2. `mailapi` 验签、幂等判断后写入 Mongo（raw 优先写对象存储，如 MinIO）
3. `mailapi` 通过 Redis 发布 SSE（或其他通知方式）

必须考虑的正确性与健壮性：

- 鉴权：Webhook 必须携带共享密钥或签名（例如 HMAC），避免任意第三方伪造投递。
- 幂等：Webhook 会重试或重复投递，需要以 `upstream_mail_id` 做去重键。
- 幂等建议：
  - 数据库侧建议建立唯一约束（例如 `source + upstream_mail_id`），确保并发重试不会重复落库。
  - 若同一封邮件会被上游拆分为多条（例如多收件人拆分），应把“幂等粒度”明确到上游的唯一标识，而不是仅用 `Message-Id`（可能缺失或不唯一）。
- 部分失败：
  - webhook 到达但落库失败：应返回可重试错误，或内部入队重试。
  - webhook 丢失：需要补偿机制（见下）。
- 补偿拉取：定时从上游 `/admin/mails` 做增量对账补拉（至少保证“不丢信”）。

建议在方案层面预先定义“镜像落库 webhook”的最小契约（示例）：

```json
{
  "source": "cfworker",
  "upstream_mail_id": "123456",
  "address": "user@example.com",
  "received_at": 1710000000000,
  "raw": "base64...", 
  "raw_url": "https://...presigned...",
  "meta": {
    "from": "a@b.com",
    "to": ["user@example.com"],
    "subject": "hello"
  }
}
```

实现要点：

- `raw` 与 `raw_url` 二选一即可（B1 用 raw；B2 用 raw_url）。
- 认证建议走 `X-Signature`（HMAC）+ `X-Timestamp` +（可选）`X-Nonce`，并在服务端校验时间窗与重放。
- 返回建议包含 `local_message_id`（便于上游排障），并在重复投递时返回同一个 `local_message_id`（幂等）。

## 发送链路（可选）

如果需要兼容其发信 API：

- 模式 A（Proxy）：直接转发 `/api/send_mail` 与 `/external/api/send_mail`。
- 模式 B（Mirror）：在 `mailapi` 内实现发信 provider（例如对接 Resend/SMTP/其他），并保持请求体字段与返回结构尽量一致。

建议策略：优先以“字段与行为一致”为目标，不要引入过多的自定义字段；必要扩展以兼容字段保留（例如 `extra`）。

## 部署与配置建议（概念级）

### Cloudflare 侧（上游）

- 按 `cloudflare_temp_email` 的方式部署 Worker。
- 配置 Cloudflare Email Routing，将目标域名的邮件路由到 Worker。
- 如启用镜像落库：在 Worker 中配置 webhook 目标地址与签名密钥。

### mailapi 侧（下游）

- 模式 A：配置一个或多个上游 Worker BaseURL（按域名/站点映射），并启用“兼容路由”。
- 模式 B：开放 webhook 接收端点（仅内网或强签名），并配置幂等存储与补偿拉取任务。

补充建议（便于后续实现时落地）：

- 为上游调用设置严格的超时与重试策略（避免慢上游拖垮下游）。
- 对 `x-admin-auth/x-user-token/x-custom-auth` 等敏感头禁止写入日志（或做脱敏）。
- 对镜像落库链路建议记录：`source/upstream_mail_id/address/latency/result`，以便对账与追溯。

## 分阶段落地路线（推荐）

### 阶段 1：API 兼容代理（Proxy）

- 目标：让上游前端/脚本无需改动即可调用 `mailapi`。
- 交付：
  - 兼容端点路由
  - 鉴权头透传（`Authorization/x-user-token/x-admin-auth/x-custom-auth`）
  - 关键接口可用：`/admin/new_address`、`/api/mails`（以及可选 delete）

### 阶段 2：镜像落库（Webhook + 幂等 + SSE）

- 目标：让邮件进入 `mailapi` 的存储与通知体系。
- 交付：
  - webhook 接收端点 + 签名校验
  - 幂等去重与 ID 映射表（唯一索引）
  - 补偿拉取任务（对账增量）

### 阶段 3：发送邮件与统一增强能力

- 目标：补齐发信接口、统一审计/配额/检索等控制能力。
- 交付：
  - `/api/send_mail` 与 `/external/api/send_mail` 兼容
  - 可选：统一配额、统计、审计日志、跨 provider 查询等

## 风险与边界清单（必须显式处理）

- 安全：
  - webhook 入口必须强鉴权（签名/密钥），并建议仅允许 Cloudflare 出口 IP 或走专线/内网。
  - `x-admin-auth` 等明文口令头需要避免日志泄露（访问日志需脱敏或禁用头记录）。
- 一致性：
  - 幂等与去重是必须项，不能依赖“不会重试”。
  - 设计补偿拉取避免 webhook 丢失导致永久丢信。
- ID 兼容：
  - 避免强行把 D1 自增 ID 映射为 Mongo ObjectID；通过映射表解决。
- 性能：
  - 若 webhook 携带 raw RFC822，需注意 payload 大小与传输成本；建议上游把 raw 存 R2/S3，仅下发引用（后期优化项）。
  - 代理模式下应控制上游请求超时与连接池，避免慢上游拖垮 `mailapi`。
