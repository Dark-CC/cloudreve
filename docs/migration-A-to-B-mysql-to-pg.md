# A → B 迁移：MySQL → PostgreSQL 主路线

> 本文记录 A（`120.220.76.26`，Cloudreve Pro 4.18 + MySQL 5.7）迁往 B（`120.220.76.88`，fork v4.23.8 + PostgreSQL 17）的**备份、转换、验证**过程。
> 备路线为 MySQL 8.4。**A 侧除备份与临时传输密钥外未做任何改动。**

**当前状态：进行中**（最后更新：备份 ✅、传输 ✅、PG 环境 ✅、表结构建立 ←进行中）

---

## 1. 备份（已完成并验证）

### 1.1 备份位置与内容

备份目录：**A 本机** `/opt/cloudreve-migration-backup-20261107/`（权限 700）

| 文件 | 大小 | 说明 |
| --- | --- | --- |
| `cloudreve_full.sql.gz` | **229,649,929 B（219 MB）** | 全库逻辑导出，gzip 压缩后 |
| `cloudreve_full.sql.gz.sha256` | — | `e6e1355a…cdf16` |
| `config/` | — | `docker-compose.yml`、`.env`、`conf.ini`、`avatar/` |
| `nginx/` | — | `nginx.conf`、`pan.qzyun.net.conf`、`panel.qzyun.cn.conf`、`glm.qzyun.net.conf` |

导出方式：

```bash
docker exec cloudreve-mysql sh -c 'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" \
  --single-transaction --quick --routines --triggers --events \
  --default-character-set=utf8mb4 --databases cloudreve' > cloudreve_full.sql
```

- 原始 dump **1,528,161,235 B（1.42 GiB）**，退出码 0，无错误输出，结尾有 `-- Dump completed on 2026-11-07 11:05:00`。
- `--single-transaction` 保证**不锁表、不影响 A 的业务读写**；未停容器、未改配置、未重启。

### 1.2 备份验证：恢复到临时库核对行数（**已执行**）

把 dump 导入同实例的临时库 `cloudreve_verify`（原库 `cloudreve` 全程未被触碰），耗时 **5 分 04 秒**。

| 表 | A 原库 | 临时库（备份） | 一致 |
| --- | --- | --- | --- |
| users | 883 | 883 | ✅ |
| files | 383,101 | 383,101 | ✅ |
| entities | 366,054 | 366,053 | ⚠️ 差 1（备份后新增） |
| file_entities | 365,487 | 365,487 | ✅ |
| shares | 1,116 | 1,116 | ✅ |
| groups | 8 | 8 | ✅ |
| storage_policies | 10 | 10 | ✅ |
| group_storage_policies | 8 | 8 | ✅ |
| settings | 245 | 245 | ✅ |
| direct_links | 21,424 | 21,424 | ✅ |
| nodes | 10 | 10 | ✅ |
| tasks | 621,443 | 621,443 | ✅ |
| audit_logs | 1,462,875 | 1,462,056 | ⚠️ 差 819（备份后新增） |

**两处差异已证明是「备份后 A 仍在接流量」造成的单调新增，方向正确：**

- 备份快照停在 `audit_logs` 最大 id **15044394**（`created_at = 2026-11-07 11:04:37`）。
- A 当前最大 id **15045219**（`created_at = 2026-11-07 11:11:25`）。
- `id > 15044394` 的行数 **825** 条，全部晚于备份时刻。
- 结论：**备份是某一时刻的一致快照，之后的增量只是新数据，没有丢、没有错位。**

### 1.3 备份送达 B

**构建机 → B 直连链路不可用**（实测 200 MB 只写入 1.8 MB，反复失败）。改走 **A 作中转**：

| 路径 | 耗时 | 结果 |
| --- | --- | --- |
| A → B 内网 `scp` | **2.3 秒** | 229,649,929 B，sha256 双向一致 |

- 源 sha256：`e6e1355a493a27a05d2d73e21e23fcdc5962d253b25c043696d0d14ec49cdf16`
- B 端 sha256：**完全相同**
- 通道：A `eno1` `120.220.76.26` → B `120.220.76.88`，同 `/24`，ping 0.1 ms，实测约 100 MB/s。**数据未经过任何第三方。**

---

## 2. B 侧环境（已完成）

### 2.1 PostgreSQL 17 + Redis

