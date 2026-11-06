# 生产服务器 155.117.85.143 迁移报告（Pro 4.18 → fork 4.23.8 并行环境）

执行时间：2026-11-07
目标主机：`155.117.85.143`（x86_64，主机名 `elaborate-extreme`）
结论：**新环境已跑起来，数据完整，分享与真实下载链路验证通过。旧站与旧库原样保留，可随时回滚。**

---

## 一、当前状态一览

| 项目 | 旧环境（生产，原样保留） | 新环境（fork 4.23.8） |
|---|---|---|
| 容器 | `cloudreve-service` + `cloudreve-postgresql` + `cloudreve-redis` | `cloudreve-fork` + `cloudreve-fork-postgresql` + `cloudreve-fork-redis` |
| 版本 | 4.18.0-pro | 4.23.8（linux_amd64 发版二进制） |
| 监听 | `127.0.0.1:5212`（已关公网） | `127.0.0.1:5213`（仅回环） |
| 数据库 | PG 17，库 `cloudreve`，4.8 GB | PG 17.10，库 `cloudreve_fork`，**2.36 GB** |
| Redis | `cloudreve-redis` | `cloudreve-fork-redis`（独立实例） |
| 目录 | `/opt/cloudreve/` | `/opt/cloudreve-fork/` |
| 对外 | 无（nginx 现返回维护页） | 无（验收通过后由 nginx 切换） |

**用户访问现状：`pan.cdn.ad` 显示「系统维护中」页面。** 这是有意为之——迁移期间停站，防止用户上传造成数据分叉。

---

## 二、备份（可回滚的基础）

位置：**`/opt/backup-prod-155/20261107/`**，合计 **644 MB**

| 文件 | 大小 | 内容 |
|---|---|---|
| `cloudreve-full.dump` | 511 MB | 生产库全量 `pg_dump`（custom 格式，207 个 TOC 条目、27 张表） |
| `cloudreve-full.dump.sha256` | — | 该 dump 的校验值 |
| `cloudreve-full.toc` | 13 KB | dump 的目录清单（可逐表核对） |
| `cloudreve-config.tar.gz` | 40 MB | `/opt/cloudreve/` 全部配置（compose、`.env`、`conf.ini`） |
| `cloudreve-data.tar.gz` | 7 MB | 本机数据目录（策略为对象存储，本地数据本就很少） |
| `cloudreve_v4.18.0.running` | 92 MB | 正在运行的 Pro 二进制原件 |
| `openlist-backup.tar.gz` | 327 KB | 删除 OpenList 前的完整备份 |
| `nginx.conf.before-maint` | 13 KB | 改维护页之前的 nginx 配置 |
| `docker-compose.yml.before` | 1.2 KB | 改 5212 绑定之前的 compose |
| `docker-ps-before.txt` / `ss-listen-before.txt` | — | 操作前的容器与端口快照 |
| `SHA256SUMS` | — | 上述关键文件的校验清单 |

**备份可读性已实测**：把 dump 恢复到临时库 `verify_restore` 并逐表比对行数，全部一致；核对完成后已删除该临时库。

⚠️ 备份目录里有生产配置，其中的 AWS 凭据等内容**不进入 git，也不写进本报告**。

---

## 三、5212 公网关闭

**改动**：`/opt/cloudreve/docker-compose.yml` 端口映射
```diff
-      - "5212:5212"
+      - "127.0.0.1:5212:5212"
```

**实测结果**
- 端口监听：`LISTEN 127.0.0.1:5212`（仅回环）✓
- 外网访问 `http://155.117.85.143:5212/`：连接失败（`curl` 返回 000）✓
- 内网/本机访问：正常返回 `{"code":0,"data":"4.18.0-pro"}` ✓

nginx 与旧站仍在同一台机，`proxy_pass http://127.0.0.1:5212` 的通路形式保留（当前被维护页取代），切换时无需再动绑定。

---

## 四、OpenList 停止与删除

**删除前的安全判定（三道全过才动手）**

1. **存储策略引用为空**：Cloudreve 策略 #7「Q」指向 `http://155.117.85.143:5244/`，该策略下**实体数为 0**。
2. **无任何用户组可用**：策略 #7 不在任何组的允许策略集合里，即使用户想用也选不到。
3. **OpenList 本身零真实数据**：仅默认管理员账号，`x_storages` 表 0 行，无挂载、无实际文件。

**结论：删除不影响任何用户文件。** 已执行停容器并删除容器，**数据目录保留未删**。

