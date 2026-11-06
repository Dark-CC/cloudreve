# InstaBox PostgreSQL 部署 + Pro 15213 / fork v4.23.8 数据库迁移实操报告

日期：2026-11-06
环境：InstaBox 免费实例 `baixiong`（x86_64 / 4 vCPU / 8 GB / 20 G 持久盘挂 `/data` / Debian 13）

---

## 一、结论先行

| 项目 | 结果 |
| --- | --- |
| PostgreSQL 部署 | ✅ 完成，PostgreSQL 17.11，数据目录 `/data/pgdata` |
| 生产 dump 导入 PG | ✅ 完成，300,504,329 字节，导入零错误 |
| Pro 15213 接 PG 并运行 | ✅ 运行中，`:15213`，版本 `4.18.0-pro`，数据与生产逐表一致 |
| fork v4.23.8 接 PG 并运行 | ✅ 运行中，`:15214`，迁移后数据完整可用 |
| 数据迁移完整性 | ✅ 20 张共有表逐行核对一致（1 张初版漏迁已补） |
| 上传→下载往返 | ✅ 262144 字节，MD5 完全一致 |
| Pro 时期老文件下载 | ✅ 154,715 字节 zip，长度/MD5/ZIP 魔数全部正确 |
| 分享链路 | ✅ 分享信息、列目录、下载 URL 全部正常 |
| 必须手工补的关联 | 4 类（详见 §五） |
| 迁不过去的数据 | 7 张表 + 20 个列（详见 §六） |

**一句话**：迁移可行，Pro 和 fork 现在都跑在 PostgreSQL 上；但 fork 的数据模型比 Pro 新，有 7 张表和 20 个列在 fork 里没有归宿，这部分数据会丢，需要在迁移前决策。

---

## 二、PostgreSQL 部署

- 用 Debian 原生 apt 包安装 PostgreSQL 17.11。
- **数据目录迁到 `/data/pgdata`**（`/data` 是持久盘；`/tmp` 曾被清空导致过写库失败，禁用）。
- 角色 `cloudreve`（应用账号）+ 超级用户 `postgres`（peer auth）。
- 凭据保存在 `/data/pg15214/.pg_creds`（权限 600，仅 root 可读），**未进入 git / 日志 / 本报告**。
- 监听 `127.0.0.1:5432`，不对外暴露。

数据库布局：

| 库 | 用途 | 大小 |
| --- | --- | --- |
| `prod_base` | 生产 dump 的原始导入，**只读基线**，用于任何迁移结果的逐表对照 | 2,652 MB |
| `pro15213` | Pro 15213 使用，由 `prod_base` 克隆 | 2,652 MB |
| `fork_ref` | fork v4.23.8 的迁移目标库 | 2,737 MB |

---

## 三、生产 dump 的处理（问题 1）

### 现象
下载得到的 `cloudreve.dump` 是 **300,506,273 字节**，但用 `pg_restore -l` 打开报格式错误。

### 根因
下载通道返回的是 **HTTP chunked 编码未被解码的产物**：文件里混入了 `0\r\nx-amz-checksum-...` 这类分块尾部和长度标记。

### 解决办法
用 Python 的 chunk 解码器逐段拆开、去掉块头尾、重组为干净字节流：

- 结果 `/data/pg15214/dump/prod.dump`，**300,504,329 字节**（比原始少 1,944 字节，差值正是块标记）
- sha256 `8cc19dbf...af821a3`
- 校验：`pg_restore -l` 识别为 **CUSTOM 格式、PostgreSQL 17.10 导出、196 个 TOC 条目**

### 经验
**判断 dump 是否损坏前，先确认下载链路是不是 chunked。** 现象是"文件大小对得上但格式错"，很容易误判为文件本身坏了而重新下载。

---

## 四、迁移过程中遇到的问题与解决办法

### 问题 2：Pro 4.18 和 fork 4.23.8 都在同一处启动崩溃

**现象**：库里有完整数据，但两个程序启动即 panic：

```
failed to migrate database: Failed creating schema resources:
create "settings" table: pq: relation "settings" already exists
```

**根因**（读源码定位）：

- `inventory/migration.go` 的 `needMigration()` 通过查找特定版本标记（如 `db_version_4.23.8`）判断"是否需要建表"。
- 生产库里**没有** fork 4.23.8 的版本标记（生产最后是 `4.18.0-pro`），于是 `needMigration()` 返回 true。
- 接着 `migrate()` 直接执行裸的 `Schema.Create()` —— 它不管表是否已存在，硬要建表，撞上已有的 `settings` 表就 panic。

