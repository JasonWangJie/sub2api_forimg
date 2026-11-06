# 仓库协作规则

本仓库是 `JasonWangJie/sub2api_forimg` Fork。开始任务前先检查真实工作树和当前提交，并阅读 `readmenew.md`、`开发台账.md`、`agent.md` 及相关 `wiki-new/` 文档；不得把旧交接摘要当成当前状态。

## 任务完成文档同步（强制）

每次代码、配置、测试或文档任务完成时，必须同步更新以下三份记录：

1. `readmenew.md`：当前版本快照、近期功能摘要和文档入口。
2. `开发台账.md`：日期、完整 Git SHA、变更范围、验证命令/结果、未完成项。
3. `agent.md`：下一位助手需要的上下文、运行时边界和下一步。

同步时必须以 `git rev-parse HEAD`、`git status --short --branch`、`git describe --tags --always --dirty` 和 `backend/cmd/server/VERSION` 的实际输出为准。没有执行过的测试、生产操作或 CI 不得写成已通过；生产服务器变更必须单独说明授权和范围。

完成前运行 `git diff --check`，并检查新增文档链接。`wiki-new/` 新文档使用简体中文语义文件名，重命名必须同步全仓引用。

## 对外异步生图接口文档同步（强制）

凡修改对外异步生图接口的路径、请求参数、默认值、类型与范围校验、模型适用范围、响应格式、错误码、结果格式或重试行为，必须在同一任务同步更新：

1. 根目录 `异步生图接口文档new.md`。
2. 页面 `/guides/async-image-api` 的中文和英文内容（`frontend/src/views/user/guideAsyncImageApiContent.ts`，必要时包括对应 Vue 页面与组件）。
3. 受影响的 `wiki-new/` 专题文档，以及上述三份任务记录。

文档与页面必须反映最终实现，包含适用入口、可选/必填、默认值、范围、兼容边界及可直接使用的请求示例；不能仅修改后端或仅更新一份文档。完成时核对双语参数表、JSON/multipart 示例和结果说明与代码一致。

## 修改边界

- 保留用户已有未提交改动，不使用破坏性回滚命令覆盖工作树。
- Fork 身份、发行脚本、安装路径和上游合并保护遵守 `.cursor/rules/fork-release-deploy.mdc`。
- 生产部署、远程服务器连接和重启不属于默认本地开发步骤，必须由用户明确要求后再执行。
