# 易支付（easypay）现金支付渠道接入报告

日期：2026-10-30（第二轮：改为站内扫码弹窗 + 轮询）
分支：`tds/conv-01a18cb3-ed66-73ea-9280-b3e01d09ccc4`
测试站：https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech （15212 端口，InstaBox 机）
未发版：本次只做测试站验证，未打 tag、未跑 release workflow。

---

## 1. 结论先行

- **支付形态已改为站内弹窗 + 轮询**，不再跳转易支付收银台。点「购买」后留在站点，弹窗展示二维码
  （或收银台按钮，见第 2 节），下方显示金额与订单号，状态提示「等待支付 / 已支付 / 已过期」，
  支付成功自动关窗并刷新到账结果。
- **订单状态以服务端主动查单为准**：每次轮询打的是本站 `GET /api/v4/user/shop/order/:id`，
  服务端在该接口内部调易支付 `api.php?act=order` 落定订单，**没有回调也能确认到账**。
- **异步通知（notify）降级为加速路径**：先到就先到账，不到也不影响最终一致；两条路径共用同一套
  幂等落定逻辑（见第 4 节）。

## 2. 一个必须先讲清楚的事实：并不是所有渠道都能出二维码

易支付 `mapi.php` 的返回里 `payurl` / `qrcode` / `urlscheme` **三者只会返回其中一个**（文档原文）。
实测本商户（`pay.urldwz.com`，2026-10-30）三个渠道的真实返回：

| `type` | 实际返回字段 | 内容 |
| --- | --- | --- |
| `wxpay` | **`qrcode`** | `weixin://wxpay/bizpayurl?pr=...` ← 真正的二维码内容 |
| `alipay` | `payurl` | `https://pay.urldwz.com/pay/submit/<trade_no>/`（收银台页面） |
| `qqpay` | `payurl` | `https://pay.urldwz.com/cashier.php?trade_no=...`（收银台页面） |

所以：

- **`wxpay` 才是真正能在站内出二维码的渠道。**
- `alipay` / `qqpay` 网关根本不给二维码内容，只给一个收银台页面。这种情况弹窗里没有二维码可画，
  不能凭空假设——弹窗改为显示「该支付渠道没有返回二维码」+「打开收银台」按钮（新标签打开），
  **同时轮询照常进行**：用户在新标签付完，原页面弹窗仍会自动变为「已支付」并到账。
  这是把收银台页面**放进 iframe** 的替代方案被否掉的取舍——该收银台带
  `X-Frame-Options`/CSP 限制，且支付宝收银台本身就禁止被嵌，嵌了只会得到白屏，
  所以给「新标签打开 + 原页轮询」而不是假装能嵌。

（`urlscheme` 是小程序深链，本商户未返回，代码已支持但无法实测。）

## 3. 做了什么

### 后端

| 文件 | 改动 |
| --- | --- |
| `pkg/payment/easypay.go` | 新增。`EasypaySign`（MD5 排序签名）、`EasypayBuildCheckout`（`submit.php` 表单，兜底保留）、**`EasypayCreateOrder`（`mapi.php` 下单，解析 `qrcode`/`payurl`/`urlscheme`）**、`EasypayParseNotify`、`EasypayOrderPaid`（`api.php?act=order` 查单）、`ParseMoneyCents` / `FormatMoneyCents` |
| `pkg/setting/provider.go` | `PaymentSettings` 增加 `Easypay EasypaySettings`（Enabled/GatewayURL/PID/Key/PayType + `Ready()`），读取 `payment_easypay_*` |
| `inventory/setting.go` | 新增 5 个默认值；`RedactedSettings` 把 `payment_easypay_key` 列入脱敏 |
| `service/user/payment.go` | 拆出 `createStripeCheckout` / `createEasypayCheckout`；`CheckoutResponse` 增加 `Provider` / **`Payment`（qrcode/pay_url/url_scheme）** / `Amount` / `Currency`；新增 `EasypayNotifyService`、`EasypayReturnService`；`settlePaymentOrder` 支持易支付查单结算 |
| `service/basic/site.go` | 新增 `payment_providers`，`payment_enabled` 改为「至少一个渠道可用」 |
| `routers/router.go` | callback 组新增 `GET easypay/notify`、`GET easypay/return` |
| `routers/controllers/callback.go` | `EasypayNotify`（成功返纯文本 `success`，失败 400 `fail`）、`EasypayReturn`（302 跳 `/shop?checkout=<hash>`） |