**这不是配置问题，是两个版本二进制自身的缺陷**：只要"库里有数据但不是空库、又没有对应版本标记"，就必然崩。

**解决办法**：在 fork 库中写入版本标记 `db_version_4.23.8`，让 `needMigration()` 返回 false，跳过裸建表：

```sql
INSERT INTO settings (name, value, type, ...) VALUES ('db_version_4.23.8', 'installed', 'type', ...);
```

写入后 fork 正常启动。**这条标记是让 fork 能吃 Pro 库的关键，迁移脚本必须包含这一步。**

Pro 15213 之所以能跑起来，是因为 `prod_base` 克隆里本来就带着它自己的 `db_version_4.18.0-pro` 标记。

---

### 问题 3：数据库连接密码带了单引号

**现象**：config 里写 `Password = '<密码>'`（带单引号），程序连不上库。

**根因**：写 config 的脚本把凭据文件里的引号一起写进去了。

**解决办法**：写 config 时对密码做 `.strip("'")` 剥掉引号。fork 和 Pro 两边都踩过这个问题。

---

### 问题 4：`TEMPLATE` 克隆的库，应用账号没有写权限

**现象**：Pro 15213 起来了，但日志刷 `pq: permission denied for table tasks` / `audit_logs`。

**根因**：`CREATE DATABASE ... TEMPLATE prod_base` 复制过来的表，属主是 `postgres`，应用角色 `cloudreve` 只有读权限。

**解决办法**（对新库执行一次）：

```sql
GRANT ALL ON SCHEMA public TO cloudreve;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO cloudreve;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO cloudreve;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO cloudreve;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO cloudreve;
```

授权后日志归零。

---

### 问题 5：序列（sequence）没有推进，所有写操作必然失败

**现象**：读操作全部正常，但一上传就报

```
duplicate key value violates unique constraint "tasks_pkey"
```

**根因**：迁移是**带显式 id** 灌入数据的（`INSERT ... SELECT id, ...`），这种写法**不会推进序列**。`tasks` 表灌了 466,269 行，但 `tasks_id_seq` 还停在低位，下一条插入直接撞 id。

**这是最隐蔽的一类问题**：读起来完全正常，只有等到有人真正写入才炸；而且报错指向的是 `tasks`，不是上传功能本身。

**解决办法**：批量把所有 identity 序列推到当前最大值：

```sql
DO $$
DECLARE r record; maxid bigint;
BEGIN
  FOR r IN
    SELECT c.relname AS tbl, a.attname AS col
    FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public' AND a.attidentity IN ('a','d')
      AND NOT a.attisdropped AND c.relkind = 'r'
  LOOP
    EXECUTE format('SELECT COALESCE(max(%I),1) FROM %I', r.col, r.tbl) INTO maxid;
    EXECUTE format('SELECT setval(pg_get_serial_sequence(%L,%L), %s)', r.tbl, r.col, maxid);
  END LOOP;
END $$;
```

**验证**：`tasks`=466269、`users`=599、`files`=471776 全部到位，之后上传正常。

---

### 问题 6：一张表初版迁移漏掉了（`fs_events`）

**现象**：迁移完成后的逐表核对发现 `fs_events` 在 fork 是 **0 行**，生产是 **874 行**。表结构完全相同，是迁移脚本里漏写了这张表。

**解决办法**：补一段 `INSERT ... SELECT` + 重置该表序列，补完后 874 = 874。

**经验**：**迁移完必须做"全表逐行数对比"，不能只查几张核心表。** 这次是靠 `information_schema` 自动枚举所有表 + 两边行数 join 才发现的，人工列表很容易漏。

---

### 问题 7：磁盘写满导致 PostgreSQL 崩溃恢复

**现象**：一次操作后 `/data` 使用率到 100%，PostgreSQL 直接进入 recovery，服务中断。

**根因**：同一份 2.65 GB 的数据在盘上存了 **4 份副本**（`prod_base` + `pro15213` + `fork15214` + `fork_ref`），另外还有 2 份冗余 dump。

**解决办法**：删掉多余副本（-5.1 GB）和冗余 dump（-600 MB），PG 自行完成崩溃恢复。当前 `/data` 使用率 88%，剩 2.6 GB。

**经验**：20 G 盘上每份库都是 2.6 GB，**只留"原始基线"和"迁移目标"两个库**。做实验前先算空间，不要一边跑一边克隆。

---

