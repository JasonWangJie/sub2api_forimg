# 系统架构

[← Wiki 首页](Home.md)

## 1. 总体视图

```text
┌──────────────┐   API Key    ┌─────────────────────────────────────┐
│ Claude Code  │─────────────►│           Sub2API Server            │
│ Codex / CLI  │              │  Gin HTTP + h2c                     │
│ 自建应用     │              │                                     │
└──────────────┘              │  ┌─────────┐  ┌──────────────────┐  │
                              │  │ Gateway │  │ /api/v1 管理/用户 │  │
┌──────────────┐   JWT/Cookie │  │ /v1/*   │  │ Auth Payment Ops │  │
│  Web 管理端  │─────────────►│  └────┬────┘  └────────┬─────────┘  │
│  (Vue SPA)   │              │       │                │            │
└──────────────┘              └───────┼────────────────┼────────────┘
                                      │                │
                         ┌────────────┼────────────────┼────────────┐
                         ▼            ▼                ▼            │
                    PostgreSQL      Redis         上游 AI 服务       │
                    用户/账号/用量   限流/会话/任务   Claude/OpenAI…  │
                         └──────────────────────────────────────────┘
```

## 2. 仓库分层

| 路径 | 职责 |
|------|------|
| `backend/cmd/server` | 进程入口、Wire 依赖注入 |
| `backend/internal/handler` | HTTP 层：协议适配、参数校验 |
| `backend/internal/service` | 业务核心：调度、计费、账号、网关转发 |
| `backend/internal/repository` | 仓储抽象 |
| `backend/ent` + `migrations` | 数据模型与迁移 |
| `backend/internal/server/routes` | 路由注册 |
| `backend/internal/payment` | 支付通道 |
| `backend/internal/setup` | 首次安装向导 |
| `frontend/src` | Vue3 管理端 / 用户端 |
| `deploy/` | Compose、安装脚本、示例配置 |

路由模块（`backend/internal/server/routes`）：

- `gateway.go` — 对外模型 API
- `auth.go` — 注册登录 / OAuth
- `user.go` — 用户侧业务
- `admin.go` — 管理端 API
- `payment.go` — 支付与 Webhook

## 3. 网关请求链路

```text
1. 接收请求（/v1/... 或别名路径）
2. RequestBodyLimit / ClientRequestID / Ops 错误采集
3. API Key 鉴权（缓存可选）
4. RequireGroup — Key 必须绑定分组
5. 按 Group.Platform 选择 Handler：
     Anthropic → GatewayHandler
     OpenAI / Grok → OpenAIGatewayHandler
     Gemini → Gemini v1beta 路径
     Antigravity → ForcePlatform + 对应 Handler
6. Service 选账号（调度策略 / 粘性会话 / 并发槽）
7. 出站代理 / TLS 指纹（若配置）
8. 转发上游，流式或非流式回写
9. 解析 usage → 异步记 UsageLog → 扣余额/订阅配额
```

故障时：账号级 failover、错误透传规则、Ops 记录上游错误。

## 4. 核心领域实体

| 实体 | 说明 |
|------|------|
| **User** | 平台用户：余额、并发、角色、平台配额 |
| **APIKey** | 调用凭证，绑定 User + Group |
| **Account** | 上游账号（凭证、状态、并发、可调度标记） |
| **Group** | 平台、模型范围、倍率、消息分发策略 |
| **AccountGroup** | 账号与分组多对多 |
| **UsageLog** | 每次调用的 Token 与费用 |
| **Proxy** | 出站代理池 |
| **RedeemCode / PromoCode** | 兑换与优惠 |
| **SubscriptionPlan / UserSubscription** | 订阅体系 |
| **PaymentOrder** | 充值订单 |
| **ChannelMonitor** | 渠道探活与日报 |

完整 schema 见 `backend/ent/schema/`。

## 5. 前端结构

| 目录 | 用途 |
|------|------|
| `views/admin` | 仪表盘、用户、账号、分组、渠道、用量、兑换、支付、运维 Ops、风控、设置 |
| `views/user` | Key、用量、订阅、充值、推广、批量图片 |
| `views/auth` | 登录注册与 OAuth 回调 |
| `stores` | Pinia |
| `api` | Axios 封装 |

生产构建产物写入 `backend/internal/web/dist/`，可用 `-tags embed` 嵌入二进制。

## 6. 运行模式

| 模式 | 行为 |
|------|------|
| `standard` | 完整计费与 SaaS 功能 |
| `simple` | 隐藏 SaaS UI、跳过计费；适合内网。生产需 `SIMPLE_MODE_CONFIRM=true` |

## 7. 相关阅读

- [网关 API](Gateway-API.md)
- [配置参考](Configuration.md)
- [本地开发](Development.md)