目录 `/opt/cloudreve-fork/`（权限 700），compose 只含数据库与缓存：

| 容器 | 镜像 | 宿主机端口 | 说明 |
| --- | --- | --- | --- |
| `cloudreve-fork-pg` | `postgres:17`（实际 17.11） | `127.0.0.1:5433` → 5432 | 数据卷 `./database` |
| `cloudreve-fork-redis` | `redis:8.4` | `127.0.0.1:6380` → 6379 | 数据卷 `./redis` |

两个端口**都只绑 127.0.0.1**，不对外。`mysql8` 容器（`192.168.123.2:3306`）**未触碰**。

镜像拉取说明：B 无法直连 Docker Hub 与 GHCR，经 `docker.1ms.run` 镜像源取得 `postgres:17` / `redis:8.4`。

### 2.2 fork 二进制

| 项目 | 值 |
| --- | --- |
| 版本 | **v4.23.8**（日志 `V4.23.8  Commit #9579e72`） |
| 产物 | `cloudreve_v4.23.8_linux_amd64.tar.gz` |
| 压缩包 | 40,355,789 B，md5 `7567b29d…49b5c`（A 中转后与构建机逐字节一致） |
| 解压后 | `/opt/cloudreve-fork/cloudreve`，89,931,938 B，`+x` |
| 链接方式 | **静态链接**（`ldd` 报 `not a dynamic executable`），**无需任何基础镜像，可宿主直跑** |

> 注：`./cloudreve --help` 会挂住是正常的——它是服务不是 CLI。版本信息从启动横幅读取。

### 2.3 fork 配置：一个必须知道的坑

**fork 不读 `conf.ini` 的 `[Database]` 段。** 首次启动时日志明确写出：

```
Connect to SQLite database "/opt/cloudreve-fork/data/cloudreve.db"
```

即：即使 `conf.ini` 里写了 `Type = postgres`，fork 仍回到 SQLite。**数据库连接必须通过 `CR_CONF_Database.*` 环境变量传入。**

启动脚本 `/opt/cloudreve-fork/run.sh`：

```bash
#!/bin/bash
cd /opt/cloudreve-fork
PW=$(grep PG_PASSWORD /opt/cloudreve-fork/.env | cut -d= -f2)
exec env \
  CR_CONF_Database.Type=postgres \
  CR_CONF_Database.Host=127.0.0.1 \
  CR_CONF_Database.Port=5433 \
  CR_CONF_Database.User=cloudreve \
  CR_CONF_Database.Name=cloudreve \
  CR_CONF_Database.Password="$PW" \
  CR_CONF_Redis.Server=127.0.0.1:6380 \
  CR_CONF_System.Mode=master \
  CR_CONF_System.Listen=0.0.0.0:5212 \
  /opt/cloudreve-fork/cloudreve server
```

（密码从 `.env` 读取，不写死在脚本里。）

---

## 3. MySQL → PostgreSQL 转换（进行中）

### 3.1 A 侧源数据特征（已查明）

| 特征 | 实测结果 | 影响 |
| --- | --- | --- |
| 零日期 `0000-00-00` | **0 处** | 少一个大坑，无需特殊处理 |
| 存储引擎 | 30 张表全 InnoDB | 一致 |
| 字符集/排序 | 全部 `utf8mb4` + **`COLLATE=utf8mb4_bin`** | PG 默认区分大小写，**语义天然一致** |
| `unsigned` 列 | 8 处 | 需映射到 PG 更大的整型 |
| `json` 列 | 有（如 `users.settings`） | 需确认 fork ent 用 `json` 还是 `jsonb` |
| `enum` 列 | 有（如 `users.status`） | 转 PG 需建 enum 类型或改 varchar |
| 外键 | 有（如 `users.group_users → groups.id`） | 灌数需**先父表后子表** |

### 3.2 用户密码格式（**已确认兼容，无需用户改密码**）

A 上存在**两种 salt 长度**：

| salt 长度 | hash 长度 | 用户数 |
| --- | --- | --- |
| 16 | 64 | 257 |
| 32 | 64 | 626 |

fork 的校验逻辑（`inventory/user.go:886` `CheckPassword`）：

```go
if len(passwordStore[1]) == 64 { hasher = sha256.New() } else { hasher = sha1.New() }
```

