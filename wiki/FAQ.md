# 常见问题 FAQ

[← Wiki 首页](Home.md)

## 产品理解

**Q: Sub2API 和新 API 中转有什么区别？**  
A: 它强调「订阅账号配额分发」：管理多上游账号池，给用户发 Key，做调度、限流与 Token 计费，并带完整管理后台与支付。

**Q: 能否只给自己用、不要计费？**  
A: 可以，使用简易模式 `RUN_MODE=simple`（生产加 `SIMPLE_MODE_CONFIRM=true`）。

**Q: Sora 能用吗？**  
A: 当前不可用，配置仅为预留。

---

## 安装与登录

**Q: Docker 起来了但登不进后台？**  
A: 查日志里的初始 admin 密码；或确认是否跳过了 Setup。源码部署勿在首次启动前手写空用户的 config。

**Q: 管理员密码忘了？**  
A: 通过数据库重置用户密码哈希，或重建管理员（注意备份）。PowerShell 下 bcrypt 含 `$` 易被展开，见 DEV_GUIDE。

**Q: 升级后全部掉线、2FA 失效？**  
A: `JWT_SECRET` / `TOTP_ENCRYPTION_KEY` 变了，请固定密钥并备份 `.env`。

---

## 调用与客户端

**Q: Claude Code 连上了但会话乱跳账号？**  
A: 检查反代是否丢弃下划线 Header；开启 `underscores_in_headers on`。

**Q: 同一 Key 有时走 Claude 有时走 GPT？**  
A: 不会。Key 绑定单一分组，分组有固定平台。换平台请换 Key/分组。

**Q: Antigravity 和官方 Claude 能混着聊吗？**  
A: 不建议同一上下文混用；用不同分组隔离。

**Q: Codex 模型列表不对？**  
A: 使用带 `client_version` 的 `/models`，或走 `/backend-api/codex/models`。

---

## 账号与调度

**Q: 提示无可用账号？**  
A: 检查账号是否过期、不可调度、并发满、代理失败、OAuth 需刷新。

**Q: 如何多账号负载？**  
A: 多个 Account 绑同一 Group；平台按策略选择，支持粘性会话与 failover。

---

## 计费支付

**Q: 还要单独部署 Sub2ApiPay 吗？**  
A: 不需要，支付已内置。见 [Billing-Payment](Billing-Payment.md)。

**Q: 用户付了款余额没到？**  
A: 查 Webhook、订单状态、服务商回调日志；确认前台支付方式已路由到正确实例。

---

## 运维

**Q: 数据目录怎么备份？**  
A: `docker-compose.local.yml` 场景直接打包部署目录；并定期 `pg_dump`。

**Q: 用量表太大？**  
A: 使用用量清理配置/任务（`usage_cleanup` 与后台清理）。

**Q: 如何用脚本管账号？**  
A: 使用 [sub2api-admin Skill](../skills/sub2api-admin/SKILL.md)。

---

## 开发

**Q: 前端必须用 pnpm 吗？**  
A: 是。CI 使用 frozen lockfile；混用 npm 易出 EPERM / lock 不一致。

**Q: 改了 Ent schema 要做什么？**  
A: `go generate ./ent` 与 `go generate ./cmd/server`。

---

## 还有问题？

- 搜索 GitHub Issues：https://github.com/Wei-Shaw/sub2api/issues  
- 对照 [运维与排障](Operations.md) 检查清单  
- 阅读官方 [README_CN.md](../README_CN.md)
