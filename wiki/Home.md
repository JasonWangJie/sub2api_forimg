# Sub2API Wiki

面向部署者、管理员与开发者的项目文档中心。

> 官方 README：[README_CN.md](../README_CN.md) · 精简导读：[readmenew.md](../readmenew.md) · 开发备忘：[DEV_GUIDE.md](../DEV_GUIDE.md)

---

## 文档目录

| 文档 | 适合谁 | 内容 |
|------|--------|------|
| [入门指南](Getting-Started.md) | 所有人 | 概念、首次安装、第一次调用 |
| [系统架构](Architecture.md) | 开发 / 运维 | 分层、请求链路、核心实体 |
| [部署手册](Deployment.md) | 运维 | Docker / 二进制 / 源码 / 升级迁移 |
| [配置参考](Configuration.md) | 运维 | `config.yaml` 与环境变量要点 |
| [网关 API](Gateway-API.md) | 开发 / 用户 | 协议路径、工具对接、Antigravity |
| [管理后台](Admin-Guide.md) | 管理员 | 账号、分组、调度、运维看板 |
| [用户使用](User-Guide.md) | 终端用户 | Key、用量、充值、客户端配置 |
| [计费与支付](Billing-Payment.md) | 管理员 | 计费模型、支付通道、兑换码 |
| [运维与排障](Operations.md) | 运维 | 日志、限流、反代、常见故障 |
| [本地开发](Development.md) | 贡献者 | 环境、生成代码、测试、PR |
| [常见问题 FAQ](FAQ.md) | 所有人 | 高频问答速查 |

---

## 项目一句话

**Sub2API = 上游 AI 订阅账号池 + 统一 API 网关 + 用户配额分发与计费后台。**

管理员接入 Claude / OpenAI / Gemini / Grok / Antigravity 等账号，按分组调度；用户用平台 API Key 以官方兼容协议调用，平台完成鉴权、限流、转发与 Token 级计费。

---

## 快速导航

```text
想立刻跑起来     → Getting-Started / Deployment
想搞清怎么工作   → Architecture / Gateway-API
想配好生产环境   → Configuration / Operations
想开支付与计费   → Billing-Payment（详见 docs/PAYMENT_CN.md）
想改代码提 PR    → Development / DEV_GUIDE.md
```

---

## 专题文档（仓库内）

| 路径 | 说明 |
|------|------|
| [docs/PAYMENT_CN.md](../docs/PAYMENT_CN.md) | 支付完整配置 |
| [docs/ASYNC_IMAGE_TASKS.md](../docs/ASYNC_IMAGE_TASKS.md) | 异步图片任务 |
| [docs/BATCH_IMAGE_MVP.md](../docs/BATCH_IMAGE_MVP.md) | 批量图片 |
| [deploy/DATAMANAGEMENTD_CN.md](../deploy/DATAMANAGEMENTD_CN.md) | 数据管理守护进程 |
| [deploy/APPLE_CONTAINER.md](../deploy/APPLE_CONTAINER.md) | macOS Apple container |
| [skills/sub2api-admin](../skills/sub2api-admin/SKILL.md) | 管理端 CLI Skill |

---

## 合规提醒

使用前请阅读官方 README 中的服务条款风险、合规与免责声明。本 Wiki 仅作技术说明，不构成使用许可或商业授权。
