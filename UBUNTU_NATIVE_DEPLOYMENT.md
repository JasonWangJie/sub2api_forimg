# Sub2API Ubuntu 裸机生产部署教程

本文档适用于以下部署条件：

- Ubuntu 22.04/24.04；
- 不使用 Docker；
- 服务器已经有 PostgreSQL、Redis、Nginx，本文不安装这三个服务；
- 使用当前仓库源码编译，保留本地定制功能和性能优化；
- Nginx 对公网提供 HTTP/HTTPS，Sub2API 只监听 `127.0.0.1:8080`；
- 前端由 Vite 构建后嵌入 Go 二进制，生产环境不需要常驻 Node/Vite 进程。

本文覆盖两种场景：

1. **全新上线**：PostgreSQL 和 Redis 服务已经存在，但 Sub2API 使用一个新的空数据库，尚无管理员和业务数据；
2. **接入已有线上数据**：已有正在使用的 Sub2API PostgreSQL 数据库、Redis 和 `config.yaml`，新服务器继续连接原数据。

> 重要：不要运行 `deploy/install.sh` 部署当前定制代码。该脚本下载官方 Release，不包含当前工作区尚未发布的修改。Windows 编译的 `.exe` 也不能直接在 Ubuntu 运行。

---

## 0. 新机器一键部署

仓库提供了 [`deploy/ubuntu-native-deploy.sh`](deploy/ubuntu-native-deploy.sh)。它适合 Ubuntu 22.04/24.04 的 `amd64`、`arm64` 新机器，也可以在后续版本发布时重复执行。

脚本自动完成：

1. 检查 Ubuntu 版本、CPU 架构和 root 权限；
2. 安装 Git、curl、编译器、Node.js 22、pnpm 10.34.5，以及 `backend/go.mod` 指定的 Go 版本；
3. 拉取指定分支，或使用服务器上已经上传的源码；
4. 构建前端，再用 `-tags embed` 构建包含前端和 SQL 迁移的 Go 二进制；
5. 安装到 `/opt/sub2api/releases/<时间戳>`，并切换 `/opt/sub2api/current`；
6. 创建 `sub2api` 系统用户，安装并启动 systemd 服务；
7. 配置现有 Nginx，关闭流式响应缓冲，并执行 `nginx -t`；
8. 等待服务启动和自动数据库迁移完成，最长等待 10 分钟。

脚本**不会安装、初始化、停止或修改 PostgreSQL 和 Redis**，也不会创建 PostgreSQL 数据库。运行前必须保证这两个服务已经存在，而且应用服务器能够访问它们。Nginx 也按“服务器已经安装”处理；如果没有 Nginx，脚本会明确退出，可以在确认不需要反向代理时使用 `--skip-nginx`。

### 0.1 所有参数

```text
--domain DOMAIN         Nginx 使用的域名；除非指定 --skip-nginx，否则必填
--repo URL              要拉取的 Git 仓库；默认是当前定制仓库
--branch NAME           分支或标签；默认 main
--source-dir PATH       使用服务器上的现有源码，不再执行 git clone
--config PATH           安装已有的生产 config.yaml，权限自动设置为 600
--skip-nginx            不创建、不检查、不重载 Nginx 配置
--enable-certbot        安装 Certbot，并申请和启用 HTTPS 证书
--certbot-email EMAIL   Certbot 注册邮箱；启用 Certbot 时必填
-h, --help              显示帮助
```

查看脚本帮助不会修改服务器：

```bash
bash deploy/ubuntu-native-deploy.sh --help
```

命令参数只放域名、仓库、分支和文件路径。不要把 PostgreSQL、Redis 密码或带访问令牌的仓库 URL 直接写进命令，避免进入 Shell 历史和进程参数。

### 0.2 在新机器上取得脚本

新机器先安装下载工具，并把脚本下载到临时目录：

```bash
sudo apt update
sudo apt install -y curl ca-certificates
curl -fsSL \
  https://raw.githubusercontent.com/JasonWangJie/sub2api/main/deploy/ubuntu-native-deploy.sh \
  -o /tmp/sub2api-deploy.sh
less /tmp/sub2api-deploy.sh
```

建议先通过 `less` 检查将以 root 权限执行的内容，再运行。脚本默认拉取 `https://github.com/JasonWangJie/sub2api.git` 的 `main` 分支，也可以显式填写 `--repo` 和 `--branch`。

如果是私有仓库，不要把 Git Token 拼进 `--repo`。应先用部署密钥或凭据助手把仓库克隆到服务器，再按照 0.5 节通过 `--source-dir` 部署。

### 0.3 全新上线

适用条件：PostgreSQL 和 Redis 已存在；目标 PostgreSQL 数据库是新建的空库；尚未创建 Sub2API 管理员。

先确认：

- 域名已经解析到这台服务器；
- Nginx 已经安装；
- PostgreSQL 中已经创建空数据库和有 DDL 权限的应用用户；
- Redis 已经准备好专用实例或逻辑 DB；
- 云安全组开放 `22`、`80`，准备直接签发 HTTPS 时还应开放 `443`；
- 不向公网开放 `8080`、`5432`、`6379`。

执行：

```bash
sudo bash /tmp/sub2api-deploy.sh \
  --domain api.example.com \
  --repo https://github.com/JasonWangJie/sub2api.git \
  --branch main
```

把 `api.example.com` 替换成真实域名。脚本会启动首次 Setup 服务，但不会要求你在命令行输入数据库密码。完成后，在自己的电脑建立 SSH 隧道：

```bash
ssh -L 8080:127.0.0.1:8080 ubuntu@SERVER_IP
```

