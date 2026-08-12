# 本地前后端运行操作手册

本文以当前 Fork 的 `main` 为准，以 Windows PowerShell 为主，并在关键位置给出 Linux/macOS 等价命令。目标是让开发者能够在本机运行 PostgreSQL、Redis、Go 后端和 Vue 前端，并验证图片工作台、持久异步任务、图库和审核页面。

## 1. 运行方式选择

| 方式 | 地址 | 适用场景 | 特点 |
|---|---|---|---|
| Docker 一体化 | `http://127.0.0.1:8080` | 快速验收当前完整代码 | 从当前源码构建，前端嵌入后端，最省事 |
| 前后端分开运行 | 前端 `http://127.0.0.1:3000`，后端 `http://127.0.0.1:8080` | 日常二次开发 | Vite 热更新，后端手动重启 |

不要用 `deploy/docker-compose.local.yml` 的默认镜像验证 Fork 功能。该文件默认指向 `weishaw/sub2api:latest`，可能不包含本 Fork 的改动。本地验证 Fork 应使用 `docker-compose.dev.yml`，它会从当前工作树构建镜像。

## 2. 环境要求

### 2.1 源码联调要求

- Git。
- Go `1.26.5`，必须与 `backend/go.mod` 一致。
- Node.js 22 或 24；Docker 构建使用 Node 24。
- pnpm 9 或 10；禁止改用 npm 安装依赖。Docker 构建固定使用 pnpm 9，当前代码也已在 pnpm 10.34.5 下验证。
- PostgreSQL 15+，推荐使用仓库 Compose 对齐的 PostgreSQL 18。
- Redis 7+，推荐使用仓库 Compose 对齐的 Redis 8。

### 2.2 Docker 一体化要求

- Docker Desktop 或 Docker Engine。
- Docker Compose v2，即 `docker compose` 命令。
- 首次构建需要访问 Node、Go、Alpine 和依赖镜像源。

检查环境：

```powershell
git --version
go version
node --version
pnpm --version
docker version
docker compose version
```

如果尚未启用 pnpm：

```powershell
corepack enable
corepack prepare pnpm@9 --activate
```

## 3. 获取并核对代码

```powershell
git clone https://github.com/JasonWangJie/sub2api.git
Set-Location sub2api
git switch main
git pull --ff-only origin main
git status --short --branch
git rev-parse HEAD
Get-Content backend\cmd\server\VERSION
```

预期当前分支是 `main`，工作树没有未说明的修改。文档中的 SHA 可能随着后续提交变化，以命令输出为准。

## 4. 方式 A：Docker 一体化运行

### 4.1 准备本地配置

```powershell
Set-Location deploy
Copy-Item .env.example .env
```

编辑 `deploy/.env`，本地至少修改：

```dotenv
BIND_HOST=127.0.0.1
SERVER_PORT=8080
SERVER_MODE=debug
POSTGRES_USER=sub2api
POSTGRES_PASSWORD=只用于本地且足够长的密码
POSTGRES_DB=sub2api
REDIS_PASSWORD=
ADMIN_EMAIL=admin@sub2api.local
ADMIN_PASSWORD=只用于本地的管理员密码
JWT_SECRET=至少64个十六进制字符
TOTP_ENCRYPTION_KEY=至少64个十六进制字符
TZ=Asia/Shanghai
```

PowerShell 生成 32 字节随机密钥：

```powershell
[Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)).ToLower()
```

`.env` 已被 Git 忽略，不能提交。即使是本地环境，也建议固定 `JWT_SECRET` 和 `TOTP_ENCRYPTION_KEY`，否则重启后登录会话或已保存的加密凭据可能失效。

### 4.2 处理开发代理

`docker-compose.dev.yml` 当前为大陆网络开发预置了宿主机 `7897` 代理。如果本机确实运行该代理，可直接启动。如果本机没有代理，新建已被 Git 忽略的 `deploy/docker-compose.override.yml`：

```yaml
services:
  sub2api:
    environment:
      HTTP_PROXY: ""
      HTTPS_PROXY: ""
      ALL_PROXY: ""
```

没有代理时，后续所有 Compose 命令同时传入两个文件：

```powershell
docker compose -f docker-compose.dev.yml -f docker-compose.override.yml config
docker compose -f docker-compose.dev.yml -f docker-compose.override.yml up -d --build
```

有代理时：

```powershell
docker compose -f docker-compose.dev.yml config
docker compose -f docker-compose.dev.yml up -d --build
```

### 4.3 查看启动状态

```powershell
docker compose -f docker-compose.dev.yml ps
docker compose -f docker-compose.dev.yml logs -f sub2api
```

如果使用了 override，查看日志时也建议带上同样的 `-f` 参数组合。第一次启动会：

