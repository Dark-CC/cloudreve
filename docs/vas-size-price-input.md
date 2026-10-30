# VAS 数值输入修复：容量单位 + 小数单价

> 状态（2026-10-30）：**代码与测试完成，真机验证未完成**。
> 原因：InstaBox 磁盘被三条并行构建挤爆（`/data` 一度 100%），按用户指示暂停本机重型构建，改为在 Baixiong 上继续。

## 1. 问题

`/admin/settings?tab=vas` 的增值服务弹窗有两类缺陷，且在用户的真实部署上复现：

### 1.1 容量/流量没有单位

添加容量包、流量包、流媒体流量包时，只能填一个纯数字，单位固定隐含为「字节」。

**真机复现证据**（InstaBox 15212，镜像 `cloudreve-fork:easypay-1030-r2`，即修复前）：

```
添加容量包            unitPicker: 0   label: "大小"
添加流量包            unitPicker: 0   label: "流量大小（字节）"
添加流媒体流量包      unitPicker: 0   label: "流量大小（字节）"
添加可购用户组        unitPicker: 0   （有效期天，本就无单位需求）
```

`unitPicker: 0` 表示弹窗内不存在任何单位下拉。截图与 JSON 在 `shots/before-*`。

### 1.2 单价不能填小数

价格输入框用整数解析，填 `0.01` 实际得到 `0`。同一个探针的输出：

```
添加容量包   typedPrice001: "0"
添加流量包   typedPrice001: "0"
添加用户组   typedPrice001: "0"
```

`0` 在数据模型里语义是「不可现金购买」，所以这不是显示问题，而是把一次有效定价静默变成了「禁用」。

## 2. 根因

| 位置 | 问题 |
| --- | --- |
| `ent/schema/sku.go` | `price int64` 存的是**最小货币单位（分）**，不是元 |
| `service/admin/vas.go` | `SkuUpsertService` 直接收 `*ent.Sku`，前端传什么就存什么，没有单位换算 |
| `frontend/.../VAS/SkuTable.tsx` | 价格用整数解析，未按 `payment_currency_unit` 换算；容量只有一个裸数字输入 |
| `frontend/.../VAS/GiftCodes.tsx` | 字节类兑换码数量同样是裸数字 |

货币单位来自站点配置 `payment_currency_unit`（默认 `100`，即 1 元 = 100 分）。`Shop.tsx` 展示时已做 `price / paymentCurrencyUnit`，说明「存分、显示元」是既有约定，只是**编辑侧没有遵守**。

## 3. 修复

### 3.1 新增前端工具

- `frontend/src/util/byteSize.ts` — `ByteSizeUnit`（KB/MB/GB/TB）、`byteSizeUnitFactor`、`bestByteSizeUnit`、`toBytes`、`splitBytes`、`trimNumber`。
  `splitBytes` 为既有字节值挑选最合适的单位，保证**已存在的 SKU 打开时数值无损**。
- `frontend/src/util/price.ts` — `toMinorUnits`、`fromMinorUnits`、`smallestPriceStep`。
  `toMinorUnits` 对正数取 `Math.max(1, Math.round(major * scale))`，避免 `0.001` 这类输入被四舍五入成 `0` 而静默变成「不可购买」；`0` 仍然返回 `0`（语义保留）。
- `frontend/src/component/Common/ByteSizeInput.tsx` — 数值 + 单位下拉的组合输入，支持小数。

### 3.2 组件接入

- `SkuTable.tsx`：容量/流量改用 `ByteSizeInput`；价格编辑用**主单位**（`fromMinorUnits` 回填、`toMinorUnits` 提交），`step="any"`、`min=0`；表格价格列 `${currencyMark}${fromMinorUnits(...).toFixed(2)}`。
- `GiftCodes.tsx`：新增 `isByteAmount`（storage/traffic/stream_traffic），字节类数量用 `ByteSizeInput`，积分/用户组仍为整数输入。

### 3.3 新增后端金额换算

- `pkg/payment/money.go`
  - `ScaleToCents(amount, chargedCents int64) (int64, bool)` — 由 `scaleToCents` 导出，用于支付回调对账。
  - `OrderCents(amount int64, currencyUnit int) int64` — 下单金额换算；`currencyUnit <= 0` 或 `== 100` 原样返回，否则 `amount*100/unit`。零小数货币（JPY/KRW，`unit=1`）也走这条路径。
