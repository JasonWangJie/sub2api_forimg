# Sub2API Fork 二次开发总览

> 本文件只记录 `JasonWangJie/sub2api_forimg` Fork 的二次开发与交接信息。安装、升级、在线更新和 Release 以当前仓库文档为准；原作者文档仅作为上游参考。

## 从这里开始

换电脑、换会话或交给新的 AI 后，按以下顺序恢复上下文：

1. 阅读 [wiki-new/文档索引.md](wiki-new/文档索引.md)。
2. 阅读 [wiki-new/当前状态与完成度.md](wiki-new/当前状态与完成度.md) 和 [wiki-new/智能助手交接清单.md](wiki-new/智能助手交接清单.md)。
3. 根据任务阅读异步任务、工作台、图库或审核专题。
4. 运行 `git status --short --branch`、`git rev-parse HEAD`、`git describe --tags --always --dirty`，以实际工作树为准。
5. 不要把本文中的开发基线当成当前提交；以 `origin/main` 和实际命令输出为准。Fork CI 仍未运行，不能把主线已推送误写成 CI 已通过。

`wiki-new/` 下所有 Markdown 文件统一使用简体中文语义文件名。后续新增文档不得恢复英文 slug、拼音或英文编号前缀；文件重命名后必须同步更新全仓链接并检查断链。

## 当前版本快照

记录日期：`2026-08-14`（续更：1 Key 异步生图双平台分组映射；存储后端可随时切换；管理端清理覆盖异步任务结果与「清理全部」；固定 `TOTP_ENCRYPTION_KEY` 才能保存 OSS Secret；本机目录 `DATA_DIR` 回落与保存错误可读化）。

| 项目 | 当前记录 |
|---|---|
| 发布版本文件 | `backend/cmd/server/VERSION`（以仓库文件为准） |
| 文档记录时 HEAD | `91ecca6114a6e5e8eecac89eccb76ce04603a38b`（工作树可能 dirty；以 `git rev-parse`/`git status` 为准） |
| HEAD 描述 | `v0.1.173.3-dirty`（以 `git describe --tags --always --dirty` 为准） |
| 当前及后续默认分支 | `main` |
| 已合并原作者主线 | 以 `git log` / `upstream/main` 实际为准 |
| SC 上传安全迁移 | `backend/migrations/187_ZJ_async_image_upload_reservations.sql` |
| 延期广场投稿迁移 | `backend/migrations/188_ZJ_plaza_submission_deferred_upload.sql` |
| 结果上传意图迁移 | `backend/migrations/189_ZJ_async_image_result_upload_intents.sql` |
| API Key 双平台生图映射 | `backend/migrations/221_ZJ_api_key_platform_groups.sql` |
| Fork CI | 发版时在 `JasonWangJie/sub2api_forimg/actions` 核对实际结果 |

最终交付必须同时报告 `VERSION`、完整 SHA、`git describe`、推送分支和 CI 链接/结果。历史 `2026-07-22` 基线与测试证据仍保留在下方「当前完成度」与 [wiki-new/测试与验收记录.md](wiki-new/测试与验收记录.md)。

## 本 Fork 的图片能力

本 Fork 在原有网关能力上增加了两层图片工作流：

- 持久化异步生图兼容层：为下游提供 Gemini BB、OpenAI BB 和 Gemini SC 方言，任务、结果、账务与 OSS 状态持久化。
- 站内图片产品层：图片工作台根据所选 API Key 的当前分组自动选择实时或异步执行；**实时结果默认只保存在本机浏览器**；公开作品必须显式投稿并经管理员审核；**审核通过后由用户再同步上传 OSS 才进入广场**，避免恶意投稿占满 OSS。

两层能力共享原有账号调度、模型映射、内容审核、故障切换、并发控制、资格检查和计费链路。BB、SC 只是下游协议方言，不是新的上游供应商。

## 工作台模式矩阵

工作台不提供手工“实时/异步”切换。每次提交前重新读取 Key 能力；平台、分组开关或能力版本发生变化时，应停止本次提交并要求用户重新确认。

**持久异步 API（下游客户端）** 支持「1 Key 双用」：Key 可额外绑定 `image_platform_groups.gemini` / `openai`，按路径选计费组（映射优先，主分组平台匹配则回退）。配置见 [wiki-new/API密钥双平台生图映射.md](wiki-new/API密钥双平台生图映射.md)。