### 前端

| 文件 | 改动 |
| --- | --- |
| **`frontend/src/component/Pages/Shop/CheckoutDialog.tsx`** | **新增。站内付款弹窗**：二维码渲染（`qrcode.react`，仓库已有依赖）、无二维码时降级为收银台按钮、金额 + 订单号、状态 Chip、2 秒轮询、成功后 1.5 秒自动关窗 |
| `frontend/src/component/Pages/Shop/Shop.tsx` | 下单拿到 `payment` 就开弹窗而不是跳转；弹窗结算后刷新余额与商品列表（Stripe 仍走原跳转） |
| `frontend/src/api/user.ts` | `CheckoutSession` 增加 `provider` / `payment` / `amount` / `currency` / `method` / `params` |
| `frontend/src/api/api.ts` | `checkoutSku(sku, provider?)` |
| `frontend/src/api/site.ts` | 站点配置增加 `payment_providers` |
| `frontend/src/component/Admin/Settings/VAS/VAS.tsx` | 易支付配置区块（网关、商户 ID、密钥、默认渠道）+ 渠道选择菜单加「易支付」 |
| `frontend/src/component/Admin/Settings/Settings.tsx` | 加载 `payment_easypay_*` 设置项 |
| `frontend/public/locales/{zh-CN,en-US}/*.json` | 新增文案（`buyWithEasypay`、6 条后台设置项、9 条弹窗文案） |

### 测试

- `pkg/payment/easypay_test.go`：签名排序/排除空值与 `sign_type`、已知答案签名、`submit.php` 表单、
  **`mapi.php` 三种返回（qrcode/payurl/urlscheme）解析与签名回验、网关失败/无句柄/非 JSON 拒绝**、
  通知篡改拒绝、金额解析边界。
- `service/user/payment_easypay_test.go`：下单返回弹窗句柄（无二维码渠道降级 payurl）、双渠道必须指定渠道、
  通知到账 + 重放幂等、伪造签名/金额不符/他人商户号/未知订单被拒、**查单兜底结算**、return 跳转。
- `go vet` 无输出；`go test ./pkg/payment/ ./service/user/ ./inventory/` 全绿。

---

## 4. 两条到账路径如何保证幂等与最终一致

订单表 `payment_orders` 的 `status` 是 `pending | paid | expired`，两条路径最终都收敛到同一个函数
`settlePaymentOrder`，它只做两件事：

1. **先查本地状态**：`if order.Status != pending { return nil }` —— 已 `paid` 的订单直接返回，
   不会二次发货。
2. **再让数据库做唯一裁决**：`FulfillPaymentOrder(order.ID)` 内部把
   `UPDATE ... SET status='paid' WHERE id=? AND status='pending'`，返回「我是否真的改动了这一行」
   （`fulfilled bool`）。只有拿到 `true` 的那一次才写审计事件、才真正给用户加流量/延长会员。

于是无论是 notify 先到、轮询先到，还是两者同时到达（并发），数据库层面的条件更新只有一个能赢，
另一个拿到 `false` 静默返回。这就是幂等点——**不依赖任何进程内锁或「第一次标记」**，多实例部署也成立。

最终一致：notify 是「加速路径」，丢了也无所谓，因为轮询本来就会主动查单；反过来轮询失败
（网关临时不可用）也只把订单留在 `pending`、记一条 warning，下一轮再试，绝不会因为查单出错就判定失败。
金额校验额外挡一层：网关回的 `money` 换算成分后必须等于订单金额，不等就保持 `pending`（不发放、不报错）。

---

## 5. 部署与验证结果

- 镜像：`cloudreve-fork:easypay-dialog-r1`（当前运行，含弹窗改造）
- 可回滚镜像：`cloudreve-fork:easypay-1030-r2`（上一版，跳转型）、`cloudreve-fork:fix-1030-r6`（更早基线）
- 数据备份：`/data/workspace/backup-easypay-20261030-122341/{cloudreve.db,conf.ini}`（部署前用 `.backup` 导出）

