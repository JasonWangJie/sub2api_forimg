# Fork 发行版约定（JasonWangJie/sub2api）

> 本文件是 Fork 身份的人类可读副本。AI 与合并上游时以 `.cursor/rules/fork-release-deploy.mdc` 与 `.cursorrules` 为准。  
> **合并 upstream 时禁止删除或改回本文件；冲突时保留 Fork 侧。**  
> 日常「怎么打 tag / 怎么装」操作步骤见根目录：[发行版发布与安装操作手册.md](../发行版发布与安装操作手册.md)。

## 一键安装 / 升级

```bash
curl -sSL https://raw.githubusercontent.com/JasonWangJie/sub2api/main/deploy/install.sh | sudo bash
curl -sSL https://raw.githubusercontent.com/JasonWangJie/sub2api/main/deploy/upgrade.sh | sudo bash
```

## 身份常量

| 项 | 值 |
|---|---|
| 仓库 | `JasonWangJie/sub2api` |
| 安装目录 | `/opt/sub2api` |
| 配置 | `/opt/sub2api/config.yaml`（systemd `DATA_DIR=/opt/sub2api`） |
| Release 资产 | `sub2api_{version}_linux_{amd64\|arm64}.tar.gz` + `checksums.txt` |
| 版本号 | 三段跟上游（`v0.1.162`）；Fork 中途更新用四段（`v0.1.162.1`） |

## 设计原则

1. **保留**原作者 `deploy/` 结构与 `install.sh` / systemd / GoReleaser 设计，只替换仓库身份与必要增强。
2. 用户环境只允许 `curl | bash`，禁止要求源码构建。
3. tag `v*` 必须走完整 Release；默认禁止 SIMPLE_RELEASE（会跳过二进制，导致 install 失败）。
4. 升级须备份二进制与 `config.yaml`，再替换程序并 restart systemd。

## 上游合并检查清单

合并 `Wei-Shaw/sub2api` 后执行：

```bash
# deploy / CI 中不应再出现原作者仓库（定价源等第三方 URL 除外）
rg "Wei-Shaw/sub2api" deploy .github .goreleaser.yaml .goreleaser.simple.yaml Dockerfile.goreleaser AGENTS.md

# 关键文件必须仍指向本 Fork
rg "JasonWangJie/sub2api" deploy/install.sh deploy/upgrade.sh deploy/sub2api.service
test -f deploy/upgrade.sh
rg "DATA_DIR" deploy/install.sh deploy/sub2api.service
rg "Verify install.sh release assets" .github/workflows/release.yml
```

若检查失败：先恢复 Fork 身份，再提交合并结果。