浏览器打开 `http://127.0.0.1:8080`，在 Setup 页面填写 PostgreSQL、Redis 和首个管理员信息。Setup 会生成 `/opt/sub2api/data/config.yaml`、`.installed`，执行全部数据库迁移，然后由 systemd 自动重启进入正式服务。

### 0.4 接入已有线上数据库和 Redis

已有线上数据时，必须继续使用原来的 `config.yaml`，以保留 PostgreSQL/Redis 连接信息、JWT Secret、TOTP Encryption Key 和其他业务密钥。不要重新进入 Setup，也不要只填写一个新的最小配置。

先在原服务器或安全备份位置取得原配置，再上传到新服务器：

```powershell
scp F:\secure\sub2api-config.yaml ubuntu@SERVER_IP:/home/ubuntu/sub2api-config.yaml
```

上线前先对 PostgreSQL 做快照或 `pg_dump` 备份，然后执行：

```bash
sudo bash /tmp/sub2api-deploy.sh \
  --domain api.example.com \
  --repo https://github.com/JasonWangJie/sub2api.git \
  --branch main \
  --config /home/ubuntu/sub2api-config.yaml
```

脚本会把配置安装为 `/opt/sub2api/data/config.yaml`，属主为 `sub2api:sub2api`，权限为 `0600`。如果目标目录已有不同配置，会先在同一受保护目录中生成带 release 时间戳的备份。部署成功并确认配置可用后，删除上传到用户主目录的临时副本：

```bash
rm /home/ubuntu/sub2api-config.yaml
```

新二进制启动时会自动连接配置中的现有 PostgreSQL/Redis，并执行尚未运行的 SQL 迁移。脚本不会清空数据库或 Redis。新增表是否自动创建、迁移约束和回滚注意事项见第 16 节。

### 0.5 使用已经上传的当前源码

如果源码已经通过 Git 或压缩包放在服务器上，可以不让脚本重新拉取：

```bash
cd /home/ubuntu/sub2api
sudo bash deploy/ubuntu-native-deploy.sh \
  --domain api.example.com \
  --source-dir /home/ubuntu/sub2api
```

`--source-dir` 必须指向同时包含 `frontend/` 和 `backend/` 的仓库根目录。构建会更新该目录下的 `frontend/node_modules`、`backend/internal/web/dist` 和 `backend/sub2api`；若不希望修改上传目录，应让脚本从 Git 仓库重新拉取。

### 0.6 同时启用 HTTPS

DNS 已经生效，且公网可访问 `80/443` 时，可以让脚本安装 Certbot 并签发证书：

```bash
sudo bash /tmp/sub2api-deploy.sh \
  --domain api.example.com \
  --enable-certbot \
  --certbot-email admin@example.com
```

默认不运行 Certbot，避免在 DNS 尚未生效时反复申请证书。已有证书或复杂 Nginx 站点时，建议不加 `--enable-certbot`，部署完成后按第 12 节把反代配置合并到现有 HTTPS 站点。

### 0.7 后续升级重复执行

同一条命令可以用于升级。每次执行都会构建一个新的 release，保留 `/opt/sub2api/data`，切换 `current` 并重启服务：

```bash
sudo bash /tmp/sub2api-deploy.sh \
  --domain api.example.com \
  --repo https://github.com/JasonWangJie/sub2api.git \
  --branch main
```

已经部署过的服务器不需要再次传 `--config`，脚本会原样保留数据目录。包含数据库迁移的版本升级前仍必须先备份 PostgreSQL。Nginx 站点文件如果已经存在，脚本会保留它，避免覆盖 Certbot 或人工修改；同时仍会运行配置检查并重载服务。

常用验证和排查命令：

```bash
readlink -f /opt/sub2api/current
sudo systemctl status sub2api --no-pager
sudo journalctl -u sub2api -n 200 --no-pager
curl -fsS http://127.0.0.1:8080/health
sudo nginx -t
```

脚本失败时不会删除 `/opt/sub2api/data`。构建失败发生在切换 release 之前；服务启动或数据库迁移失败时，查看 `journalctl`，不要在未确认数据库兼容性前盲目切回旧二进制。

---

## 1. 最终架构

```text
Internet
   |
   | 80 / 443
   v
Nginx（已有）
   |
   | HTTP/1.1，127.0.0.1:8080
   v
Sub2API Go 二进制（systemd）
   |                         |
   | TCP                     | TCP
   v                         v
PostgreSQL（已有）          Redis（已有）
```

生产环境安全原则：

- 公网或云安全组只开放 `22`、`80`、`443`；
- 不向公网开放 `8080`；
- PostgreSQL、Redis 优先使用本机回环地址或私网地址；
- PostgreSQL、Redis 如果必须跨公网访问，必须限制来源 IP，并启用 TLS；
- `config.yaml` 包含数据库、Redis、JWT、TOTP 等敏感信息，权限必须是 `600`，不得提交 Git；
- 不要在聊天、工单或部署日志里粘贴密码和密钥。

---

## 2. 部署前填写的信息

先记录以下信息。示例值必须替换为真实值：

| 项目 | 示例 | 说明 |
|---|---|---|
| 应用域名 | `api.example.com` | 已解析到 Ubuntu 公网 IP |
| SSH 用户 | `ubuntu` | 具有 `sudo` 权限 |
| CPU 架构 | `amd64` / `arm64` | 使用 `dpkg --print-architecture` 查看 |
| PostgreSQL 地址 | `127.0.0.1` 或私网 IP | 与应用同机时用 `127.0.0.1` |
| PostgreSQL 端口 | `5432` | 以现有服务实际端口为准 |
| PostgreSQL 数据库 | `sub2api` | 全新场景必须先创建空数据库 |
| PostgreSQL 用户 | `sub2api` | 建议为目标数据库 Owner |
| PostgreSQL SSL | `disable` / `require` / `verify-full` | 公网连接不能使用明文 |
| Redis 地址 | `127.0.0.1` 或私网 IP | 与应用同机时用 `127.0.0.1` |
| Redis 端口 | `6379` | 以现有服务实际端口为准 |
| Redis 用户 | 空或 ACL 用户 | 默认用户可留空 |
| Redis DB | `0` | 建议使用专用 DB，不要与其他应用混用 |
| Redis TLS | `false` / `true` | 公网连接应启用 TLS |