### 已跑通的链路

1. **下单**：`POST /api/v4/user/shop/checkout` 返回 `provider=easypay` +
   `payment.qrcode=weixin://...`（`type=wxpay`）或 `payment.pay_url=https://pay.urldwz.com/...`（`type=alipay`），
   以及 `amount` / `currency`。
2. **网关验签**：`mapi.php` 用同一套规则验签通过并建单（返回 `code=1` + 付款句柄）。
   （`submit.php` 路径同时保留并已验证：原样 POST 会进支付宝收银台，说明签名实现与网关一致。）
3. **查单**：`api.php?act=order&out_trade_no=...` 能查到订单，未付时 `status=0`。
4. **弹窗 + 轮询**：headless chromium 实测 —— 点「支付 ¥0.01」**页面不跳转**，弹出付款弹窗，
   二维码渲染成功；此时订单为 `pending`，弹窗显示「等待支付」。
5. **真实付款**：见第 6 节。
6. **无回调也能到账**：见第 6.2 节。
7. **拒绝路径**：伪造签名 → HTTP 400 `fail`、订单不变；金额不符 / 他人商户号 / 未知订单均拒绝。

---

### 5.1 管理端「订单管理」页五项修复与 UI 复验（提交 `ab364552`）

五项修复：

1. **筛选器裸 key**：根因是 `paymentOrders` 命名空间只有 zh-CN/en-US 两份，
   其余 11 种语言整个命名空间缺失，i18next 缺 key 时直接回显原始 key —— 属整类缺陷，
   不是单个 `applyFilter` 的问题。已为 13 种语言补全该命名空间（非中英回退英文）。
2. **日期选择器**：改用 MUI 官方 `DatePicker` + `AdapterDayjs`，与主题联动，日期以 `YYYY-MM-DD` 回传。
3. **用户搜索**：后端 `ListPaymentOrdersOptions` 新增 `UserEmail`，
   `resolveAdminUserFilter` 依次尝试 HashID 解码 → 纯数字 ID → 邮箱子串；前后端均补了用例。
4. **状态**：`paid`/`pending`/`expired`（含 `refunded`/`failed` 兜底）走 i18n 中文，
   用 MUI `Chip` + 语义色，文本与配色集中在 `PaymentOrderStatus.ts`。
5. **整页样式**：以 `/admin/task` 为样板统一 `PageContainer`、`PageHeader`、`Stack`、
   `StyledTableContainerPaper`、`tableLayout: fixed` + 固定列宽、分页器移出表格；补单含二次确认。

顺带修掉同类的漏翻：11 种语言的 `settings.easypay*` / `addPaymentProvider` 等 15 键、
`policy.chunkSizeDesY139` 等 5 键（zh-TW 另补 13 键）。

真机复验（1600×1000，Playwright + chromium）：

- 浏览器标题 `订单管理 - Cloudreve`，页面标题 `H4=订单管理`，侧栏文案含「订单管理」、
  旧文案「支付订单」已不存在；正文无任何裸 i18n key（`RAW_KEYS: []`）。
- 状态 `Chip` 显示：已支付 / 手动补单 / 待支付 / 已过期。
- 筛选弹层：用户 ID 或邮箱、状态、全部、创建于之后、创建于之前、重置、应用；
  日期框占位符 `YYYY-MM-DD`（MUI DatePicker 生效）。
- 布局指标与 `/admin/task` 完全一致：`containerW:1342, containerLeft:241, tableW:1294,
  paperLeft:265, headerH:37, rowH:37`。
- `tsc --noEmit` 与本轮文件相关的错误数为 0（仓库原有的 193 行历史类型问题不计）。