| API Key 当前分组 / 映射 | 执行模式 | 实际入口 |
|---|---|---|
| OpenAI，异步开关关闭 | 实时 | `/v1/images/generations`、`/v1/images/edits` |
| OpenAI，异步开关开启（主组或 openai 映射） | 异步 | `/v1/images/generations_oa`（有有效 `image_urls` / 参考图 → 图生图，否则文生图）；查询 `/v1/images/tasks_async/{task_id}` |
| Gemini，异步开关关闭 | 实时 | `/v1beta/models/{model}:generateContent` |
| Gemini，异步开关开启（主组或 gemini 映射） | 异步 | `/v1/uploads/images_sc`、`/v1/images/generations_sc`；查询同 OpenAI：`/v1/images/tasks_async/{task_id}` |
| Grok 图片分组 | 仅实时 | 现有 `/v1/images/generations`、`/v1/images/edits` |
| Antigravity 或其他平台（且无 gemini/openai 映射） | 不可用 | 不在图片工作台显示为可用 Key |

失败、超时、`execution_unknown`、`403` 或 `409` 都不能让工作台在实时和异步链路之间自动回退。异步重试必须复用同一请求字节和 `Idempotency-Key`。

实时生图：结果留在本机 IndexedDB；「投稿审核」只提交元数据（checksum/尺寸等），**此时不上传 OSS**。管理员批准后状态为 `approved_pending_sync`；用户再次上线在工作台/个人图库点击「同步至图片广场」时才上传并直接 `published`。异步任务结果仍走服务端 OSS 归档；工作台侧栏不再因归档失败提示「等待恢复归档」。

## 数据与公开模型

数据库迁移 `182_ZJ_add_image_plaza.sql` 建立初版图片广场；迁移 `185_ZJ_async_image_tasks.sql` 建立持久异步任务中心；迁移 `186_ZJ_image_library_and_plaza_moderation.sql` 建立统一图片对象、个人图库、审核投稿、举报、事件、Outbox、清理任务和旧广场迁移状态；迁移 `187_ZJ_async_image_upload_reservations.sql` 增加 SC 上传的两阶段 admission、幂等 reservation、URL alias 和崩溃恢复意图；迁移 `188_ZJ_plaza_submission_deferred_upload.sql` 建立本机持图延期投稿队列表 `image_plaza_submission_requests`；迁移 `189_ZJ_async_image_result_upload_intents.sql` 为异步结果增加 PUT 前持久化意图，并为 Outbox 增加 claim token 所有权；迁移 `221_ZJ_api_key_platform_groups.sql` 为 API Key 增加 Gemini/OpenAI 异步生图计费分组映射（1 Key 双用）。

本 Fork 自研 SQL 迁移统一使用 `NNN_ZJ_description.sql`，原作者迁移文件不加该标记。`182_add_image_plaza.sql` 和 `185` 至 `189` 的无标记旧名仅作为已部署数据库兼容别名保留在迁移器映射中，不再作为仓库文件存在；同编号的上游 `182_prompt_audit_full_prompt.sql` 保持原名。

关键约束：

- 新图片默认私有；公开必须由用户显式投稿并由管理员批准。
- **实时本机投稿在审核前不占用 OSS**；只有 `approved_pending_sync` 后用户同步才写入对象存储并进入广场。
- OSS 只保存实际对象；对象 key 按 UTC **年/月/日**分区（如 `library/{userId}/2026/07/22/...`、`{prefix}/results/2026/07/22/{taskId}/...`）。数据库保存稳定的 provider、bucket、object key 和校验元数据，不保存过期预签名 URL。
- 异步任务结果与图库引用同一 `image_storage_objects` 身份，不能因为一处解除引用就删除仍被其他记录使用的对象。
- 旧 `image_plaza_items` 的历史公开数据先强制转私有，再由可恢复 Worker 严格校验并迁入私有图库和 `pending_review` 投稿。
- 危险、损坏、路径越界或不支持的旧图片只计入隔离数量，不继续公开。
- 普通广场只返回已批准、未撤回、未隐藏且未过期的投稿。
- SC 上传在 multipart body 前先以 `async_image_upload_attempts` 做 PostgreSQL rolling-rate admission，读取有界文件后、解码/OSS 前再以 `async_image_upload_reservations` 原子执行幂等和 Key 级字节额度；`async_image_input_url_aliases` 绑定原 URL 与重签 URL 的所有权。
- 上传默认 20 次/Key/分钟（最大 1000）、默认 1 GiB/Key 输入额度（最大 100 GiB）、单图/请求有效图片负载硬上限 64 MiB、单次 OSS Put 默认 300 秒且最大 600 秒、输入最长保留 720 小时。相同幂等上传只重签并返回 `X-Idempotency-Replayed: true`；冲突、处理中或结果墓碑返回 `409`。
- 每个输入对象最多保留 128 个重签 URL alias。注册由输入对象行锁串行化，过期 alias 仍作为所有权墓碑保留；第 129 个新 alias 返回结构化 `429`，不会无限扩张表。
- SC 客户端文件名会净化且不进入对象 key；OSS 前持久化 deterministic object intent。失败或 stale intent 第一次 Delete 后保留恢复事实，至少十分钟后二次 Delete 成功才移除；未清理 failed intent 始终计入 Key 容量。
- 异步结果的每个 OSS PUT 也必须先写入 `async_image_result_upload_intents`。对象 key 由任务提交日期、任务号和结果序号确定；部分上传或进程崩溃后只覆盖同一 key，不重新生成。结果清单落库时同事务删除 intent；过期孤儿由 retention Worker 在确认没有任务、图库或广场活动引用后删除。
- `2026-08-14`：管理端清理已覆盖异步任务结果与「清理全部」；存储后端切换不再被活跃对象拦截，但切换前仍建议先清理。

