# 部署手册

[← Wiki 首页](Home.md)

## 部署方式对比

| 方式 | 适合场景 | 向导 | 依赖 |
|------|----------|------|------|
| Docker Compose | 生产 / 快速上线 | 通常自动完成初始化 | Docker 20.10+、Compose v2 |
| 二进制 + systemd | 已有 PG/Redis 的 Linux 服务器 | Web Setup | PostgreSQL 15+、Redis 7+ |
| 源码编译 | 定制开发 | Web Setup | Go、Node/pnpm、PG、Redis |
| Apple container | Apple 芯片本机试用 | 自动 | macOS 26 + container ≥ 1.1 |

详细文件说明见 [deploy/README.md](../deploy/README.md)。

---

## 方式 A：Docker Compose（推荐）

### 一键脚本

```bash
mkdir -p sub2api-deploy && cd sub2api-deploy
curl -sSL https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/deploy/docker-deploy.sh | bash
docker compose up -d
docker compose logs -f sub2api
```

脚本会：下载 compose、生成 `JWT_SECRET` / `TOTP_ENCRYPTION_KEY` / `POSTGRES_PASSWORD`、创建数据目录。

### Compose 文件选择

| 文件 | 数据 | 建议 |
|------|------|------|
| `docker-compose.local.yml` | 本地目录 `data/` `postgres_data/` `redis_data/` | **生产推荐**，便于打包迁移 |
| `docker-compose.yml` | 命名卷 | 试用 |

### 必要环境变量（`.env`）

```bash
POSTGRES_PASSWORD=...
JWT_SECRET=...                 # openssl rand -hex 32
TOTP_ENCRYPTION_KEY=...        # openssl rand -hex 32
ADMIN_EMAIL=admin@example.com  # 可选
ADMIN_PASSWORD=...             # 可选；未设则看日志
SERVER_PORT=8080               # 可选
```

### 升级

```bash
docker compose -f docker-compose.local.yml pull
docker compose -f docker-compose.local.yml up -d
```

### 整机迁移（local 版）

```bash
docker compose -f docker-compose.local.yml down
tar czf sub2api-complete.tar.gz sub2api-deploy/
# 传到新机器后解压，再 up -d
```

### 数据管理（datamanagementd）

管理后台「数据管理」依赖宿主机进程，Socket 固定为 `/tmp/sub2api-datamanagement.sock`。Docker 需挂载同路径。见 [DATAMANAGEMENTD_CN.md](../deploy/DATAMANAGEMENTD_CN.md)。

---

## 方式 B：二进制安装

```bash
curl -sSL https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/deploy/install.sh | sudo bash
sudo systemctl enable --now sub2api
```

浏览器打开 `http://IP:8080` 完成向导。

常用命令：

```bash
sudo systemctl status sub2api
sudo journalctl -u sub2api -f
sudo systemctl restart sub2api
# 卸载
curl -sSL https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/deploy/install.sh | sudo bash -s -- uninstall -y
```

管理后台左上角支持「检测更新」在线升级（二进制场景）。

---

## 方式 C：源码编译

```bash
git clone https://github.com/Wei-Shaw/sub2api.git
cd sub2api

cd frontend && pnpm install && pnpm run build && cd ..

cd backend
VERSION="$(./scripts/resolve-version.sh)"
go build -tags embed -ldflags="-X main.Version=${VERSION}" -o sub2api ./cmd/server
./sub2api   # 勿先复制 config.yaml，让向导生成
```

配置模板：`deploy/config.example.yaml`。

**管理员只能通过 Setup 向导创建。** 若提前放了 `config.yaml`，向导会被跳过，导致无法登录。处理方式：

```bash
mv config.yaml config.yaml.bak
./sub2api   # 完成向导
# 再合并配置后重启
```

---

## 方式 D：Apple container

```bash
cd deploy
./apple-container.sh init
./apple-container.sh up
./apple-container.sh status
```

见 [APPLE_CONTAINER.md](../deploy/APPLE_CONTAINER.md)。生产仍建议 Docker Compose。

---

## 反向代理

### Nginx

开启下划线请求头（Codex / 粘性会话需要）：

```nginx
http {
    underscores_in_headers on;
}
```

### Caddy h2c

后端默认支持 h2c + HTTP/1.1 回退：

```caddyfile
transport http {
    versions h2c h1
}
```

---

## 健康检查

```bash
curl -I http://localhost:8080/health
curl --http2-prior-knowledge -I http://localhost:8080/health
```

---

## 下一步

- [配置参考](Configuration.md)
- [运维与排障](Operations.md)
- [管理后台](Admin-Guide.md)
