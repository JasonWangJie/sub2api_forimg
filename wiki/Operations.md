# 运维与排障

[← Wiki 首页](Home.md)

## 日常检查

```bash
# 健康
curl -I http://127.0.0.1:8080/health

# Docker
docker compose -f docker-compose.local.yml ps
docker compose -f docker-compose.local.yml logs -f sub2api --tail=200

# systemd
sudo systemctl status sub2api
sudo journalctl -u sub2api -f
```

管理后台 **运维 Ops**：实时 QPS、错误、上游失败、账号可用性。

---

## 日志与可观测

| 来源 | 用途 |
|------|------|
| 应用日志 (`log` 配置) | 启动、panic、业务错误 |
| Ops 错误 / 上游错误 | 单次请求级排障 |
| UsageLog | 计费与用量审计 |
| 渠道监控 | 主动探活趋势 |
| Server-Timing（可选） | 管理端请求耗时 |

可临时调高 Ops 运行时日志级别；生产排障完改回。

---

## 反向代理检查清单

- [ ] HTTPS 终结正确，回源超时足够（流式长连接）
- [ ] `underscores_in_headers on`（Nginx）
- [ ] WebSocket 路径放行（Ops QPS、`/v1/responses` WS）
- [ ] `server.trusted_proxies` 配置正确，避免伪造 XFF
- [ ] 缓冲关闭或调大，避免截断 SSE

---

## 常见故障

### 1. 无法登录 / invalid email or password

- 预置了 `config.yaml` 导致跳过 Setup，未创建管理员  
- 解决：移走配置触发向导，或查 Docker 日志中的初始密码  

### 2. Key 调用 401

- Key 复制错误、已删除、用户被禁  
- Authorization 头格式错误  

### 3. 提示未绑定分组

- 用户创建 Key 时未选 Group  
- 管理端给用户限制了可用分组  

### 4. 粘性会话失效 / 多账号串会话

- Nginx 丢弃了带下划线的 Header  
- 客户端未传 `session_id` 等字段  

### 5. 429 / 无可用账号

- 用户并发或 RPM 打满  
- 上游账号全部不可调度 / 冷却 / 凭证失效  
- 在账号页刷新 OAuth、检查代理  

### 6. 计费异常熔断

- `billing.circuit_breaker` 触发 fail-closed  
- 检查价格表、Redis、数据库写入  

### 7. 异步图片 404

- 未配置或未启用 `image_storage`  
- 见 [ASYNC_IMAGE_TASKS](../docs/ASYNC_IMAGE_TASKS.md)  

### 8. Docker 升级后丢登录 / 2FA 失效

- `JWT_SECRET` / `TOTP_ENCRYPTION_KEY` 变更  
- 固定写入 `.env` 并备份  

### 9. 支付不到账

- Webhook URL 未公网可达或签名校验失败  
- 服务商实例选错、前台路由未指定来源  
- 详见 [PAYMENT_CN](../docs/PAYMENT_CN.md)  

---

## 性能与容量提示

- Redis 承载限流、会话、异步任务状态 — 勿当可丢缓存随便清  
- PostgreSQL 的 `usage_logs` 会增长 — 配置 `usage_cleanup` 或后台清理任务  
- 上游响应读取上限防止大包打爆内存  
- 高并发场景关注账号并发槽与出站代理质量  

---

## 备份建议

| 项目 | 建议 |
|------|------|
| PostgreSQL | 定期 `pg_dump` |
| Redis | 视是否持久化；关键状态勿只靠内存 |
| `.env` / `config.yaml` | 加密备份 |
| `docker-compose.local` 数据目录 | 整目录打包迁移 |
| 账号导出 | 含密钥，严格控权 |

---

## 相关

- [部署手册](Deployment.md)
- [配置参考](Configuration.md)
- [FAQ](FAQ.md)