## 异步并发与性能边界

- Redis `ready/delayed/active` 负责投递，PostgreSQL 负责任务事实。每次 Reserve 生成独立 lease token；心跳、Ack 和延迟重排都以 Lua 原子校验 token，旧 Worker 不能操作新 Worker 的租约。若唯一上游调用已经进入 `invoking`，丢失 Redis 租约的原 Worker 只保留数据库心跳并完成该次调用；上传/计费阶段则取消并交给新 Worker 幂等续跑。
- PostgreSQL `updated_at` 同时覆盖调用、上传和账务后处理心跳。Redis 租约被恢复但数据库心跳仍在有效窗口时，后来的投递不会提前把任务标为 `execution_unknown`；只有数据库心跳也超过租约窗口才进入不确定状态。
- Outbox 每批认领写入 UUID claim token；发布、失败回退和终态更新都校验 `id + claim_token`，超时的旧 dispatcher 不能覆盖新 dispatcher 的结果。
- 本地图片并发门禁拒绝发生在确认未调用上游时，任务从 `invoking` 回到 `queued` 并延迟重排；真正的上游 `429` 不走该分支。
- 单实例 `worker_concurrency` 硬上限为 64，默认 4；多实例总并发是各实例之和。Worker 数量只在进程启动时创建，修改该配置后必须重启服务。
- Gemini 参考图默认限制为单图 40 MP、最多 8 张、总计 64 MiB/80 MP；硬上限为单图 80 MP、16 张、总计 256 MiB/320 MP。绑定的 SC OSS 输入由 Worker 直接 `Read`，不再经预签名 URL 回环下载；读取后仍重新校验 MIME、完整解码、像素和 SHA-256。
- 单任务结果上传保持串行，避免少量图片下额外 goroutine、锁和峰值内存。扩容应先观察数据库连接池、Redis 命令延迟、图片并发门禁、staging 字节和 OSS 吞吐，再逐步提高实例数或 Worker 数。

## 计费不变量

图片工作台、异步任务和图库归档没有新建计价公式：

- Gemini/OpenAI/Grok 仍走现有分组、用户专属分组、账号倍率、订阅、余额、API Key/账号额度和图片计费规则。
- Gemini/OpenAI 的全部输出图片按实际解码宽高和实际数量计费。
- 混合规格输出按每张图片所属档位分别求和，不能用最大档位乘总数量。
- 异步任务上游成功后只 Prepare 一次固定账单；存储或账务重试不能重新调用上游、重新定价或重复扣费。
- 图库归档失败不能把已经成功的模型调用改成失败，也不能触发重新生成或再次计费。
- `execution_unknown` 禁止自动重调；若确需再生成，必须创建新任务号并接受第二次上游成本风险。
- 异步上游失败时任务 `error_message` 应包含 HTTP 状态码与脱敏后的上游正文摘要，不能只写笼统的 `upstream image generation failed`。

详情见 [wiki-new/计费与幂等.md](wiki-new/计费与幂等.md)。

## 存储、配额与安全默认值

