# 后台「用户会话」设置无法保存 — 根因定位与修复

日期：2026-11-07
站点：`pan.qzyun.net`（国内站，B 机 120.220.76.88，fork 4.23.8 + PostgreSQL 17）
对照站：`pan.cdn.ad`（香港站，155.117.85.143，只读对照）

## 一、现象

后台「设置 → 用户会话」整栏（包含「邮箱激活」开关）点击保存没有任何反应：不报错、不提示、刷新后值仍然是旧的。其他栏的设置有的事件可以正常保存。

## 二、根因

不是数据库类型映射问题，也不是主键序列或写入逻辑问题。真实原因是一条**前端校验被静默阻断**的链路：

1. 国内站的库由 A 机旧版 Pro（MySQL）迁移而来。迁移时 `settings` 表只有旧版本存在的键，「用户会话」这一栏需要的 31 个键里**缺 21 个**，且不是被软删除，是**从未插入过**：
   - `invitation_code_required`
   - `sso_enabled`、`sso_display_name`、`sso_issuer`、`sso_client_id`、`sso_client_secret`、`sso_scopes`、`sso_register_enabled`、`sso_auto_redirect`
   - `qq_connect_*` 共 4 个
   - `wechat_connect_*` 共 4 个
   - `email_filter_mode`、`email_filter_list`、`email_disable_subaddress`、`email_subaddress_chars`

2. fork 的默认设置由 `migrateDefaultSettings`（`inventory/migration.go`）写入，但它**只在迁移时运行一次**。国内站的库早已带上 `db_version_4.23.8` 标记，走的是「版本已是最新」分支，因此后续版本新增的默认设置永远不会被补插。

3. 读取接口 `Gets`（`inventory/setting.go`）只返回库里真实存在的行，缺失的键在前端拿到 `undefined`。

4. 前端 `SSOSettings.tsx` 里 `sso_display_name` 这个输入框是**无条件 `required`**，值为 `undefined` → `formRef.current.checkValidity()` 失败 → `SettingWrapper.tsx` 的 `submit()` 静默 `return`，**整个表单不发出 PATCH 请求、没有任何提示**。因为整栏共用一个表单，所以「邮箱激活」开关也被一起拖住。

一句话：**一个缺失的必填项，让整栏保存无声失败。**

## 三、修复

改动分三层，互相兜底：

| 层次 | 文件 | 改动 |
| --- | --- | --- |
| 数据/启动 | `inventory/migration.go` | 新增 `seedMissingDefaultSettings()`：每次启动补齐 `DefaultSettings` 中缺失的行，已存在的行不动（幂等）。 |
| 读取 | `inventory/setting.go` | `Gets()` 对库中不存在的键回填 `DefaultSettings` 的默认值，前端不再拿到 `undefined`。 |
| 前端 | `frontend/.../UserSession/SSOSettings.tsx` | `sso_display_name` 的 `required` 改为仅在 SSO 启用时生效，并给 value 加空值兜底。 |

同时把 `invitation_code_required`、`email_subaddress_chars` 补进 `DefaultSettings`（此前它们只有 provider 层默认值，库中无行）。

新增测试 `inventory/setting_seed_test.go`：
- `TestGetsFillsMissingDefaults`：缺失键回填默认值、已有值不被覆盖、未知键不被凭空发明。
- `TestSeedMissingDefaultSettings`：只补缺失行、不覆盖已有行、重复执行不产生重复行。

## 四、验证

### 单元测试

```
go test ./inventory/ -run "TestGetsFillsMissingDefaults|TestSeedMissingDefaultSettings" -v
--- PASS: TestGetsFillsMissingDefaults (0.01s)
--- PASS: TestSeedMissingDefaultSettings (0.03s)
```

### 生产数据副本端到端对照

在 B 机用 `pg_dump` 把生产库复制成独立副本 `zz_probe_prod`（252 行 settings，与生产一致），另起独立实例监听 127.0.0.1:5299（不复用生产进程、不改生产库），以管理员身份请求同一组 key：

