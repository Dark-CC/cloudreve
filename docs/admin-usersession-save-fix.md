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

## 五、国内站现状复核（2026-11-07 17:2x）

复核时发现国内站生产库已与排查初期不同，此处以实测为准：

| 项目 | 排查初期 | 当前实测 |
| --- | --- | --- |
| settings 行数（未软删除） | 245 | **299** |
| 软删除行数 | — | 7 |
| `sso_display_name` | 缺失 | 存在，值 `SSO` |
| `invitation_code_required` | 缺失 | **仍缺失** |
| `email_subaddress_chars` | 缺失 | **仍缺失** |
| `max(id)` / 序列 | — | 311 / `last_value=312`（正常） |

也就是说：早前缺失的 21 个「用户会话」键中，**19 个已在今天 16:53–17:19 之间被补进库**（`created_at` 为证），只剩 `invitation_code_required`、`email_subaddress_chars` 两个不含在前端表单必填项里、不影响保存的键仍然缺失。

**这里没有任何本任务的写操作。** 这些行由生产进程自身的启动迁移逻辑写入，形成于本任务只读排查期间另一次重启之后；本任务的全部验证都在独立副本 `zz_probe_prod` 与独立实例（127.0.0.1:5299）上进行。

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
- 国内站实际还缺 2 个键（`invitation_code_required`、`email_subaddress_chars`），新版本启动时会按 `DefaultSettings` 的默认值（分别为 `0` 与 `+`）补上，不臆造值。
- 「邮箱激活」当前生产值 `email_active=0` 不会被改动。
- 本任务与 #84（极验地址修复、公告支持 HTML）**必须合并为一次构建**，两台站部署同一份二进制。
- 部署方式不同：国内站宿主直跑（127.0.0.1:5212），香港站 Docker 容器（127.0.0.1:5213）。
- 补齐前已在 `/opt/cloudreve-fork/backup/settings-before-seed-20261107-172251.{csv,sql}` 留存 `settings` 表全量备份（含软删除行、id 与时间戳，`--column-inserts` 可回灌）。
- 回滚：换回旧二进制即可；新增的默认设置行对旧版本无副作用。
- 本次未修改 A 服务器（120.220.76.26），**未由本任务重启任何生产服务**，未改动任何用户账号与密码。

## 七、遗留

「站点公告支持 HTML 格式」是另一件事（#84），本次只记录不实现。