全站使用一个当前图片对象存储，支持 `oss`（`qiniu` / `aliyun` / `tencent` / `custom_s3`）、`superbed` 聚合图床与 `local` 本机目录。后台「备份 / 异步生图对象存储」统一管理后端选择、凭证与异步/图库运行参数。

**存储后端可随时切换并立即生效**（不再因库内仍有活跃对象而拒绝保存）。切换后新上传写入新后端；旧对象若未迁移，查看/签名/删除可能失败或落到错误位置。生产切换前建议先在「图片管理 → 清理」执行 **清理全部存储对象** 或 **异步生图任务结果**，再改配置。按历史身份并行解析多套凭证的 resolver 仍是后续 P1。

保存对象存储 Secret / Superbed Token 前必须配置**固定** `TOTP_ENCRYPTION_KEY`（或 `config.yaml` 的 `totp.encryption_key`，可用 `openssl rand -hex 32` 生成）。未配置固定密钥时拒绝落库 Secret，避免重启后密文无法解密。

管理端清理 scope：

| scope | 含义 |
|---|---|
| `all` | 清理全部：图库项、异步任务结果、孤儿 `image_storage_objects`、SC 参考图与上传残留 |
| `async_results` | 仅异步生图任务结果（`async_image_results`） |
| `expired` / `deleted` / `user` | 仅个人图库资产（原有范围） |

本机目录的 `local_url`、对象 CDN、`async_image.public_base_url` 等公开访问地址不属于存储定位身份，可单独修改。

**本机目录 `data_dir` 留空时**：优先 `{DATA_DIR}/data/image_storage`（发行版 `DATA_DIR=/opt/sub2api` 时即 `/opt/sub2api/data/image_storage`）；保存前会做本机读写探测，失败返回可读错误而非 `internal error`。自定义路径须在 systemd `ReadWritePaths` 可写范围内。

| 配置 | 默认值 |
|---|---:|
| 图库/公开资产保留 | 90 天 |
| 每用户图库条目 | 1000 |
| 每用户对象总量 | 5 GiB |
| 单图字节 | 20 MiB |
| 单图像素 | 40 MP |
| 图库签名 URL | 3600 秒 |
| 每用户导入限频 | 20 次/分钟 |
| 每用户投稿限频 | 10 次/分钟 |
| 异步参考图保留 | 24 小时 |
| 异步 Worker | 每实例默认 4，硬上限 64；修改后重启 |
| 参考图任务总预算 | 默认 8 张、64 MiB、80 MP |
| SC 参考图 OSS 上传超时 | 300 秒（最大 600） |
| 每输入对象 URL alias | 最多 128 个 |
| 异步任务与结果保留 | 90 天 |
| 本机延期投稿 blob | 浏览器 IndexedDB，约 90 天 |

图片只接受完整解码成功且容器、魔数、实际 MIME 一致的 PNG/JPEG/WebP。SVG、HTML、JavaScript、伪 MIME、尾随载荷、超字节、超像素、解压炸弹和路径穿越必须拒绝。远程图片导入和参考图下载继续执行 HTTPS、DNS、重定向、内网地址、MIME、字节、像素和超时限制。

首页自定义 HTML 经 DOMPurify 严格净化；自定义 URL 使用受限 iframe sandbox 和 `no-referrer`。图片内容响应使用 `nosniff`、隔离 CSP 和安全的 `Content-Disposition`。

## 主要页面

- 用户图片工作台：`/image-workbench`
- 用户个人图库：`/image-library`
- 审核后图片广场：`/image-plaza`
- 用户/管理员异步任务中心：任务号加宽并可一键复制；列表行点击不打开详情，仅「查看」打开
- 管理员图片审核、举报、全站图库和清理：含「本机投稿审核」页签；清理支持全部存储 / 异步任务结果 / 过期 / 软删 / 指定用户
- 分组创建/编辑：“图片生成计费”区域内的“异步生图”开关
- 密钥创建/编辑：可选 Gemini / OpenAI 生图分组（`image_platform_groups`，1 Key 双用）
- 备份/存储设置：本机目录、聚合图床、对象存储、异步运行参数和图库保留/配额配置

## 当前完成度

工作树中已经存在工作台能力接口、实时/异步分流、Gemini 实时图片计费采集、服务端图库、统一对象引用、投稿审核、举报、维护 Worker、旧广场迁移、管理页面和安全校验实现。管理员批量审核 API/UI、旧数字/`imgpub_*`/`img_*` 删除兼容、Worker 优雅 `Stop()`、历史成功异步任务归档回填、永久归档错误终止重排，以及迁移 `187` 的 SC 上传安全层均已补齐。

