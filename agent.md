# AI 交接文档

## 当前上下文

这是 `JasonWangJie/sub2api_forimg` Fork，默认在 `main` 开发。交接基线：

- HEAD：`3c682ec2e346042229b6551d3346e3e8e363cff9`
- `git describe --tags --always --dirty`：`v0.1.173.35-1-g3c682ec-dirty`
- `backend/cmd/server/VERSION`：`0.1.173.35`
- 最近功能：异步生图账号尝试审计、Gemini 快速换号、容量重试排除最近失败账号、`execution_unknown` 对账待处理、参考图混合传输回退与下载闸门
- 本次交接维护：补齐 Gemini messages 路径的异步 400 换号与参考图抓取错误分类；确认 OpenAI/Gemini 上游 CDN 抓取失败会交给 Worker 持久化切换本地 multipart/inlineData；同步三份交接记录
- 本次冒烟结论：后端 handler/service/repository 异步定向测试、前端全量 Vitest（239 文件/1620 断言）、类型检查和生产构建均通过；构建只有既有警告；超时成本对账、真实上游/存储端到端验收仍未完成
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

后端 Service 的 Gemini 透传预算与模型能力回归、Handler、Repository、gateway route、middleware 定向测试通过；本轮最近三次提交冒烟覆盖异步/Gemini/网关/迁移用例、五包加 `cmd/server` 编译检查、`go test ./migrations` 和 `go vet ./migrations`；前端异步 Vitest 14/14、`pnpm typecheck`、目标 ESLint、`pnpm build`、`git diff --check` 通过。构建仅有既有 pnpm overrides、Browserslist、动态导入和大 chunk 警告。真实生产服务器、真实上游账号、Redis、PostgreSQL/testcontainers、真实 OSS 和 Fork CI 未验证。

完整 Service 包测试仍有既有外部 OpenAI token 对比用例因网络/API 不可用而失败；这不是本次任务中心改动造成的。

## 每次任务完成的强制动作

1. 用命令确认实际 HEAD、`VERSION`、分支和工作树状态。
2. 更新 [readmenew.md](readmenew.md) 的版本快照、功能摘要和入口链接。
3. 更新 [开发台账.md](开发台账.md) 的任务条目、完整 SHA、验证证据和未完成项。
4. 更新本 `agent.md` 的当前上下文和下一步，避免下一位助手依赖过期摘要。
5. 运行 `git diff --check`，检查 Markdown 链接和文档命名；没有真实证据的项目保留为“未验证”。

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