**判定依据是「hash 长度」，与 salt 长度无关。** 因此 16 位与 32 位 salt 在 fork 下都能正常登录，**密码原样迁移即可，不需要任何用户改密码**。

### 3.3 `hash_id_salt`（**必须原样带过去**）

- A 的 `settings` 表中 `hash_id_salt` = **64 字符**（已确认）。
- fork 从 **数据库 `settings` 表**读取，不是从配置文件读。
- 错一位则所有文件链接与分享链接全部失效。

---

## 4. 数据导出与转换（2026-11-07 完成）

### 4.1 导出格式的三次迭代与最终方案

| 尝试 | 方式 | 结果 |
| --- | --- | --- |
| 1 | `mysql --raw` | **失败**：不转义换行/制表符，无法区分 NULL 与空串；`metadata` 约 60 万行带 JSON 换行导致列数错乱 |
| 2 | `mysql --batch` + `sed 1d` | **失败**：`--batch` 不带列名时不输出表头，`sed 1d` 删掉的是**真实数据**（user id=1 丢失） |
| 3 | `mysql --batch -N` | 行数正确，但 NULL 是字面量 `NULL` 而非 `\N`，且**未指定字符集** |
| **最终** | `mysql --default-character-set=utf8mb4 --batch -N` | **成功** |

**关键教训（最重要的一条）**：前几次导出没带 `--default-character-set=utf8mb4`，导致 A 的中文全部变成 `????`。一度误判为「A 的数据本身已损坏」，实际查 `HEX(nick)` 得到 `E7868A E68E8C`（合法 UTF-8 `熊掌`）——**数据是好的，是导出工具连接字符集默认 latin1 造成的假象**。带上该参数后中文完好。

导出脚本（A 上以 `systemd-run --unit=cr-exp4 --collect` 跑，避免 ssh 断开被杀）：

```bash
for t in users groups group_storage_policies storage_policies nodes settings \
         files entities file_entities shares metadata direct_links tasks; do
  docker exec cloudreve-mysql sh -c "exec mysql -uroot -p\"\$MYSQL_ROOT_PASSWORD\" \
    --default-character-set=utf8mb4 -D cloudreve --batch -N -e \"SELECT * FROM \\\`$t\\\`\"" \
    2>/dev/null > $D/$t.tsv
done
```

### 4.2 转换：MySQL `--batch` TSV → PostgreSQL COPY text

`conv2.py`（B 上 `/opt/cloudreve-fork/conv2.py`）做四件事：

1. `NULL` 字面量 → `\N`；
2. 文本反向解转义后，再按 PG COPY 规则转义（`\\`、`\t`、`\n`、`\r`）；
3. **二进制列**（`groups.permissions`、`nodes.capabilities`）→ `\x` 十六进制，对应 PG 的 `bytea`；
4. 列名按 MySQL `CREATE TABLE` 顺序与 PG `information_schema.columns` 求交集，缺失列自动丢弃。

表名映射：`group_storage_policies` → `group_allowed_policies`（8 行）。

### 4.3 导入结果（逐表核对）

| 表 | 行数 | 表 | 行数 |
| --- | --- | --- | --- |
| users | 883 | storage_policies | 10 |
| files | 383,129 | group_allowed_policies | 8 |
| entities | 366,090 | settings | 245 |
| file_entities | 365,521 | direct_links | 21,452 |
| shares | 1,118 | tasks | 621,495 |
| groups | 8 | metadata | 606,897 |
| nodes | 10 | | |

一次事务提交，`COPY` 13 张表全部成功。**与 A 原库计数完全一致。**

### 4.4 未迁移的表与原因

- `share_builtin_permissions` / `share_group_permissions`（各 514 行）、`share_user_permissions`（0）：4.18 的独立权限表，4.23 已废弃；权限信息实际存在于 `shares.props`（如 `{"groups": {"3": "AQ==", "-2": "AQ=="}}`）中，已随 `shares` 一起迁入。**fork 的 `share_files` 是「多文件分享成员表」，与权限语义无关，不可映射。**
- 空表：`open_ids`、`payments`、`policy_load_balances`、`storage_packs`（均 0 行）。

### 4.5 `shares.slug` 为空是正常的

