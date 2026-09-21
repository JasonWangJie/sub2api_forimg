# AI 交接文档

## 2026-09-21 当前交接：本地文档提交并合并 origin/main

- 用户要求合并远程并提交本地变更：已提交 OVH TCP 兜底与 sysctl 调优交接（`3d18116`），再 `git pull origin main` 合并远程 3 个提交（含 tag `v0.1.173.50` 的 Gemini 错误诊断功能）；`agent.md` / `readmenew.md` / `开发台账.md` 自动合并成功，无冲突。
- 合并后工作树干净基线：完整 HEAD `d48b1324588fcc87c104b528cfca71b5b723a9e2`，`git describe --tags --always --dirty`=`v0.1.173.50-3-gd48b132`，VERSION=`0.1.173.50`，分支 `main...origin/main [ahead 2]`（docs 提交 + merge）。本轮交接同步提交后会再领先 1 个提交；尚未 push。
- OVH 代理运行边界仍以 2026-09-16 交接为准：主机 `40.160.139.185`、TCP Reality `8443`、sysctl 缓冲调优与回退目录不变；本轮未连接或改动生产。
- 下一步：若需远端同步，由用户明确要求后再 `git push origin main`；业务侧仍待授权部署 `v0.1.173.50` 的 Gemini 诊断改动，以及账号级参考图上限。

## 2026-09-20 异步 Gemini 错误诊断持久化交接

- 已完成本地代码：异步 Gemini Worker 私有路径会保留经 `logredact` 脱敏、空白归一化和限长后的上游 `error.message`，并提取 provider code（优先 `error.status`）与 request ID；同步 Chat Completions 的客户端错误仍保持通用文案。
- 普通映射错误通过 `GeminiAsyncImageUpstreamError` 携带诊断；账号切换错误通过 `UpstreamFailoverError.ProviderErrorCode`、`ProviderErrorMessage`、`UpstreamRequestID` 携带。换号耗尽时仅异步上下文把安全 message/code 写回内部 recorder，避免再次退化为 `All available accounts exhausted`。
- `AsyncImageAccountAttempt` 的既有 JSONB 新增 `provider_error_code`，无需迁移；失败尝试保存 message/code/request ID，仓储现有逻辑会将 request ID 提升到任务顶层。Worker 最终错误文案因此可命中 611（超过 8 图）、612（缺少/未检测到参考图）、613（输入无法处理）。管理员详情和前端尝试历史可查看 provider code，用户接口继续隐藏整个尝试审计。
- 验证结果：本次定向 Service/Handler 用例通过；完整 Handler 包通过；Service 包排除既有外部 OpenAI token 对照测试后通过，未排除时仅该测试的三个外部 API 子用例失败；前端 `pnpm typecheck`、`git diff --check` 和三份记录的 Markdown 本地链接检查通过。未做真实 Gemini、数据库、浏览器、生产部署或重启。
- 当前快照：`main@defccfacba610450ac5f9fd06e0f54c2f25fbb7a`，describe `v0.1.173.49-1-gdefccfa-dirty`，VERSION `0.1.173.49`。保留用户原有两份 `wiki-new/` 修改及四份未跟踪迁移文档；不要误删、覆盖或写成本轮产物。
- 下一步仍是账号级参考图上限与候选路由过滤；生产上线须另行明确授权。部署后应新建 611/612/613 三类真实失败样本，核对任务 `error_message`、`upstream_request_id`、`account_attempts.provider_error_code` 和公共查询整数码，历史任务不会自动回填。

## 2026-09-20 Gemini 异步 400 生产诊断交接

- 用户明确授权只读登录 `40.160.139.185` 查看异步生图日志。生产 `sub2api` 在会话结束时仍为 `active`，二进制 `0.1.173.49`、commit `52b0cf537f2e091506d52d72692195f6720088d6`；本轮未改服务器文件、配置、数据库或服务，未部署、重启或重放任务，登录口令不得写入仓库。
- 过去 24 小时的滚动快照先得到 Gemini `succeeded=4739`、`failed=485`、`execution_unknown=4`、`invoking=2`；485 个失败均为内部 `upstream_failed` 和 `上游生图失败（HTTP 400）：Invalid request`。查询继续运行时失败增至 486，故数字只能作为 `2026-09-20 19:37–19:43`（Asia/Shanghai）的动态快照。
- 参考图数是当前最强的根因信号：`<=8` 参考图成功 4675/失败 150，`>8` 成功 64/失败 336（失败率 84.0%）。账号 1/25 的 `>8` 样本全部失败（125/186），账号 33 成功 64、失败 25。生产 `max_reference_images=16`，而本地模型表允许 `gemini-3-pro-image-preview` 14 张；当前路由没有账号级参考图能力，导致 9–14 张请求会落到实际只支持更低上限的账号。
- 不要把 `Invalid request` 当作已确认的上游原文。部署版本在 `writeGeminiChatCompletionsMappedError` 前能提取 `upstreamMsg`，但 `mapGeminiErrorBodyToClaudeError` 故意不给 mapped message 赋原始值，400 再回退为固定 `Invalid request`；Worker 最终以 `upstream_failed` 兜底。原始 body 日志未启用、内部异步请求没有 `ops_error_logs` 行、失败终态已清空 `request_payload`，历史具体原文不能补查。
- 修复方向分两层：第一层的异步 message/provider code/request ID 持久化已在本地完成（见上节），尚未部署；第二层账号参考图上限仍待实现。若要止血可把全局上限降到 8，但会牺牲账号 33 已证实存在的 9–12 张成功能力，必须由用户明确选择并授权修改。
- 本地实际基线仍为 `main@defccfacba610450ac5f9fd06e0f54c2f25fbb7a`，describe `v0.1.173.49-1-gdefccfa-dirty`，VERSION `0.1.173.49`；工作树保留此前三份记录修改和四份未跟踪迁移文档，本轮只继续修改 `readmenew.md`、`开发台账.md`、`agent.md`。详见 [开发台账.md](开发台账.md)。
- 完成前 `git diff --check` 与三份记录的 Markdown 本地链接检查均通过；未运行代码测试、CI 或真实上游重放。

## 2026-09-16 PostgreSQL 远程连接排查交接

