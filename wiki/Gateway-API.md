# 网关 API

[← Wiki 首页](Home.md)

网关面向终端客户端，使用用户 **API Key** 鉴权。多数路径同时提供 `/v1/...` 与无前缀别名（如 `/chat/completions`）。

路由实现：`backend/internal/server/routes/gateway.go`。

---

## 鉴权方式

常见两种：

```http
Authorization: Bearer sk-xxx
```

```http
x-api-key: sk-xxx
```

- Key **必须绑定分组**，否则被拦截
- 分组的 **平台类型** 决定走哪套上游与 Handler
- 计费信息：`GET /v1/sub2api/billing`

---

## 平台与路径对照

| 分组平台 | 主要路径 | 说明 |
|----------|----------|------|
| Anthropic (Claude) | `POST /v1/messages` | Claude Code 常用 |
| OpenAI | `/v1/chat/completions`、`/v1/responses`、`/v1/embeddings`、图像等 | Codex / OpenAI 兼容客户端 |
| Grok | 与 OpenAI Responses 兼容路径；另有 `/v1/videos/*`、图像 | |
| Gemini | `/v1beta/models...` | Gemini SDK / CLI |
| Antigravity | `/antigravity/v1/*`、`/antigravity/v1beta/*` | 专用，不与其它账号混调（除非开混合调度） |

同一路径可能按平台自动分发，例如：

- `POST /v1/messages` — OpenAI/Grok 走 OpenAI 兼容桥，其它走 Anthropic
- `POST /v1/responses` — 同上
- `POST /v1/images/generations` — OpenAI 或 Grok

---

## 路径清单（摘要）

### Anthropic / 通用

| 方法 | 路径 |
|------|------|
| POST | `/v1/messages` |
| POST | `/v1/messages/count_tokens` |
| GET | `/v1/models` |
| GET | `/v1/usage` |
| GET | `/v1/sub2api/billing` |

### OpenAI 兼容

| 方法 | 路径 |
|------|------|
| POST | `/v1/chat/completions` |
| POST | `/v1/responses`、`/v1/responses/*` |
| GET | `/v1/responses`（WebSocket） |
| POST | `/v1/embeddings` |
| POST | `/v1/alpha/search` |
| POST | `/v1/images/generations`、`/edits` |
| POST | `/v1/images/generations/async`、`/edits/async` |
| GET | `/v1/images/tasks/:task_id` |
| POST/GET/DELETE | `/v1/images/batches...` |
| POST/GET | `/v1/videos/...`（Grok） |

### Codex 直连接口

| 路径前缀 | 说明 |
|----------|------|
| `/backend-api/codex/responses` | Responses |
| `/backend-api/codex/models` | Codex 模型清单 |
| `/backend-api/codex/alpha/search` | Alpha Search |

`GET /v1/models?client_version=...` 会返回 Codex 期望的清单格式。

### Gemini

| 方法 | 路径 |
|------|------|
| GET | `/v1beta/models`、`/v1beta/models/:model` |
| POST | `/v1beta/models/*modelAction`（如 `generateContent`） |

### Antigravity

| 路径 | 模型侧 |
|------|--------|
| `/antigravity/v1/messages` | Claude |
| `/antigravity/v1beta/` | Gemini |
| `/antigravity/models` | 模型列表 |

开启分组「混合调度」后，通用 `/v1/messages` 与 `/v1beta/` 也可调度 Antigravity 账号。  
**勿在同一对话上下文混用 Anthropic Claude 与 Antigravity Claude**，请用分组隔离。

---

## 客户端配置示例

### Claude Code

```bash
export ANTHROPIC_BASE_URL="https://your-host"
export ANTHROPIC_AUTH_TOKEN="sk-xxx"
```

### Antigravity + Claude Code

```bash
export ANTHROPIC_BASE_URL="https://your-host/antigravity"
export ANTHROPIC_AUTH_TOKEN="sk-xxx"
```

### OpenAI SDK

```python
from openai import OpenAI
client = OpenAI(base_url="https://your-host/v1", api_key="sk-xxx")
```

### curl（Messages）

```bash
curl https://your-host/v1/messages \
  -H "x-api-key: sk-xxx" \
  -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -d '{"model":"claude-sonnet-4-20250514","max_tokens":128,"messages":[{"role":"user","content":"hi"}]}'
```

---

## 异步图片

- 提交：`POST /v1/images/generations/async` → `202` + `task_id`
- 查询：`GET /v1/images/tasks/{task_id}`
- **需启用 `image_storage`**，否则 404
- 仅 OpenAI / Grok 分组；不支持 streaming

详见 [docs/ASYNC_IMAGE_TASKS.md](../docs/ASYNC_IMAGE_TASKS.md)、[docs/BATCH_IMAGE_MVP.md](../docs/BATCH_IMAGE_MVP.md)。

---

## Sora

当前不可用。相关媒体签名 URL 配置仅为预留。

---

## 管理 API 前缀

业务与管理走 `/api/v1/...`（JWT 或管理员 Key），与网关 `/v1` 分离。  
CLI Skill：[skills/sub2api-admin](../skills/sub2api-admin/SKILL.md)。