**实测结果**：`openlist` 容器不存在；5244 / 5245 端口无监听 ✓

**保留位置**：原数据目录仍在原处，另有完整备份 `/opt/backup-prod-155/20261107/openlist-backup.tar.gz`（327 KB），需要时可恢复。

---

## 五、新环境部署

```
/opt/cloudreve-fork/
├── docker-compose.yml          # 三容器编排，独立网络 fork-net
├── bin/cloudreve-4.23.8        # 发版二进制（sha256 a4154e37… 已校验）
├── cloudreve/conf.ini          # 运行配置
├── pgdata/                     # 新 PG 数据
└── redis/                      # 新 Redis 数据
```

**架构与生产一致**：Docker 容器 + PostgreSQL + Redis（用户要求）。

**关键配置要点**
- `Mode = master`（slave 模式启动即 panic，不适用）。
- 数据库连接用显式连接串指定库 `cloudreve_fork`。**注意**：仅靠 `CR_CONF_Database.Name` 环境变量时，fork 实际连到了 PG 容器的初始库 `cloudreve`，导致写入跑到空库；改用 `DatabaseURL` 后才真正指向目标库。这是本次最隐蔽的坑，已修正并复验。
- `SessionSecret` 与生产一致（避免用户会话失效）；**HashID salt 取自数据库 `settings.hash_id_salt`（64 位），不是 `conf.ini`**——生产 `conf.ini` 里的 `HashIDSalt = {HashIDSalt}` 是未替换的占位符（生产本身也如此），已随库一起迁移，无需干预。

---

## 六、数据库迁移

**流程**：生产库 dump → 恢复到新 PG 的 `cloudreve_fork` → 排除 `audit_logs` 数据 → 补版本标记 → 重置序列 → 补齐 4.23.8 新增表。

**排除 `audit_logs`**（用户要求）：只迁表结构，不迁数据。生产中该表现有 **5,659,958 行**，新库为 **0 行**，**生产原数据未动**。

### 6.1 两个必须做的步骤

1. **写入 `db_version_4.23.8` 标记**。缺这一步，fork 会把非空库误判为空库并执行裸建表，撞上已存在的 `settings` 表而 panic。
2. **重置所有 identity 序列**到各表 `max(id)`。漏掉的话读数据正常、一写入就报主键冲突。

### 6.2 迁移中发现的三个真实缺口（均已修复）

这三个都是「启动日志一切正常、功能却集体失灵」的隐蔽问题，值得记录：

**① 11 张新增表被静默跳过**
fork 4.23.8 需要 31 张基线表，实际库里只有 28 张，缺 `acl_entries`、`activity_events`、`credit_txns`、`group_allowed_policies`、`group_memberships`、`invitation_codes`、`payment_orders`、`share_files`、`skus`、`sso_bindings`、`user_grants`。
fork 启动时打印 `Database schema is up to date`，但**一张表都没建**（判定逻辑认为无需迁移）。结果是登录、关联查询全部失败。
**修复**：编译一个 Go 小工具直接调用 fork 自己的 `migrate.NewSchema(drv).Create(...)`，让 schema 与 ent 定义严格一致。修复后共 **38 张表**，数据无损。

**② 组与存储策略的绑定关系换了存储结构**
生产用 `group_storage_policies` 关联表（6 行），fork 改用 `group_allowed_policies`（6 行）+ `groups.storage_policy_id` 默认值。
已迁移 6 条绑定，并把各组默认策略设为组内可用的最小策略 id：Admin / User → 2，1YUNPAN → 12，User-US → 13，Anonymous 留空。

**③ `share_files` 表不能手工填**
一开始把 270 条分享↔文件关联写进 `share_files`，结果所有分享被判定为「多文件分享」，生成合成目录，访问报错。**回滚为空后恢复正常**——4.23.8 中单文件分享走 `shares.file_shares` 列，`share_files` 只用于多文件分享。

### 6.3 迁移后的数据核对

| 指标 | 生产 | fork | 一致 |
|---|---|---|---|
| 用户 | 630 | 630 | ✓ |
| 文件 | 727,997 | 727,997 | ✓ |
| 存储实体 | 1,094,499 | 1,094,499 | ✓ |
| 分享 | 349 | 349 | ✓ |
| 用户组 | 5 | 5 | ✓ |
| 存储策略 | 14 | 14 | ✓ |
| 组可用策略绑定 | 6 | 6 | ✓ |
| 审计日志 | 5,659,958 | **0**（按要求清空） | — |
| 数据表数 | 27 | **38** | 新增 |
| 库大小 | 4.8 GB | 2.36 GB | — |