- 用户明确授权只读登录 `40.160.139.185` 诊断；本轮未改生产配置/权限/数据，未部署或重启。不要记录登录口令，也不要把该服务器与历史 `108.186.246.14` 的授权或运行状态混用。
- 当前证据：宝塔 PostgreSQL 18.0，进程 `/www/server/pgsql/bin/postgres -D /www/server/pgsql/data`，实际仅监听 `127.0.0.1:25432`。文件 `postgresql.conf` 第 60 行 `listen_addresses='*'`，第 896 行 `port=25432`；`pg_settings.listen_addresses=localhost` 且 `pending_restart=true`，`pg_file_settings` 显示 `*` 未应用。配置于 2026-09-16 12:56 UTC 修改，服务自 2026-09-07 06:14 UTC 启动，reload 不能让监听地址生效。
- UFW 已放行 TCP 5432/25432，HBA 有 IPv4 远程密码认证规则且无解析错误；本机 22 可达，5432/25432 均连接被拒绝。默认 5432 不适用；SSH root 账号不是数据库登录角色，现有可登录角色为 postgres/sub2api/newapi，应使用用户原数据库账号。
- 重启方案已核对：宝塔 `/etc/init.d/pgsql` 管理该实例；若用户明确要求，可用 `runuser -u postgres -- /www/server/pgsql/bin/pg_ctl -D /www/server/pgsql/data -m fast -w restart`，使现有监听配置生效，不改端口、HBA、密码或业务数据。会短暂断开数据库连接；随后核对 `ss`、`pg_settings.pending_restart`、本机 PostgreSQL 协议和用户账号登录、应用重连。该命令本轮未执行，不能宣称公网数据库已恢复。
- 当前实际本地快照：`main`；完整 HEAD=`defccfacba610450ac5f9fd06e0f54c2f25fbb7a`；status=`## main...origin/main`加三份记录修改/四份既有迁移文档未跟踪；describe=`v0.1.173.49-1-gdefccfa-dirty`；VERSION=`0.1.173.49`。本轮仅增补三份记录，保留既有文档；[开发台账.md](开发台账.md) 记录命令、授权及未完成项。
- 日志再次确认监听参数必须重启；本地 `git diff --check` 和本轮新增文档链接检查通过。SSH 会话已退出，未保留数据库隧道；生产恢复仍待重启及后续实测，不能把只读诊断写成修复完成。

## 2026-09-16 new-api 图片迁移开发包交接

- 用户本轮要求提取本项目图片相关功能说明/方案，交给 Codex 在 new-api 网关重新开发；范围为文档生成到来源项目根目录。本轮已生成四份自包含开发文档，未在目标仓库开发，不能把文档交付写成 new-api 功能已实现。
- 入口：[newapi图片功能Codex开发任务书.md](newapi图片功能Codex开发任务书.md)，配套：[总纲](newapi图片功能迁移开发总纲.md)、[API兼容规范](newapi异步生图API兼容规范.md)、[页面/图库/广场规格](newapi图片页面与图库广场功能规格.md)。一起复制到目标根目录即可使用；可选源码索引不是目标必需依赖。
- 提取依据当前源码和迁移，已纠正旧 SC pending/completed/code-data 包装、固定 24 小时签名、本机/服务端图库混淆、默认自动归档、存储切换阻断等过时描述。管理员参考图 URL 保持仅详情；个人图库仅本机实时 Blob，异步结果默认只在任务系统；unknown 不自动重新生成。
- 目标 `F:/Code/Git/new-api` 只读 SHA=`69a50029819a26c53e6babd276d49cfe2f8880ad`、干净；实际使用 Token、字符串分组、Channel、GORM 三数据库、React/Rsbuild/Bun。最大迁移点是具体产物存储和跨重启持久账务，不能只复用内存 BillingSession 或直接拷贝 PostgreSQL SQL/Vue。
- 当前实际基线：`main`；完整 HEAD=`defccfacba610450ac5f9fd06e0f54c2f25fbb7a`；status为`## main...origin/main`加三份记录修改/四份新增文档；describe=`v0.1.173.49-1-gdefccfa-dirty`；VERSION=`0.1.173.49`。提取前工作树干净，本轮未提交、未改代码。
- 已检查：4份文档/16个合法JSON示例/65个源码与专题路径、开发包12个内部配套链接和三记录12个新增链接均通过；开发包可独立转交，无可点击文件链接越出四份文档。代码围栏/尾随空格/末尾换行与`git diff --check`通过。未做代码测试、真实三数据库/Redis/OSS/上游/浏览器/CI，未连接、修改、部署或重启生产。
- 下一步如用户要求在 new-api 开发，先转到用户指定目标checkout、读取其适用 AGENTS.md和实际Git状态，再按任务书实施。源仓库与目标仓库的状态/验证/生产授权独立记录，不能把本轮只读核对当目标代码验收。

## 2026-09-15 异步任务参考图 URL 交接

- 迁移 `226_ZJ_async_image_reference_urls.sql` 为 `async_image_tasks.reference_image_urls` 增加非空 JSONB 空数组默认值；只对新版任务写入远程 HTTP(S) URL，历史任务不回填，内联 `data:` 图片不重复持久化。
- 提交入口已把 Gemini BB/SC `normalized.Parts` 和 OpenAI `ReferencedImageURLs()` 得到的 URL 传入 `CreateAsyncImageTaskParams.ReferenceImageURLs`；Service 统一 trim/过滤并保留顺序与重复项，Repository 插入和扫描该字段。不要从终态会清除的 `request_payload` 临时解析。
- `asyncImageTaskCenterView.reference_image_urls` 只在 `detailView(..., admin=true)` 填充。管理员/用户列表使用 summary 查询且不加载 URL，普通用户详情也不输出；不要把字段移入共享 `newAsyncImageTaskCenterView` 初始化逻辑。
- 管理员页面在提示词下显示完整 URL，经过 `sanitizeUrl` 后才生成 HTTP(S) 外链，使用 `target=_blank` 与 `noopener noreferrer`。签名 URL 可能自然过期且可能含敏感参数，必须保持管理员专用并随任务保留期清理。
- 已通过后端 Service/Repository/Handler 三包完整测试、Repository URL 读回定向测试、前端 API/页面 Vitest 13/13、typecheck、目标 ESLint 和生产构建。未执行真实 PostgreSQL 迁移、登录后浏览器、真实 Redis/OSS/上游、生产部署/重启或 Fork CI。
- 当前实际基线：`main`；完整 HEAD `d30dd111f2209df9c6e1fe681d86fdfe807cfd0e`；`git describe=v0.1.173.48-1-gd30dd11-dirty`；VERSION=`0.1.173.48`；继续保留同一工作树中的错误码 611–613 改动。

## 2026-09-15 异步生图独立错误码 611–613 交接

- 失败查询分类器新增 `611`（`image: at most 8 images are allowed`）、`612`（缺少、未检测到或无法使用所需参考图）和 `613`（`prompt or input images could not be processed`）。内容政策 601、参考图网络拉取 602、通用输入 604 和未知兜底 610 的原有优先级保持不变。
- BB `/v1/images/tasks_async/{task_id}` 与 SC 查询共用 `writeBBQuery`，因此三个新码同时生效；没有新增数据库字段或响应字段。`fail_reason` 必须继续返回脱敏后的上游原文，不要替换成固定中文文案。
- 中文含义和简短建议已同步两份接口文档及站内中英文指南。后续若扩充关键词，应使用真实脱敏样本并防止“请上传”等短语抢占内容政策或网络拉取分类。
- 已通过分类/BB 响应定向测试、Handler 全包测试和前端 typecheck；未执行真实上游、Redis/PostgreSQL/OSS、生产部署/重启或 Fork CI。
- 当前实际基线：`main`；完整 HEAD `d30dd111f2209df9c6e1fe681d86fdfe807cfd0e`；`git describe=v0.1.173.48-1-gd30dd11-dirty`；VERSION=`0.1.173.48`。