### 问题 8：上传接口的路由与调用顺序（前端已处理，脚本直调时才踩）

用脚本直接调 API 时踩到，记录备查：

1. 建上传会话是 **`PUT /api/v4/file/upload`**（不是 POST，POST 会 404）。
2. S3 策略**不走本机分片端点**。`POST /api/v4/file/upload/:sessionId/:index` 只对本机/远端策略有效，对 S3 会报"上传会话已过期"（`code 40006`），极易误判成会话过期。
3. S3 的正确流程是三步：
   - `PUT` 建会话，拿到 `upload_urls[0]`、`completeURL`、`callback_secret`
   - `PUT` 上传内容到预签名地址 → 拿到 ETag → `POST completeURL` 完成分片（**这一步不能省，否则回调报 404 NotFound**）
   - `GET /api/v4/callback/s3/:sessionId/:callbackSecret` 通知完成（`callback_secret` 用响应里返回的，不能用自己编的）
4. 下载地址是 `POST /api/v4/file/url`（body 里 `uris` 数组），不是 `file/source`。

---

### 问题 9：登录接口限速会伪装成"密码错误"

**现象**：连续几次登录后，响应里没有 `data.token`，看起来像密码错误。

**根因**：`/api/v4/session/token` 有 IP 级限速（约 20 次/20 分钟）。超限时返回的 JSON 里没有正常结构。

**解决办法**：等 60 秒左右重试，或重启服务清空计数。**排查登录失败时优先排除限速，不要急着改密码。**

---

## 五、必须手工补的关联（共 4 类）

迁移不能只搬行，下面这 4 类关联在 fork 里换了结构，必须单独构造。

### 5.1 组 ↔ 存储策略（关系表换名 + 结构变化）

- Pro：`group_storage_policies`（组 → 策略的多对多关联表，6 行）
- fork：`group_allowed_policies`（6 行）+ `groups.storage_policy_id` 单列（"该组默认策略"）

**做法**：
1. 把 6 行关联逐条映射到 `group_allowed_policies`（6 条全部落位）。
2. `groups.storage_policy_id` 取该组**允许策略集合里的最小值**作为默认。

**验证**：Admin 组登录后可见 `Storage-HK`（id 2）和 `Storage-US`（id 13），与生产的 6 条关联一致；根目录返回 `storage_policy: Storage-HK, type: s3`。

**已知偏差**：Anonymous 组本来就没有策略，`storage_policy_id` 为 NULL，符合生产状态。"默认策略取最小值"是我的推断规则，**如果生产上不同组有不同的实际默认策略，这里需要按生产配置逐组修正**——这一点建议上线前人工确认一遍。

### 5.2 `users.credit` → `credits`（列改名 + 语义）

- Pro：`users.credit`（单列积分）
- fork：`users.credits`（还有配套的 `credit_txns` 流水表）

**做法**：`COALESCE(credit, 0)` 映射过去。

**验证**：599 个用户全部迁入，积分值一致。**但历史积分流水（`credit_txns`）在 Pro 里没有对应表，这部分历史无法还原**——用户余额对了，但"积分变动明细"是空的。

### 5.3 `share_purchases` 的买家与票据

- Pro：`share_purchases.{payment_id, ticket_id, user_id}`
- fork：`share_purchases.{buyer_id, points, ticket}` + 新表 `share_files`

**做法**：`user_id` → `buyer_id`，`ticket` 用 UUID 补（fork 要求非空），`price` → `points`。

**实际影响**：生产这张表**是空表**（0 行），所以本项在本次迁移中不影响任何数据，但换到有付费分享的库上就是必须在迁移前处理的点。

### 5.4 `shares` 的 slug 与价格

- Pro：`shares.{price, permissions}`
- fork：`shares.{price_points, slug, listed_publicly}`

**做法**：`price` → `price_points`；`slug` 用 `md5(id)` 生成（fork 要求唯一）；`listed_publicly` 补 false。

**验证**：311 条分享全部迁入，`/s/<slug>` 短链 302 正常，分享信息接口正确返回名称、大小（1,183,539,275 字节）、分享者昵称、所属组、来源 URL。

**已知偏差**：`permissions`（分享的细粒度权限）在 fork 没有对应列，**分享的权限配置会退回默认值**。311 条分享里如果有设过"禁止下载""需要密码"之类，这部分设置需要人工重设。

---

## 六、迁不过去的数据

### 6.1 7 张表在 fork 里没有对应结构