> 系统设置项：生产 237 条，fork 297 条。多出的 60 条是 4.23.8 引入的新配置项（默认值），属正常。

**真实用户抽样**（8 个用户，逐个比对文件数与总字节）：

| 用户 id | 文件数 | 总大小（字节） | 与生产一致 |
|---|---|---|---|
| 16 | 249 | 254,707,992 | ✓ |
| 23 | 19 | 13,225,305,086 | ✓ |
| 111 | 7 | 564,585,842 | ✓ |
| 359 | 53 | 87,393,387 | ✓ |
| 500 | 44 | 20,322,771,437 | ✓ |
| 505 | 29 | 829,886,893 | ✓ |
| 541 | 2,142 | 373,571,662,360 | ✓ |
| 579 | 11 | 33,007,940 | ✓ |
| 362 | 46,884 | 6,983,908,258,053 | ✓ |
| 675 | 124 | 42,247,057,605 | ✓ |

### 6.4 增量同步（维护期内产生的数据）

迁移完成后复查发现，**维护页生效前的几分钟仍有用户上传**（17:04–17:08 UTC），产生了 32 个文件、2 个分享。这些只写在旧库里，若直接切换会丢失。

已做增量同步：按主键水位导出并导入新库，涉及 `files`、`entities`、`file_entities`、`shares` 四张表。同步后逐表核对，**与生产完全相等**。

过程中发现两处**表结构差异**必须显式指定列名，否则导入会失败或错位：
- `entities`：fork 比生产多一列 `hash`（生产 12 列，fork 13 列）。
- `shares`：fork 多出 `price_points`、`listed_publicly`、`slug` 三列。

另外补入了 **700 个孤儿实体**（生产有、fork 无）。经核查这些实体 `reference_count = 0`、无任何文件引用、无 `file_entities` 关联，是上传会话残留，**不影响任何用户文件**；补齐只是为了让两边数据完全一致。

---

## 七、功能验收（全部实测通过）

### 7.1 服务健康
`GET /api/v4/site/ping` → `{"code":0,"data":"4.23.8"}`；首页、队列、缓存均正常。

### 7.2 分享访问
| 分享 | 结果 |
|---|---|
| `bAuQ`（apk 文件，18.9 MB） | 返回真实文件名、大小 18,959,715、上传者昵称、创建时间 ✓ |
| `LEsA`（目录） | 返回目录名、所有者 `746762468` ✓ |
| `3WSe`（flac 音频，45.4 MB） | 返回真实大小 45,473,280 ✓ |
| `GAhm`（音乐目录） | 列出真实子目录「音乐软件…」「音频」✓ |

### 7.3 真实文件下载（端到端）
通过匿名分享 `bAuQ` 取得具体文件的下载直链，返回的是 **S3 预签名地址**（AWS4-HMAC-SHA256，指向对象键 `uploads/87/3iK34YjR.apk`，存储策略 Storage-HK），证明「实体 → 存储策略 → 对象路径」整条链路正确。

实际下载结果：
```
HTTP 200，下载 18,959,715 字节（与数据库记录完全一致）
文件头 504b 0304（"PK.."，合法 APK/ZIP）
速度约 11 MB/s
```

### 7.4 登录链路
用**临时测试账号**（独立注册，未触碰任何真实用户）验证：登录成功、签发 token、`user/me`、文件列表、下载直链生成全部正常。验证后该账号及其文件记录**已全部删除**，序列已校正。

> 说明：早期手工插入的测试账号登录失败，原因是密码 salt 长度写成了 16 位，而 fork 实际生成 **32 位** salt、摘要为 `sha256(密码 + salt)`、存储格式 `salt:hash`。改用正确长度后一次通过——**不是迁移数据的问题**。

### 7.4.1 大批量用户数据
用户 362 有 **46,884 个文件、6.98 TB** 容量，在新环境中文件数与总字节数与生产完全一致；用户 675（124 文件 / 42.2 GB）同样一致。

### 7.5 存储策略配置
14 条策略全部迁移，状态均为 `active`。以 Storage-HK（策略 2）为例，其完整配置（含 S3 凭据、自定义代理等）在生产与 fork 中是**逐字节相同的**（比对配置串的 MD5 一致）。凭据长度亦一致：access key 20 位、secret key 40 位。

---

## 八、回滚操作（每条可直接执行）