## 2026-09-13 生图账号池多维调度冒烟复核交接

- 本轮在原多维账号池实现上补了六类边界：Gemini 非方图的池档位改用与计费相同的短边规则；独立绑定在保存和运行时都不能绕过 `require_oauth_only`；强制 Antigravity 原生入口会建立图片意图/池/计费上下文；单账号 503 判断读取当前图片池；渠道别名映射到 Gemini 生图模型时仍以请求侧 ID 查池；Antigravity 分组不再接受运行时永远不可选的 Gemini 账号。
- 严格池边界不变：缺维度或没有精确绑定才回退 `account_groups`；精确键有绑定但被 OAuth、平台、状态、过期/限流、模型、熔断或 failover 排除清空时返回 `ErrNoAvailableAccounts`。不要在错误处理里增加默认池二次查询。
- 计费链未重写。池绑定只覆盖调度 `Priority`，Repository 转换继续保留账号 `RateMultiplier`；最终仍由既有分组图片价格、图片独立倍率、用户/高峰/全局倍率和同步/异步账单流程结算。回归已锁定 Gemini `1792x1024` 的路由/计费同档，以及 OAuth 过滤后倍率字段不丢失。
- 迁移 225 的三个新命名约束在 ADD 前均 DROP IF EXISTS；模型候选不会再返回无法保存的 `*` 通配项。旧清晰度接口仍只改 `model=''` 行，模式与模型行不动。
- 验证已通过：六个核心后端包完整测试、相关定向测试、unit-tag Gemini/Chat/Image/Billing、全仓 Go 编译、Ent 全包编译、前端 5 项 Vitest、typecheck、目标 ESLint 和生产构建。工作树直接 Ent 生成两次被 Windows 文件映射锁中断，已恢复全部中间产物；临时干净克隆生成成功并只同步 13 个预期文件，临时目录已删除。
- 仍未验证真实 PostgreSQL 222→225、integration build-tag、登录后浏览器、真实 Redis/上游/OSS、生产部署/重启或 Fork CI。本机没有 `psql`/Docker，且配置指向非隔离外部服务；后续必须准备隔离环境，不能直接用当前配置补数据库或页面证据。
- 当前实际基线：`main`；完整 HEAD `38372af3ebf76fea96493f60cb49c35cbf5edd0a`；`git describe=v0.1.173.47-1-g38372af-dirty`；VERSION=`0.1.173.47`。继续保留本任务范围外的 `发行版发布与安装操作手册.md` 改动。

## 2026-09-13 生图账号池多维调度交接

- 数据真值在 `groups.image_account_pool_mode` 与沿用旧表名的 `group_image_size_accounts`：`model='' + tier` 是清晰度，`model + tier=''` 是模型，`model + tier` 是组合。迁移 225 默认 `resolution` 并保留旧行；不能把空模型改成 NULL，也不能删除旧 `/image-size-accounts` 适配接口。
- 管理入口是 `GET/PUT /api/v1/admin/groups/:id/image-account-pools`。PUT 必须继续通过仓储事务同时改模式、删除旧三维行并重建；旧 PUT 只能删除 `model=''` 行。账号池允许独立绑定未在 `account_groups` 中的账号，但账号必须存在、未删除且平台兼容。
- `WithImageAccountPoolRoute` 保存客户端请求侧精确模型和清晰度，账号/渠道映射不改池键。当前模式缺必要维度或没有精确绑定时回退默认分组池；有绑定但调度过滤后为空时必须保持 `ErrNoAvailableAccounts`，不得再查询默认池。
- 接入范围只包括 OpenAI 专用图片生成/编辑、Gemini 原生与兼容生图、持久异步 OpenAI/Gemini Worker 及这些路径中的 Composite。共享 Gateway 只有在上下文存在图片路由时才读池模式，所以 Responses/WS、普通 Chat Completions、批量生图和非图片请求不改变选号。
- 前端 `groupsImageAccountPools.ts` 管理三套独立草稿，`GroupsView.vue` 即使关闭生图也保留并提交已加载配置；加载新接口失败时 `editImageAccountPoolsLoaded=false`，本次保存跳过账号池 PUT，防止空覆盖。模型区分大小写、拒绝 `*` 和控制字符，服务端另按 UTF-8 255 字节兜底。
- 已验证 Ent 生成、后端定向测试、全仓 Go 编译、前端 5 项定向 Vitest、typecheck、目标 ESLint、生产构建。未执行 PostgreSQL integration build-tag、真实外部服务、登录后视觉、生产部署/重启或 Fork CI；本机配置不是隔离本地数据库/Redis，不要为补视觉证据直接启动并连接未知环境。
- 当前实际基线：`main`；完整 HEAD `38372af3ebf76fea96493f60cb49c35cbf5edd0a`；`git describe=v0.1.173.47-1-g38372af-dirty`；VERSION=`0.1.173.47`。工作树另有本任务范围外的 `发行版发布与安装操作手册.md` 改动，后续不得回滚覆盖。
- 继续验收时先阅读 [wiki-new/生图账号池多维调度.md](wiki-new/生图账号池多维调度.md)，在隔离 PostgreSQL 验证 222→225 和 `-tags=integration` 用例，再启动本地服务做 `/admin/groups` 的亮/暗色、键盘和窄屏检查。

## 2026-09-09 OpenAI GPT Image 2.5 与图片工作台模型目录交接

- OpenAI 图片接口的 `IsGPTImageGenerationModel`/校验按 `gpt-image-*` 前缀工作，未做固定枚举限制；默认目录现在包含 `gpt-image-2.5-flare` 和 `gpt-image-2.5-sunburst`。官方 2.5 模型支持 `auto/low/medium/high/xhigh/max`，请求参数会继续透传给上游。
- `backend/internal/service/image_workbench.go` 的 `ImageWorkbenchModel` 新增可选 `qualities`。只有 `gpt-image-2.5-*` 返回扩展质量；前端按当前选择的模型覆盖平台级质量选项，避免旧模型误发 `xhigh/max`。
- `frontend/src/views/admin/GroupsView.vue` 的创建/编辑分组模型目录均可手动输入 ID，`groupsModelsList.ts` 的 `addModelsListItem` 负责去重、自动选中和持久化顺序。保存后新打开/刷新图片工作台会读取新目录；若账号存在模型映射白名单，要同时在账号配置中添加精确 ID 或 `gpt-image-*` 通配规则。
- 已验证：相关 Go 定向测试、前端 Vitest 27/27、`npm run typecheck`、`npm run build`、`git diff --check`。未做真实账号/上游、数据库/Redis/OSS、浏览器登录视觉、生产部署或 Fork CI 验证。
- 当前实际基线：`main`，HEAD `abb8a33d71aa5d05a5fc9437fee4d365582d43c5`，`git describe=v0.1.173.46-dirty`，VERSION=`0.1.173.44`；保留发布手册原有未提交改动。

