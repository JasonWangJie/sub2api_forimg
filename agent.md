# AI 交接文档

## 2026-08-31 生产清理结果

- 用户已明确授权连接 `108.186.246.14` 并清理近几天卡住任务。已在 PostgreSQL `sub2api` 单事务结束 7 个超过 1 小时的 `invoking` 任务。
- 每个任务状态改为 `failed`，错误码 `admin_terminated`，清空加密请求载荷并写入 `admin_task_terminated` 事件。`execution_unknown` 既有终态未改写。
- 复核 `stuck_nonterminal_over_1h=0`；当前 `invoking` 仅为新开始任务。生产二进制仍未部署本地修复，也未重启服务。

## 2026-08-31 本轮修复上下文

- 异步 Worker 超时后使用 `context.WithoutCancel` 派生短超时 context 写入终态，避免 `context canceled` 使任务永久停在 `invoking`。
- 恢复循环调用 `ListTimedOutInvokingAsyncImageTasks`，按 `started_at`/`created_at` 墙钟执行时限把忽略取消的调用收敛为 `execution_unknown`。
- 管理员任务中心新增 `POST /api/v1/admin/async-image-tasks/{task_id}/terminate`，服务层使用版本号和状态 CAS，允许将可终止状态置为 `failed`（`error_code=admin_terminated`）；前端列表和详情均有确认按钮。
- 定向 Go Service/Handler、前端 API Vitest 7/7、`pnpm run typecheck` 已通过；完整 Service 包仍有既有外部 OpenAI token 对比用例 `TestEstimateOpenAIInputTokens_CompareWithOpenAIAPI` 失败。生产服务器 `108.186.246.14` 未部署、未重启，历史任务未清理。

## 当前上下文

这是 `JasonWangJie/sub2api_forimg` Fork，默认在 `main` 开发。交接基线（以本轮命令实际输出为准）：

- HEAD：`fd10de10fb3fe22e92e0ba7916ccd0433a8316c5`
- `git describe --tags --always --dirty`：`v0.1.173.36-1-gfd10de1-dirty`
- `backend/cmd/server/VERSION`：`0.1.173.36`
- 最近功能：异步生图账号尝试审计、Gemini 快速换号、容量重试排除最近失败账号、`execution_unknown` 对账待处理、参考图混合传输回退与下载闸门
- 本次交接维护：任务查询增加 `error_code` 601-609，失败时透传 Worker 保存的脱敏上游原文；同步 API 文档与用户指南；本轮继续完善 `异步生图接口文档new.md` 的状态码、轮询、原文样例和生产错误快照
- 本次冒烟结论：`go test ./internal/handler -run 'AsyncImageFailure|WriteBBQuery' -count=1`、完整 `go test ./internal/handler`、`pnpm typecheck` 和 `git diff --check` 均通过；前端生产构建未在本轮执行
- 生产环境：已只读连接 `108.186.246.14` 查看服务状态和日志，未修改、未部署、未重启

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

后端 Service 的 Gemini 透传预算与模型能力回归、Handler、Repository、gateway route、middleware 定向测试通过；本轮最近三次提交冒烟覆盖异步/Gemini/网关/迁移用例、五包加 `cmd/server` 编译检查、`go test ./migrations` 和 `go vet ./migrations`；前端异步 Vitest 14/14、`pnpm typecheck`、目标 ESLint、`pnpm build`、`git diff --check` 通过。构建仅有既有 pnpm overrides、Browserslist、动态导入和大 chunk 警告。真实生产服务器、真实上游账号、Redis、PostgreSQL/testcontainers、真实 OSS 和 Fork CI 未验证。

完整 Service 包测试仍有既有外部 OpenAI token 对比用例因网络/API 不可用而失败；这不是本次任务中心改动造成的。

## 每次任务完成的强制动作