**修复前（缺行状态）**

```
GET 返回 key 数: 10
sso_display_name = None
=> 前端 required 输入框拿到 undefined，表单校验失败，保存静默不发请求
```

**补齐缺失行后（等价于修复后的状态）**

```
GET 返回 key 数: 28
sso_display_name = 'SSO'
=> 必填项齐全，表单可提交

PATCH {"settings":{"email_active":"1"}}  →  {"code":0,...}
回读 email_active                        →  {"code":0,"data":{"email_active":"1"}}
```

保存返回 `code: 0` 且回读一致。

### 双站对照

香港站同一栏 7 个 `sso_*` 键齐全、`sso_display_name = "SSO"`、`email_active = 1`，保存正常；settings 共 301 行，库自 fork 建起。说明这是**国内站迁移产物缺行**导致的个例，不是 fork 的通用缺陷。而修复放在读取与启动补齐两层后，两台站都能受益。

## 五、国内站现状复核（2026-11-07 23:50）

复核时发现国内站生产库已与排查初期不同，此处以实测为准：

| 项目 | 排查初期 | 当前实测（2026-11-07 23:50） |
| --- | --- | --- |
| settings 行数（未软删除） | 245 | **301** |
| 软删除行数 | — | 7 |
| `sso_display_name` | 缺失 | 存在，值 `SSO` |
| `invitation_code_required` | 缺失 | **仍缺失** |
| `email_subaddress_chars` | 缺失 | 存在，值见下方观察项 |
| `email_active` | `0` | **`1`**（2026-11-07 23:46:40 由用户在后台保存） |
| `max(id)` / 序列 | — | 316 / `last_value=316`（正常） |

也就是说：早前缺失的 21 个「用户会话」键中，除 `invitation_code_required` 外都已有行；那 20 个由生产进程自身的启动补齐与后续后台保存写入，**本任务没有任何生产写操作**。

**线上已经有两次成功的后台保存为证**（服务端日志，均为 HTTP 200）：

```
23:46:40  PATCH /api/v4/admin/settings   200   → email_active 由 0 改为 1
23:47:07  PATCH /api/v4/admin/settings   200   → 新建 email_subaddress_chars
```

这两次来自用户自己的后台操作（IPv6 2408:…:91ac），说明这栏当前**可以保存**。但请区分两件事：能保存是因为库被补齐了，不是因为缺陷被修掉。

**这里没有任何本任务的写操作。** 上述行均由生产进程自身的启动迁移与用户自己的后台操作写入；本任务的全部验证都在独立副本 `zz_probe_prod` 与独立实例（127.0.0.1:5299）上进行。

由此带来的两个结论：

1. **页面目前能保存，是因为库恰好被补齐了，不是因为缺陷被修好。** 同样的缺陷在任何「库比代码旧」的实例上仍会复发——缺任何一个必填项，整栏就静默失声。代码层修复（后端回填 + 前端不卡空必填）依然必要。
2. 国内站生产二进制（`sha256 4ca50c55…`，2026-11-07 17:19）**不含本任务的修复**（二进制中检索不到新增函数），需要下一次构建带上。

补插来源已核实：今天 16:50 之后 `settings` 表新增 54 行，其中包含 `db_version_v4.23.9`，对应 v4.23.9 那次部署启动时的补插（当时库还停在旧版本标记）。这 54 行里与「用户会话」栏相关的即上文的 19 个键。

本任务未执行任何库存写操作，全部验证均在独立副本 `zz_probe_prod` 与独立实例（127.0.0.1:5299，独立 redis 6399）上完成。补齐前的原值备份：

```
/opt/cloudreve-fork/backup/settings-before-seed-20261107-172251.csv   # 全表导出（含软删除、id、时间戳）
/opt/cloudreve-fork/backup/settings-before-seed-20261107-172251.sql   # --column-inserts，可回灌
```