## 2026-09-09 管理员批量结束当前页异步任务交接

- 管理端 `/admin/async-image-tasks` 顶部新增“结束当前页（N）”。`N` 只统计当前 API 页中可结束的任务；点击时把这些任务 ID 冻结为快照，经一次确认后调用 `POST /api/v1/admin/async-image-tasks/batch-terminate`。没有跨页选择状态，普通用户页不显示按钮。
- 批量接口最多接收 100 个 ID，并按输入顺序去重、逐条处理。每项复用单任务 `terminateAsFailed` 的版本/状态 CAS，成功仍写 `failed`、`admin_terminated`、`admin_task_terminated` 并清除加密请求载荷；已进入不可结束终态或 CAS 冲突计为 `skipped`，缺失等其他错误计为 `failed`，不会覆盖并发 Worker 的成功结果。
- 前端按 `terminated/skipped/failed` 汇总提示，随后重新加载当前页与全局统计；如整个请求失败，目标快照和确认框保留，管理员可重试或取消。
- 已验证：`go test ./internal/service ./internal/handler ./internal/server/routes`；异步任务 API/页面组件 Vitest 2 文件 11 项；目标 ESLint；`pnpm typecheck`；`pnpm build`；`git diff --check`。构建只有既有 pnpm overrides、Browserslist、动态导入和大 chunk 警告。
- 下一步边界：需要运行时验收时，先在隔离数据库制造排队、已成功和 CAS 竞态任务，核对逐项结果、任务事件与请求载荷清除；本轮未连接真实 PostgreSQL/Redis/上游/OSS，未做登录后浏览器视觉，未部署、未重启、未运行 Fork CI。
- 当前实际基线：`main`，HEAD `b373fa5906abee97b90d0d4890264d1917c2371c`，`git describe=v0.1.173.44-1-gb373fa5-dirty`，VERSION=`0.1.173.44`；工作树还包含此前尚未提交的生图并发系统设置与 Worker 上限改动，后续不得回滚覆盖。

## 2026-09-09 生图并发设置与 Worker 并发交接

- 管理端网关页现有独立 `ImageConcurrencySettingsCard.vue`，API 为 `GET/PUT/DELETE /api/v1/admin/settings/image-concurrency`。数据库键 `gateway_image_concurrency_settings` 存完整 JSON；有键时系统设置优先，无键或 DELETE 后读取启动时 `config.yaml` 的 `gateway.image_concurrency`。
- `config.Config.ImageConcurrencySettings()` 是 OpenAI/Gemini 生图入口的运行时读取点；`SetImageConcurrencySettings()` 更新原子快照。当前实例写后立即生效，其他实例由 Redis `settings:image_concurrency` 通知；只影响新进入请求，不终止活动请求，已经进入旧等待循环的请求仍保留原等待参数。
- 异步 `worker_concurrency` 已移除 UI `max=64` 与服务端 64 截断，可保存任意正整数；`<=0` 仍归一化为默认 4。Worker 池大小只在服务启动时读取，修改后必须重启，生产不能忽略数据库连接、Redis 轮询、内存、OSS 与上游容量。
- Wire 已重新生成。为防止生成时删除既有图片账号熔断注入，`NewImageAccountCircuitBreaker` 已进入 Repository ProviderSet，`provideImageAccountCircuitBreakerRuntime` 在启动构图时正式完成 breaker 配置和三个 Gateway Service 注入。
- 已验证：后端 handler/service/config/repository/admin handler/routes/cmd server 全包测试（含上限从 1 热改为 2 后 OpenAI/Gemini 新请求共享槽位）、Wire 生成、前端 typecheck/build、3 文件 46 项 Vitest、目标 ESLint、Vite 根页面 HTTP 200 和 `git diff --check`。未做登录后的浏览器视觉、真实数据库/Redis/上游/OSS、多实例或生产验收；未部署、未重启、未运行 Fork CI。
- 当前实际基线：`main`，HEAD `b373fa5906abee97b90d0d4890264d1917c2371c`，`git describe=v0.1.173.44-1-gb373fa5-dirty`，VERSION=`0.1.173.44`。

## 2026-09-08 异步生图冒烟复核与重试状态修复

- 本轮确认的逻辑漏洞：首次 `invoking` 无账号已显示为 `queued`，但账号 7 失败后回到 `queued` 未清除旧 `account_id`，下一次重新抢占且尚未选号时会误显示 `processing`；任务中心状态筛选也曾按原始状态过滤，和展示状态不一致。
- 已修复：`AsyncImageTaskTransition` 增加 `ClearAccountID`；参考图、容量、上游临时错误、账号尝试超时重试回队列，以及 `queued -> invoking` 重新抢占均清除当前账号；`enrichAsyncImageAttemptTransition` 不再用最近尝试账号覆盖清除意图。账号历史仍保留在 `account_attempts`/`attempted_account_ids`。
- Repository 的 `queued` 筛选匹配原始 `queued` 或未分配账号的 `invoking`；`invoking` 筛选仅匹配 `account_id > 0`。公共查询和任务中心展示规则保持不变。
- 已验证：Handler/Repository/Service 异步定向 Go 测试、前端异步 API Vitest `8/8`、`pnpm typecheck`、`git diff --check` 均通过。未做真实 PostgreSQL/Redis/上游/OSS 端到端验证，未部署或重启生产。
- 当前实际基线：`main`，HEAD `eecbd846cf2d0c5b0a1a25e07a226994e3711cab`，`git describe=v0.1.173.43-1-geecbd84-dirty`，VERSION=`0.1.173.43`。

## 2026-09-08 异步生图未分配账号时状态展示修复