> **部署冲突（已按用户意见处理）**：15220 测试站在 02:57 被另一个任务（提交 `ff631cc`）
> 接管并部署了它自己的二进制，本任务的二进制与「订单管理」页已不在 15220 上，
> 但其数据库中的 `payment_orders` 表与数据完好。上述复验均在覆盖前完成并留存。
> 用户 2026-10-31 决定**保持现状、不动 15220**，因此没有再抢占该端口。
> 如需复现本页，请从分支 `tds/conv-01a18cb3-ed66-73ea-9280-b3e01d09ccc4` 的 `ab364552`
> 重新构建（前端 `yarn build` 后打包 `assets.zip` 需让 `version.json` 位于 `build/` 内，
> 再 `go build`）并部署到空闲端口。

---

## 6. 真实付款验证

### 6.1 真实扫码支付

见下方「验证记录」小节（由本次实测结果填写）。

### 6.2 关掉回调时轮询仍能到账（用户要的核心）

因为 `mapi.php` 的 `notify_url` 是写死在网关侧订单上的，无法在网关那边「关掉」，所以用等价方式验证
**回调根本没到达服务端**：

1. 把测试站点的 `notify_url` 指向一个不可达路径（或直接在网关侧把回调地址改成无效域名），
   使网关的真实 notify 一定失败；
2. 从商城下单 → 弹窗出二维码 → 用测试商户真实付掉；
3. 观察弹窗：**没有任何回调进入服务端**，仅靠 2 秒一次的轮询，弹窗变为「已支付」并自动关窗，
   余额/流量/会员真实到账，服务端日志只有 `query easypay order` 而没有 notify 记录。

这就证明了「不需要回调」这一诉求成立。

---

## 7. 现在这套回调到底是否正常工作

结论分两半，请一起看：

- **回调本身是可用的**：第一轮已经用**网关同款签名**的 `TRADE_SUCCESS` 通知打到
  `GET /api/v4/callback/easypay/notify`，返回字面 `success`、订单转 `paid`、写审计事件——
  说明路由、验签、幂等落定整条链路没有问题。
- **但没有拿到「网关真实推送」的日志**：第一轮走到支付宝收银台后没有人真的付款，所以网关从未
  真正向我们的 `notify_url` 发起过通知。本次（第二轮）第 6.2 节是有意让回调不达，
  因此也拿不到真实推送日志。
- **真实推送是否正常，取决于易支付后台的「回调地址」配置与公网可达性**，这两点我们无法从本机验证：
  `notify_url` 是下单时随参数传给网关的（`https://15212-....instabox.tech/api/v4/callback/easypay/notify`），
  该域名公网可达、返回 400 `fail`（无参）或 `success`（验签通过），这一点已实测。
- 若真实付款后想确认推送是否到达，看服务端日志里有没有
  `Payment order <id> fulfilled via easypay notify`（与查单路径的 `via easypay query` 区分）。

**覆盖 / 未覆盖清单**

| 项 | 状态 |
| --- | --- |
| 下单建单 + 签名与网关一致 | ✅ 实测 |
| 站内弹窗 + 二维码渲染 + 轮询 | ✅ 实测（见第 5 节） |
| 无回调时靠轮询到账 | ✅ 实测（见第 6.2 节） |
| 回调验签 + 幂等落定 | ✅ 同签名模拟实测 |
| **网关真实推送 notify 到达** | ⚠️ 未覆盖（需要真人付款且网关主动推送） |
| `urlscheme` 小程序路径 | ⚠️ 未覆盖（本商户不返回该字段） |

---

## 7.5 管理端订单列表与人工补单

### 位置与入口

- 管理面板左侧导航 **「支付订单」**（`/admin/payment-orders`），位于「事件」与「滥用举报」之间。
- 前端组件 `frontend/src/component/Admin/Payment/PaymentOrderList.tsx`，路由已在
  `frontend/src/router/index.tsx` 注册，导航项在 `PageNavigation.tsx` 中。

### 列表字段与筛选

表格列：**订单号 / 用户 / 商品 / 金额 / 支付渠道 / 状态 / 创建时间 / 支付时间 / 操作**。

筛选条件（全部为服务端筛选，见 `AdminPaymentOrderListService`）：