服务器终端可以先定义非敏感变量，减少后续输错：

```bash
export APP_DOMAIN="api.example.com"
export APP_PORT="8080"
export PG_HOST="127.0.0.1"
export PG_PORT="5432"
export PG_DATABASE="sub2api"
export PG_USER="sub2api"
export REDIS_HOST="127.0.0.1"
export REDIS_PORT="6379"
```

不要把数据库、Redis 密码定义在命令行环境变量里，避免进入 Shell 历史或进程信息。

---

## 3. 检查现有基础设施

本文不安装 PostgreSQL、Redis、Nginx，只做检查。

```bash
lsb_release -a
dpkg --print-architecture
nginx -v
sudo systemctl status nginx --no-pager
```

如果 PostgreSQL、Redis 与应用同机，可查看监听端口：

```bash
sudo ss -lntp | grep -E ":(${PG_PORT}|${REDIS_PORT})\b"
```

不依赖额外客户端的 TCP 连通性检查：

```bash
timeout 3 bash -c "cat < /dev/null > /dev/tcp/${PG_HOST}/${PG_PORT}" && echo "PostgreSQL TCP OK"
timeout 3 bash -c "cat < /dev/null > /dev/tcp/${REDIS_HOST}/${REDIS_PORT}" && echo "Redis TCP OK"
```

建议版本：PostgreSQL 15+、Redis 7+。如果数据库是远程托管服务，还要检查安全组、白名单和 TLS 要求。

---

## 4. 将当前源码送到 Ubuntu

### 4.1 推荐方式：提交到自己的 Git 仓库

本机先检查：

```powershell
cd F:\Code\Git\sub2api
git status
```

确认本次修改和未跟踪源码均已纳入提交，且没有把 `backend/config.yaml`、`.env`、日志或密码提交进去，然后推送自己的仓库。

Ubuntu 执行：

```bash
git clone --branch main https://github.com/JasonWangJie/sub2api.git "$HOME/sub2api-src"
export SOURCE_DIR="$HOME/sub2api-src"
cd "$SOURCE_DIR"
git rev-parse --short HEAD
git status --short
```

必须把 `YOUR_GIT_REPOSITORY_URL` 替换为自己的仓库地址。服务器克隆官方仓库不会包含本地定制修改。

### 4.2 未提交工作区：打包上传

如果暂时不能提交，可以在 Windows PowerShell 打包当前工作区。该方式会包含未跟踪源码：

```powershell
cd F:\Code\Git\sub2api
tar.exe --exclude=".git" --exclude="frontend/node_modules" --exclude="backend/config.yaml" --exclude="backend/.installed" --exclude="backend/logs" --exclude=".env*" -czf ..\sub2api-src.tgz .
scp ..\sub2api-src.tgz ubuntu@SERVER_IP:/home/ubuntu/
```

Ubuntu 执行：

```bash
export SOURCE_DIR="$HOME/sub2api-src-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$SOURCE_DIR"
tar -xzf "$HOME/sub2api-src.tgz" -C "$SOURCE_DIR"
cd "$SOURCE_DIR"
```

把 `ubuntu`、`SERVER_IP` 替换为实际 SSH 用户和服务器地址。

---

## 5. 安装源码构建工具

只安装 Git、编译器、Node.js 和 Go，不安装 PostgreSQL、Redis、Nginx。

### 5.1 基础工具

```bash
sudo apt update
sudo apt install -y git curl ca-certificates build-essential
```

### 5.2 安装 Go 1.26.5

当前 `backend/go.mod` 要求 Go `1.26.5`。以后仓库升级 Go 版本时，以 `backend/go.mod` 为准。

```bash
export GO_VERSION="1.26.5"
export GO_ARCH="$(dpkg --print-architecture)"

case "$GO_ARCH" in
  amd64|arm64) ;;
  *) echo "Unsupported architecture: $GO_ARCH"; exit 1 ;;
esac

curl -fLO "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
sudo install -d -m 0755 "/opt/go/${GO_VERSION}"
sudo tar -C "/opt/go/${GO_VERSION}" --strip-components=1 -xzf "go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
sudo ln -sfn "/opt/go/${GO_VERSION}/bin/go" /usr/local/bin/go
sudo ln -sfn "/opt/go/${GO_VERSION}/bin/gofmt" /usr/local/bin/gofmt
go version
```

### 5.3 安装 Node.js 22 和 pnpm 10

Node.js 只用于构建前端，不会作为生产服务常驻。

```bash
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt install -y nodejs
node --version
```

使用 pnpm 官方安装脚本安装固定版本。固定版本可以避免不同发布日期的服务器自动取得不同主版本：

```bash
curl -fsSL https://get.pnpm.io/install.sh -o /tmp/install-pnpm.sh
sudo env \
  PNPM_VERSION=10.34.5 \
  PNPM_HOME=/usr/local/share/pnpm \
  SHELL=/bin/bash \
  sh /tmp/install-pnpm.sh
sudo ln -sfn /usr/local/share/pnpm/pnpm /usr/local/bin/pnpm
pnpm --version
```

官方安装器会配置 pnpm 的目录；这里额外创建 `/usr/local/bin/pnpm` 软链接，因此当前 Shell 不需要再加载 `/root/.bashrc`。如果只按官方默认命令安装、没有创建软链接，则需要重新登录 Shell，或加载安装器输出中提示的环境文件：