- 根因：Worker 为防止重复执行，会在账号路由前先将任务从 `queued` CAS 到 `invoking`；共享图片并发门或账号容量不足时尚未选中账号，短窗口仍被页面显示为“调用上游”。
- 修复：`backend/internal/handler/durable_async_image_handler.go` 的公共 BB/SC 查询，以及 `backend/internal/handler/async_image_task_center_handler.go` 的用户/管理员列表和详情，在 `status=invoking` 且 `account_id` 为空或非正数时返回展示状态 `queued`；账号已落库则保持 `invoking`。内部持久化状态和 CAS/恢复逻辑未改变。
- 文档同步：`wiki-new/异步生图架构.md`、`docs/DURABLE_ASYNC_IMAGE_API.md`、`异步生图接口文档new.md` 已说明 `invoking` 可能仍处于账号路由/容量准入窗口，未分配账号时 BB/SC 对外显示 `queued`。
- 回归：`go test ./internal/handler -run 'TestAsyncImage|TestDurableAsyncImage' -count=1`、`go test ./internal/service -run 'AsyncImage' -count=1`、前端异步任务 API 8/8、`pnpm typecheck` 均通过；`git diff --check` 通过。另补充任务详情时间线在未分配账号窗口显示 `queued` 的回归测试。
- 当前实际基线：分支 `main`；HEAD `eecbd846cf2d0c5b0a1a25e07a226994e3711cab`；`git describe=v0.1.173.43-1-geecbd84-dirty`；VERSION=`0.1.173.43`。未部署或重启生产，未做真实数据库/上游端到端验证。

## 2026-09-07 异步生图结果文件布局调整

- Worker 结果上传已取消“一个任务一个文件夹”：`backend/internal/handler/durable_async_image_worker.go` 使用 `service.AsyncImageResultObjectKey`，对象直接落在 `results/YYYY/MM/DD/` 日目录。
- 文件名格式为 `yyyyMMddHHmmss+GUID.ext`。GUID 使用任务 ID + 图片序号的稳定 UUID；同一任务的图片序号不同则文件名不同，重试/恢复仍复用原 key，符合 upload intent 的幂等要求。
- 定向验证已通过：`go test ./internal/service -run TestAsyncImageResultObjectKeyUsesDayDirectoryAndStableDistinctNames -count=1`、`go test ./internal/handler -run 'AsyncImage|Upload' -count=1`。尚未验证真实 OSS/数据库端到端，未部署或重启生产。
- 冒烟复核追加通过：`go test ./internal/service -run 'ImageStorage|ImageObject|ImageResultUploader' -count=1`、`go test ./internal/handler -run 'AsyncImage|Upload' -count=1`、`go test ./internal/service ./internal/handler -run '^$' -count=1`。
- 本轮实际基线：分支 `main`；HEAD `cc55ab9a181490ada4124d0eb84688a425d1ce43`；`git describe=v0.1.173.41-1-gcc55ab9-dirty`；VERSION=`0.1.173.41`；工作树另有用户既有 `sub2所需.md` 改动。`git diff --check` 已执行，唯一提示为该既有文件第 12 行尾随空格。

## 2026-09-05 个人图库同步广场上传修复

- `frontend/src/api/imageLibrary.ts` 的同步广场上传和普通图库文件导入不再手动设置 `Content-Type: multipart/form-data`；浏览器/Axios 会生成带 boundary 的请求头，避免 Go `ParseMultipartForm` 解析失败并被前端归一化为 Network error。
- 回归测试位于 `frontend/src/api/__tests__/imageLibrary.spec.ts`，已验证 `file` 字段、同步 URL、未传手动 headers 以及导入幂等键。
- 当前基线以命令为准：`git rev-parse HEAD=0ea8f5195ff64e6acecb3d131d68eac8553f203b`，`git describe=v0.1.173.40-dirty`，`VERSION=0.1.173.38`；保留工作树既有未提交改动。
- 已通过前端定向 Vitest `5/5`、`pnpm typecheck`、目标 ESLint、`pnpm build`、后端 Handler 全量测试（37.030s）及 Service 定向测试，并通过 `git diff --check`。未做真实浏览器或外部存储端到端验证，未部署/重启生产。

## 2026-09-04 管理员异步任务搜索与列布局

- 当前工作树在 `main`，HEAD `0ea8f5195ff64e6acecb3d131d68eac8553f203b`，`git describe`=`v0.1.173.40-dirty`，`backend/cmd/server/VERSION`=`0.1.173.38`；保留既有未提交改动。
- `/admin/async-image-tasks` 的 `q` 搜索后端现在匹配任务号、模型、提示词、`accounts.name` 及账号 ID，使用同一个 `%...%` 参数实现大小写不敏感模糊搜索；前端搜索提示已同步。
- 管理员列表 owner 列只渲染 `user_email`，列宽 `160px`；最终账号列宽 `200px`，保留名称与 `#ID`。
- 本轮验证已通过：前端全量 Vitest `239` 文件/`1623` 用例（异步 API `8/8`）、`pnpm typecheck`、目标 ESLint、`pnpm build`、Repository 全量测试、Handler 全包测试、`git diff --check`。构建警告为既有提示。
- 运行边界：未连接真实 PostgreSQL/上游/Redis/OSS，未部署或重启生产，Fork CI 未运行；后续如需验收应在隔离环境验证账号名称/ID 搜索和长邮箱截断。

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

- HEAD：`b373fa5906abee97b90d0d4890264d1917c2371c`
- `git describe --tags --always --dirty`：`v0.1.173.44-1-gb373fa5-dirty`
- `backend/cmd/server/VERSION`：`0.1.173.44`
- 最近功能：生图并发系统设置优先/YAML 回退和多实例热通知；异步 Worker 并发移除 64 上限（仍需重启生效）
- 本次验证结论：后端 handler/service/config/repository/admin handler/routes/cmd server 测试、Wire 生成、前端 46 项定向 Vitest、`pnpm typecheck`、`pnpm build`、目标 ESLint、Vite 根页面 HTTP 200 和 `git diff --check` 通过；未做登录后的浏览器视觉及外部服务验收
- 生产环境：未连接、未修改、未部署、未重启；未做真实 PostgreSQL/Redis/上游/OSS 端到端验证

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
- 真实人物露骨亲密、性化、情色/色情、裸露等 HTTP 400 内容安全拦截已补入 601 关键词，未知错误仍使用 610。
- “违反了关于裸露、色情或情色内容的防护限制”已加入精确回归测试，分类为 601。
- 安全结论：管理员接口由 AdminAuth/合规中间件保护，终止操作使用状态+版本 CAS；`execution_unknown` 禁止自动重放；`610` 未知错误仅用于保留原文和排查，不应盲目重试。
- 剩余边界：生产服务器仍运行旧二进制；真实上游、Redis/PostgreSQL/OSS 端到端、部署后告警和账单对账尚未验证。
- 生产只读复核 `2026-08-31 14:50 UTC`：`invoking=3`，超过 1 小时为 `0`；当前任务均为分钟级，未见新的长时间卡住任务。

## 2026-09-03 当前交接：管理员异步任务列表紧凑化