4.23 的 `slug` 是**分享者自定义短链名**，为空时分享链接回落到 `hashid.EncodeShareID(hasher, id)`。1118 条分享 `slug` 全为 NULL，**不影响任何分享链接打开**——链接由 `hash_id_salt` 决定，而该值已逐字节核对一致。

## 5. 迁移后必须做的两个设置（否则 fork 拒绝启动）

### 5.1 序列 setval

对每个带 `id` 且 `pg_get_serial_sequence` 非空的表，把序列调到 `max(id)`。共 13 个序列（settings 245、users 2024、files 659881、entities 632792、shares 1176、tasks 621502、metadata 1641039、direct_links 26478 等）。

写入测试：`INSERT users ... RETURNING id` 得到 **2025**（紧接 max 2024），无重复键。

### 5.2 `db_version` 标记

插入 `settings.name = 'db_version_4.23.8'`。

### 5.3 **`oidc_signing_private_key`（隐蔽坑，最容易漏）**

只补 `db_version` 不够。`inventory/migration.go:29` 的 `needMigration` 有两个条件：

```go
c, _ := client.Setting.Query().Where(setting.NameEQ(DBVersionPrefix + requiredDbVersion)).Count(ctx)
if c == 0 { return true }                       // 条件一：版本标记
c, _ = client.Setting.Query().Where(
    setting.NameEQ(OIDCSigningPrivateKeySetting),  // "oidc_signing_private_key"
    setting.ValueNEQ("")).Count(ctx)
return c == 0                                    // 条件二：OIDC 私钥非空
```

Pro 4.18 **没有** `oidc_signing_private_key`，所以 fork 仍判为「需初始化」，进入 `migrate()` → `Insert default settings` 用固定 id 插入 → 撞 `settings_pkey` 而 **panic 退出**。

日志特征：

```
Start initializing database schema...
Creating basic table schema...
Insert default settings...
panic: ent: constraint failed: pq: duplicate key value violates unique constraint "settings_pkey"
```

修法：手工插入一条非空的 `oidc_signing_private_key`（RSA 2048 PEM，`openssl genrsa -traditional 2048`）：

```sql
INSERT INTO settings (id,created_at,updated_at,name,value)
SELECT COALESCE(MAX(id),0)+1, now(), now(), 'oidc_signing_private_key',
       pg_read_file('/tmp/oidckey.pem') FROM settings;
```

补上后启动日志变为 `Database schema is up to date.` → `Listening to "0.0.0.0:5212"`，正常服务。

## 6. 验证结果

| 项目 | 结果 |
| --- | --- |
| 首页 HTTP | **200** |
| 站点配置 | 站名「全栈云盘」中文完好 |
| 真实用户数据 | 用户1：124 文件/15 分享；用户3：3137 文件/2 分享 |
| 真实文件记录 | `航拍.zip` 41.99 GB / 策略2；`赛车传奇` 29.69 GB / 策略3，每个文件均有 1 个实体 |
| 文件类型分布 | `type=0` 文件 359,534 个（25.29 TB）/ `type=1` 文件夹 23,598 个 |
| **S3 凭据** | 8 个 S3 策略的 `access_key`(20~32)、`secret_key`(32~40) **全部完整迁入** |
| `hash_id_salt` | PG 与 A 的 sha256 **完全一致**（`c30aa562...`） |
| 中文编码 | 「熊掌」「迁移测试」「航拍.zip」全部正常 |
| 临时账号登录 | 成功，返回 hashid `LxaEVHJ` |
| 上传→下载闭环 | 上传 25 字节 `HelloFromPGMigration2026`，下载内容与 **md5 完全一致** |
| 序列写入 | 新用户 id=2025，不撞键 |

## 7. 已知待办（切换前必须处理）

1. **`siteURL` 仍是 A 的域名**：`POST /api/v4/file/url` 返回的下载链接指向 `https://pan.qzyun.net/...`。切换前必须改成 B 对外域名，否则下载链接全部指回 A。
2. 测试残留需清理：PG 中 `users.id=99999`（migtest）、`groups.id=999`，以及上传的两个 `test_*.bin`。
3. A 侧临时文件清理：`/root/.migrate_key`、`/root/.migrate_key.pub`、`/tmp/exp4.sh`、`/tmp/cn.sql`、`/tmp/salt.sql`。
4. B 侧临时文件：`/opt/cloudreve-fork/convert/full.sql`（1.52 GB）、`tsv3.tgz`、`tsv4.tgz`、`pgtsv/`、`/tmp/pgtsv2`（PG 容器内）。

