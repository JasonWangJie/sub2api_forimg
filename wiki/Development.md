# 本地开发

[← Wiki 首页](Home.md)

更细的环境坑点见仓库根目录 [DEV_GUIDE.md](../DEV_GUIDE.md)。

---

## 环境要求

| 组件 | 版本建议 |
|------|----------|
| Go | 与 `backend/go.mod` 一致（当前为 1.26.x 系） |
| Node.js | 18+ |
| 包管理 | **pnpm**（前端必须） |
| PostgreSQL | 15+ / 16 |
| Redis | 7+ |
| golangci-lint | CI 使用 v2.7 |

---

## 启动

```bash
# 终端 1 — 后端
cd backend
go run ./cmd/server

# 终端 2 — 前端
cd frontend
pnpm install
pnpm run dev
```

首次仍可通过 Setup 向导写本地 `config.yaml`，或复制 `deploy/config.example.yaml` 后自行改库连接。

---

## 代码生成

修改 `backend/ent/schema` 后：

```bash
cd backend
go generate ./ent
go generate ./cmd/server   # Wire
```

---

## 测试与质量

```bash
# 后端
cd backend
go test -tags=unit ./...
go test -tags=integration ./...
golangci-lint run ./...

# 前端
cd frontend
pnpm lint:check
pnpm typecheck
pnpm test:run
```

CI 要点（见 `.github/workflows`）：

- 后端单元 + 集成 + lint  
- 安全扫描 govulncheck / gosec / pnpm audit  
- 前端 `pnpm install --frozen-lockfile` — **必须提交 pnpm-lock.yaml**  

---

## 嵌入前端构建

```bash
cd frontend && pnpm run build
cd ../backend
go build -tags embed -o sub2api ./cmd/server
```

无 `embed` tag 的二进制不含管理界面。

---

## 目录约定（改代码时）

| 改什么 | 优先看哪里 |
|--------|------------|
| 网关路径 | `internal/server/routes/gateway.go` + `handler/*gateway*` |
| 调度/计费 | `internal/service/` |
| 管理 API | `routes/admin.go` + `handler` 管理相关 |
| 支付 | `internal/payment/` + `docs/PAYMENT*.md` |
| 前端页面 | `frontend/src/views/{admin,user,auth}` |
| 配置项 | `internal/config` + `deploy/config.example.yaml` |

---

## 贡献建议

1. 小步 PR，附测试  
2. 不要提交 `.env`、密钥、账号导出  
3. 文档变更可同步更新本 Wiki 或 `docs/`  
4. 遵守 LGPL-3.0  

上游：https://github.com/Wei-Shaw/sub2api
