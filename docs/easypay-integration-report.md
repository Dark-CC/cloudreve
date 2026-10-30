# 易支付（easypay）现金支付渠道接入报告

日期：2026-10-30
分支：`tds/conv-01a18cb3-ed66-73ea-9280-b3e01d09ccc4`
测试站：https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech （15212 端口，InstaBox 机）
未发版：本次只做测试站验证，未打 tag、未跑 release workflow。

---

## 1. 做了什么

在 Cloudreve 社区 fork 里新增了一条独立于 Stripe 的现金支付渠道，走标准易支付协议
（`submit.php` 页面跳转 + MD5 签名 + 异步通知/同步跳转 + 查单兜底）。

### 后端

| 文件 | 改动 |
| --- | --- |
| `pkg/payment/easypay.go` | 新增。`EasypayConfig` / `EasypayParams` / `EasypaySign` / `EasypayBuildCheckout` / `EasypayParseNotify`、`EasypayGateway` 接口与实现、`ParseMoneyCents` / `FormatMoneyCents` |
| `pkg/setting/provider.go` | `PaymentSettings` 增加 `Easypay EasypaySettings`（Enabled/GatewayURL/PID/Key/PayType + `Ready()`），读取 `payment_easypay_*` |
| `inventory/setting.go` | 新增 5 个默认值；`RedactedSettings` 把 `payment_easypay_key` 列入脱敏 |
| `service/user/payment.go` | 拆出 `createStripeCheckout` / `createEasypayCheckout`；`CheckoutResponse` 增加 `Provider` / `Method` / `Params`；新增 `EasypayNotifyService`、`EasypayReturnService`；`settlePaymentOrder` 支持易支付查单结算 |
| `service/basic/site.go` | 新增 `payment_providers`，`payment_enabled` 改为「至少一个渠道可用」 |
| `routers/router.go` | callback 组新增 `GET easypay/notify`、`GET easypay/return` |
| `routers/controllers/callback.go` | `EasypayNotify`（成功返纯文本 `success`，失败 400 `fail`）、`EasypayReturn`（302 跳 `/shop?checkout=<hash>`） |

### 前端

| 文件 | 改动 |
| --- | --- |
| `frontend/src/api/user.ts` | `CheckoutSession` 增加 `provider` / `method` / `params` |
| `frontend/src/api/api.ts` | `checkoutSku(sku, provider?)` |
| `frontend/src/api/site.ts` | 站点配置增加 `payment_providers` |
| `frontend/src/component/Pages/Shop/Shop.tsx` | 按渠道列表出支付按钮；`method=POST` 时自建 hidden form 提交（易支付推荐 POST，防参数被中途劫持） |
| `frontend/src/component/Admin/Settings/VAS/VAS.tsx` | 易支付配置区块（网关、商户 ID、密钥、默认渠道）+ 渠道选择菜单加「易支付」 |
| `frontend/src/component/Admin/Settings/Settings.tsx` | 加载 `payment_easypay_*` 设置项 |
| `frontend/public/locales/{zh-CN,en-US}/*.json` | 新增文案（`buyWithEasypay` 与 6 条后台设置项） |

### 测试

- `pkg/payment/easypay_test.go`：签名排序/排除空值与 `sign_type`、已知答案签名、URL 拼接与字段校验、收银台模式、篡改与伪造通知拒绝、金额解析边界。
- `service/user/payment_easypay_test.go`：下单生成签名表单、双渠道必须指定渠道、未配置渠道拒绝、通知到账 + 重放幂等、伪造签名/金额不符/他人商户号/未知订单被拒、查单兜底结算、return 跳转。
- `go test ./pkg/payment/ ./service/user/ ./inventory/` 全绿（Baixiong 与 InstaBox 各跑一遍）。

---

## 2. 部署与验证结果

- 镜像：`cloudreve-fork:easypay-1030-r2`（当前运行）
- 可回滚镜像：`cloudreve-fork:fix-1030-r6`（上一版，容器 `cloudreve-fork-test-prev` 已停）
- 数据备份：`/data/workspace/backup-easypay-20261030-122341/{cloudreve.db,conf.ini}`（部署前用 `.backup` 导出）

### 已跑通的链路

1. **下单**：`POST /api/v4/user/shop/checkout` 返回 `provider=easypay`、`method=POST`、
   `url=https://pay.urldwz.com/submit.php`、`money=0.01`、公网 `notify_url` / `return_url`、32 位签名。
2. **网关验签**：把同一组参数原样 POST 给 `submit.php` → 网关接受并返回支付宝收银台自动提交表单
   （`action='https://openapi.alipay.com/gateway.do'`）。**签名实现与网关一致**。
   注意：少传任一参与签名的字段（例如 `param`）会被网关判 `签名校验失败`，属预期。
3. **网关侧建单**：`api.php?act=order` 查到 `trade_no=20261030202845…`、`status=0`（未付）。
4. **回调结算**：用网关同款签名模拟 `TRADE_SUCCESS` 通知 → 返回 `success`、订单转 `paid`，
   日志 `Payment order 1 fulfilled via easypay`，并写入审计事件。
5. **拒绝路径**：签名错误 → HTTP 400 `fail`、订单不变；金额不符 / 他人商户号 / 未知订单均拒绝。
6. **前端全流程**（headless chromium 实测）：登录 → 店铺「流量包」标签 → 出现 `支付 ¥0.01` 按钮 →
   点击触发 **POST** `https://pay.urldwz.com/submit.php` → 网关跳到
   `https://excashier.alipay.com/standard/auth.htm?payOrderId=...`。

### 没有跑通的一步

**真实付款到账没有完成**：网关已把我们带到支付宝收银台，剩下需要真人扫码支付 ¥0.01。
扫码完成后易支付会向 `notify_url` 推通知，服务端这段已用同签名模拟验证过，
但「真人扫码 → 网关真实推送」这一跳未实测。

---

## 3. 测试数据清理

- `payment_orders`、`skus` 已清空（测试 SKU 与两笔订单删除）。
- 相关 `activity_events` 已删除，验证残留计数 = 0。
- 测试站点本身的配置（易支付启用）保留。

## 4. 磁盘清理

| 机器 | 清理前 | 清理后 | 释放 |
| --- | --- | --- | --- |
| InstaBox `/`（根分区） | 剩余 182M（99%） | 剩余 2.6G（83%） | ≈ 2.4G |
| InstaBox `/data` | 剩余 2.8G（86%） | 剩余 5.6G（72%） | ≈ 2.8G |
| Baixiong `/` | 剩余 68G（30%） | 未变 | 工作树仅 73M，无需清理 |

清理内容：旧镜像（`r3`/`r4`/`r5`/`easypay-1030`）、已停容器、`frontend/node_modules`、
前端 `build`、Go 产物、构建日志、apt/journal 缓存。保留当前运行镜像与 r6 回滚镜像。

---

## 5. ⚠️ 凭据处置（请务必处理）

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

## 6. 后续可做的事

1. 真人扫码完成一次真实 ¥0.01 支付，确认网关真实推送也能结算（服务端逻辑已验证，只差这一跳）。
2. 生产上线前把 `payment_easypay_type` 留空以走网关收银台（让买家自选支付宝/微信/QQ），
   或按需指定 `alipay`/`wxpay`/`qqpay`。
3. 如需自动过期未付订单，可加一个定时任务把 `pending` 超过 N 分钟的订单置 `expired`
   （`PaymentOrder` 状态枚举已支持）。
