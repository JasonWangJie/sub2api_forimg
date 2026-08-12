# 管理后台指南

[← Wiki 首页](Home.md)

管理端为嵌入式 Vue SPA，默认与 API 同端口（如 `:8080`）。管理员登录后使用 JWT；亦可用 Admin API Key（`x-api-key`）调用 `/api/v1/admin/*`。

---

## 推荐上手顺序

1. **设置** — 站点名、注册开关、邮件、OAuth、Turnstile、支付总开关  
2. **代理（可选）** — 为上游出站配置 Proxy  
3. **账号** — 导入 OAuth / API Key 上游账号  
4. **分组** — 建 Group，选平台与模型范围，绑定账号  
5. **用户** — 建用户、调余额/并发、限制可用分组  
6. **兑换码 / 订阅计划** — 按运营需要配置  
7. **运维 Ops** — 看实时流量、错误、告警  
8. **风控 / 渠道监控** — 内容审核与探活  

---

## 功能模块对照

| 模块 | 典型能力 |
|------|----------|
| 仪表盘 | 用量趋势、模型/分组统计、用户消费排行 |
| 用户 | CRUD、余额、API Key 列表、RPM、批量并发、换组 |
| 账号 | 导入导出、刷新凭证、可调度开关、并发、批量更新、CRS 同步 |
| 分组 | 平台、模型、倍率、账号绑定、消息分发策略 |
| 渠道 / 监控 | 渠道状态、探活模板、日报 |
| API Key（管理） | 代管用户 Key |
| 用量 | 全站 Usage 查询与清理任务 |
| 兑换码 / 优惠码 | 生成、作废、使用记录 |
| 订阅 | 套餐与用户订阅 |
| 支付 / 订单 | 服务商实例、套餐商品、订单与退款 |
| 公告 | 站内公告 |
| 风控 | 内容审核配置、封禁/解封、日志 |
| 备份 / 数据管理 | 备份与 datamanagementd 联动 |
| 运维 Ops | QPS WebSocket、错误日志、上游错误、告警规则、运行时日志级别 |
| 设置 | 全局业务开关、定价、OAuth、支付、合规确认 |

---

## 账号与分组要点

- **一个分组 = 一个平台类型**（Anthropic / OpenAI / Gemini / Grok / Antigravity…）
- 用户 Key 绑定分组后，请求只在该组账号池内调度
- 粘性会话依赖客户端传递的会话相关 Header；Nginx 需 `underscores_in_headers on`
- 账号可标记不可调度、可设并发上限
- TLS 指纹 Profile、错误透传规则可在管理端维护

---

## 运维 Ops

路径概念：`/api/v1/admin/ops/...`

- 实时：并发、账号可用性、流量摘要、`/ws/qps`
- 仪表：吞吐趋势、延迟直方图、错误分布、OpenAI Token 统计
- 日志：请求错误、上游错误、系统日志
- 告警：规则、事件、静默、邮件通知配置
- 运行时：临时调整日志级别等（重启可能丢失，视实现而定）

---

## Admin CLI

仓库内置 Skill，适合脚本化运维：

```bash
export SUB2API_BASE_URL='https://your-host'
export SUB2API_ADMIN_API_KEY='...'
# 或 SUB2API_JWT='...'

node skills/sub2api-admin/scripts/sub2api-admin.js accounts list
node skills/sub2api-admin/scripts/sub2api-admin.js groups all
```

说明见 [skills/sub2api-admin/SKILL.md](../skills/sub2api-admin/SKILL.md)。

注意：`accounts export` 含敏感凭证，勿在聊天中明文输出。

---

## 合规

部分环境需管理员接受合规声明后才能继续操作，见 `docs/legal/`。

---

## 相关

- [用户使用](User-Guide.md)
- [计费与支付](Billing-Payment.md)
- [网关 API](Gateway-API.md)