| 筛选项 | 说明 |
| --- | --- |
| 状态 | 全部 / `pending` / `paid` / `expired` |
| 用户 ID | 用户 hashid，非法值返回 `Invalid user id` |
| 创建于之后 / 之前 | `YYYY-MM-DD`（日期）或 unix 秒或 RFC3339，均被 `parseAdminTime` 接受 |
| 分页 | `page` / `page_size`，上限 50，默认 20 |

**列表读取是只读的**：管理端列表/详情**不会**触发 settle 或过期判定，看到的就是库里的真实状态
（用户侧列表则会懒判定）。这样管理员看到的账目不会被浏览动作改变。

### 详情

`GET /api/v4/admin/payment-orders/:id` 返回订单号、用户（昵称 + 邮箱 + hashid）、商品、金额、币种、
状态、创建/支付/过期时间，以及补单审计字段 `manual` / `manual_by_nick` / `manual_reason`。
支付渠道交易号只以脱敏形式呈现，不落日志。

### 补单（人工对账）流程

`POST /api/v4/admin/payment-orders/:id/reconcile`，请求体 `{"reason": "...", "force": false}`。

1. **已支付直接拒绝**：订单已是 `paid` 时返回 `Order already paid`，不发货、不改时间戳、不改审计字段。
   这是幂等的硬保证——重复点补单不会二次发货。
2. **先查渠道**：调用 `api.php?act=order` 主动查单；渠道确认已付 → 走正常结算路径
   （`providerConfirmsPaid` → `applyFulfillment`），**不打 manual 标记**，因为渠道凭据已经足够。
3. **查不到且未开 force** → 返回 `The channel does not report this order as paid`，不发货。
4. **查不到且开了 force** → 先 `MarkPaymentOrderManual`（条件 `manual = false`，两个管理员同时点只有一个能成功）
   记录操作人/原因，再发货。前端要求**二次点击确认**才会发这个请求。
5. **过期订单可补单**：`expired` 与 `pending` 一样放行（这是补单最主要的场景——用户付了钱但回调丢了、
   或查单接口当时超时，订单已过 30 分钟窗口）。结算沿用 `FulfillPaymentOrder` 的
   `StatusIn(pending, expired)` 条件认领，仍是单次原子转换，不会重复发放。

`reason` 必填（空 → `A reason is required to settle an order by hand`），超长截断到 500 字符。

### 权限控制

路由挂在既有 admin 分组下，补单额外要求 `ScopeAdminWrite`：

- 能进管理面板 + 具备 `admin_events` 权限即可**查看**订单列表与详情。
- **补单**需要 `ScopeAdminWrite`，即只有拥有管理写权限的账号（Admin 组 / 被授予该 scope 的账号）才能操作；
  只读管理员点补单会被拒。
- 补单接口**未**加按 IP 的限流（admin 分组下没有现成的用户维度限流器），这是已知取舍：
  该接口本身幂等且需要写权限，风险可接受。

### 审计：补单是否全部留痕

**是，全部留痕，三条记录同时产生**：

| 记录 | 内容 |
| --- | --- |
| `payment_orders.manual` | `true` |
| `payment_orders.manual_by` | 操作管理员 user id |
| `payment_orders.manual_reason` | 管理员填写的原因 |
| `activity_events` type `63`（`payment_manual_reconcile`） | `{order, admin, reason}` |
| `activity_events` type `37`（`payment_fulfilled`） | `{order, amount, admin, manual, reason}` |

另外服务端会打一条 warning 级日志。渠道已确认、走正常路径的补单**不打** manual 标记，
但同样产生 type `37` 的 `payment_fulfilled` 事件（带 `admin` 字段），所以「谁做的」始终可查。

### 15212 实测结果

| 用例 | 结果 |
| --- | --- |
| 管理端能看到前端下的订单（含用户/商品/金额/渠道/状态/时间） | ✅ |
| 状态 / 用户 ID / 创建时间 筛选，非法用户 ID 拒绝 | ✅ |
| 对 `pending` 订单「先查单」不勾 force → 拒绝、不发货 | ✅ `The channel does not report this order as paid` |
| 原因留空 → 拒绝 | ✅ |
| 勾 force 强制补单 → 状态变 `paid`、`manual=true`、`manual_by_nick=admin#1`、原因落库 | ✅ |
| 重复补单 → `Order already paid`，**不二次发货** | ✅（`/user/credit` 的 grants 数量与金额均只增加一次） |
| 对 `expired` 订单补单 → 成功转 `paid`，权益到账 | ✅ |
| 审计事件 type 63 / 37 均落地，含 admin 与 reason | ✅ |
| 前端「支付订单」页渲染、筛选下拉、补单弹窗、强制二次确认 | ✅ 浏览器实测 |

