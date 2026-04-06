设计一个对标 `api.mail.tm` 的极高并发、可分布式部署的临时邮件后端，兼容 [DuckMail API 标准](https://www.duckmail.sbs/zh/api-docs)，是一个非常经典的**高吞吐量 I/O + 实时流推送**的架构场景。

为了满足"极高并发"、"可分布式"和"深入思考"的要求，我们抛弃单体架构，采用**微服务架构**与**事件驱动设计 (EDA)**。

以下是完整的架构设计、技术栈选型以及严格对照 `mail.tm` 的 API 设计。

---

### 一、 核心技术栈选型与深度论证

| 组件 | 技术选型 | 深度论证 |
| :--- | :--- | :--- |
| **编程语言** | **Golang** | Go 语言天生适合高并发的网络 I/O。利用 Goroutine 处理海量长连接（SMTP 接收、SSE 实时推送）资源占用极低；编译为单一二进制文件，极易部署。 |
| **SMTP 核心库** | `emersion/go-smtp` | Go 生态中最优秀的 SMTP 库（ProtonMail 也在使用其底层组件），支持中间件模式，易于控制并发和安全防御。 |
| **MIME 解析库** | `emersion/go-message` | 处理复杂邮件结构的行业标准，流式解析，防止大邮件导致内存溢出 (OOM)。 |
| **主数据库** | **MongoDB** | 临时邮件是非结构化数据（JSON 完美契合）。更重要的是，MongoDB 原生支持 **TTL 索引**，可以实现邮件和账号的**自动过期删除**，无需手工写定时清理脚本，完美契合临时邮件的生命周期。 |
| **缓存与状态** | **Redis (Cluster)** | 1. 存储 SMTP 的速率限制状态。<br>2. 缓存活跃的邮件地址（用于 SMTP `RCPT TO` 阶段的极速拦截）。<br>3. 提供 Pub/Sub 功能用于分布式 SSE 推送。<br>4. 三层速率限制（全局 IP + 单 Key + 单 Key 单域名）的固定窗口计数器（Redis INCR + EXPIRE）。 |
| **消息队列** | **NATS JetStream / Kafka** | **核心解耦点**。SMTP 服务器收到原始邮件后**不立即解析**，而是扔进队列立刻响应 `250 OK`。防止高并发洪峰压垮数据库。 |
| **对象存储** | **MinIO (S3 兼容)** | 分布式存储邮件附件，同样支持 Lifecycle Policy 自动过期清理，剥离数据库的大字段存储压力。 |

---

### 二、 分布式高并发架构设计

整个系统分为三个核心无状态服务，可以根据流量随时进行水平扩容（Horizontal Scaling）：

#### 1. 架构拓扑
```text
[外部发件方 (Gmail/QQ)]     [前端/API 调用方]
         │                          │
  (TCP Port 25)               (HTTPS / WSS)
         ▼                          ▼
  +--------------+          +---------------+
  | L4 负载均衡  |          | L7 负载均衡   | (Nginx/HAProxy/AWS ALB)
  +--------------+          +---------------+
         │                          │
         ▼                          ▼
+-----------------+       +-------------------+
| SMTP Cluster    |       | API Cluster       | (Go + Gin)
| (go-smtp 多监听) |       | (RESTful & SSE)   |
| 域名过滤·多IP   |       | Bearer 统一鉴权    |
+-----------------+       | sk_/dk_ Key + JWT |
         │ (原始 EML 流)   +-------------------+
         ▼                     │ (读写)    ▲ (Pub/Sub)
+-----------------+       +-------------------+
| Message Queue   |       |   Redis Cluster   | (三层限流 + 缓存 + 事件)
| (NATS/Kafka)    |       +-------------------+
+-----------------+                 ▲
         │ (消费)                   │ (查询活跃用户)
         ▼                          ▼
+-----------------+       +-------------------+
| Worker Cluster  | ----> |   MongoDB Cluster | (账号/域名/邮件元数据)
| (解析 MIME)      |       +-------------------+
+-----------------+                 │
         │ (上传附件)               │
         ▼                          ▼
+-----------------+       +-------------------+
| MinIO (S3)      |       |  TTL Index 删除   | (自动 GC 清理)
| (附件存储集群)  |       +-------------------+
+-----------------+
```

#### 2. 核心数据流转（深入思考）

*   **防洪峰设计 (The `RCPT TO` Check)**：
    SMTP 节点在收到 `RCPT TO: <user@domain.com>` 指令时，**必须**去 Redis 中查询该邮箱是否存在（API 创建账号时会同步写入 Redis，设置与账号存活期相同的过期时间）。如果不存在，直接返回 `550 User unknown` 断开连接。**绝对不能接收后再丢弃，否则系统会被垃圾扫描者瞬间打满 I/O。**
*   **异步解析与解耦 (Asynchronous Processing)**：
    SMTP 节点接收到 `DATA`（邮件源码）后，只做简单的大小校验（例如限制 20MB），然后直接打包发入 NATS/Kafka 队列。SMTP 连接随即结束，释放资源。
*   **分布式实时推送 (SSE via Redis Pub/Sub)**：
    用户连在 `API 节点 A` 上保持 SSE 长连接；邮件被 `Worker 节点 B` 解析完毕并存入 MongoDB；`Worker 节点 B` 向 Redis 发布事件 `channel:user_id`；`API 节点 A` 收到事件，将数据通过 SSE 管道推给前端。

---

### 三、 对标 `mail.tm` 的 API 设计 (RESTful API)

这里严格参照 `mail.tm` 的规范，并兼容 DuckMail API 标准，设计核心接口（基于 JSON，Bearer 统一鉴权）。

#### 1. 认证方式 — DuckMail 兼容

统一通过 `Authorization: Bearer` 头传递认证信息：
- **API Key**：默认推荐 `Authorization: Bearer sk_xxx`（兼容 DuckMail 的 `dk_xxx`）
- **JWT**：`Authorization: Bearer eyJ...`

系统自动通过 `sk_`/`dk_` 前缀区分 API Key 和 JWT 令牌。JWT 用户自动限定在其邮箱域名范围内。

#### 2. 域名模块 (Domains)
*   **`GET /domains`**
    *   **功能**：获取可用域名列表（按 API Key/JWT 域名权限过滤）。

#### 3. 账号模块 (Accounts)
*   **`POST /accounts`**
    *   **功能**：创建临时邮箱账号。支持两种模式：
        - 指定地址：`{"address": "user@domain.com", "password": "..."}`
        - 自动生成人类化前缀：`{"domain": "domain.com", "password": "..."}`
    *   **后端逻辑**：校验域名、生成 bcrypt 哈希、存入 MongoDB、缓存到 Redis。自动模式下碰撞重试 5 次。
*   **`GET /accounts/{id}`** — 获取账号信息（需 JWT）。
*   **`DELETE /accounts/{id}`** — 提前销毁账号（级联删除邮件和附件）。
*   **`GET /addresses/random`** — 生成随机人类化邮箱地址（不创建账号，最多 50 个）。

#### 4. 鉴权模块 (Authentication)
*   **`POST /token`** — 登录并获取 JWT。

#### 5. 邮件消息模块 (Messages)
*   **`GET /messages`** — 分页获取邮件列表（需 JWT，不含大字段）。
*   **`GET /messages/{id}`** — 获取邮件详情（含 HTML/Text 正文和附件元数据）。
*   **`PATCH /messages/{id}`** — 标记已读。
*   **`DELETE /messages/{id}`** — 删除指定邮件。

#### 6. 附件模块 (Attachments)
*   **`GET /messages/{id}/attachments/{attachmentId}`** — 下载附件。

#### 7. 邮件源码模块 (Source)
*   **`GET /messages/{id}/download`** — 下载原始 `.eml` 格式文件。

#### 8. 实时推送模块 (Real-time SSE)
*   **`GET /sse`** — Server-Sent Events 流（需 JWT）。

#### 9. 速率限制
*   三层体系：全局 IP 限制 + 每 API Key 限制 + 每 API Key 每域名限制
*   所有计数器基于 Redis 固定窗口计数器实现（INCR + 首次设置 TTL）

---

### 四、 邮箱前缀生成器

内置智能邮箱前缀生成器，支持：
- **个人姓名模式（55%）**：15 种组合模式 × ~420 名 × ~420 姓 × 6 种后缀
- **昵称模式（30%）**：6 种组合模式 × ~170 昵称 + 形容词×名词组合
- **商务模式（15%）**：4 种组合模式 × ~50 前缀 × ~35 部门
- 国际化多元姓名（英、中拼音、西、法、德、日、韩等）
- 单域名超 50 亿种独立组合
- 全部小写 ASCII 字符

---

### 五、 深入思考的难点与防御性工程 (Defense In Depth)

做这套后端，把 API 写出来只占 20% 的工作量，剩下的 80% 都在处理**安全与极端情况**：

#### 1. 垃圾邮件与连接洪流防御 (DDoS at Port 25)
*   **对策**：
    *   OS 级别：使用 `iptables` / `ufw` 限制单 IP 的并发连接数和连接速率。
    *   应用级别：三层 Redis 固定窗口限流（全局 IP + 单 Key + 单 Key 单域名）。
    *   使用 `go-smtp` 的中间件，在 `Connection` 和 `HELO` 阶段就进行黑名单校验。

#### 2. MIME 地狱与解析器炸弹 (MIME Bomb)
*   **对策**：
    *   `emersion/go-message` 解析时，限制最大层级 50 层。
    *   严格设置 `MaxMessageBytes`（20MB），超过在 TCP 层截断。
    *   始终使用 `io.Reader` 流式处理，将附件通过 stream 直接透传写入 MinIO。

#### 3. 内存回收与 TTL 风暴 (GC & TTL Storm)
*   **对策**：
    *   API 查询增加条件 `createdAt > now - expire_time`，逻辑屏蔽已过期数据。
    *   考虑按日分表或 Time Series Collections。

#### 4. 敏感数据与隐私合规 (Privacy)
*   所有数据 TTL 机制，物理销毁不可逆。
*   禁止日志中包含邮件正文。

按照这套架构设计，配合几个节点（前端 Nginx，两台 Go 逻辑机，一套托管 MongoDB + Redis），足以支撑千万级别的每日吞吐量。
