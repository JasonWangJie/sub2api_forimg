# 配置参考

[← Wiki 首页](Home.md)

完整示例见 [`deploy/config.example.yaml`](../deploy/config.example.yaml)。Docker 部署多通过 `.env` / 环境变量覆盖同名配置。

## 配置块一览

| 块 | 作用 |
|----|------|
| `server` | 监听地址、模式、h2c、请求体上限、可信代理、`frontend_url` |
| `run_mode` | `standard` / `simple` |
| `cors` | 跨域白名单 |
| `security` | URL 白名单、CSP、响应头过滤 |
| `gateway` | 网关超时、响应读取上限、Sora 预留、调试开关等 |
| `log` | 日志级别与输出 |
| `database` | PostgreSQL |
| `redis` | Redis |
| `jwt` / `totp` | 会话与双因素加密 |
| `default` | 默认并发、余额、Key 前缀、倍率等 |
| `rate_limit` / `concurrency` | 限流与并发 |
| `billing` | 计费与熔断 |
| `pricing` | 价格数据源 |
| `ops` | 运维采集相关 |
| `turnstile` | 人机验证 |
| `gemini` | Gemini 相关 |
| OAuth 块 | LinuxDo / OIDC / 微信 / 钉钉等 |
| `image_storage` | 异步图片对象存储（S3 兼容） |

---

## 必改项（生产）

```yaml
database:
  host: "..."
  password: "强密码"
  dbname: "sub2api"

redis:
  host: "..."
  password: "..."   # 若有

jwt:
  secret: "openssl rand -hex 32 的结果"
  expire_hour: 24

totp:
  encryption_key: "openssl rand -hex 32 的结果"

server:
  mode: "release"
  trusted_proxies: ["反代网段CIDR"]
  frontend_url: "https://你的域名"
```

Docker `.env` 对应：`POSTGRES_PASSWORD`、`JWT_SECRET`、`TOTP_ENCRYPTION_KEY` 等。

---

## 安全相关

### URL 白名单

```yaml
security:
  url_allowlist:
    enabled: true                 # 生产建议开启
    allow_insecure_http: false    # 生产禁止 HTTP 上游
    allow_private_hosts: false    # 按需
```

`enabled: false` 时为开发友好模式，**默认允许 HTTP**，仅适合内网调试。

### CORS

```yaml
cors:
  allowed_origins:
    - "https://你的前端域名"
```

留空则禁用跨域。

### 网关防御

| 配置 | 建议 |
|------|------|
| `gateway.upstream_response_read_max_bytes` | 默认约 8MB，防内存放大 |
| `gateway.proxy_probe_response_read_max_bytes` | 探测响应上限 |
| `gateway.gemini_debug_response_headers` | 默认 false，排障再开 |
| `billing.circuit_breaker` | 计费异常 fail-closed |
| `server.max_request_body_size` | 默认 256MB |

登录/注册等接口有服务端限流；Redis 故障时 fail-close。

---

## 简易模式

```bash
RUN_MODE=simple
SIMPLE_MODE_CONFIRM=true   # 生产强制确认
```

或：

```yaml
run_mode: "simple"
```

效果：隐藏 SaaS 相关 UI、跳过计费/余额校验。

---

## 异步图片存储

异步图片默认关闭，需配置 S3 兼容存储，否则 `/v1/images/*/async` 返回 404。

```yaml
image_storage:
  enabled: true
  endpoint: "https://xxx.r2.cloudflarestorage.com"
  region: "auto"
  bucket: "my-images"
  access_key_id: "..."
  secret_access_key: "..."
  prefix: "images/"
  public_base_url: ""
  presign_expiry_hours: 24
```

详见 [docs/ASYNC_IMAGE_TASKS.md](../docs/ASYNC_IMAGE_TASKS.md)。

---

## Sora（暂不可用）

`gateway.sora_*` / `sora:` 配置块为预留。当前上游与媒体链路有问题，**生产勿依赖**。

---

## 管理员创建提醒

- Setup 向导是创建首个管理员的唯一正规方式
- `default.admin_email` / `default.admin_password` **不会**自动建管理员
- 已有 `config.yaml` 会跳过向导 — 参见 [部署手册](Deployment.md)

---

## 相关

- [运维与排障](Operations.md)
- [计费与支付](Billing-Payment.md)