1. 用命令确认实际 HEAD、`VERSION`、分支和工作树状态。
2. 更新 [readmenew.md](readmenew.md) 的版本快照、功能摘要和入口链接。
3. 更新 [开发台账.md](开发台账.md) 的任务条目、完整 SHA、验证证据和未完成项。
4. 更新本 `agent.md` 的当前上下文和下一步，避免下一位助手依赖过期摘要。
5. 运行 `git diff --check`，检查 Markdown 链接和文档命名；没有真实证据的项目保留为“未验证”。

## 2026-08-29 本轮交接：异步生图错误对照码与生产审计

- 任务查询 `GET /v1/images/tasks_async/{task_id}` 对失败任务仍返回 HTTP `200`，新增顶层整数 `error_code`；`fail_reason` 优先返回已脱敏的 `ErrorMessage`，不再把 `upstream_failed` 折叠成 `upstream image generation failed`。
- 对照码：`601` 内容安全/第三方相似性，`602` 参考图 URL 拉取，`603` 账号或容量，`604` 输入参数，`605` 上游限流，`606` 通用上游，`607` 图片输出解析，`608` 超时/未知，`609` 存储或计费后处理。6xx 是应用层分类，不是 HTTP 状态码。
- 生产只读审计（`2026-08-22 00:00` 至 `2026-08-29 11:45`，服务器时间）记录 `async_image.upstream_failed` 2312 次：400/502/503/504/429/524/403/408 分别为 1526/319/125/114/113/50/64/1；主要为参考图抓取失败 1231、账号容量耗尽 733、Gemini Invalid request 154、第三方相似性 36、内容安全至少 29、输入问题 16、通用失败 4（关键词分类可能重叠）。
- 已更新：`backend/internal/handler/durable_async_image_handler.go`、对应 Handler 测试、`docs/DURABLE_ASYNC_IMAGE_API.md`、`异步生图接口文档new.md`、`frontend/src/api/imageWorkbench.ts`、`frontend/src/views/user/ImageWorkbenchView.vue`、`frontend/src/views/user/guideAsyncImageApiContent.ts`、三份交接记录。
- 已验证：`go test ./internal/handler -run 'AsyncImageFailure|WriteBBQuery' -count=1`、完整 `go test ./internal/handler`、`pnpm typecheck`、`git diff --check`；未执行生产部署、重启、前端生产构建、真实上游或 OSS 端到端验证。

## 2026-08-31 本轮交接：异步生图接口文档更新

- 更新 `异步生图接口文档new.md`：补充 Base URL、按 `Retry-After` 轮询、失败响应 `error_code` + `fail_reason`、HTTP 408/429/502/503/504/524 说明、上游原文样例和 2026-08-22 至 2026-08-29 生产错误快照。
- 本轮仅文档变更，未重新执行测试；`git diff --check` 已通过，前一轮 `go test ./internal/handler` 与 `pnpm typecheck` 结果仍有效。

## 2026-08-31 本轮交接：生产异步生图卡住任务诊断

- 已按用户明确授权只读连接 `root@108.186.246.14`。服务 `sub2api` 运行中，生产二进制为 `0.1.173.36`、commit `0b2bf648dc0c1616068b90a861cd71c496da1a77`，本轮未修改、部署或重启。
- `2026-08-31T13:25:34Z` 状态快照：`succeeded=13626`、`failed=1278`、`execution_unknown=60`、`invoking=8`；7 个 `invoking` 已超过 120 秒租约，全部是 Gemini `gemini-3-pro-image-preview`，事件都停在 `queued -> invoking`。
- 每个卡住任务均有 `async_image.execution_timeout_cancel`（900 秒）后紧接 `async_image.execution_unknown_transition_failed`（`context canceled`）。调用链位于 `backend/internal/handler/durable_async_image_worker.go:272`、`:359`、`:582`、`:596`、`:626`、`:1950`：超时取消 `processCtx` 后，终态 transition 仍复用该已取消 context，数据库写入失败，任务永久留在 `invoking`。
- 运行参数来自后台 `settings.image_storage_config.async_image`：`worker_concurrency=50`、`worker_lease_seconds=120`、`recovery_interval_seconds=30`、`execution_timeout_seconds=900`；Redis PING 正常，`pg_stat_activity` 无长时间 active SQL 证据。恢复扫描应处理 stale `invoking`，但本次观察未产生 `stale_invocation_detected`，需后续修复/告警验证。
- 近 7 天失败分类：`upstream_failed=356`、`invalid_reference_image=297`、`local_capacity_exhausted=63`、`execution_timeout=46`、`upstream_capacity_exhausted=27`、`execution_unknown=34`。不要自动重复提交卡住任务，避免重复生成或计费；人工收敛、改 context 和生产部署均待明确授权。
- 文档已同步 `异步生图接口文档new.md` 与 `docs/DURABLE_ASYNC_IMAGE_API.md` 的卡住诊断，另同步 `readmenew.md` 和 `开发台账.md`。本轮末需执行 `git diff --check`、检查 Markdown 链接，并保持“未修复/未部署/未测试”边界。

