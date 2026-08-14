# API 密钥双平台生图映射（1 Key 双用）

更新时间：`2026-08-14`。

一把 API Key 可同时走 Gemini 与 OpenAI **持久异步生图**路径，计费按路径解析到对应分组；聊天等非异步生图能力仍使用主 `group_id`。

## 数据与解析

迁移：`backend/migrations/221_ZJ_api_key_platform_groups.sql`

表：`api_key_platform_groups (api_key_id, platform, group_id)`，`platform` 仅允许 `gemini` / `openai`。

解析顺序（提交与 Worker 复核相同）：

1. 映射表命中 `(key, expectedPlatform)` → 使用该 `group_id`
2. 否则主分组存在且 `group.platform == expectedPlatform` → 回退主分组
3. 否则 `403`（如 `group_required` / `group_platform_mismatch`）

校验仍要求目标组：`allow_image_generation`、`allow_async_image_generation`、分组 active。

任务行仍只存一个 `group_id`（本次解析出的计费组）。查询 `GET /v1/images/tasks_async/{task_id}` 只校验任务归属 Key，与平台无关。

## 部署与迁移

服务启动连接 PostgreSQL 时自动应用嵌入迁移，**不必手工执行 SQL**。发行版升级后 restart 即可：

```bash
curl -sSL https://raw.githubusercontent.com/JasonWangJie/sub2api_forimg/main/deploy/upgrade.sh | sudo bash
```

验证：

```sql
SELECT filename FROM schema_migrations
WHERE filename LIKE '%221_ZJ_api_key_platform_groups%';

\d api_key_platform_groups
```

## 如何配置

### 前置

准备两个（或至少要用的一侧）异步生图分组：

- `platform=gemini`，开启图片生成 + 异步生图，配账号与图片价
- `platform=openai`，同上

### 用户端

「API 密钥」创建/编辑：

- **主分组**：必选（兼容聊天等旧逻辑）
- **Gemini 生图分组** / **OpenAI 生图分组**：可选；都选齐即双用

字段：`image_platform_groups`，例如 `{ "gemini": 10, "openai": 20 }`。

### 管理端

用户 → 查看 API 密钥 → 每条 Key 可改两侧生图组（可不改主分组）。`PUT /api/v1/admin/api-keys/:id` 可带 `image_platform_groups`。

### 客户端

同一把 Key：

| 路径 | 计费组 |
|---|---|
| `completions_gm` / `generations_sc` / `uploads/images_sc` | Gemini 映射或主组回退 |
| `generations_oa` / `edits_oa` | OpenAI 映射或主组回退 |
| `tasks_async` | 同一 Key 可查两侧任务 |

请求 JSON 方言不统一，仍按 BB/SC/OA 各自文档。

### 旧 Key

仅主分组、无映射：行为与升级前一致，只有主分组平台那一侧能异步生图。

## 工作台

能力接口会读取映射；双用 Key 可合并两侧异步 endpoints/models。UI 仍以首选平台为主协议，API 客户端可直接打另一侧路径。

## 相关代码

```text
backend/migrations/221_ZJ_api_key_platform_groups.sql
backend/internal/repository/api_key_platform_group_repo.go
backend/internal/service/api_key_platform_group.go
backend/internal/service/api_key_platform_group_service.go
backend/internal/handler/durable_async_image_handler.go
backend/internal/handler/durable_async_image_worker.go
frontend/src/views/user/KeysView.vue
frontend/src/components/admin/user/UserApiKeysModal.vue
```

## 冒烟（2026-08-14）

```text
go test ./internal/service/ ./internal/handler/ ./internal/handler/admin/ -count=1  → PASSED
go build ./cmd/server/ → PASSED
定向：Resolve/Validate/Normalize、ImageWorkbench、AdminAPIKeyHandler → PASSED
真实库/上游双路径联调：PENDING
```