```bash
source /root/.bashrc
# 或者 source /etc/profile
```

pnpm 10.26+ 会默认阻止未审批的依赖构建脚本。当前仓库已经在 `frontend/pnpm-workspace.yaml` 中仅批准构建所需的两个包：

```yaml
allowBuilds:
  esbuild: true
  vue-demi: true
```

安装依赖时还要明确启用脚本：

```bash
CI=true pnpm install --frozen-lockfile --ignore-scripts=false
```

如果使用的是尚未修复 `pnpm-workspace.yaml` 的旧源码，运行 `pnpm approve-builds`，在交互界面中只选择 `esbuild` 和 `vue-demi`，再重新安装。当前 pnpm 的 `approve-builds` 是交互式命令，不要写成 `pnpm approve-builds esbuild vue-demi`。

---

## 6. 编译前端和后端

先确认当前 Shell 已设置源码目录。Git 克隆方式通常是：

```bash
export SOURCE_DIR="$HOME/sub2api-src"
```

打包上传方式已经在解压时设置了带时间戳的 `SOURCE_DIR`。确认后执行：

```bash
cd "$SOURCE_DIR/frontend"
CI=true pnpm install --frozen-lockfile --ignore-scripts=false
pnpm run build

cd "$SOURCE_DIR/backend"
VERSION="$(sh ./scripts/resolve-version.sh)"
go build -trimpath -tags embed -ldflags="-X main.Version=${VERSION}" -o sub2api ./cmd/server

file ./sub2api
ls -lh ./sub2api
```

构建过程说明：

1. `pnpm run build` 将前端输出到 `backend/internal/web/dist`；
2. `go build -tags embed` 将前端文件和 `backend/migrations/*.sql` 迁移文件嵌入二进制；
3. 生产服务器最终只运行 `sub2api` 二进制，不运行 Vite；
4. 如果漏掉 `-tags embed`，生产二进制可能不包含管理前端。

可选的部署前测试：

```bash
cd "$SOURCE_DIR/backend"
go test ./internal/server/middleware ./internal/pkg/logger -count=1
```

---

## 7. 创建生产目录和系统用户

目录布局：

```text
/opt/sub2api/
├── current -> /opt/sub2api/releases/20260723-120000/
├── releases/
│   ├── 20260723-120000/sub2api
│   └── 20260724-180000/sub2api
└── data/
    ├── config.yaml
    ├── .installed
    └── logs/
```

创建用户和目录：

```bash
id sub2api >/dev/null 2>&1 || sudo useradd --system --home /opt/sub2api --shell /usr/sbin/nologin sub2api
sudo install -d -o root -g root -m 0755 /opt/sub2api
sudo install -d -o root -g root -m 0755 /opt/sub2api/releases
sudo install -d -o sub2api -g sub2api -m 0750 /opt/sub2api/data
```

安装本次编译结果：

```bash
export RELEASE_ID="$(date +%Y%m%d-%H%M%S)"
export RELEASE_DIR="/opt/sub2api/releases/${RELEASE_ID}"

sudo install -d -o root -g root -m 0755 "$RELEASE_DIR"
sudo install -o root -g root -m 0755 "$SOURCE_DIR/backend/sub2api" "$RELEASE_DIR/sub2api"
sudo ln -sfn "$RELEASE_DIR" /opt/sub2api/current
readlink -f /opt/sub2api/current
```

二进制由 `root` 持有，运行用户只能写 `/opt/sub2api/data`。

---

## 8. 配置 systemd

创建 `/etc/systemd/system/sub2api.service`：

```ini
[Unit]
Description=Sub2API API Gateway
Documentation=https://github.com/Wei-Shaw/sub2api
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
User=sub2api
Group=sub2api
WorkingDirectory=/opt/sub2api/current
ExecStart=/opt/sub2api/current/sub2api

Environment=GIN_MODE=release
Environment=SERVER_HOST=127.0.0.1
Environment=SERVER_PORT=8080
Environment=DATA_DIR=/opt/sub2api/data
Environment="SERVER_TRUSTED_PROXIES=127.0.0.1/32,::1/128"

Restart=always
RestartSec=5
KillSignal=SIGTERM
TimeoutStopSec=20
LimitNOFILE=1048576
UMask=0077

StandardOutput=journal
StandardError=journal
SyslogIdentifier=sub2api

NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/opt/sub2api/data

[Install]
WantedBy=multi-user.target
```

安装并检查 unit，但先根据下一章选择全新或已有数据场景：

```bash
sudo systemctl daemon-reload
sudo systemctl enable sub2api
sudo systemctl cat sub2api
```

为什么使用 `Restart=always`：首次 Setup 完成后程序会以状态码 `0` 正常退出，依靠 systemd 自动重启进入正式服务模式。

该 unit 将 `/opt/sub2api` 视为只读，只允许写 `data`。因此不要使用管理后台的在线二进制更新功能，统一使用本文的版本目录升级流程。

---

## 9. 场景 A：全新上线

适用条件：PostgreSQL、Redis 服务已经存在，但目标 PostgreSQL 数据库是空的，尚未创建 Sub2API 管理员。

### 9.1 准备空 PostgreSQL 数据库

Setup 向导会建表，但**不会创建 PostgreSQL 数据库本身**。

如果 PostgreSQL 与应用同机，且你有本机 PostgreSQL 管理权限，可以执行：

```bash
sudo -u postgres createuser --pwprompt sub2api
sudo -u postgres createdb --owner=sub2api sub2api
```

如果数据库或用户已经存在，不要重复执行。远程或托管 PostgreSQL 应在其管理控制台创建：