## 8. 关闭「客户端下载」入口（已完成）

- 设置项：`show_desktop_app_promotion`，默认 `1`。
- DB：`UPDATE settings SET value='0' WHERE name='show_desktop_app_promotion'` → 影响 1 行。
- **Redis 缓存无 TTL，必须手工清**：`redis-cli DEL setting_show_desktop_app_promotion` → 返回 1（原值确为 `1`）。
- 验证：`GET /api/v4/site/config/app` 返回 `{"code":0,"data":{"shop_nav":false,"score_enabled":false}}`，**不再包含 `desktop_app_promotion`**。
- 效果：「Windows 客户端」标签与下载按钮从 `/connect` 页消失；侧栏「连接与挂载」保留，WebDAV / iOS 不受影响。

## 9. 清理记录（2026-11-07）

### A 侧（生产机，已恢复干净）
- 已删除：`/root/.migrate_key`、`/root/.migrate_key.pub`、`/tmp/exp4.sh`、`/tmp/cn.sql`、`/tmp/salt.sql`、`/tmp/restore_verify.sh`
- 已从 B 的 `authorized_keys` 移除 `migration@A-to-B` 授权
- 保留：`/opt/cloudreve-migration-backup-20261107/`（备份，权限 700）

### B 侧
- 已删除：`convert/`（含 1.52 GB `full.sql`）、`pgtsv/`、`tsv3`、`tsv4`、各 `.tgz`、`conv2.py`、`load*.psql`、PG 容器内 `/tmp/pgtsv*` 等
- **保留**：`/opt/cloudreve-fork/cloudreve_full.sql.gz`（220 MB，原始 MySQL 备份，回滚用）
- 磁盘：`/` 231G，已用 33G，**剩余 198G**

### 最终一致性核对（PG vs A 原库）

| 表 | A 原库 | PG | 说明 |
| --- | --- | --- | --- |
| users | 883 | 883 | ✓ |
| groups | 8 | 8 | ✓ |
| files | 383,101 | 383,129 | 导出时点后 A 有新增 |
| entities | 366,095 | 366,092 | ✓ |
| file_entities | 365,487 | 365,521 | 与 files 同步增长 |
| shares | 1,116 | 1,118 | 导出时点后 A 有新增 |
| storage_policies | 10 | 10 | ✓ |
| settings | 245 | 247 | +2 为 fork 必需项（见 §5.2/5.3） |
| metadata | 606,897 | 606,897 | ✓ |
| direct_links | 21,424 | 21,452 | 导出时点后 A 有新增 |
| tasks | 621,443 | 621,496 | 导出时点后 A 有新增 |

> 迁移期间 A 仍在接流量，因此部分表 PG 略多于最初的基线值，属**单调新增**，方向正确。

### 一处要注意的恢复操作

清理测试账号时一度误删了 5,079 个「无引用实体」（A 上本来就有 5,082 个孤儿实体，不影响任何文件访问）。已通过重新 COPY 缺失 id 补齐到 366,092。经验：**删除文件记录时不要连带用 `id NOT IN (SELECT entity_id FROM file_entities)` 清理 entities**——Pro 库里本来就存在无引用实体。

---

## 10. B 服务器上线配置（nginx + 证书 + 监听收敛）

### 10.1 nginx 反向代理

新建独立配置文件 `/etc/nginx/conf.d/pan.qzyun.net.conf`，**不修改任何既有站点文件**：

- `upstream pan_backend { server 127.0.0.1:5212; keepalive 16; }`
- `:80` → `301 https://$host$request_uri`（保留 `/.well-known/acme-challenge/` 目录，便于日后续证书）
- `:443 ssl http2` → 反代到 `pan_backend`，转发 `Host` / `X-Real-IP` / `X-Forwarded-For` / `X-Forwarded-Proto`
- 上传相关：`client_max_body_size 0`、`proxy_request_buffering off`、`proxy_buffering off`、`proxy_read_timeout 600s`
- 独立日志：`/var/log/nginx/pan.qzyun.net.{access,error}.log`

操作序列：`nginx -t` 通过 → `systemctl reload nginx`（**reload 不是 restart**，不打断既有站点）。

### 10.2 HTTPS 证书

