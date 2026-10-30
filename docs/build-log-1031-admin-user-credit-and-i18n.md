# 2026-10-31 合并构建与验收记录

测试站 `http://192.168.31.202:15220`，源码 `/data/cr-src-build45`（HEAD `13d8414`），
service `cloudreve-test-15220.service`。本轮把四件事并入同一次编译。

## 一、`/admin/user` 积分无法修改

### 根因（两处叠加）

1. **前端只读且恒显示 0**：`frontend/src/component/Admin/User/UserDialog/UserForm.tsx`
   积分框硬编码 `htmlInput: { readOnly: true }` 且 `value={0}`，既不能编辑，
   也从不显示用户真实余额。
2. **后端丢弃该字段**：`inventory/user.go` 的 `userClient.Upsert` 更新语句只写
   `email/nick/avatar/status/group/password`（及封禁字段），`credits` 完全不在
   更新列里——即使提交也会被静默丢弃。

用户表积分字段名为 `credits`（ent schema `field.Int64("credits")`）。

### 上游设计的正确路径

积分唯一的写入路径是 `inventory/vas.go` 的 `CreditAdjust(ctx, userID, delta, typ, ref, des)`：
事务内 `AddCredits(delta)`，负数防透支（`ErrInsufficientPoints`），每次写一条
`CreditTxn` 流水（金额 / 类型 / 引用 / 说明）作为审计账本。
管理端 VAS 路由此前只有礼品码与 SKU，**没有任何管理员调积分接口**；礼品码是上游
原本的积分发放路径。

因此本轮**不新增「直接改数」入口**，而是补一个走流水账本的管理员调整接口。

### 改动

- `service/admin/user.go`：新增 `AdjustCreditService` 与 `Adjust`。取用户 → 目标为
  管理员且调用者非 full admin 则拒绝 → `CreditAdjust(..., TypeAdjust, "", des)` →
  `ErrInsufficientPoints` 映射参数错误 → 记录 `EventUserChanged` activity → 返回用户详情。
- `routers/router.go`：`user.PUT(":id/credit", RequiredScopes(ScopeAdminWrite), FromUriJSON, AdminAdjustUserCredit)`。
- `routers/controllers/admin.go`：`AdminAdjustUserCredit` 控制器。
- 前端 `api.ts`：`adjustUserCredit(id, delta, reason?)`。
- 前端 `UserForm.tsx`：积分框改为显示真实 `values.credits`（只读）+「调整积分」按钮。
- 新建 `AdjustCreditDialog.tsx`：有符号 delta（非 0）+ 原因输入。

### 绑定校验的坑（记录以免复发）

`FromUriJSON` 先 `ShouldBindUri` 再 `ShouldBindJSON`，**两阶段都会跑 validator**。
给 `Delta int64` 加 `binding:"ne=0"` 会让 URI 阶段（此时 delta 仍为 0）判失败，
表现为所有请求都返回「Invalid parameters」。同理 `ID` 若带 `json:"id"`，JSON 阶段
又要求 body 里有 id。正确写法与补单接口一致：`ID` 只带 `uri:"id"`，`Delta` 不带
binding，非 0 校验放进服务方法。

## 二、`/admin/payment-orders` 补单弹窗显示英文 `cancel`

**来源**：`PaymentOrderList.tsx:172` 的 `<Button onClick={onClose}>{t("cancel")}</Button>`。
组件用的是 `useTranslation("dashboard")`，而 `cancel` 只存在于 `common` 命名空间，
key 解析失败时 i18next 回退为**裸 key 本身**，于是渲染成小写英文 `cancel`。
详情弹窗的 `t("close")` 是同一类问题，一并修。

修法（不硬编码）：`t("common:cancel")` / `t("common:close")`。

### 补单弹窗逐项核对（全中文，无缺）

| key | 中文 |
|---|---|
| `paymentOrders.reconcileTitle` | 手动补单确认 |
| `paymentOrders.orderNo` / `buyer` / `item` | 订单号 / 用户 / 商品 |
| `paymentOrders.reconcileHint` | 系统会先向支付渠道查单… |
| `paymentOrders.reason` / `reasonHint` | 补单原因 / 必填，会记入审计日志 |
| `paymentOrders.force` / `forceHint` | 渠道无法确认时强制补单 / 强制补单会… |
| `paymentOrders.reconcile` / `forceConfirm` | 补单 / 我已确认，强制补单 |
| `paymentOrders.reconcileSuccess` / `alreadyPaid` | 补单成功，商品已到账 / 该订单已支付，未重复发货 |

### MUI 默认英文文案

该页用的是自定义 `Admin/Common/TablePagination.tsx`，分页由 `settings.perPage`
（`每页 {{num}} 条`）渲染，**不存在** `TablePagination` 的「Rows per page」「of」
默认英文，因此不是漏翻来源。全站扫描也未发现其他 MUI 默认英文文案残留。

### 命名空间错配的排查

