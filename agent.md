# AI 交接文档

## 当前上下文

这是 `JasonWangJie/sub2api_forimg` Fork，默认在 `main` 开发。交接基线：

- HEAD：`d5797a8c7ff7dab6f1dc4e23f0bbc104a4d101ff`（本轮代码尚未提交）
- `git describe --tags --always`：`v0.1.173.30-dirty`
- `backend/cmd/server/VERSION`：`0.1.173.29`
- 最近功能：异步生图参考图传输/分类重试、可选的异步结果个人图库自动归档，以及 API Key 用户统计接口
- 本次交接维护：已补齐 `readmenew.md`、`开发台账.md`、`agent.md`，并新增仓库级同步规则
- 本次任务：新增 `GET /v1/images/tasks_async/stats`，返回 API Key 所属用户余额和服务器时区当天异步任务统计
- 生产环境：本轮未连接、未修改、未重启

开始工作前必须运行：

```powershell
git status --short --branch
git rev-parse HEAD
git describe --tags --always --dirty
Get-Content backend\cmd\server\VERSION
```

然后阅读 [readmenew.md](readmenew.md)、[开发台账.md](开发台账.md)、[wiki-new/文档索引.md](wiki-new/文档索引.md) 和 [wiki-new/智能助手交接清单.md](wiki-new/智能助手交接清单.md)。

## 已交付的异步生图改动

核心代码位于：

- `backend/internal/service/image_storage_settings.go`
- `backend/internal/service/async_image_protocol.go`
- `backend/internal/handler/durable_async_image_worker.go`
- `backend/internal/repository/async_image_task_repo.go`
- `frontend/src/views/admin/BackupView.vue`
- `backend/internal/config/config.go`（`config.yaml` 兜底）

功能包括 OpenAI/Gemini 参考图三种传输策略、data URI 本地校验、OpenAI multipart、Gemini inlineData、三类独立重试计数、指数退避与抖动、`Retry-After`、混合回退阶段持久化，以及迁移 `backend/migrations/223_ZJ_async_image_reference_retry_state.sql`。

本轮新增 `async_image.auto_archive_to_library`，默认关闭。成功任务仍上传并保存 `async_image_results`，查询接口继续签发图片 URL；关闭时成功事务不创建 `library_archive` Outbox，恢复补偿不回填，遗留归档事件会记录为跳过。开启后恢复原有幂等图库归档行为。后台设置保存后运行参数热读取，无需重启；仅 `worker_concurrency` 仍需重启才能改变 Worker 数量。

本轮新增 `backend/internal/handler/async_image_stats_handler.go` 的统计处理器，路由为 `GET /v1/images/tasks_async/stats`。网关 API Key 中间件负责 `Authorization: Bearer <API_KEY>` 鉴权；接口按服务器配置时区调用 `StatsForUser`，返回 `balance`、`today_requests`、`success_count`、`failure_count` 和百分比 `success_rate`。接口文档见 [异步生图接口文档new.md](异步生图接口文档new.md)。

`execution_unknown` 不自动重放。参考图像素默认 80 MP；普通运行参数从后台设置热读取，`worker_concurrency` 在 `startRuntime` 创建 Worker 池时读取，修改后需要重启。

## 已验证证据

后端异步 Handler、Service、Repository 定向测试通过；本次新增统计 Handler、gateway route、middleware 测试通过；前端异步任务 API/date 用例 7/7 通过；`pnpm typecheck`、`git diff --check` 通过。前端构建的 Browserslist 和 chunk 大小提示不是本次失败。真实生产服务器、真实上游账号和真实 OSS 未验证。

完整 Service 包测试仍有既有外部 OpenAI token 对比用例因网络/API 不可用而失败；这不是本次任务中心改动造成的。

## 每次任务完成的强制动作

1. 用命令确认实际 HEAD、`VERSION`、分支和工作树状态。
2. 更新 [readmenew.md](readmenew.md) 的版本快照、功能摘要和入口链接。
3. 更新 [开发台账.md](开发台账.md) 的任务条目、完整 SHA、验证证据和未完成项。
4. 更新本 `agent.md` 的当前上下文和下一步，避免下一位助手依赖过期摘要。
5. 运行 `git diff --check`，检查 Markdown 链接和文档命名；没有真实证据的项目保留为“未验证”。

该要求同时写入根目录 [AGENTS.md](AGENTS.md) 和 [.cursor/rules/fork-release-deploy.mdc](.cursor/rules/fork-release-deploy.mdc)，属于仓库协作规则，不是可选建议。

## 下一位助手快速入口

- 异步任务状态机：`backend/internal/handler/durable_async_image_worker.go`
- 请求构造与参考图：`backend/internal/service/async_image_protocol.go`
- 运行参数默认值与归一化：`backend/internal/service/image_storage_settings.go`
- 任务字段和迁移：`backend/internal/service/async_image_task.go`、`backend/migrations/223_ZJ_async_image_reference_retry_state.sql`
- 管理端设置：`frontend/src/views/admin/BackupView.vue`、`frontend/src/views/admin/asyncImageRuntimeConfig.ts`
- API 契约：`docs/DURABLE_ASYNC_IMAGE_API.md`
- 对象存储/图库策略：`wiki-new/异步生图架构.md`、`wiki-new/图片图库与对象模型.md`
