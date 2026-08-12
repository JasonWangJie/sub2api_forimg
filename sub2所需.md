## 吧文件传到另外一个服务器
# 传文件方法
rsync -avzP /www/backup/database/sub2api_2026-07-21_23-31-37_pgsql_data.sql.gz root@170.178.174.119:/www/backup/database/
# 迁移数据库文件
rsync -avzP /www/backup/database/pgsql/sub2api/sub2api_2026-08-07_16-07-54_pgsql_data.sql.gz root@64.32.27.60:/www/backup/database/

# 还需要把config.yaml 迁移

# sub2生成的图片广场迁移：
rsync -avzP /opt/sub2api/data/image_durable root@64.32.27.60:/opt/sub2api/data

# sub2监控数据迁移：
rsync -avzP /www/sub2-api-monitoring/cursor版/data root@64.32.27.60:/www/sub2-api-monitoring/cursor版/


curl -sSL https://raw.githubusercontent.com/JasonWangJie/sub2api_forimg/main/deploy/install.sh | sudo bash
# 1. 启动服务
sudo systemctl start sub2api
sudo systemctl stop sub2api

# 2. 设置开机自启
sudo systemctl enable sub2api

# 3. 在浏览器中打开设置向导
# http://你的服务器IP:8080


# 查看状态
sudo systemctl status sub2api

# 查看日志
sudo journalctl -u sub2api -f

# 重启服务
sudo systemctl restart sub2api

# 卸载
curl -sSL https://raw.githubusercontent.com/JasonWangJie/sub2api_forimg/main/deploy/install.sh | sudo bash -s -- uninstall -y



## PGSQL 相关
#### 1. 安装 postgresql 全套（服务端 + 客户端）

```
apt update
apt install postgresql postgresql-client -y
```

安装完成后，`psql`命令就会被注册到系统环境变量。

#### 2. 验证服务运行状态

```
systemctl status postgresql
```

若未启动，执行启动、开机自启：

```
systemctl start postgresql
systemctl enable postgresql
```

#### 3. 本机登录 PostgreSQL（两种常用方式）

##### 方式 1：系统用户免密登录（推荐）

```
sudo -u postgres psql
```

成功后会进入`postgres=#`数据库交互终端。



## 将用户key 移动到其他分组

1. 先查清楚
-- 看分组
SELECT id, name, status, platform, deleted_at
FROM groups
WHERE deleted_at IS NULL
ORDER BY id;
-- 看某用户的 Key 及当前分组
SELECT k.id, k.name, k.key, k.user_id, k.group_id, g.name AS group_name, k.status
FROM api_keys k
LEFT JOIN groups g ON g.id = k.group_id
WHERE k.deleted_at IS NULL
  AND k.user_id IN (123, 456)          -- 改成你的用户 ID
ORDER BY k.user_id, k.id;
记下：源分组 ID、目标分组 ID、要动的 api_keys.id。

2. 移动 Key 到新分组
BEGIN;
-- 例：把指定 Key 从分组 10 挪到分组 20
UPDATE api_keys
SET group_id = 20,          -- 目标分组 ID
    updated_at = NOW()
WHERE deleted_at IS NULL
  AND group_id = 10         -- 源分组（可去掉，若按 Key ID 精确改）
  AND id IN (101, 102, 103) -- 要迁移的 Key ID
RETURNING id, user_id, name, group_id;
COMMIT;
按用户批量挪（该用户在源分组下的全部 Key）：

BEGIN;
UPDATE api_keys
SET group_id = 20,
    updated_at = NOW()
WHERE deleted_at IS NULL
  AND group_id = 10
  AND user_id IN (123, 456)
RETURNING id, user_id, group_id;
COMMIT;
3. 若目标是专属/受限分组，还要给用户开权限
-- 允许这些用户使用目标分组 20
INSERT INTO user_allowed_groups (user_id, group_id)
SELECT u.uid, 20
FROM (VALUES (123), (456)) AS u(uid)
ON CONFLICT DO NOTHING;
（若你们表还有 created_at 等非空列，按实际表结构补上。）

4. 迁移前建议确认

-- 目标分组是否有上游账号
SELECT ag.account_id, a.name, a.platform, a.status
FROM account_groups ag
JOIN accounts a ON a.id = ag.account_id
WHERE ag.group_id = 20
  AND a.deleted_at IS NULL;
-- 源/目标平台是否一致（OpenAI ↔ Gemini 不要硬挪）
SELECT id, name, platform
FROM groups
WHERE id IN (10, 20);