### 8.1 从维护页切回旧站（让用户先恢复访问）
```bash
# 宿主机编辑：/opt/nginx/nginx.conf 第 224-228 行附近
#   把 location / 里的
#       root /usr/share/nginx/html;
#       try_files /maintenance.html =503;
#   改回
#       proxy_pass http://127.0.0.1:5212;
#   改完执行：
docker exec nginx nginx -t && docker exec nginx nginx -s reload

# 验证
curl -sk -H "Host: pan.cdn.ad" https://127.0.0.1/ | head -c 200
```
若 `nginx.conf` 已改乱，可直接用备份覆盖：
```bash
cp /opt/backup-prod-155/20261107/nginx.conf.before-maint /opt/nginx/nginx.conf
docker exec nginx nginx -t && docker exec nginx nginx -s reload
```

### 8.2 5212 恢复公网绑定
**一般情况下不需要**——用户是通过 nginx 的 443 访问的，5212 只做本机回环转发，恢复公网绑定反而扩大暴露面。
确需恢复时：
```bash
cp /opt/backup-prod-155/20261107/docker-compose.yml.before /opt/cloudreve/docker-compose.yml
cd /opt/cloudreve && docker compose up -d
ss -lntp | grep 5212      # 应显示 0.0.0.0:5212
```

### 8.3 恢复 OpenList
```bash
mkdir -p /opt/openlist-restore && cd /opt/openlist-restore
tar -xzf /opt/backup-prod-155/20261107/openlist-backup.tar.gz
# 原始 compose 在该压缩包内，按其中定义启动：
docker compose up -d
ss -lntp | grep -E ':5244|:5245'    # 应重新出现监听
```

### 8.4 数据库回滚（极端情况）
新环境完全独立，**旧库从头到尾没被改过**（审计日志、任务、全部数据原样）。所以数据库层面不需要「回滚」，旧容器重新对外即可。
灾难场景才需要从备份重建：
```bash
docker exec -i cloudreve-postgresql psql -U cloudreve -d postgres -c 'CREATE DATABASE cloudreve_restore;'
cat /opt/backup-prod-155/20261107/cloudreve-full.dump | \
  docker exec -i cloudreve-postgresql pg_restore -U cloudreve -d cloudreve_restore --no-owner
```

### 8.5 停止新环境（不影响旧站）
```bash
cd /opt/cloudreve-fork && docker compose down
```

---

## 九、磁盘与清理

- 磁盘余量：**118 GB 可用**（总 193 GB，使用 39%），全程未紧张。
- 已清理：Go 编译临时源码目录（约 261 MB）、验证下载的文件、各步临时 SQL 与脚本、迁移演练库（26 GB，早前已删）。
- 保留：`/opt/backup-prod-155/20261107/`（644 MB，回滚依赖，**请勿删**）、`/opt/cloudreve-fork/`（新环境本体）。
- 未触碰该机其他业务：订阅面板、sing-box、frps、advpn、`kuaichuan.service`、misub、subconverter 全部原样运行。

---

## 十、建议但本轮未动

**HTTPS 证书已全线过期。** `*.cdn.ad`（Cloudreve 实际使用的那张）于 **2026-10-21 到期**，其余几张更早。这意味着用户现在通过浏览器访问时会看到证书告警，即便切到新环境也一样。

本轮用户未要求处理证书，故**未做任何改动**。建议尽快续期，可选项：
- `acme.sh` 已在机器上有镜像（`neilpang/acme.sh`），可用 DNS 方式自动签发并配置续期；
- 或者若 `cdn.ad` 域名已不再续费，则应同步调整 nginx 的 `server_name` 与站点地址配置。

这一项不影响本次迁移的正确性，但**会直接影响用户能否正常访问**，建议作为下一项独立任务处理。

---

## 十一、下一步（需你确认后进行）

1. **切换流量**：nginx `pan.cdn.ad` 从维护页改为 `proxy_pass http://127.0.0.1:5213`，用户即访问新环境。
2. 切换后观察一段时间（建议数小时到一天），确认无异常。
3. 再决定旧容器（`cloudreve-service` / `cloudreve-postgresql` / `cloudreve-redis`）的最终退役，**在此之前旧环境一律不动**。

> 数据一致性：维护期内产生的 32 个文件、2 个分享已做增量同步（见 6.4 节），**当前 fork 与生产逐表相等**，可以直接切换。
>
> 但要注意：**维护页生效后，理论上用户已无法上传**。若切换前发现旧库又有新写入，需再跑一次增量同步，否则这批数据不会出现在新环境。