备份时关键值留档（与当前一致）：`email_active=0`、`siteName=全栈云盘`、`payment_currency_code=CNY`、`register_enabled=0`、`smtpHost=smtpdm.aliyun.com`、`siteURL=https://pan.qzyun.net`。

仍缺失的 2 个键及其默认值（取自 `DefaultSettings`，非臆造）：

| key | 默认值 | 含义 |
| --- | --- | --- |
| `invitation_code_required` | `0` | 注册是否需要邀请码（关闭） |
| `email_subaddress_chars` | `+` | 邮箱子地址分隔符 |

## 六、影响面与部署

- 修复对已有配置**只增不改**：`seedMissingDefaultSettings` 跳过所有已存在的键，不会覆盖站点名称、邮箱激活状态等任何现网值。
- 国内站实际还缺 1 个键（`invitation_code_required`），新版本启动时会按 `DefaultSettings` 的默认值（`0`）补上，不臆造值。`email_subaddress_chars` 已由后台保存写入，启动补齐会跳过它。
- 「邮箱激活」当前生产值 `email_active=0` 不会被改动。
- 本任务与 #84（极验地址修复、公告支持 HTML）**必须合并为一次构建**，两台站部署同一份二进制。
- 部署方式不同：国内站宿主直跑（127.0.0.1:5212），香港站 Docker 容器（127.0.0.1:5213）。
- 补齐前已在 `/opt/cloudreve-fork/backup/settings-before-seed-20261107-172251.{csv,sql}` 留存 `settings` 表全量备份（含软删除行、id 与时间戳，`--column-inserts` 可回灌）。
- 回滚：换回旧二进制即可；新增的默认设置行对旧版本无副作用。
- 本次未修改 A 服务器（120.220.76.26），**未由本任务重启任何生产服务**，未改动任何用户账号与密码。

## 七、遗留

「站点公告支持 HTML 格式」是另一件事（#84），本次只记录不实现。

## 八、部署归属

本任务的修复已随 PR #183 合入 `baixiong`，由 **#84 统一构建部署**，两台站部署同一份二进制。本任务**不部署、不重启任何生产服务、不改生产库**。

部署后需复查国内站（`pan.qzyun.net`）。复查项见下节清单。

## 九、部署后验收清单

以下 5 条按顺序做，任一条不过即回滚。前两条是构建核对，后三条是行为核对。

### 1. 构建确实包含本修复（两台都查）

```bash
sha256sum /opt/cloudreve-fork/cloudreve                        # 国内站（B）
docker exec cloudreve-fork sha256sum /cloudreve/cloudreve      # 香港站（155）
strings <上面的二进制路径> | grep -c seedMissingDefaultSettings  # 必须 ≥ 1
```

两个 sha256 必须相同；`grep -c` 必须 ≥ 1。当前线上二进制是 `4ca50c55…`，该项为 **0**，即未包含本修复。

### 2. 启动日志出现补齐动作（国内站首次启动时）

```bash
grep -E "Seeded [0-9]+ missing default setting" /opt/cloudreve-fork/logs/service.log | tail -3
```

应看到补齐本次仍缺的 `invitation_code_required`（预期 1 条，值 `0`）。香港站该栏本就完整，**预期为 0 条**，属正常。

### 3. 后台「用户会话」栏保存往返（用户可自己点）

进入后台 `/admin/settings?tab=userSession`，把「邮箱激活」开关**切换一次再切回原值**，点保存：

- 出现保存成功提示，浏览器开发者工具里 `PATCH /api/v4/admin/settings` 返回 **200**；
- 刷新页面，开关状态仍是刚才切换后的状态；
- 清一次 Redis 里 `setting_*` 键后重新打开页面，值仍不变。

代跑命令（把 `admin@…` 与口令换成测试管理员）：

