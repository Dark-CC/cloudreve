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

## 五、影响面与部署

- 修复对已有配置**只增不改**：`seedMissingDefaultSettings` 跳过所有已存在的键，不会覆盖站点名称、邮箱激活状态等任何现网值。国内站启动时会新插入 21 行默认设置。
- 「邮箱激活」当前生产值 `email_active=0` 不会被改动；补行后该栏恢复可编辑。
- 本任务与 #84（极验地址修复、公告支持 HTML）**必须合并为一次构建**，两台站部署同一份二进制；不要各自往生产机放各自的二进制。
- 部署方式不同：国内站宿主直跑（127.0.0.1:5212），香港站 Docker 容器（127.0.0.1:5213）。
- 回滚：换回旧二进制即可；新增的 21 行默认设置对旧版本无副作用（旧版本本就在它们的默认值语义下工作）。
- 本次未修改 A 服务器（120.220.76.26），未重启任何生产服务，未改动任何用户账号与密码。

## 六、遗留

「站点公告支持 HTML 格式」是另一件事（#84），本次只记录不实现。