- `service/user/payment.go`：`scaleToCents` 委托给 `payment.ScaleToCents`；下单 `Money` 改用 `payment.OrderCents`。
- `service/admin/vas.go`：`upsertSku` 增加字段校验（`Amount<=0`、`Duration<0`、`Price<0`、`Weight<0` 报错；`Points` 非空且 `<=0` 报错；用户组类型校验目标组存在）。

收敛到 `pkg/payment/money.go` 的原因：下单与回调对账必须用**同一套**换算，否则小数价格下两边会算出不同金额。

### 3.4 i18n

13 个 locale 的 `dashboard.json`：
- `vas.trafficSize` 去掉「（字节）」
- `vas.trafficSizeDes` / `vas.packSizeDes` 说明可选 KB/MB/GB/TB
- 新增 `vas.priceStoredAs`、`vas.priceStepHint`

## 4. 测试

`pkg/payment/money_test.go`（新增）：`TestOrderCents`、`TestOrderCentsFormatsSmallestPrice`、`TestScaleToCents` — 全部 PASS。

`service/user/payment_easypay_test.go`（追加）：`TestEasypaySmallestPriceCheckoutAndNotify`、`TestEasypayCheckoutHonoursCurrencyUnit`。

Baixiong 上复核：

```
$ go test ./pkg/payment/
ok  github.com/cloudreve/Cloudreve/v4/pkg/payment  0.017s
```

`go vet ./pkg/payment/ ./service/admin/ ./service/user/` 无告警。
`go build ./...` 在 Baixiong 上因仓库缺 `application/statics/assets.zip`（`go:embed` 目标）而中断，属预期 —— 该文件由前端构建产出，本分支不含构建产物。

## 5. 未完成：真机验证

修复后的镜像已在 InstaBox 上构建并部署过一次（`cloudreve-fork:vas-1030-r3`，`/admin` 返回 200，静态资源版本校验通过），但**验证探针没有跑完**就因磁盘压力停机：

- 探针在「容量包」一步失败：`waiting for getByRole('option', { name: /容量包|Storage/i })` 超时。
  这可能是弹出菜单里选项文案与探针假设不符（探针自身问题），也可能是修复未生效 —— **当前无法判定，必须重跑才能下结论**。
- 已确认的部分：`/admin` 可访问、后端启动无静态资源告警。

因此 §1 中「修复前」的复现证据是完整的，而「修复后」的对照证据**缺失**。

## 6. 继续所需条件

1. 一台有空闲磁盘的构建机（Baixiong 67G 空闲可用，但只有 2 核，前端打包需 `NODE_OPTIONS=--max-old-space-size` 控制）。
2. 构建顺序必须是：**先放好 `frontend/build` 并重新生成 `application/statics/assets.zip`，再 `go build`** —— `assets.zip` 由 `go:embed` 编进二进制，先编后端再换 zip 是无效的（这一坑已踩过一次）。
3. `assets.zip` 内路径必须为 `assets/build/...`（`statics.go` 用 `fs.Sub(statics, "assets/build")`）。
4. `version.json` 中 `name` 必须为 `cloudreve-frontend`、`version` 必须等于后端 `constants.BackendVersion`，否则静态资源校验失败、前端 404。
5. 修好探针里容量包菜单的选项选择器后重跑，补齐「修复后」对照。
6. 补验：易支付下单金额的小数精度（含 `currencyUnit=1` 的零小数货币路径）。
7. `CreateGiftCodeService.Amount` 的 `binding:"required,min=1"` 是否阻挡小数字节兑换码 —— 需实测确认。

## 7. 构建过程记录（踩坑）

- InstaBox `/data` 与根分区双双 100% 时，`go build` 会**静默返回 `EXIT=0` 但不产出二进制**（临时目录写不进去）。必须把 `TMPDIR`/`GOTMPDIR` 指到有空间的分区，并**核对产物文件是否存在**，不能只看退出码。
- 期间我清理 `yarn` 缓存（2.5G）与 `go clean -cache` 释放了空间，随后 `/data` 被另外两条并行构建再次占满。按 2026-10-30 用户指示，之后**不再清理 InstaBox 上任何非本次构建产生的文件**。