`check-i18n-keys.py` 默认带 `defaultNS` 回退链（application → common → dashboard），
会掩盖「组件用 ns A、key 实际在 ns B」这类问题。本轮额外做了一次严格交叉检查，
列出 16 处候选，逐条核实后**只有 `cancel` 和 `close` 两处是真错配**，其余
（`CustomHTML.tsx` 的 `settings.*` / `login.*`、`DefaultViewerApps.tsx` 等）是脚本
对同文件内多个 `useTranslation`、多个子组件的误判。

## 三、`vas.priceStepHint` 与 `paymentOrders.userSearch`

两个 key 在源码与线上 JSON 中都存在且中文正常。真实原因是 i18n 的 localStorage
缓存键 `__ASSETS_VERSION__` 取自 `npm_package_version`（`4.0.0-next`），自 fork 起
不变，旧缓存（7 天）遮蔽了新加的 key。

修法（`frontend/vite.config.ts`）：`BUILD_VERSION = ${npm_package_version}+${Date.now().toString(36)}`，
`define.__ASSETS_VERSION__` 使用它，缓存随每次构建失效。

**注意**：`version.json` 的 `version` 字段**不能**改成 `BUILD_VERSION`——后端
`application/statics/statics.go` 会拿它与 `constants.BackendVersion`（`4.23.7`）比较，
不等就打印 mismatch 错误。最终写法：

```json
{"name":"cloudreve-frontend","version":"4.23.7","build":"4.0.0-next+mvvenkoq"}
```

`version` 供后端校验，`build` 是可审计的构建戳。

## 四、全站 i18n 扫描补缺

`frontend/scripts/check-i18n-keys.py` 抽取 `t(...)` / `i18nKey=` / `Trans i18nKey`，
解析 `useTranslation("ns", { keyPrefix })`。全 `src` 扫描 2335 个 key，真实缺失 46 个
（`shop.*` 26、`policy.y139*` 9、`summary.*` 5、`fileManager.*` 3、
`modals.folderDirectLinkNotAllowed`、`vas.*`、`file.importWarnings`），13 种语言补齐。
终态只剩 1 条已知假阳性：`markdown_editor:`，源于 `Editor.tsx:242` 的
`t("markdown_editor:" + key)` 动态前缀拼接。

详见 `docs/i18n-missing-keys-1031.md`。

## 五、构建与部署

顺序：`NODE_OPTIONS=--max-old-space-size=4096 yarn build` → 打 `assets.zip`
（内部 `assets/build/` 前缀、含 `version.json`）→ `go build -ldflags "-s -w"`。

```bash
# 前端
cd /data/cr-src-build45/frontend && yarn build          # ✓ built in 38.51s

# 打包（前缀必须是 assets/build/）
mkdir -p /tmp/p/assets && cp -r frontend/build /tmp/p/assets/build
(cd /tmp/p && zip -qr /data/cr-src-build45/application/statics/assets.zip assets)

# 后端
cd /data/cr-src-build45 && go build -ldflags "-s -w" -o /data/cloudreve-new50
```

部署前备份：`/data/cr-test-15220/backup-20261031-040951/`（`cloudreve.bak` + `cloudreve.db.bak`）。
停服 → 覆盖二进制 → 启服，`active (running)`。

> `remote_shell` 的断连会杀掉会话内的后台进程，长时间构建必须用
> `systemd-run --unit=... --collect` 启动，否则会被反复打断。
> 另注意 `systemd-run` 环境没有 nvm 的 PATH，需 `--setenv=PATH=...`。

## 六、验收（实测输出）

### 端点

```
/              200
/version.json  200
/admin         200
```

静态资源版本 mismatch 日志：**0 次**（部署前为 1 次）。

### 积分调整接口（真机）

```bash
PUT /api/v4/admin/user/1/credit  {"delta":100,"reason":"verify"}
# → HTTP 200 {"code":0, ...}
```

```sql
sqlite> SELECT id,credits FROM users;
1|60
sqlite> SELECT id,amount,type,des FROM credit_txns ORDER BY id;
1|100|adjust|verify
2|-40|adjust|deduct test
```

- 加 100、扣 40 均落库，余额 60，两条 `adjust` 流水齐全（含金额、类型、原因）。
- 防透支：`{"delta":-9999}` → `40001 Adjustment would make the balance negative`。
- 零值：`{"delta":0}` → `40001 Delta cannot be zero`。

### 补单与订单页

```
GET  /api/v4/admin/payment-orders?page=1&page_size=5   → 200，返回真实订单
POST /api/v4/admin/payment-orders/nonexistent/reconcile → 200 {"code":40001,"msg":"Invalid order id"}
```

补单路由存在且鉴权正常。线上 `common.json`：`cancel=取消`、`close=关闭`；
补单弹窗 7 个文案 key 全部为中文。

### 前端文案

线上 `locales/zh-CN/dashboard.json`：

```
user.adjustPoints   = 调整积分
paymentOrders.userSearch = 用户 ID 或邮箱
vas.priceStepHint   = 支持小数，最小可填 {{min}}。
```

## 七、边界

未触碰 15212、15213 与生产 5212；未发版。测试后 admin 积分留有 60（验证痕迹）。