- 数据库名：`sub2api`；
- 应用用户：建议为数据库 Owner；
- 至少拥有连接、创建表、修改表、创建/删除索引和执行迁移的权限；
- 编码使用 UTF-8；
- 不要使用只读账号。

### 9.2 准备 Redis

Redis 不需要预建表。建议使用专用 Redis 实例或专用逻辑 DB。

- Standalone Redis 可选择未被其他应用使用的 DB；
- Redis Cluster 通常只能使用 DB `0`；
- 不要对正在使用的共享 Redis 执行 `FLUSHDB` 或 `FLUSHALL`；
- 如果使用 ACL，准备允许读写 Key、TTL、集合、Lua/事务等应用操作的账号。

### 9.3 确认数据目录没有旧安装标记

只在确认是全新安装时执行检查：

```bash
sudo ls -la /opt/sub2api/data
```

目录中不应存在 `config.yaml` 或 `.installed`。如果存在，不要直接删除；先确认它们是否属于旧生产实例。

### 9.4 启动 Setup 服务

```bash
sudo systemctl start sub2api
sudo systemctl status sub2api --no-pager
curl -fsS http://127.0.0.1:8080/setup/status
sudo journalctl -u sub2api -n 100 --no-pager
```

预期 `/setup/status` 返回 `needs_setup: true`。

### 9.5 使用 SSH 隧道访问向导

不要临时开放公网 8080。在本机 PowerShell 执行：

```powershell
ssh -L 8080:127.0.0.1:8080 ubuntu@SERVER_IP
```

保持终端不关闭，浏览器打开：

```text
http://127.0.0.1:8080
```

向导填写：

| 字段 | 填写内容 |
|---|---|
| Database Host | PostgreSQL 与应用同机填 `127.0.0.1`，否则填私网地址 |
| Database Port | 实际端口，例如 `5432` |
| Database User | 目标数据库 Owner/应用用户 |
| Database Password | 该用户密码 |
| Database Name | `sub2api` 或实际数据库名 |
| SSL Mode | 同机可 `disable`；远程建议 `require` 或 `verify-full` |
| Redis Host | 同机填 `127.0.0.1`，否则填私网地址 |
| Redis Port | 实际端口，例如 `6379` |
| Redis Username | 默认用户留空；ACL 填用户名 |
| Redis Password | Redis 密码 |
| Redis DB | 专用逻辑 DB 编号 |
| Redis TLS | 按现有 Redis 配置选择 |
| Admin Email | 首个管理员邮箱 |
| Admin Password | 至少 8 位，生产环境使用高强度密码 |

向导会执行：

1. 测试 PostgreSQL 和 Redis；
2. 自动执行所有数据库迁移；
3. 空数据库中创建首个管理员；
4. 生成 JWT Secret；
5. 写入 `/opt/sub2api/data/config.yaml`；
6. 写入 `/opt/sub2api/data/.installed`；
7. 正常退出，由 `Restart=always` 自动重启为正式服务。

### 9.6 补齐生产安全配置

Setup 生成的是最小配置。完成后执行：

```bash
sudo systemctl stop sub2api
sudoedit /opt/sub2api/data/config.yaml
```

在已有配置中补充或确认以下内容，不要覆盖向导已经写入的数据库、Redis 和 JWT Secret：

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  mode: "release"
  frontend_url: "https://api.example.com"
  trusted_proxies:
    - "127.0.0.1/32"
    - "::1/128"
  max_request_body_size: 268435456

security:
  trust_forwarded_ip_for_api_key_acl: false

totp:
  encryption_key: "REPLACE_WITH_OPENSSL_RAND_HEX_32"

timezone: "Asia/Shanghai"
```

生成固定 TOTP 加密密钥：

```bash
openssl rand -hex 32
```

将输出填入 `totp.encryption_key`。不要每次重启重新生成；如果已经有用户启用 TOTP，丢失或更换该密钥会导致已有 TOTP Secret 无法解密。

配置权限和启动：

```bash
sudo chown sub2api:sub2api /opt/sub2api/data/config.yaml /opt/sub2api/data/.installed
sudo chmod 600 /opt/sub2api/data/config.yaml
sudo chmod 400 /opt/sub2api/data/.installed
sudo systemctl start sub2api
```

把示例域名 `api.example.com` 替换为真实域名。

---

## 10. 场景 B：连接已有线上 PostgreSQL/Redis

适用条件：目标 PostgreSQL 已经有 Sub2API 用户、账号、API Key、用量和系统设置，且已有一份正在使用的 `config.yaml`。

### 10.1 不要重新走 Setup

已有线上数据时应保留原配置中的：

- PostgreSQL 和 Redis 地址、账号、密码；
- JWT Secret；
- TOTP Encryption Key；
- 第三方登录、支付、邮件、对象存储等密钥；
- 当前业务配置。

如果错误地重新生成密钥，可能导致登录会话失效，或已有加密数据无法解密。

### 10.2 上线前备份 PostgreSQL

使用数据库主机现有的 `pg_dump` 或托管数据库快照。示例命令会交互式询问密码：

```bash
mkdir -p "$HOME/sub2api-backups"
pg_dump -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DATABASE" -Fc \
  -f "$HOME/sub2api-backups/sub2api-$(date +%Y%m%d-%H%M%S).dump"
```

如果应用服务器没有 PostgreSQL 客户端，不需要为了部署应用安装 PostgreSQL 服务；可以在数据库服务器或托管平台执行等价备份。

### 10.3 安全上传原配置

Windows PowerShell：

```powershell
scp F:\Code\Git\sub2api\backend\config.yaml ubuntu@SERVER_IP:/home/ubuntu/sub2api-config.yaml
```

Ubuntu：

```bash
sudo systemctl stop sub2api
sudo install -o sub2api -g sub2api -m 0600 \
  "$HOME/sub2api-config.yaml" \
  /opt/sub2api/data/config.yaml