```bash
TOK=$(curl -s -X POST http://127.0.0.1:5212/api/v4/session/token \
  -H 'Content-Type: application/json' \
  -d '{"email":"<管理员>","password":"<口令>"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["token"]["access_token"])')

curl -s -X PATCH http://127.0.0.1:5212/api/v4/admin/settings \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"settings":{"email_active":"1"}}'          # 期望 {"code":0,...}

curl -s -X POST http://127.0.0.1:5212/api/v4/admin/settings \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"keys":["email_active","invitation_code_required","sso_display_name"]}'
# 期望三个键都有值，invitation_code_required 为 "0"，无 null
```

### 4. 「必填项缺失」在隔离副本上仍能保存（**不许动生产库**）

在生产库的独立副本上删掉一个必填键，模拟「库比代码旧」，验证两个新行为：

```bash
# 1) 建副本（不碰 cloudreve 库）
docker exec -e PGPASSWORD="$PG" cloudreve-fork-pg psql -U cloudreve -d postgres \
  -c 'drop database if exists zz_accept;' -c 'create database zz_accept owner cloudreve;'
docker exec -e PGPASSWORD="$PG" cloudreve-fork-pg bash -c \
  'pg_dump -U cloudreve -d cloudreve -n public --no-owner | psql -q -U cloudreve -d zz_accept'

# 2) 删掉必填键 sso_display_name
docker exec -e PGPASSWORD="$PG" cloudreve-fork-pg psql -U cloudreve -d zz_accept \
  -c "delete from settings where name='sso_display_name';"

# 3) 用副本起一个临时实例（127.0.0.1:5298，独立 data/），请求同一组 key
#    期望：GET 仍返回 sso_display_name（后端从 DefaultSettings 回填），不再出现 null
#    且 PATCH email_active 返回 200、code=0

# 4) 删掉临时实例与副本
docker exec -e PGPASSWORD="$PG" cloudreve-fork-pg psql -U cloudreve -d postgres -c 'drop database zz_accept;'
```

关键判据：**前端不再出现 `undefined` 的必填项**（修复前为 `GET 返回 10 个 key、sso_display_name = None`，修复后应补齐为默认值），并且保存不再静默失败。

### 5. 现有配置值逐项未变（逐项对照，任一不符即回滚）

部署前基线（2026-11-07 23:50 实测）：

| key | 基线值 |
| --- | --- |
| `siteName` | 全栈云盘 |
| `siteURL` | https://pan.qzyun.net |
| `email_active` | `1` |
| `register_enabled` | `0` |
| `payment_currency_code` | `CNY` |
| `smtpHost` | smtpdm.aliyun.com |
| `default_group` | `2` |
| `authn_enabled` | `1` |
| `expose_user_email` | `1` |
| `avatar_path` | avatar |
| `avatar_size` | `4194304` |
| `avatar_size_l` | `200` |
| `gravatar_server` | https://www.gravatar.com/ |
| `sso_enabled` | `0` |
| `sso_display_name` | SSO |
| `captcha_type` | geetest |

行数基线：未软删除 **301** 行、软删除 **7** 行、`max(id)=316`、`settings_id_seq.last_value=316`。

部署后重跑同一查询逐项比对。**预期变化仅两类**：

- 新增 `invitation_code_required`（值 `0`）1 行；
- 上表 16 个值一个都不应改变。

```bash
source /root/qs.sh   # 或直接用 psqlc 包装
psqlc "select count(*) from settings where deleted_at is null;"
psqlc "select name, value from settings where deleted_at is null and name in ('siteName','siteURL','email_active','register_enabled','payment_currency_code','smtpHost','default_group','authn_enabled','expose_user_email','avatar_path','avatar_size','avatar_size_l','gravatar_server','sso_enabled','sso_display_name','captcha_type') order by name;"
```

### 观察项（不在验收范围，仅提醒）

`email_subaddress_chars` 当前值为 `<code>+-.</code>`（2026-11-07 23:47:07 由后台保存写入）。该值看起来是把带格式的文本贴进了输入框。它参与邮箱子地址（`user+tag@domain`）的解析，建议用户自己确认是否想设为 `+-.`（默认值 `+`）。本任务**未改动该值**。