| 表 | 生产行数 | 说明 |
| --- | --- | --- |
| `audit_logs` | **2,853,333** | 审计日志，量最大。fork 用 `activity_events` 替代，但**历史审计记录无法平移** |
| `group_storage_policies` | 6 | 已按 §5.1 映射到新结构，不算丢失 |
| `file_ancestor_permissions` | 0 | 空表，无影响 |
| `open_ids` | 0 | 空表，无影响 |
| `payments` | 0 | 空表，无影响 |
| `policy_load_balances` | 0 | 空表，无影响 |
| `storage_packs` | 0 | 空表，无影响 |

**真正有数据损失的只有 `audit_logs`（285 万行审计日志）。** 这是合规/追责数据，如果业务上需要保留，得单独导出归档，不能依赖迁移。

### 6.2 20 个列在 fork 里没有对应字段（数据会丢）

| 表 | 丢失的列 |
| --- | --- |
| `users` | `credit`（已映射到 credits）、`group_expires`、`notify_date`、`previous_group`、`unmanaged_email` |
| `shares` | `permissions`（分享权限配置）、`price`（已映射） |
| `abuse_reports` | `category`、`file_uri`、`reported_user_id`、`share_id` |
| `direct_links` | `type` |
| `files` | `permissions` |
| `gift_codes` | `product_props`、`qyt`、`used`、`used_by` |
| `share_purchases` | `payment_id`、`ticket_id`、`user_id`（已映射） |

**需要业务侧确认影响的**：`users.group_expires`（用户组到期时间）、`shares.permissions`（分享权限）、`abuse_reports` 的举报详情。这三个是有实际业务含义的配置，丢了会导致用户组不过期、分享权限放宽、举报记录不完整。

`gift_codes` 和 `direct_links.type` 在本次库里是空表/低影响，但换库前要查。

---

## 七、迁移结果逐项核对

### 7.1 全表行数对照（`prod_base` 原始 vs `fork_ref` 迁移后）

自动枚举 `information_schema` 全部表逐一比对，**20 张共有表全部一致或差异可解释**：

| 表 | 生产 | fork | 差异说明 |
| --- | --- | --- | --- |
| `settings` | 237 | 297 | fork 自带 60 条新设置（预期） |
| `users` | 599 | 600 | +1 是我的临时测试账号 |
| `groups` | 5 | 5 | ✅ |
| `storage_policies` | 14 | 14 | ✅ |
| `nodes` | 4 | 4 | ✅ |
| `files` | 471,776 | 471,785 | +9 是测试期间运行时写入 |
| `entities` | 688,768 | 688,329 | −439 是 fork 回收了无引用实体（预期行为） |
| `file_entities` | 499,612 | 499,620 | +8 同上 |
| `metadata` | 3,218,330 | 3,218,337 | +7 同上 |
| `tasks` | 466,269 | 466,277 | +8 是队列任务 |
| `shares` | 311 | 311 | ✅ |
| `direct_links` | 5,415 | 5,415 | ✅ |
| `dav_accounts` | 232 | 232 | ✅ |
| `oauth_clients` | 2 | 2 | ✅ |
| `oauth_grants` | 41 | 41 | ✅ |
| `passkeys` | 19 | 19 | ✅ |
| `fs_events` | 874 | 874 | ✅（初版漏迁，已补） |
| `abuse_reports` | 1 | 1 | ✅ |
| `share_purchases` | 0 | 0 | ✅ |
| `gift_codes` | 0 | 0 | ✅ |

**孤儿检查**（证明文件树没有断裂）：

- `files` 里没有 owner 的记录：**0**
- `file_entities` 里指向不存在 file 或 entity 的记录：**0**
- `metadata` 里指向不存在 file 的记录：**0**

### 7.2 功能验证（fork v4.23.8，`:15214`）