rm "$HOME/sub2api-config.yaml"
sudo stat /opt/sub2api/data/config.yaml
```

`config.yaml` 存在时程序会跳过 Setup，直接进入正常启动。

### 10.4 修改连接地址

```bash
sudoedit /opt/sub2api/data/config.yaml
```

如果应用与 PostgreSQL、Redis 部署在同一台服务器，建议改为：

```yaml
database:
  host: "127.0.0.1"
  port: 5432  # 按实际端口修改

redis:
  host: "127.0.0.1"
  port: 6379  # 按实际端口修改
```

如果不在同一台服务器，优先使用私网 IP/内网域名。公网连接必须配置来源白名单和 TLS。

同时确认：

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  mode: "release"
  frontend_url: "https://api.example.com"
  trusted_proxies:
    - "127.0.0.1/32"
    - "::1/128"

security:
  trust_forwarded_ip_for_api_key_acl: false
```

### 10.5 启动并验证

```bash
sudo systemctl start sub2api
sudo systemctl status sub2api --no-pager
sudo journalctl -u sub2api -n 200 --no-pager
curl -fsS http://127.0.0.1:8080/health
sudo ss -lntp | grep ':8080\b'
```

必须确认只监听 `127.0.0.1:8080`，不是 `0.0.0.0:8080`。

不要长期同时运行本地开发实例和服务器实例并连接同一个 PostgreSQL/Redis。虽然迁移有数据库锁，但后台任务、定时任务和队列消费者可能同时工作。服务器验证完成后停止旧实例。

---

## 11. 手工配置模板说明

全新安装优先使用 Setup 向导，因为管理员只能通过初始化流程创建。下面的模板用于：

- 已有线上数据库但原配置需要整理；
- Setup 完成后补充生产参数；
- 故障恢复时重建配置。

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  mode: "release"
  frontend_url: "https://api.example.com"
  read_header_timeout: 10
  max_header_bytes: 65536
  idle_timeout: 120
  max_request_body_size: 268435456
  trusted_proxies:
    - "127.0.0.1/32"
    - "::1/128"

run_mode: "standard"
timezone: "Asia/Shanghai"

security:
  trust_forwarded_ip_for_api_key_acl: false

database:
  host: "127.0.0.1"
  port: 5432
  user: "sub2api"
  password: "REPLACE_WITH_DATABASE_PASSWORD"
  dbname: "sub2api"
  sslmode: "disable"
  max_open_conns: 100
  max_idle_conns: 25
  conn_max_lifetime_minutes: 30
  conn_max_idle_time_minutes: 5

redis:
  host: "127.0.0.1"
  port: 6379
  username: ""
  password: "REPLACE_WITH_REDIS_PASSWORD"
  db: 0
  enable_tls: false
  dial_timeout_seconds: 5
  read_timeout_seconds: 3
  write_timeout_seconds: 3
  pool_size: 256
  min_idle_conns: 32

jwt:
  secret: "REPLACE_WITH_EXISTING_OR_OPENSSL_RAND_HEX_32"
  expire_hour: 24

totp:
  encryption_key: "REPLACE_WITH_EXISTING_OR_OPENSSL_RAND_HEX_32"

log:
  level: "info"
  format: "console"
  output:
    to_stdout: true
    to_file: true
```

注意事项：

- 已有系统必须填写原 JWT/TOTP 密钥，不能随意更换；
- 数据库、Redis 跨公网时不能照抄 `disable`/`false`；
- `max_open_conns` 必须小于 PostgreSQL 可分配给应用的连接数；
- `max_idle_conns` 不能大于 `max_open_conns`；
- Redis 连接池按并发量和 Redis 最大客户端数调节；
- 同域名前后端不需要额外配置 CORS；跨域时再配置精确 Origin，不能随意使用 `*`；
- 完整可选项参考 `deploy/config.example.yaml`，不要直接把示例密码用于生产。

---

## 12. 配置现有 Nginx

### 12.1 WebSocket Upgrade 映射

先检查现有 Nginx 是否已经定义 `$connection_upgrade`：

```bash
sudo nginx -T 2>/dev/null | grep -n 'connection_upgrade'
```

如果没有，创建 `/etc/nginx/conf.d/sub2api-websocket-map.conf`：

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
```

如果已有等价 `map`，直接复用，不要重复定义同名变量。

### 12.2 创建站点配置