1. 等待 PostgreSQL 和 Redis 健康。
2. 自动创建配置。
3. 按文件顺序执行数据库迁移，包括 `185`、`186`、`187`、`188`。
4. 创建首个管理员。
5. 启动嵌入前端的后端服务。

验证：

```powershell
curl.exe -fsS http://127.0.0.1:8080/health
```

预期返回：

```json
{"status":"ok"}
```

浏览器打开 `http://127.0.0.1:8080`，使用 `.env` 中的管理员账号登录。

### 4.4 停止与重新启动

```powershell
docker compose -f docker-compose.dev.yml stop
docker compose -f docker-compose.dev.yml start
docker compose -f docker-compose.dev.yml down
```

`down` 不会主动删除 `deploy/data`、`deploy/postgres_data` 和 `deploy/redis_data`。不要使用 `down -v` 作为普通停止命令。

彻底重置本地环境会删除所有本地用户、账号、任务和配置。确认不需要数据后，先执行 `down`，再手工删除上述三个目录，然后重新启动。

## 5. 方式 B：前后端分开运行

### 5.1 启动本地 PostgreSQL 和 Redis

已有本机 PostgreSQL/Redis 时可跳过本节。以下命令只用 Docker 提供依赖服务，Go 和 Vue 仍在宿主机运行。

```powershell
docker volume create sub2api-dev-postgres
docker volume create sub2api-dev-redis

docker run -d --name sub2api-dev-postgres `
  -e POSTGRES_USER=sub2api `
  -e POSTGRES_PASSWORD=sub2api_dev_password `
  -e POSTGRES_DB=sub2api `
  -p 127.0.0.1:5432:5432 `
  -v sub2api-dev-postgres:/var/lib/postgresql/data `
  postgres:18-alpine

docker run -d --name sub2api-dev-redis `
  -p 127.0.0.1:6379:6379 `
  -v sub2api-dev-redis:/data `
  redis:8-alpine redis-server --appendonly yes --appendfsync everysec
```

检查依赖：

```powershell
docker exec sub2api-dev-postgres pg_isready -U sub2api -d sub2api
docker exec sub2api-dev-redis redis-cli ping
```

后续开机只需：

```powershell
docker start sub2api-dev-postgres sub2api-dev-redis
```

### 5.2 首次启动 Go 后端

打开一个 PowerShell 窗口，先用资源管理器在仓库根目录打开终端，或自行 `Set-Location` 到仓库根目录，然后执行：

```powershell
Set-Location backend
New-Item -ItemType Directory -Force data | Out-Null

$env:DATA_DIR = (Resolve-Path .\data).Path
$env:AUTO_SETUP = "true"
$env:SERVER_HOST = "127.0.0.1"
$env:SERVER_PORT = "8080"
$env:SERVER_MODE = "debug"
$env:DATABASE_HOST = "127.0.0.1"
$env:DATABASE_PORT = "5432"
$env:DATABASE_USER = "sub2api"
$env:DATABASE_PASSWORD = "sub2api_dev_password"
$env:DATABASE_DBNAME = "sub2api"
$env:DATABASE_SSLMODE = "disable"
$env:REDIS_HOST = "127.0.0.1"
$env:REDIS_PORT = "6379"
$env:REDIS_DB = "0"
$env:ADMIN_EMAIL = "admin@sub2api.local"
$env:ADMIN_PASSWORD = "replace_with_local_admin_password"
$env:JWT_SECRET = "replace_with_64_hex_characters"
$env:TOTP_ENCRYPTION_KEY = "replace_with_another_64_hex_characters"
$env:TZ = "Asia/Shanghai"

go run ./cmd/server
```

第一次运行会在 `backend/data/` 写入 `config.yaml` 和 `.installed`，执行迁移并创建管理员。该目录已被 Git 忽略。

后续启动仍应保留固定 `DATA_DIR` 和 `TOTP_ENCRYPTION_KEY`：

```powershell
# 从仓库根目录执行
Set-Location backend
$env:DATA_DIR = (Resolve-Path .\data).Path
$env:TOTP_ENCRYPTION_KEY = "与首次启动相同的密钥"
go run ./cmd/server
```

配置优先级是 `config.yaml > 环境变量 > 默认值`。首次安装后，数据库地址等主要连接配置已经写入 `backend/data/config.yaml`；仅修改同名环境变量不一定覆盖现有文件。需要换数据库时，应先备份，再编辑本地配置或重建一次性开发环境。

验证后端：

```powershell
curl.exe -fsS http://127.0.0.1:8080/health
```

### 5.3 启动 Vue 前端

打开第二个 PowerShell 窗口，同样先进入仓库根目录：

```powershell
Set-Location frontend
pnpm install --frozen-lockfile
$env:VITE_DEV_PROXY_TARGET = "http://127.0.0.1:8080"
$env:VITE_DEV_PORT = "3000"
pnpm dev
```

浏览器打开 `http://127.0.0.1:3000`。Vite 会把 `/api`、`/v1` 和 `/setup` 代理到后端，因此一般不需要为本地同源联调额外配置 CORS。