## 2026-08-27 本轮交接：Gemini 异步 400 与输出错误分类

- 当前实际基线：分支 `main`，HEAD `3c682ec2e346042229b6551d3346e3e8e363cff9`，`git describe`=`v0.1.173.35-1-g3c682ec-dirty`，`backend/cmd/server/VERSION`=`0.1.173.35`。工作树包含本轮未提交代码/测试和文档变更；不要回滚用户已有改动。
- 已修改 `backend/internal/service/gemini_async_image_errors.go`：异步 Gemini 400 在响应映射前分类；账号级/未包装 `Invalid request` 返回 `UpstreamFailoverError`，保留响应体、响应头和 request ID；参考图抓取、像素、格式、内容政策、参数错误排除。
- 已修改 `gemini_chat_completions_compat_service.go` 与 `gemini_messages_compat_service.go`：ErrorPolicy `Skipped`、`Matched` 和普通路径都先处理异步账号级 400，避免先写 `Invalid request` 导致外层 `upstream_error_response_already_written` 而停止换号。
- 已修改 `durable_async_image_worker.go`：完整上游响应的 MIME/图片容器/Base64/空图片错误记为 `upstream_invalid_output`；真正没有完整响应的请求继续标记 `execution_unknown` 并等待对账。
- 新增测试：`gemini_async_image_errors_test.go`；补充 Gemini 兼容服务不提交响应的 400 测试、Handler 输出错误分类测试。
- 已通过：Gemini/Handler 定向测试；完整 `go test ./internal/handler`；`go test ./internal/service -run 'Gemini|ErrorPolicy' -count=1`；`go test ./internal/repository ./internal/server/routes ./internal/server/middleware`；`go build -tags embed ./cmd/server`；`git diff --check`。Service 完整包仍可能包含既有外部 OpenAI token 对比用例失败，不能据此宣称全包通过；未执行生产部署、重启或真实上游账号轮换。
- 本轮补充修复：池模式 ErrorPolicy failover 现在保留完整上游响应头，异步任务可持久化 `x-goog-request-id`；Gemini 400 分类器不再把空响应误判为 `Invalid request`，仅明确错误文本触发换号。
- 下一步：如需生产生效，必须另行授权构建、备份、部署、重启和冒烟验收；部署后重点观察 `account_attempts`、`attempted_account_ids`、`upstream_request_id` 及 `upstream_invalid_output`/`execution_unknown` 分类。

该要求同时写入根目录 [AGENTS.md](AGENTS.md) 和 [.cursor/rules/fork-release-deploy.mdc](.cursor/rules/fork-release-deploy.mdc)，属于仓库协作规则，不是可选建议。

## 下一位助手快速入口

