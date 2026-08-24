# AI 交接文档

## 当前上下文

这是 `JasonWangJie/sub2api_forimg` Fork，默认在 `main` 开发。交接基线：

- HEAD：`81111d3fc2b9cae415ddf561d69e1a26075fac54`
- `git describe --tags --always`：`v0.1.173.32-dirty`
- `backend/cmd/server/VERSION`：`0.1.173.31`
- 最近功能：异步生图参考图传输/分类重试、可选的异步结果个人图库自动归档，以及 API Key 用户统计接口
- 本次交接维护：同步 `/guides/async-image-api` 与 `docs/DURABLE_ASYNC_IMAGE_API.md` 到当前 BB/SC 路由、状态、限制和存储契约；工作树包含本次页面/文档改动和交接记录
- 本次审查结论：页面已覆盖 Gemini BB `completions_gm`、Gemini SC 上传、OpenAI `edits_oa`、统一查询与 `/tasks_sc` 别名、模型级参考图限制、幂等/重试、错误和存储 URL 有效期；超时成本对账、输出总量保护和真实端到端验收仍是下一步
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
- `frontend/src/views/user/GuideAsyncImageApiView.vue` 与 `frontend/src/views/user/guideAsyncImageApiContent.ts`（用户可见异步生图 API 页面）
- `docs/DURABLE_ASYNC_IMAGE_API.md`（静态持久化异步协议）

功能包括 OpenAI/Gemini 参考图三种传输策略、data URI 本地校验、OpenAI multipart、Gemini inlineData、三类独立重试计数、指数退避与抖动、`Retry-After`、混合回退阶段持久化，以及迁移 `backend/migrations/223_ZJ_async_image_reference_retry_state.sql`。

本轮新增 `async_image.auto_archive_to_library`，默认关闭。成功任务仍上传并保存 `async_image_results`，查询接口继续签发图片 URL；关闭时成功事务不创建 `library_archive` Outbox，恢复补偿不回填，遗留归档事件会记录为跳过。开启后恢复原有幂等图库归档行为。后台设置保存后运行参数热读取，无需重启；仅 `worker_concurrency` 仍需重启才能改变 Worker 数量。

本轮新增 `backend/internal/handler/async_image_stats_handler.go` 的统计处理器，路由为 `GET /v1/images/tasks_async/stats`。网关 API Key 中间件负责 `Authorization: Bearer <API_KEY>` 鉴权；接口按服务器配置时区调用 `StatsForUser`，返回 `balance`、`today_requests`、`success_count`、`failure_count` 和百分比 `success_rate`。接口文档见 [异步生图接口文档new.md](异步生图接口文档new.md)。

`execution_unknown` 不自动重放。参考图像素默认 80 MP；普通运行参数从后台设置热读取，`worker_concurrency` 在 `startRuntime` 创建 Worker 池时读取，修改后需要重启。

## 已验证证据

后端 Service 的 Gemini 透传预算与模型能力回归、Handler、Repository、gateway route、middleware 定向测试通过；前端异步配置/策略/任务中心相关 19/19 通过；本次页面/静态文档同步后 `pnpm typecheck`、目标文件 ESLint、`pnpm build`、`git diff --check` 通过。Gemini `passthrough` 现在每张参考图只消费一次预算；已知 Flash Image/Pro Image 模型分别执行 3/14 张上限，未知模型继续使用全局上限。真实生产服务器、真实上游账号、Redis、PostgreSQL/testcontainers 和真实 OSS 未验证。

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
- 用户 API 页面：`/guides/async-image-api`（`frontend/src/views/user/GuideAsyncImageApiView.vue`）
- 对象存储/图库策略：`wiki-new/异步生图架构.md`、`wiki-new/图片图库与对象模型.md`

## 下一步建议

1. 对 `execution_timeout` 建立上游请求 ID、账号用量和本地账单的对账指标；当前墙钟超时会在仍有心跳时把任务标为失败，这是防止重复生成的设计取舍，但尚未做真实上游成本验收。
2. 对上游返回结果增加总张数、总字节和单任务 staging 峰值保护，再进行隔离环境的 Redis/PostgreSQL/对象存储端到端演练。