创建 `/etc/nginx/sites-available/sub2api`。把 `api.example.com` 替换为真实域名：

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name api.example.com;

    # session_id 等粘性会话 Header 包含下划线，必须允许。
    underscores_in_headers on;

    # 与后端默认 256 MiB 请求体限制一致。
    client_max_body_size 256m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Port $server_port;

        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        # LLM SSE/流式输出必须禁用缓冲，否则会增加首字延迟。
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;

        proxy_connect_timeout 10s;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
        proxy_socket_keepalive on;
    }
}
```

如果域名已经有 Nginx `server` 配置，不要创建重复的 `server_name`，把 `underscores_in_headers`、请求体大小和 `location /` 合并到现有站点。

启用并检查：

```bash
sudo ln -sfn /etc/nginx/sites-available/sub2api /etc/nginx/sites-enabled/sub2api
sudo nginx -t
sudo systemctl reload nginx
```

### 12.3 配置 HTTPS

如果服务器已经有该域名证书，把反代 `location` 合并到已有 443 站点即可。

如果只有 Nginx、还没有证书，可以安装 Certbot 插件并签发：

```bash
sudo apt install -y certbot python3-certbot-nginx
sudo certbot --nginx -d "$APP_DOMAIN"
sudo certbot renew --dry-run
```

DNS 必须提前解析到服务器，云安全组必须允许 80/443。

---

## 13. 防火墙与端口

云安全组建议：

| 端口 | 来源 | 用途 |
|---|---|---|
| 22 | 管理 IP | SSH |
| 80 | 公网 | HTTP/证书验证/跳转 HTTPS |
| 443 | 公网 | HTTPS API 和管理页面 |
| 8080 | 不开放 | 只允许本机 Nginx 访问 |
| PostgreSQL | 应用服务器私网 IP | 不向全公网开放 |
| Redis | 应用服务器私网 IP | 不向全公网开放 |

如果使用 UFW，先确认 SSH 规则，再操作：

```bash
sudo ufw allow OpenSSH
sudo ufw allow 'Nginx Full'
sudo ufw status verbose
```

不要在未确认 SSH 规则时直接执行 `ufw enable`，否则可能把自己锁在服务器外。

---

## 14. 上线验证

### 14.1 后端和 systemd

```bash
sudo systemctl is-enabled sub2api
sudo systemctl is-active sub2api
sudo systemctl status sub2api --no-pager
sudo journalctl -u sub2api -n 200 --no-pager
curl -fsS http://127.0.0.1:8080/health
sudo ss -lntp | grep ':8080\b'
```

健康检查应返回：

```json
{"status":"ok"}
```

### 14.2 Nginx 和公网

```bash
sudo nginx -t
curl -I "http://${APP_DOMAIN}/"
curl -fsS "https://${APP_DOMAIN}/health"
curl --http2 -I "https://${APP_DOMAIN}/"
```

### 14.3 日志观察

```bash
sudo journalctl -u sub2api -f
```

文件日志默认位于 `DATA_DIR` 下，可检查：

```bash
sudo find /opt/sub2api/data/logs -maxdepth 1 -type f -ls 2>/dev/null
```

### 14.4 数据库迁移记录

在 PostgreSQL 管理终端执行：

```sql
SELECT filename, applied_at
FROM schema_migrations
ORDER BY applied_at DESC
LIMIT 20;
```

当前代码的迁移文件应能在该表中找到对应记录。

---

## 15. 后续发布和升级

### 15.1 升级原则

每次发布遵循：

```text
审查代码和迁移
  -> PostgreSQL 备份/快照
  -> 构建新二进制
  -> 安装到新的 releases 目录
  -> 切换 current 链接
  -> 重启
  -> 等待自动迁移完成
  -> 检查 health、日志和 schema_migrations
```

### 15.2 构建新版本

获取新源码后重复第 6 章。不要在 `/opt/sub2api/current` 内直接覆盖正在运行的文件。

```bash
cd "$SOURCE_DIR/frontend"
pnpm install --frozen-lockfile
pnpm run build

cd "$SOURCE_DIR/backend"
VERSION="$(sh ./scripts/resolve-version.sh)"
go build -trimpath -tags embed -ldflags="-X main.Version=${VERSION}" -o sub2api ./cmd/server
```

### 15.3 备份数据库

有新增表、字段、索引或数据迁移时，必须先完成 PostgreSQL 备份或快照。Redis 通常保存缓存和运行状态，但不要在发布时清空。

### 15.4 安装并切换

```bash
export PREVIOUS_RELEASE="$(readlink -f /opt/sub2api/current)"
export RELEASE_ID="$(date +%Y%m%d-%H%M%S)"
export RELEASE_DIR="/opt/sub2api/releases/${RELEASE_ID}"