- 异步任务状态机：`backend/internal/handler/durable_async_image_worker.go`
- 请求构造与参考图：`backend/internal/service/async_image_protocol.go`
- 运行参数默认值与归一化：`backend/internal/service/image_storage_settings.go`
- 任务字段和迁移：`backend/internal/service/async_image_task.go`、`backend/migrations/223_ZJ_async_image_reference_retry_state.sql`、`backend/migrations/224_ZJ_async_image_account_attempts.sql`
- 管理端设置：`frontend/src/views/admin/BackupView.vue`、`frontend/src/views/admin/asyncImageRuntimeConfig.ts`
- API 契约：`docs/DURABLE_ASYNC_IMAGE_API.md`
- 用户 API 页面：`/guides/async-image-api`（`frontend/src/views/user/GuideAsyncImageApiView.vue`）
- 对象存储/图库策略：`wiki-new/异步生图架构.md`、`wiki-new/图片图库与对象模型.md`

## 下一步建议

1. 对 `execution_timeout` 建立上游请求 ID、账号用量和本地账单的对账指标；当前墙钟超时会在仍有心跳时把任务标为失败，这是防止重复生成的设计取舍，但尚未做真实上游成本验收。
2. 对上游返回结果增加总张数、总字节和单任务 staging 峰值保护，再进行隔离环境的 Redis/PostgreSQL/对象存储端到端演练。

## 2026-08-25 本轮交接

- 工作树基线：`main`，HEAD `b958648186fd9079d21111a7f32fa2a2a1a7566a`；`git describe` 在本轮文档同步后为 `v0.1.173.33-2-gb958648-dirty`；`backend/cmd/server/VERSION` 为 `0.1.173.33`。生产服务器未连接、未修改、未重启。
- 代码新增 `backend/internal/service/async_image_account_attempt.go` 与迁移 `backend/migrations/224_ZJ_async_image_account_attempts.sql`。数据库任务模型保存账号尝试历史、去重账号 ID 和对账状态；失败 transition 会携带最近账号和请求 ID。当前站内任务中心通过 view 映射未展示这些字段，不能把它描述为已交付的管理端审计展示。
- `durable_async_image_worker.go` 在启动时记录 configured/actual worker 数；异步上下文传递最近失败账号排除列表和 Gemini `maxSwitches`。Gemini 兼容服务异步网络请求的同账号重试预算固定为 1，避免 5 次超时后才切换。
- 管理端字段：`gemini_async_max_account_switches`，默认 3、范围 0–16；保存后普通运行参数热读取，Worker 数仍只在进程启动时生效。生产此前只读核实数据库设置为 50，必须重启后从新增启动日志确认实际值。管理员异步任务详情现展示尝试账号数、去重 ID、对账状态和可折叠尝试历史；用户接口不返回这些内部字段或完整上游 request ID。
- 已执行并通过：`go test ./internal/service -run 'AsyncImage|Gemini' -count=1`、`go test ./internal/handler -run 'AsyncImage|Gateway' -count=1`、`go test ./internal/repository -run 'AsyncImage|Migration' -count=1`、三包 `-run '^$'` 编译检查、`frontend pnpm typecheck`、`frontend pnpm build`。Build 仅有既有 chunk/Browserslist/动态导入警告。
- 本轮额外通过：`go test ./migrations -count=1`、`go vet ./migrations`、前端异步 Vitest 4 文件共 14 项和目标文件 ESLint；`git diff --check` 通过。
- 后续重点：网关若提供按上游 request ID 查询接口，再实现 `reconciliation_status=pending` 的主动对账；在此之前禁止自动重放 `execution_unknown`。补充真实网关账号轮换、容量耗尽和上游成本对账端到端测试；为管理员审计历史补充真实上游对账运行时演练。

## 2026-08-26 本轮交接