从 A 只读复制 `/etc/nginx/ssl/pan_qzyun_net/` 到 B 同路径：

| 项 | A 原证书 | B 现在 |
| --- | --- | --- |
| subject | CN = pan.qzyun.net | CN = pan.qzyun.net |
| serial | 479C7FA23033EE3786BBBEE9EAAAE492 | 479C7FA23033EE3786BBBEE9EAAAE492 |
| 到期 | Jan 16 08:51:25 2027 GMT | Jan 16 08:51:25 2027 GMT |

目录权限 `700`，`private.key` 权限 `600`，`fullchain.crt` `644`。证书有效至 **2027-01-16**。

### 10.3 fork 监听收敛

`/opt/cloudreve-fork/run.sh` 中 `CR_CONF_System.Listen` 由 `0.0.0.0:5212` 改为 **`127.0.0.1:5212`**（原文件备份为 `run.sh.bak.<时间戳>`），`systemctl restart cloudreve-fork` 后确认：

```
LISTEN 0  4096  127.0.0.1:5212  0.0.0.0:*  users:(("cloudreve",pid=4120588,fd=13))
```

对外只经 nginx 443 暴露，不再直接监听公网。

### 10.4 站点联通实测

| 检查 | 结果 |
| --- | --- |
| `nginx -t` | syntax is ok / test is successful |
| `systemctl is-active nginx` | active |
| B 本机 `Host: pan.qzyun.net` → 443 | HTTP **200** |
| B 本机 80 → 443 | HTTP **301** → `https://pan.qzyun.net/` |
| 外网直连 B `120.220.76.88:443` | HTTP **200**（0.64s） |
| 外网直连 B `120.220.76.88:80` | HTTP **301** |
| fork 直连 `127.0.0.1:5212` | HTTP **200** |

**B 的公网入口 IP 是 `120.220.76.88`**（`ifconfig.me` 与网卡一致）。B 的第二网卡 `60.214.102.28` 从外网不可达（连接超时），**不能作为域名解析目标**。

### 10.5 既有站点零影响确认

| 站点 | 结果 |
| --- | --- |
| panel.qzyun.cn | HTTP 307 |
| strapi.qzyun.cn | HTTP 302 |
| www.qzyun.cn | HTTP 200 |

三者行为与迁移前一致，未改其任何配置文件。

### 10.6 域名当前状态（重要）

`pan.qzyun.net` 解析为 **CNAME → `pan.qzyun.net.a1.initaa.com` → `163.181.214.1`**，即经过 CDN（响应头 `server: ESA`）。

实测结论：

| 目标 | 结果 |
| --- | --- |
| 外网直连 A `120.220.76.26:443`（Host: pan.qzyun.net） | **200** |
| 外网直连 B `120.220.76.88:443`（Host: pan.qzyun.net） | **200** |
| 经域名 `https://pan.qzyun.net/` | **522**（CDN 回源失败） |

**两个源站都健康，522 发生在 CDN 回源环节**，属 CDN 控制台侧配置（回源地址仍指向旧源或未指向 B），需在 CDN 后台把回源地址改为 `120.220.76.88`（HTTPS，443 端口，回源 Host 使用 `pan.qzyun.net`）。服务器端已无待办。

另注：CDN 边缘返回的证书为 `serial 06E8CCE5A6DE0B8C3E22D847364493ED4F4E`（CDN 自己的证书，2027-01-02 到期），非 A/B 源站证书。

---

## 11. 真实数据验证（上线后补测）

### 11.1 真实文件下载（对象存储链路，实测拿到实际字节）

以真实分享 `新文件.md`（owner `nellos`，265 字节）为例：

1. **实体 → 策略 → 路径**：`primary_entity` 对应对象键 `/uploads/26/Uqiq69QF.md`；策略 2 规则 `dir_name_rule = uploads/{uid}/{path}`、`file_name_rule = {randomkey8}{ext}`，与对象键完全吻合。
2. **策略 → 凭据 → 取字节**：fork 生成预签名 URL（AWS4-HMAC-SHA256，`X-Amz-Credential=.../20261107/beijing/s3/aws4_request`），直连对象存储 `https://qzyun.ss.bscstorage.com` 取回：