- 本轮仅修改 `frontend/src/features/async-image-tasks/AsyncImageTasksView.vue`：管理员 `/admin/async-image-tasks` 的任务号列约 220px、状态列约 112px、图片/存储列约 100px；状态进度条已移除；实际费用前新增最终账号列，显示 `account_name`，缺失时回退 `#account_id`。
- 用户列表不显示最终账号列；任务详情页和后端接口未改动。`account_name/account_id` 已由管理员列表响应提供，无需新增 API 字段。
- 实际基线：`main`，HEAD `314fcc3c0055a3be0c652782b646e71ad75df808`，`git describe`=`v0.1.173.38-1-g314fcc3-dirty`，VERSION=`0.1.173.38`。当前工作树还包含本轮三份文档同步改动。
- 已验证：`pnpm typecheck`、`pnpm test:run src/features/async-image-tasks/__tests__/api.spec.ts`（7/7）、目标文件 ESLint、`pnpm build`、`git diff --check`；构建仅有既有 Browserslist、动态导入和大 chunk 警告。未验证：浏览器截图/实机视觉、生产部署、重启和生产端到端链路。
- 后续如调整列宽，优先修改该组件 `columns` computed 与对应 cell 容器的 Tailwind `max-w/min-w`；不要把管理员专属账号字段暴露到用户列表。

## 2026-09-03 当前交接：异步任务中心平均耗时

- `/admin/async-image-tasks` 和 `/async-image-tasks` 顶部已在成功率旁显示平均耗时。后端 `service.AsyncImageTaskStats` 新增 `AverageDurationMS`/`average_duration_ms`，仓储按当前筛选条件查询 `status='succeeded' AND finished_at IS NOT NULL`，平均 `finished_at - submitted_at`；前端 `formatDuration` 统一展示，空样本为 `-`。
- 相关文件：`backend/internal/service/async_image_task.go`、`backend/internal/repository/async_image_task_repo.go`、`backend/internal/handler/async_image_task_center_handler.go`、`frontend/src/features/async-image-tasks/{AsyncImageTasksView.vue,api.ts,types.ts}`、中英文 `asyncImageTasks` locale；新增仓储/服务/API 断言。
- 实际基线：`main`，HEAD `314fcc3c0055a3be0c652782b646e71ad75df808`，`git describe`=`v0.1.173.38-1-g314fcc3-dirty`，VERSION=`0.1.173.38`。工作树仍包含前一轮列表紧凑化改动及本轮统计改动。
- 已验证：异步任务 Service/Repository/Handler 定向测试通过，前端 API 8/8、`pnpm typecheck`、目标文件 ESLint、`pnpm build`、`git diff --check` 通过。整包 `go test ./internal/service ./internal/repository ./internal/handler -count=1` 的 Service 仍被既有外部 OpenAI token 对比用例阻断，Repository/Handler 通过。
- 运行边界：未执行浏览器截图、真实 PostgreSQL/Redis/OSS、生产部署或重启；生产服务器仍运行旧二进制。后续修改统计时保持“已完成任务的显示花费时间平均”口径，不从当前分页数据计算。

## 2026-09-03 当前交接：生图账号调度去黏性

- 生图调度已完成：`backend/internal/handler/openai_images.go` 的专用图片端点不生成会话键；`openai_gateway_handler.go` 的 HTTP Responses、Responses WebSocket 在生图时清空 session hash，并不把 `previous_response_id` 传给账号选择器；`openai_chat_completions.go` 的显式生图意图同样清空会话键。
- Gemini 原生入口 `gemini_v1beta_handler.go` 与通用 Gemini Chat Completions 入口仅对无 `thoughtSignature` 的生图关闭黏性；普通文本和带签名生图仍保持 session/digest sticky。无签名生图不查找、创建或保存摘要会话。
- 策略函数位于 `backend/internal/service/image_generation_intent.go`：`GeminiImageStickySessionRequired` 和 `IsGeminiThoughtSignaturePresent`，回归测试在 `image_generation_intent_test.go`。清晰度账号池排序实现未改，仍是优先级、有效负载因子实时负载、LRU。
- 管理端账号池提示已更新：未填写优先级时按输入顺序生成 1/2/3；相同优先级按有效负载因子均衡；示例为 `101, 102:1, 103:1`。文件为 `frontend/src/i18n/locales/{zh,en}/admin/overview.ts`。
- 本轮实际基线：`main`；HEAD `314fcc3c0055a3be0c652782b646e71ad75df808`；`git describe`=`v0.1.173.38-1-g314fcc3-dirty`；VERSION=`0.1.173.38`。后端 Service/Handler/Repository 定向测试、前端 `pnpm typecheck`、locale ESLint、`pnpm build` 均通过；构建保留既有警告。
- 未执行完整 Service 包、真实上游/Redis/PostgreSQL/OSS、浏览器实机验收、生产部署或重启；不要把本地通过写成生产已生效。文档写入后的 `git diff --check` 已通过，工作树已复核。

## 2026-09-03 当前交接：生图调度冒烟验证

- 冒烟已通过：Service 定向调度/图片意图测试、Handler 定向测试、`internal/server/routes` 路由测试、前端异步任务 API `8/8`、`pnpm typecheck` 均通过。
- 建议下一步在隔离环境准备两个同优先级账号和一个更高优先级账号，验证优先级严格回退、同优先级按有效负载因子与当前负载均衡；用带/不带 `thoughtSignature` 的 Gemini 请求验证分别保持/解除黏性，并观察 failover。
- 上线前建议增加账号选择层指标（优先级、有效负载率、选中账号、切换原因）、上游 request ID 与本地账单对账，再经授权执行构建、灰度部署、重启和生产观察。当前未做真实上游、Redis/PostgreSQL/OSS、浏览器实机或生产验证。

## 2026-09-03 当前交接：参考图拉取失败换号重试

- 相关代码：`backend/internal/handler/durable_async_image_worker.go`、`backend/internal/service/async_image_account_attempt.go`、`backend/internal/handler/openai_images.go`、`backend/internal/handler/gateway_handler_chat_completions.go`。
- 行为：异步 image-to-image 的 `image_url fetch failed` 会持久化失败账号；任务级 sticky 让同账号完成两次重试，第三次同账号失败后切换一个账号重试一次；第二账号失败不再继续。`ReferenceFetchMaxRetries` 默认仍为 2，OpenAI 混合传输的 local fallback 仍按原策略保留。
- 运行边界：本地 Handler 全量测试和相关 Service 定向测试通过；完整 Service 包受既有外部 OpenAI token 对比用例影响未全通过。未做真实上游、Redis/PostgreSQL/OSS、生产部署、重启或灰度验收。
- 下一步：隔离环境准备两个可用账号，观察 `account_attempts`、`attempted_account_ids` 和任务 `reference_retry_count`，确认 A/A/A/B 的实际顺序及 B 失败后的终止。

## 2026-09-03 当前交接：参考图重试 invocation 状态合并修复