- 实际状态：分支 `main`；HEAD `22fc75bb1e8f231e57b953cfc7c0aad3e5c50a9a`；`git describe`=`v0.1.173.34-1-g22fc75b-dirty`；VERSION=`0.1.173.34`。工作树含用户既有改动和 `diff-review.txt`，不得回滚或删除。
- 新增修正：OpenAI/Gemini 异步普通 `400 Invalid request` 触发换号；明确 `image_url fetch failed`、`download ... reference image`、带远程抓取上下文的 `INVALID_IMAGE` 容器错误不走通用换号，而由混合模式切换本地参考图；裸 multipart 图片损坏不回退；Gemini messages 和 Chat Completions 路径行为一致。
- 本地参考图下载保持独立并发闸门与短时缓存，闸门获得后再次检查缓存；OpenAI 本地回退为 multipart，Gemini 为 inlineData。单账号异步超时默认 300 秒，超过后重新选择账号；无完整响应仍为 `execution_unknown`，不自动重放。
- 验证已通过：`go test ./internal/handler -run 'AsyncImage|Failover|Gemini.*Async|OpenAI.*Image' -count=1`；`go test ./internal/service -run 'AsyncImage|Gemini.*(Async|Image)|OpenAI.*(Image|Upstream)|Reference|Failover' -count=1`；`go test ./internal/repository -run 'AsyncImage|Migration' -count=1`；`pnpm vitest run`（239/1620）；`pnpm typecheck`；`pnpm build`；`git diff --check`。
- 未做事项：未连接生产服务器，未做真实网关账号轮换/请求 ID 对账、Redis/PostgreSQL、OSS 或 Fork CI 验收。构建的 Browserslist、动态导入和大 chunk 警告为既有提示。

## 2026-08-31 本轮交接：近48小时错误分类与容错码

- 生产只读调查：服务器 `108.186.246.14` 最近 48 小时失败任务为 `upstream_failed=103`、`invalid_reference_image=70`、`upstream_capacity_exhausted=3`、`execution_unknown=2`，另有本轮已授权管理员结束 `admin_terminated=6`；无空 `error_code` 失败任务。
- 未纳入既有 601-609 的典型文案为 `上游生图失败（HTTP 400）：由于我这边发生了错误，我未能生成图片。`；代码新增稳定容错码 `610`（未分类上游错误）。未知内部码不再默认为 606，若有安全保存的 `error_message`，查询接口会原样返回脱敏后的 `fail_reason`。
- `invalid_reference_image` 的 `IMAGE_TOO_MANY_PIXELS`、尺寸上限和 `IMAGE_MIME_MISMATCH` 现在按明确输入错误归 604；CDN/DNS/TLS/下载超时仍归 602。
- 修改位置：`backend/internal/handler/durable_async_image_handler.go`、对应 Handler 测试、`docs/DURABLE_ASYNC_IMAGE_API.md`、`异步生图接口文档new.md`、用户指南。
- 验证：`go test ./internal/handler -run 'TestAsyncImageFailure|TestWriteBBQueryFailedIncludesTaskIDAndFailReason' -count=1`、`go test ./internal/handler -count=1`、`git diff --check` 通过。
- 运行边界：生产当前仍为旧二进制，未部署、未重启；如需生效须另行授权构建与发布。

## 2026-08-31 本轮交接：异步接口冒烟与安全审查

- 冒烟通过：异步 Handler/Service 状态转换、超时终态、恢复扫描、管理员终止、路由注册；完整 Handler 测试、异步 Service 定向测试、`pnpm typecheck`、`go vet ./internal/handler ./internal/service`、`git diff --check` 均通过。
- 修复异步 Chat Completions 兼容服务未配置时提前返回路径的 account-attempt context 泄漏；该问题会留下超时计时器，现已显式取消。
- 安全结论：管理员接口由 AdminAuth/合规中间件保护，终止操作使用状态+版本 CAS；`execution_unknown` 禁止自动重放；`610` 未知错误仅用于保留原文和排查，不应盲目重试。
- 剩余边界：生产服务器仍运行旧二进制；真实上游、Redis/PostgreSQL/OSS 端到端、部署后告警和账单对账尚未验证。
- 生产只读复核 `2026-08-31 14:50 UTC`：`invoking=3`，超过 1 小时为 `0`；当前任务均为分钟级，未见新的长时间卡住任务。