sudo install -d -o root -g root -m 0755 "$RELEASE_DIR"
sudo install -o root -g root -m 0755 "$SOURCE_DIR/backend/sub2api" "$RELEASE_DIR/sub2api"
sudo ln -sfn "$RELEASE_DIR" /opt/sub2api/current
sudo systemctl restart sub2api
```

验证：

```bash
sudo systemctl status sub2api --no-pager
sudo journalctl -u sub2api -n 200 --no-pager
curl -fsS http://127.0.0.1:8080/health
curl -fsS "https://${APP_DOMAIN}/health"
```

单实例重启期间可能短暂返回 502，建议在低峰维护窗口发布。需要完全无中断时，应另外设计双实例/负载均衡发布流程。

---

## 16. 新增功能和数据库表会不会自动创建

### 16.1 结论

**会自动执行新增迁移，但不是根据 Go 代码自动猜测建表。**

正常服务每次启动都会在 `backend/internal/repository/ent.go` 的 `InitEnt` 阶段调用 `applyMigrationsFS` 数据库迁移器。迁移器会读取编译进二进制的 `backend/migrations/*.sql`，按文件名排序并执行数据库中尚未记录的迁移。

因此，新增功能需要新表或新字段时，开发必须同时新增迁移文件，例如：

```text
backend/migrations/191_ZJ_add_example_feature.sql
```

示例：

```sql
CREATE TABLE IF NOT EXISTS example_features (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_example_features_user_id
    ON example_features (user_id);
```

发布时使用 `go build -tags embed`，迁移 SQL 会被嵌入新二进制。新二进制启动后会自动执行并写入：

```text
schema_migrations(filename, checksum, applied_at)
```

### 16.2 自动迁移的保护机制

- 已成功执行的迁移按文件名自动跳过；
- 每个迁移记录 SHA-256 校验和；
- 已执行迁移被修改后，校验和不一致，应用会拒绝启动；
- PostgreSQL Advisory Lock 保证多实例同时启动时只有一个实例执行迁移；
- 普通迁移默认在事务中执行，失败时回滚；
- `_notx.sql` 用于不能放在事务中的操作，开发时必须确保可重试和幂等；
- 正常启动迁移超时为 10 分钟，迁移失败时应用启动失败，systemd 会按配置重试。

### 16.3 必须遵守的开发规则

1. 已上线的迁移文件不可修改、重命名或删除；
2. 每次数据库变更创建一个编号更大的新迁移；
3. 单纯新增 Ent Schema、Go Struct 或 Repository 代码不会自动创建表；
4. SQL 迁移才是当前项目数据库结构的权威来源；
5. 迁移尽量使用 `IF NOT EXISTS`、`IF EXISTS`，保证重试安全；
6. 大表加索引、改列类型、回填数据要评估锁表时间和磁盘空间；
7. 先备份，再发布包含迁移的新二进制；
8. 新代码和数据库变更尽量采用“先扩展、后切换、最后清理”的兼容发布方式。

### 16.4 自动迁移不等于自动回滚

迁移是向前执行的。切回旧二进制不会自动删除新表、字段或恢复旧数据。

- 二进制回滚：切回旧 `releases` 目录；
- 数据库回滚：需要人工逆向 SQL 或从发布前备份恢复；
- 如果新迁移与旧二进制不兼容，只回滚二进制可能仍然无法运行。

---

## 17. 二进制回滚

查看版本：

```bash
readlink -f /opt/sub2api/current
ls -lah /opt/sub2api/releases
```

切回确认过的旧版本：

```bash
sudo ln -sfn /opt/sub2api/releases/OLD_RELEASE_ID /opt/sub2api/current
sudo systemctl restart sub2api
sudo journalctl -u sub2api -n 200 --no-pager
curl -fsS http://127.0.0.1:8080/health
```

把 `OLD_RELEASE_ID` 替换为真实目录名。回滚前先判断数据库迁移是否与旧代码兼容；不兼容时使用发布前数据库备份恢复。

---

## 18. 常见故障排查

### 18.1 服务不断重启

```bash
sudo systemctl status sub2api --no-pager
sudo journalctl -u sub2api -n 300 --no-pager
```

重点检查：配置 YAML 格式、数据库连接、Redis 连接、迁移失败、文件权限。

### 18.2 启动后又出现 Setup 页面

```bash
sudo systemctl show sub2api -p Environment
sudo ls -la /opt/sub2api/data
sudo stat /opt/sub2api/data/config.yaml
```

确认 `DATA_DIR=/opt/sub2api/data` 且 `config.yaml` 存在。已有线上数据不要重新完成 Setup，应恢复原配置。

### 18.3 数据库权限不足

日志通常包含 `permission denied`、`must be owner` 或迁移文件名。确保应用用户是数据库 Owner，或具有迁移所需的 DDL 权限。

### 18.4 migration checksum mismatch

说明某个已经执行过的迁移文件被修改。不要直接改数据库里的 checksum，也不要删除迁移记录。应恢复该迁移文件的原始内容，并用新的迁移文件实现后续变更。

### 18.5 Nginx 502

```bash
curl -v http://127.0.0.1:8080/health
sudo ss -lntp | grep ':8080\b'
sudo tail -n 100 /var/log/nginx/error.log
```

如果本地 health 不通，先修复 Sub2API；本地通而域名不通，再检查 Nginx。

### 18.6 流式返回首字很慢

检查 Nginx 是否包含：

```nginx
proxy_buffering off;
proxy_request_buffering off;
proxy_cache off;
```

同时检查上游 API、代理节点、数据库/Redis 延迟和网关日志，不能只看总请求耗时。

### 18.7 粘性会话失效

确认 Nginx `server` 中存在：

```nginx
underscores_in_headers on;
```

否则 `session_id` 等下划线 Header 会被 Nginx 丢弃。

### 18.8 WebSocket 升级失败

确认使用 HTTP/1.1 转发，并设置 `Upgrade` 和 `Connection` Header；同时确认没有重复或错误的 `$connection_upgrade` map。

### 18.9 上传返回 413

确认 Nginx `client_max_body_size 256m`，并与 `server.max_request_body_size`、`gateway.max_body_size` 保持一致。

---

## 19. 生产上线检查清单

### 构建

- [ ] 部署的是当前定制源码，不是官方 Release；
- [ ] 未跟踪的必要源码已经提交或包含在上传包中；
- [ ] `pnpm install --frozen-lockfile` 成功；
- [ ] `pnpm run build` 成功；
- [ ] 使用 `go build -tags embed`；
- [ ] `file sub2api` 显示 Linux ELF 二进制。

### 配置和数据

- [ ] 已明确选择“全新上线”或“已有线上数据”；
- [ ] 已有线上数据时没有重新走 Setup；
- [ ] PostgreSQL 已完成备份/快照；
- [ ] `config.yaml` 属主为 `sub2api:sub2api`，权限为 `600`；
- [ ] JWT/TOTP 等原有密钥已保留；
- [ ] PostgreSQL/Redis 使用回环或私网地址；
- [ ] 没有把敏感配置提交 Git。

### 服务和反代

- [ ] systemd 使用 `Restart=always`；
- [ ] Sub2API 只监听 `127.0.0.1:8080`；
- [ ] Nginx 开启 `underscores_in_headers on`；
- [ ] Nginx 关闭响应缓冲和请求缓冲；
- [ ] Nginx 配置 WebSocket Upgrade；
- [ ] HTTPS 证书有效；
- [ ] 公网未开放 8080、PostgreSQL、Redis；
- [ ] `/health` 在本机和公网域名均正常。

### 升级和迁移

- [ ] 新数据库结构变更使用新的迁移文件；
- [ ] 没有修改已执行的历史迁移；
- [ ] 已检查 `schema_migrations`；
- [ ] 已记录上一个 release 目录；
- [ ] 已确认数据库变更是否允许旧二进制回滚。