- Worker 在收到上游 `image_url fetch failed` HTTP 400 后，先记录当前 invocation 的失败账号，再把 capture 与任务持久化历史合并判定；这修复了第三次同账号请求和第二账号请求看不到当前失败的问题。
- 默认 `ReferenceFetchMaxRetries=2` 的顺序固定为 A 初始请求、A 重试 1、A 重试 2、B 重试 1；B 失败直接进入 `failed`，不会继续选择 C。OpenAI 图片调度在 Worker 传入预取 sticky 账号时优先使用该账号，缓存冷启动也不改变顺序。
- 已通过：`go test ./internal/handler -count=1`；Service 账号尝试/图片意图定向测试；Repository 异步迁移定向测试；Service/Repository `go test -run '^$'` 编译检查；`git diff --check`。
- 完整 Service 测试未通过的唯一已确认阻断为既有 `TestEstimateOpenAIInputTokens_CompareWithOpenAIAPI` 外部 OpenAI 网络连接超时；未执行真实上游、Redis/PostgreSQL/OSS、生产部署或重启。

## 2026-09-04 当前交接：参考图重试无缓存 sticky

- `defaultOpenAIAccountScheduler.selectBySessionHash` 现允许 Worker 明确传入的 `StickyAccountID` 在 Redis sticky 缓存关闭或不可用时继续生效；缺少预取账号的普通会话请求仍要求缓存，未改变常规调度。
- 这补齐异步 `image_url fetch failed` 重试的 A/A/A/B 约束：首次 A、同账号两次重试、切换 B 一次，B 失败终止，且同账号阶段不依赖 Redis。
- 新增 `TestOpenAIGatewayService_SelectAccountWithScheduler_PrefetchedStickyWithoutCache`；该测试、参考图 Handler 定向测试均通过。完整 `go test ./internal/service -count=1` 于 2026-09-04 仍只在 `TestEstimateOpenAIInputTokens_CompareWithOpenAIAPI` 访问 `https://api.openai.com/v1/responses/input_tokens` 时连接超时失败。
- 实际基线：分支 `main`；HEAD `314fcc3c0055a3be0c652782b646e71ad75df808`；`git status --short --branch` 为 `## main...origin/main` 加本地既有未提交改动；`git describe --tags --always --dirty`=`v0.1.173.38-1-g314fcc3-dirty`；VERSION=`0.1.173.38`。未连接、部署或重启生产，真实上游、Redis/PostgreSQL/OSS 端到端仍待隔离环境验证。

## 2026-09-04 当前交接：清晰度账号池 priority 回显

- 根因：`frontend/src/views/admin/groupsImageAccountPools.ts` 的 `formatImageSizePoolInput` 只回显 `account_id`，保存后重新加载时丢掉 `priority` 文本；后端数据本身未丢失。
- 修复：回显格式改为 `account_id:priority`，因此 `1:1,32:1` 保存后仍显示为 `1:1, 32:1`。后端 `group_image_size_accounts` 允许多个账号共享同一个 priority；同优先级由调度器继续做负载均衡。
- 新增前端回显/解析 round-trip 和后端同优先级账号池调度测试。前端账号池 3/3、异步任务 API 8/8，后端图片账号池/管理员接口/迁移定向测试通过。
- 实际基线：分支 `main`；HEAD `314fcc3c0055a3be0c652782b646e71ad75df808`；`git status --short --branch` 为 `## main...origin/main` 加本地既有未提交改动；`git describe --tags --always --dirty`=`v0.1.173.38-1-g314fcc3-dirty`；VERSION=`0.1.173.38`。未部署或重启生产。

## 2026-09-04 深度冒烟复验

- 已确认账号池配置 `1:1,32:1` 保存后回显为 `1:1, 32:1`；多个账号使用相同优先级合法，后端唯一约束不限制 priority，调度器在同优先级内继续按负载均衡。
- 已通过 `go test ./internal/handler -count=1`、图片账号池/管理员接口/Repository 迁移定向测试、路由/中间件测试、`go test ./... -run '^$' -count=1`；前端账号池与异步任务 API `11/11`、`pnpm typecheck`、目标 ESLint、`pnpm build`、`git diff --check`。
- 完整 `go test ./internal/service -count=1` 的唯一确认阻断为既有 `TestEstimateOpenAIInputTokens_CompareWithOpenAIAPI` 三个子用例访问 `https://api.openai.com/v1/responses/input_tokens` 连接超时；账号池定向测试通过。
- 本轮没有浏览器实机、真实上游/Redis/PostgreSQL/OSS、生产部署、重启或灰度验收；生产仍不会因本地未提交改动自动生效。
# 2026-09-04 图片账号连续失败熔断交接

- 当前工作树在 `main`，HEAD `314fcc3c0055a3be0c652782b646e71ad75df808`，`git describe --tags --always --dirty` 为 `v0.1.173.38-1-g314fcc3-dirty`，VERSION `0.1.173.38`；保留此前未提交改动。
- Redis key 按 `image:circuit:{scope}:{account}:failures/open`；默认关闭、连续失败阈值 5、冷却 300 秒，成功清零，sync/async 隔离。
- 管理端字段位于 `frontend/src/views/admin/BackupView.vue` 图片存储运行参数区域；已完成 Go 全仓编译检查、前端类型检查和构建。
- 未保存管理端配置时，`async_image.image_circuit_breaker_enabled`、`async_image.image_circuit_breaker_failure_threshold`、`async_image.image_circuit_breaker_cooldown_seconds` 可作为 `config.yaml`/环境变量回退；已在 `asyncRuntimeFromConfig` 增加映射并补同步/异步调度过滤测试。
- Gateway 旧式 sticky lookup 也会检查 `ImageAccountCircuitBreaker`，因此图片请求不会因已有会话绑定而绕过冷却；相关回归测试位于 `backend/internal/service/image_account_circuit_breaker_scheduling_test.go`。
- 上线前在隔离环境验证两账号切换、冷却恢复、成功清零、Redis TTL 和无可用账号响应；未执行生产部署或真实外部链路。

## 2026-09-04 当前交接：生产卡住任务只读诊断

- 已按用户授权只读检查 `108.186.246.14`：`sub2api` active，生产版本 `0.1.173.38`，commit `276f2240b80796a58cfb969a4c8b7f420a1310b9`。实时 `invoking` 约 2-6，`account_id IS NULL=0`、租约超时=0、超过 10 分钟=0，更新时间持续刷新；当前没有证据表明 Worker 卡死或没有选定账号。
- 日志中的 `reference_fetch_retry` 原因是上游抓取参考图 URL 的 `curl (28)` 约 60 秒连接超时。生产版本未包含本地 `v0.1.173.40` 的 A/A/A/B 策略，因此同账号重试两次后换号一次尚未在生产生效。
- 生产版本也早于本地管理员账号列和顶部平均耗时前端改动，页面缺少字段不能据此判断任务没有账号。
- 本地验证已通过：Handler/Repository 定向测试，Service 重试相关测试，前端账号池与异步任务 API `11/11`。完整 Service 包仍被既有外部 OpenAI token 对比用例网络超时阻断。
- 账号池输入 `1:1,32:1` 在本地保存后回显为 `1:1, 32:1`；相同 priority 合法并按同优先级负载均衡。生产未修改配置，需另行授权构建/发布/重启后观察。
- 当前基线：HEAD `0ea8f5195ff64e6acecb3d131d68eac8553f203b`，`git describe`=`v0.1.173.40-dirty`，VERSION=`0.1.173.38`；工作树含本轮三份文档修改，分支落后 `origin/main` 1 个提交。

