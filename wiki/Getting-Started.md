# 入门指南

[← Wiki 首页](Home.md)

## 1. 先搞清三个角色

| 角色 | 做什么 |
|------|--------|
| **上游账号 (Account)** | Claude / OpenAI / Gemini 等真实订阅或 API Key，由管理员导入 |
| **分组 (Group)** | 调度单元：绑定平台类型、可用模型、倍率；账号挂到分组上 |
| **用户 API Key** | 发给终端用户的 `sk-...`，绑定某个分组后即可调用网关 |

数据流：

```text
用户 Key → 鉴权 → 所属分组 → 选上游账号 → 转发上游 → 计费落库
```

## 2. 最短安装路径（Docker）

```bash
mkdir -p sub2api-deploy && cd sub2api-deploy
curl -sSL https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/deploy/docker-deploy.sh | bash
docker compose up -d
```

打开 `http://<IP>:8080`，用日志中的管理员密码登录（若自动生成）：

```bash
docker compose logs sub2api | grep "admin password"
```

更完整的部署选项见 [部署手册](Deployment.md)。

## 3. 首次配置清单

1. **登录管理后台**
2. **添加上游账号**（OAuth 或 API Key，视平台而定）
3. **创建分组**，选择平台（Anthropic / OpenAI / Gemini / Grok / Antigravity…）
4. **把账号绑到分组**
5. **创建用户**（或开放注册），给用户发余额/订阅
6. **用户创建 API Key** 并绑定分组
7. **用 curl / Claude Code / Codex 试调**

## 4. 第一次 API 调用

Anthropic Messages 示例：

```bash
curl http://localhost:8080/v1/messages \
  -H "x-api-key: sk-你的密钥" \
  -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 256,
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

OpenAI Chat 示例：

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-你的密钥" \
  -H "content-type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "hello"}]
  }'
```

> Key 所属分组的平台类型决定实际走哪套上游协议。详见 [网关 API](Gateway-API.md)。

## 5. Claude Code 快速对接

```bash
export ANTHROPIC_BASE_URL="http://localhost:8080"
export ANTHROPIC_AUTH_TOKEN="sk-xxx"
```

Antigravity 专用入口：

```bash
export ANTHROPIC_BASE_URL="http://localhost:8080/antigravity"
export ANTHROPIC_AUTH_TOKEN="sk-xxx"
```

## 6. 下一步

- 生产加固 → [配置参考](Configuration.md) / [运维与排障](Operations.md)
- 开充值 → [计费与支付](Billing-Payment.md)
- 看清内部结构 → [系统架构](Architecture.md)