`2026-08-14` 续更：存储后端（本机 / 图床 / OSS）可随时切换并立即生效；管理端清理新增 `async_results` 与 `all`；保存 OSS Secret 要求固定 `TOTP_ENCRYPTION_KEY`；**1 Key 双用**（`221_ZJ_api_key_platform_groups`）已实现，本地定向测试与 `service`/`handler`/`handler/admin` 包冒烟通过，详见 [wiki-new/API密钥双平台生图映射.md](wiki-new/API密钥双平台生图映射.md)。

`2026-07-22` 续更（多在 dirty 工作树，交付前需提交）：迁移 `188` 延期广场投稿；实时本机投稿+审核后同步上传；OSS 对象 key 年/月/日分区；异步任务中心复制任务号/禁止误开详情；工作台侧栏不再提示异步归档恢复；`upstream_failed` 写入真实 HTTP 状态与上游摘要。

`2026-07-22` 已把原作者 `upstream/main` 的 `5a8d6c4e4` 非快进合并为 `433cf0096`，并在代码验证提交 `6412b5eb7` 对应的合并后树上取得以下本地证据：

- Go `1.26.5`：图片计费、SC 上传、分组、hosted-image、Grok/调度定向测试通过；`go generate ./cmd/server` 成功且无生成差异；强制无缓存 `go test -tags=unit ./... -count=1` 用时 `277.9s`，`go test ./... -count=1` 用时 `204.4s`，独立 server build 成功。
- 所有本轮新增或修改的 Go 文件均通过 `gofmt`。仓库另有 5 个未被本轮修改的基线测试文件未格式化，路径和判定见 [wiki-new/测试与验收记录.md](wiki-new/测试与验收记录.md)。
- `pnpm install --frozen-lockfile`、ESLint、`vue-tsc --noEmit`、189 个 Vitest 文件/1277 项测试和 974 模块生产构建全部通过。合并中发现并以 `6412b5eb7` 修复了 `package.json` 与锁文件顶层 overrides 不一致。
- 本地 Vite 在 `http://127.0.0.1:3000/` 启动成功；内置浏览器控制器因当前运行环境缺失 `sandboxPolicy` 元数据而无法建立会话，因此合并后的视觉复验没有标记为通过。
- 历史本机 Chrome Playwright 10 个场景曾覆盖 `360/768/1280/1440/1920`、中英文和深浅主题；横向溢出、控件裁剪及 console error 均为 0，键盘焦点、工作台 `aria-live`、广场 dialog 焦点进入/关闭恢复均通过。该证据早于最后一批 SC/后台配置和上游合并，只作为历史基线。
- 首页 WebP 为 `79,374` 字节，已成功随页面加载。

这些本地结果覆盖迁移 `187`、最后一批 SC 上传代码和本轮上游合并；`188` 与延期投稿相关改动已有定向 Go/Vitest 通过，但尚未作为独立提交合入 `origin/main`。旧仓库的 GitHub Actions 证据不代表新仓库；新仓库首次发版必须在 `https://github.com/JasonWangJie/sub2api_forimg/actions` 核对实际运行，浏览器复验仍需在连接器可用的环境补做。

以下交付项仍是 `PENDING`，不能写成已完成：

- 真实 PostgreSQL/testcontainers 下的两阶段 admission、多 Worker、租约恢复、Outbox 重放、intent/OSS 部分失败、对象引用和 `185/186/187/188` 迁移验证。
- 合并后的桌面/移动端浏览器视觉复验；当前内置浏览器连接器被环境元数据阻断。
- 七牛、阿里、腾讯真实凭证，以及真实 Gemini/OpenAI/Grok 生成和逐笔计费联调。
- 新仓库 Actions 状态尚未写入本地验收证据：首次发版前确认工作流已启用；推送新 tag 后在 `sub2api_forimg/actions` 核对 Release、CI 与 Security Scan 的实际结果。

最新状态与已经执行过的测试证据只看 [wiki-new/当前状态与完成度.md](wiki-new/当前状态与完成度.md) 和 [wiki-new/测试与验收记录.md](wiki-new/测试与验收记录.md)，不要根据早期聊天记录推断“已经通过”。

## 文档索引