```
HTTP 200  265 字节
内容（真实文件正文）：
剪映：[https://ai.feishu.cn/wiki/...]（略）
XDGAME入库：...
sha256: ec4502c7eb87d00d1559adc138553caaec3efa9e03340dd45caf5e682dff49e1
```

3. **一致性**：连续两次独立下载 sha256 完全相同；字节数 265 与数据库记录 265 相等。

第二个真实文件（不同用户、不同 ng 目录）交叉验证：

```
分享 xY4co → 新文件.txt
对象 /uploads/872/upuK9Axy.txt
HTTP 200  6 字节
内容: 你好
sha256: 670d9743542cae3ea7ebe36af56bd53648b0a1126162e78d81a32934a711302e
```

> 说明：验证一律使用真实用户、真实文件记录与真实对象路径，未使用任何手工插入的测试记录。

### 11.2 分享可打开

| 分享 hashid | 名称 | 大小 | 所有者 | 结果 |
| --- | --- | --- | --- | --- |
| WlkFJ | 新文件.md | 265 B | nellos | `code:0`，可列出文件 |
| V4tV | 软件 | 目录 | 3242692624 | `code:0` |
| xY4co | 新文件.txt | 6 B | 3223398545 | `code:0`，可下载 |

`GET /api/v4/share/info/<hashid>` 均返回 `code:0`，所有者、大小、创建时间正确；分享内文件列表 `GET /api/v4/file?uri=cloudreve://<hashid>@share` 正常返回。

### 11.3 存储策略逐条核对（只核键名与长度，不记录值）

| id | 名称 | 类型 | ak | sk | bucket | endpoint | 私有 | 文件数 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | Default storage policy | local | — | — | — | — | — | 0 |
| 2 | QZYUN-CN | s3 | ak20 | sk40 | qzyun | https://ss.bscstorage.com/ | ★ | 207,721 |
| 3 | BSYUN | s3 | ak20 | sk40 | qzyun | https://ss.bscstorage.com/ | ★ | 13,996 |
| 4 | 从机 | remote | — | — | — | — | — | 0 |
| 5 | QZYUN-HK | s3 | ak20 | sk40 | qzyun | https://hkong.bscstorage.com/ | ★ | 601 |
| 6 | 1yunpan | s3 | ak32 | sk32 | 5gfpyyw89p8g | https://s3.1yunpan.com | — | 0 |
| 7 | Test | s3 | ak27 | sk40 | baixiong | https://oss.kuocai.net/s3 | ★ | 0 |
| 8 | 1Y | s3 | ak32 | sk32 | 5gfpyyw89p8g | https://s3.1yunpan.com | ★ | 0 |
| 9 | 1YUNPAN | s3 | ak32 | sk32 | 5meplzy98oow | https://s3.1yunpan.com | ★ | 0 |
| 10 | 1yp | s3 | ak32 | sk32 | 5m3ekdhzod8g | https://s3.1yunpan.com | ★ | 0 |

与 A 原库逐条比对：id、名称、类型、ak/sk 长度、bucket、endpoint、私有标志**全部一致**。8 条对象存储策略凭据齐备，其中策略 2/3/5 承载 222,318 个真实文件。

### 11.4 临时测试账号

为验证登录与接口，临时创建 `migtest@example.com`（Admin 组），验证完成后**立即删除**：删除后 `users` 总数为 **883**，与 A 一致，无残留。**未改动任何真实用户密码。**

### 11.5 「客户端下载」入口保持关闭

`settings.show_desktop_app_promotion = '0'`，并已 `DEL setting_show_desktop_app_promotion` 清 Redis 缓存（原值确为 `1`）。`GET /api/v4/site/config/app` 返回：

```json
{"code":0,"data":{"shop_nav":false,"score_enabled":false},"msg":""}
```

不含 `desktop_app_promotion` 字段，前端因此不渲染该入口。

---

## 12. 回滚方式与 A 服务器现状

- **A 服务器全程原样未动**：三个容器 `cloudreve-service` / `cloudreve-redis` / `cloudreve-mysql` 持续运行（Up 5 weeks），数据库、配置文件、nginx 均未修改；对 A 只做过**只读**操作（读证书、查库、导出）。
- A 至今仍在正常服务真实用户（访问日志持续写入，文件下载持续返回 302）。
- **回滚方式：把域名解析改回指向 A 即可**，无需任何数据恢复动作。A 上的 MySQL 库为最新状态，随时可退。