| 项目 | 结果 |
| --- | --- |
| 站点 ping | `{"code":0,"data":"4.23.8"}` |
| 站点配置接口 | 正常（标题 "FullStack Storage"） |
| 首页 | HTTP 200 |
| 登录 | ✅ 用**新建的临时账号**验证（未改动任何生产账号密码） |
| 列目录 | ✅ 真实生产用户（如 user 2）根目录条目、类型、大小正确；用户 552 有 4,009 个文件、用户 362 有 2,158 个 |
| 存储策略解析 | ✅ Admin 组可见 Storage-HK / Storage-US，对应迁移过来的关联 |
| entity→source→policy 映射 | ✅ direct_link 1 → 文件 7 → entity 4（source `uploads/4/e5f0a823-...`，size 30,271,345，policy 2） |
| **上传→下载往返** | ✅ 上传 262,144 字节，下载回来 **MD5 完全一致** |
| **Pro 时期老文件下载** | ✅ `nav-1.0.1-release.zip`，154,715 字节，长度一致、Content-Type `application/zip`、ZIP 魔数正确 |
| 分享短链 `/s/<slug>` | ✅ 302 → 分享页 |
| 分享信息接口 | ✅ 名称、大小 1.18 GB、分享者昵称、所属组全部正确 |
| 分享内列目录 | ✅ 返回文件列表，路径与 capability 正确 |
| 分享内下载 | ✅ 生成真实 S3 预签名地址（`uploads/261/源码/niainav/Mp1ugYRP`） |

**文件树完整性**：用户 2 的一个老文件，沿 `files.file_children` 上溯 4 层到根（id 67988），链路完整无断点。

### 7.3 Pro 15213（`:15213`）

| 项目 | 结果 |
| --- | --- |
| 进程状态 | `active` |
| 监听 | `*:15213` |
| ping | `{"code":0,"data":"4.18.0-pro"}` |
| 首页 | HTTP 200 |
| 业务数据 | users 599 / files 471,776 / shares 311 / storage_policies 14，与 `prod_base` **完全一致** |
| 日志错误 | 授权后 **0 条** permission denied、0 条 Error/Panic |
| 队列 | 正常处理（EntityRecycleQueue 在跑） |
| 分享短链 | `/s/<slug>` 302 正常 |

**Pro 侧未做列级迁移**，它直接使用生产数据的克隆，所以不存在数据损失——它是"原样跑起来"的对照组。

### 7.4 安全动作记录

- 生产用户密码**一个都没改**。所有登录验证用的都是**新建的临时测试账号**（Admin 组，仅在本机测试库中）。
- 未改动 15220、15230 等任何既有实例。
- SQLite 原库已备份至 `/data/pg15214/backup/pro15213-sqlite-orig-20261106-155551.db`（724,992 字节），可用于回滚。
- 原 config 也备份为 `pro15213-config.ini.20261106-155551`。

---

## 八、遗留事项与建议

### 必须决策的（迁移前）

1. **`audit_logs` 285 万行审计日志**：fork 没有对应表。是否需要单独归档？
2. **`users.group_expires`、`shares.permissions`、`abuse_reports` 详情**：这些是有业务含义的列，fork 无归宿。丢了会导致用户组不过期、分享权限放宽。
3. **`groups.storage_policy_id` 的"取最小值"规则**：这是我的推断，需要按生产的实际默认策略逐组核对。Anonymous 组为 NULL 是符合生产的。
4. **积分历史流水**：余额迁过去了，但 `credit_txns` 明细是空的。

### 迁移脚本必须包含的两步

- **写版本标记**（`db_version_4.23.8`），否则 fork 必然启动崩溃（§问题 2）
- **重置所有序列**（§问题 5），否则所有写操作失败

### 运维

- `/data` 使用率 88%，剩 2.6 GB。**在 20 G 盘上再克隆一份 2.6 GB 的库就会再次撑爆**（§问题 7）。做下一步实验前先清理或扩盘。
- 免费实例有 480 分钟强制暂停。本次所有数据都在 `/data` 持久盘，暂停后重启即可继续，不需要重做。
- 两个实例的 config 里有数据库密码，权限都是 600，**没有进入 git**。

---

## 九、附：关键坐标

| 项 | 值 |
| --- | --- |
| InstaBox 机器 | `m_agqw5litn4acpbo7b5axu3ycku`（baixiong） |
| PostgreSQL | `127.0.0.1:5432`，数据目录 `/data/pgdata` |
| Pro 15213 | `/data/pro15213/`，二进制 `cloudreve_v4.18.0`，config `/data/pro15213/config.ini`，systemd 单元 `cloudreve-pro15213.service` |
| fork 15214 | `/data/fork15214/`，二进制 `bin/cloudreve-4.23.8`，config `conf/config.ini`，日志 `fork.log` |
| 迁移工作目录 | `/data/fork15214/work/`（迁移 SQL、差异文件、验证脚本） |
| 有效 dump | `/data/pg15214/dump/prod.dump`（300,504,329 字节） |
| 序列修复脚本 | `/data/fork15214/work/seqfix.sql` |
| SQLite 备份 | `/data/pg15214/backup/pro15213-sqlite-orig-20261106-155551.db` |