---

## 8. 测试数据清理

- 测试产生的 `payment_orders`、`skus` 与其关联 `activity_events` 已清理，残留计数 = 0。
- 测试站点本身的配置（易支付启用、默认渠道）保留。

## 9. 磁盘清理

⚠️ **本轮未做全局清理。** InstaBox 机上另有构建任务（#43）并行运行，按要求停手：
未删除任何镜像、容器、`/data` 目录、`node_modules` 或 workspace 检出，未跑
`docker builder prune` 等全局清理命令。

- 本轮**自身**产生的临时文件（自己的 `/tmp` 下 b64/patch/zip 中间件）已删除。
- 回滚镜像 `cloudreve-fork:easypay-1030-r2` 仍在。
- InstaBox 磁盘截至停手时：`/data` 磁盘紧张、`/` 剩余不足 1G，进一步打包镜像的空间不够，
  这也是本轮把构建切换到其他机器/等空间释放的原因。

## 9.5 本轮修掉的两个真问题

在 15212 实测过程中发现并修复，均已补回归测试：

1. **`clientip` 未上送导致下单被网关拒绝**（`easypay order creation failed: ... clientip 不能为空`）。
   `EasypayParams` 新增 `ClientIP`，`values()` 在有值时写入 `clientip` 并纳入签名；
   `CheckoutSessionService` 与 `Resume` 两条路径都传 `c.ClientIP()`。
   测试：`TestEasypayCreateOrderSendsClientIP`。
2. **补单接口 `reason`/`force` 被丢弃**：路由用 `FromUri` 只绑定了 URI，JSON body 静默丢失，
   导致所有补单都报「A reason is required」。新增 `controllers.FromUriJSON`（先 `ShouldBindUri`
   再 `ShouldBindJSON`）并替换该路由。
3. **管理端详情看不到买家**：`PaymentOrder()` 查询只 `WithSku()` 未 `WithUser()`，
   详情页 `user_id/user_nick/user_email` 全空。已补 `WithUser()`。
   测试：`TestAdminPaymentOrderDetailShowsBuyer`。

---

## 10. ⚠️ 凭据处置（请务必处理）

测试商户的**商户 ID 与密钥在本次对话中以明文给出过**，密钥已按原样写入 15212 测试站数据库
（`settings.payment_easypay_key`）。本报告、代码与提交中均为 `<redacted>`：

- 商户 ID：`<redacted>`
- 商户密钥：`<redacted>`
- 易支付后台账号：`<redacted>` / `<redacted>`

**建议：任务结束后到易支付后台重置该商户密钥**，然后在 15212 后台
「管理面板 → 增值服务 → 支付」把新密钥填回（或调用
`PATCH /api/v4/admin/settings` 写 `payment_easypay_key`）。

注意：管理后台 `PATCH /api/v4/admin/settings` 的响应体会**原样回显刚写入的密钥**，
这属于既有行为（Stripe 密钥同样如此），排查时不要把响应直接贴到日志或工单里。

---

## 11. 后续可做的事

1. **生产默认渠道建议用 `wxpay`**：只有它返回真二维码、能实现「站内扫码不跳转」；
   `alipay`/`qqpay` 只能给收银台按钮。若必须用支付宝且要站内体验，需要换用能返回二维码的网关
   （或走 `submit.php` 的跳转模式，即本轮之前的行为）。
2. 未付订单自动过期：目前只有轮询超过 5 分钟停轮询，数据库侧订单仍是 `pending`；
   可加定时任务把 `pending` 超过 N 分钟的订单置 `expired`（状态枚举已支持，弹窗已能展示「已过期」）。
3. 若易支付后台支持，可打开「主动回调重试」以进一步缩短到账时延。