Linux/macOS 等价启动：

```bash
cd frontend
pnpm install --frozen-lockfile
VITE_DEV_PROXY_TARGET=http://127.0.0.1:8080 VITE_DEV_PORT=3000 pnpm dev
```

### 5.4 构建嵌入前端的本地二进制

前端构建输出到 `backend/internal/web/dist`：

```powershell
Set-Location frontend
pnpm build
Set-Location ..\backend
go build -tags embed -trimpath -o bin\server.exe .\cmd\server
.\bin\server.exe -version
```

运行该二进制时，前端由后端直接提供；不再需要 Vite。

## 6. 图片功能的本地配置

基础登录、账号、分组和普通同步 API 不要求 OSS。以下能力需要对象存储配置完整：

- 持久异步生图结果上传。
- SC 参考图上传。
- 服务端图库归档和动态查看地址。
- 审核通过后需要同步到 OSS 的公开作品。

进入管理员后台的备份/图片存储设置：

1. 选择 `qiniu`、`aliyun`、`tencent` 或 `custom_s3`。
2. 填写 endpoint、region、bucket、access key、secret key 和前缀。
3. 本地 MinIO 等 S3 兼容服务通常选择 `custom_s3` 并开启 path-style。
4. 执行“测试连接”。测试必须完成上传、读取或 HEAD、删除，不能只验证凭据格式。
5. 配置异步生图 `public_base_url`。本地一般填 `http://127.0.0.1:8080`，生产必须填外部 HTTPS API 根地址。
6. 创建 Gemini/OpenAI 分组并启用普通生图；需要测试持久异步时，再开启分组的“异步生图”。

未配置 OSS 时不要把异步任务失败误判为上游或计费错误。先检查日志中是否出现 `IMAGE_STORAGE_DISABLED`。

## 7. 本地质量检查

后端：

```powershell
Set-Location backend
go generate ./cmd/server
go test -tags=unit ./... -count=1
go test ./... -count=1
go build -trimpath ./cmd/server
```

前端：

```powershell
Set-Location frontend
pnpm install --frozen-lockfile
pnpm lint:check
pnpm typecheck
pnpm test:run
pnpm build
```

涉及 Ent Schema 时运行 `go generate ./ent`；涉及 Wire 依赖注入时运行 `go generate ./cmd/server`，并提交生成文件。真实 integration/testcontainers 测试还要求 Docker 可用。

## 8. 常见问题

### 8.1 `go` 命令不存在

安装 Go 1.26.5，并把 Go 的 `bin` 加入 PATH。重新打开终端后执行 `go version`。

### 8.2 端口被占用

```powershell
Get-NetTCPConnection -LocalPort 3000,5432,6379,8080 -ErrorAction SilentlyContinue
```

修改后端端口时，同步修改 `VITE_DEV_PROXY_TARGET`。修改 Docker 暴露端口时只改 `.env` 的 `SERVER_PORT`，容器内部仍使用 8080。

### 8.3 后端仍连接旧数据库

检查 `backend/data/config.yaml`。安装完成后该文件优先于大部分环境变量。不要只修改环境变量后反复重启。

### 8.4 数据库迁移 checksum mismatch

不要修改已经执行过的迁移。恢复原迁移文件，并用新的迁移编号追加变更。迁移事实记录在 `schema_migrations`。

### 8.5 Docker 构建前端依赖失败

先在宿主机执行 `pnpm install --frozen-lockfile` 验证锁文件。国内网络可设置 `NPM_CONFIG_REGISTRY`；Go 构建默认使用 Dockerfile 中的 Go 代理配置。

### 8.6 上游 API 全部失败

Docker 开发 Compose 默认使用宿主机 7897 代理。确认代理存在，或按 4.2 节清空容器代理环境。再检查账号、模型映射、分组和上游凭据。

## 9. 每次联调前的最小检查

```powershell
git status --short --branch
git rev-parse HEAD
curl.exe -fsS http://127.0.0.1:8080/health
```

然后确认：

- 后端日志没有迁移失败。
- PostgreSQL 和 Redis 健康。
- 前端实际访问的是 `3000`，API 代理目标是当前后端。
- 所选 API Key 的分组平台和异步开关符合要测试的模式。
- 测试异步、图库或审核上传前，OSS 测试连接已通过。