| 文档 | 内容 |
|---|---|
| [wiki-new/文档索引.md](wiki-new/文档索引.md) | 二次开发 Wiki 入口和真值顺序 |
| [wiki-new/异步生图架构.md](wiki-new/异步生图架构.md) | 持久异步任务状态机和恢复边界 |
| [wiki-new/接口契约.md](wiki-new/接口契约.md) | 下游异步协议与站内图片 API |
| [wiki-new/对象存储与保留策略.md](wiki-new/对象存储与保留策略.md) | OSS、对象引用、签名和保留策略 |
| [wiki-new/图片工作台.md](wiki-new/图片工作台.md) | Key 分组驱动的工作台实时/异步分流 |
| [wiki-new/API密钥双平台生图映射.md](wiki-new/API密钥双平台生图映射.md) | 1 Key 异步生图 Gemini/OpenAI 计费组分歧与配置 |
| [wiki-new/图片图库与对象模型.md](wiki-new/图片图库与对象模型.md) | 服务端图库和统一对象引用 |
| [wiki-new/图片广场审核与迁移.md](wiki-new/图片广场审核与迁移.md) | 审核广场、举报、安全迁移和维护 Worker |
| [wiki-new/本地开发运行手册.md](wiki-new/本地开发运行手册.md) | 本地前后端运行、Docker 联调和常见故障 |
| [wiki-new/生产部署升级与回滚手册.md](wiki-new/生产部署升级与回滚手册.md) | Fork 生产部署、HTTPS、OSS、备份、升级与回滚 |
| [发行版发布与安装操作手册.md](发行版发布与安装操作手册.md) | **版本号、打 tag 发 Release、一键安装/升级** |
| [deploy/FORK_RELEASE.md](deploy/FORK_RELEASE.md) | Fork 发行版一键安装/升级与上游合并保护 |
| [wiki-new/服务器数据库快速迁移手册.md](wiki-new/服务器数据库快速迁移手册.md) | 旧备份预热、停写后最终数据追平、账务校验与服务器切换 |
| [wiki-new/智能助手交接清单.md](wiki-new/智能助手交接清单.md) | 新电脑或新 AI 的接手步骤 |

## Git 远程约定

| 远程 | 用途 |
|---|---|
| `origin` | 用户 Fork：`JasonWangJie/sub2api_forimg`；后续默认直接推送 `main` |
| `upstream` | 原作者：`Wei-Shaw/sub2api`，只用于获取和合并原作者更新 |

不得推送到 `upstream`，不得对共享 `main` 强制推送，也不得为了同步上游使用 `git reset --hard` 覆盖本地定制。

## 下一位 AI 的一句话上下文

```text
这是 JasonWangJie/sub2api_forimg Fork，当前及后续默认在 main 开发和推送。先读 wiki-new/文档索引.md、当前状态与完成度.md、测试与验收记录.md、智能助手交接清单.md，以及 deploy/FORK_RELEASE.md 与 .cursor/rules/fork-release-deploy.mdc（发行版一键安装身份；合并 upstream 不得改回 Wei-Shaw）。Fork 自研 SQL 使用 NNN_ZJ_description.sql；182_ZJ 是初版图片广场，185_ZJ 是持久异步任务，186_ZJ 是统一图片对象/个人图库/审核广场，187_ZJ 是 SC 上传 PostgreSQL admission/幂等/恢复，188_ZJ 是本机延期投稿（审核通过后再同步 OSS），189_ZJ 是异步结果上传意图；上游 182_prompt、183、184 保持原名。工作台实时结果默认本机；投稿只交元数据；模式只能由 Key 当前分组决定；默认私有，公开需审核；计费必须复用现有链路。OSS key 按年月日分区。发布、安装、升级、在线更新和 GHCR 镜像必须使用 JasonWangJie/sub2api_forimg；上游检查继续使用 Wei-Shaw/sub2api。
```


### 启动方式：
# 终端 1 — 后端（启动时自动跑 185/186/187；端口占用会先杀再启）
cd d:\个人项目\Git仓库\sub2api_forimg\backend
.\run-server.cmd
# 或：powershell -File .\scripts\run-server.ps1

cd backend
.\scripts\run-server.ps1

# 默认 http://localhost:8080
# 终端 2 — 前端
cd d:\个人项目\Git仓库\sub2api_forimg\frontend
pnpm install
pnpm run dev

cd frontend
pnpm run dev

# 默认 http://127.0.0.1:3000 ，/api 与 /v1 代理到 :8080