## 2026-09-04 当前交接：异步生图任务列表密度调整

- 管理端 `/admin/async-image-tasks` 通过 `DataTable` 的页面级 `compact` 属性使用 `px-1` 横向单元格内边距；该属性默认关闭，不影响其他表格。
- 最终账号列宽统一为 `w-[160px] max-w-[160px]`，列表容器同步为 `max-w-[160px]`；“手动结束为失败”列表操作仅渲染 ban 图标，`title`/`aria-label` 仍使用 `asyncImageTasks.terminate.action`，详情弹窗确认按钮保持文字。
- 新增 `frontend/src/components/common/__tests__/DataTable.spec.ts` 紧凑内边距回归用例。前端全量 `pnpm test:run` 为 239 个文件、1623 个用例全通过；`pnpm typecheck`、目标 ESLint、`pnpm build`、`git diff --check` 通过。
- 构建警告仍为既有 Browserslist、动态导入和大 chunk；未执行浏览器实机或真实 Redis/PostgreSQL/OSS/上游链路，未部署或重启生产。
- 当前基线：HEAD `0ea8f5195ff64e6acecb3d131d68eac8553f203b`，`git describe --tags --always --dirty`=`v0.1.173.40-dirty`，VERSION=`0.1.173.38`；分支 `main`，相对 `origin/main` 落后 1 个提交，工作树保留既有未提交改动。

## 2026-09-04 当前交接：分组清晰度账号池纵向布局

- `frontend/src/views/admin/GroupsView.vue` 中“按清晰度账号池”编辑区域已移除 `md:grid-cols-3`，固定为 `grid grid-cols-1 gap-3`；1K、2K、4K 现在各占一行。
- 本次只改布局，不改 `editImageSizePoolDraft`、`parseImageSizePoolInput`、`toImageSizePoolPayload` 或后端账号池接口；保存后的账号 ID/priority 行为保持不变。
- 验证已通过：分组图片账号池/图片定价/异步生图测试 `14/14`、`pnpm typecheck`、`pnpm eslint src/views/admin/GroupsView.vue`、`git diff --check`。
- 当前基线：HEAD `0ea8f5195ff64e6acecb3d131d68eac8553f203b`，`git describe --tags --always --dirty`=`v0.1.173.40-dirty`，VERSION=`0.1.173.38`；分支 `main`，相对 `origin/main` 落后 1 个提交；未部署或重启生产。

## 2026-09-16 当前交接：OVH TCP 代理兜底

- 生产主机 `40.160.139.185` 已新增 `sing-box-reality.service`：sing-box `1.14.1`、VLESS + REALITY、TCP `8443`；配置 `/etc/sing-box/reality.json`。不要覆盖既有未启用的 `/etc/sing-box/config.json`，它是另一份 Shadowsocks `8080` 配置。
- 原 Hysteria2 UDP `36712` 与 Salamander UDP `8443` 服务仍为 active；Nginx TCP/UDP `443` 未改。订阅 `http://40.160.139.185:9900/hy2.yaml` 把 `美西-TCP-Reality` 放在 `PROXY` 第一位。
- 生产备份和部署前快照在 `/root/hysteria-backups/20260916-ovh-tcp-reality/`。如回退，只处理新增的 `sing-box-reality.service`、`/etc/sing-box/reality.json`、UFW `8443/tcp`，并恢复该目录内的 `hy2.yaml.before-tcp`；不要停原 Hysteria2 或改 Nginx。
- BBR 已启用；本轮把运行中的 `ens3` 根队列从 `pfifo_fast` 切到 `fq`，系统默认值原本已是 `fq`，没有重启。服务器已有待重启内核提示，本轮没有为此重启生产。
- 实际链路验证：当前国内网络经本机 Mihomo 成功连接 TCP Reality，出口为 `40.160.139.185`；连续 5 次 HTTPS 成功，4 路并发约 `0.70 MB/s`。TCP 解决的是 UDP/QUIC 易被 OVH 边缘策略或跨境链路干扰的问题，不会把普通 OVH 直连线路变成机场的优化回国线路。
- 下一步先让用户刷新订阅并明确选择 `美西-TCP-Reality`，再用手机 5G 测稳定性；若仍要求多 MB/s，优先评估带 CN2/GIA/CMIN2/9929 等优化路由的落地或中转，而不是继续提高 Hysteria 带宽参数。OVH 控制台需人工确认 Network Security Dashboard 的 `Mitigation: Automatic/Forced`。
- 仓库基线：`git status --short --branch` 为 `## main...origin/main` 加三份交接文档修改；HEAD `52b0cf537f2e091506d52d72692195f6720088d6`，`git describe --tags --always --dirty`=`v0.1.173.49-dirty`，VERSION `0.1.173.48`；未改业务代码、未运行项目测试、未发布 sub2api。

### 2026-09-16 TCP 小幅调优后续交接

- 新增生产 `/etc/sysctl.d/99-zz-proxy-tcp-tuning.conf` 并已生效：core 收发上限 32 MiB、TCP 自动发送上限 32 MiB，MTU probing=1，slow_start_after_idle=0；BBR/fq 不变。仅提高缓冲上限，主机级生效；没有节点/订阅变更或服务重启。
- 调整前后一个 10 MB 样本从 60 秒超时约 156337 B/s 到完整 35.73 秒约 279873 B/s；5 次 HTTPS 成功，但随后下载出现一次 Reality 握手超时，重试下载仍偏慢。必须保留这条运行边界：配置生效不等于持续提速已验收；不要承诺用户手机 4 MB/s 会翻倍。
- 客户端接收窗口限制和跨境重传仍存在；不要盲目提高初始拥塞窗口、关闭安全重试或安装 TCP Brutal 内核模块。优先让用户断开重连 TCP 节点，在同一手机/下载源复测。
- 回退目录 `/root/hysteria-backups/20260916-tcp-buffer-tune/`。如手机表现变差，经用户指示将新增 sysctl 文件移至该备份目录禁用，再 `sysctl -p /root/hysteria-backups/20260916-tcp-buffer-tune/sysctl.before.conf`；不用重启代理。
- 仓库仍为上述完整 SHA `52b0cf537f2e091506d52d72692195f6720088d6`、`v0.1.173.49-dirty`、VERSION `0.1.173.48`，`main...origin/main` 加三份交接文档修改；未运行项目测试、未提交/发布业务代码。
