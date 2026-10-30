# i18n 漏翻排查与补齐（vas.priceStepHint / paymentOrders.userSearch / 全站扫描）

日期：2026-10-31
范围：`frontend/src` 全量（2327 个从源码抽出的 i18n key），13 种语言

## 一、两处报障的真实原因

### `vas.priceStepHint`（后台设置 → 存储包 → 编辑容量包 → 单价提示）

- **不是 key 缺失**。`vas.priceStepHint` 在 13 种语言里都存在，zh-CN 值为
  `支持小数，最小可填 {{min}}。`；调用点在
  `frontend/src/component/Admin/Settings/VAS/SkuTable.tsx:328`，命名空间
  `useTranslation("dashboard")`，路径解析完全正确。
- 线上部署的 `/locales/zh-CN/dashboard.json` 实测 `vas` 有 151 个键，
  `priceStepHint` 取值正常。
- **真实原因：前端 i18n 的 localStorage 缓存永不失效。**
  `src/i18n.ts` 生产环境用 `LocalStorageBackend` 打头（缓存 7 天），
  失效版本号取 `defaultVersion: __ASSETS_VERSION__`，而该常量在
  `vite.config.ts` 里定义为 `process.env.npm_package_version`，
  即 `package.json` 的 `"version": "4.0.0-next"` —— 这个值自 fork 起
  从未变化。于是每次部署共享同一个缓存键，早于该 key 加入就访问过的浏览器，
  一直读到旧缓存包，新 key 在页面上直接回显成 `vas.priceStepHint` 原文。
  `userSearch` 是同一个提交（`fcea4c2d` / `ab364552` 之后）新增的，
  同样被旧缓存遮蔽。
- **修复**：`vite.config.ts` 引入 `BUILD_VERSION = <pkg version>+<构建时间戳>`，
  同时用于 `version.json` 与 `__ASSETS_VERSION__`，每次构建都换缓存键。

### `paymentOrders.userSearch`（订单页筛选弹层 → 用户搜索框）

- 同上的缓存问题。key 在 13 种语言齐全，zh-CN 为 `用户 ID 或邮箱`，
  线上 JSON 实测正常。

### `vas.priceStepHint` 覆盖四类商品

- 确认是**共用组件**：`SkuTable.tsx` 的 `type: "storage" | "group" | "traffic" | "stream_traffic"`
  四类商品共用同一段价格输入（`priceStepHint` 只有一处调用），
  改一处即四类同时生效，无需分处补。

## 二、全站扫描结果

新增核对脚本：`frontend/scripts/check-i18n-keys.py`

- 从 `src/**/*.{ts,tsx}` 抽取 `t("...")`、`i18next.t("...")`、`i18nKey="..."`、`<Trans i18nKey>`；
- 解析 `useTranslation("ns", { keyPrefix })` 的命名空间与前缀（不处理 keyPrefix 会产生
  大量误报，例如 `uploader.retry` 等 81 个键）；
- 支持 `returnObjects: true` 的数组型键；
- 逐语言在 `public/locales/<lng>/<ns>.json` 里解析，报告缺失。

扫描规模：源码抽出 **2327** 个 key；13 种语言。

补齐结果（以 zh-CN / en-US 为基准，其余 11 种同步补齐）：

| 命名空间 | 新增 key 数 | 说明 |
|---|---:|---|
| `shop.*` | 26 | 商店订单页（#44 引入，原仅 en-US） |
| `policy.*`（y139 系列） | 9 | 移动云盘策略说明/提示（#43 引入，11 种语言缺） |
| `summary.*` | 5 | 后台首页凭据过期提示 |
| `fileManager.*` | 3 | 批量下载限制说明 |
| `modals.*` | 1 | 文件夹直链限制 |
| `vas.*` | 1 | 举报原因选项 |
| `file.*` | 1 | 导入告警 |
| **合计** | **46** | 涉及 26 个 locale 文件，1395 insertions |

修正的既有问题：`zh-CN` 的 `shop.*` 整段原为英文、`zh-TW` 有 29 处英文
（含 `summary.*`、`policy.y139*`、`shop.resumeFailed`），已按中文/繁体补齐；
剩余的 `WebDAV`、`AK`、`SK`、`Endpoint`、`OneDrive` 等为专有名词，保留原文。

扫描终态：**0 个真实缺失**（唯一残留 `markdown_editor:` 是
`t("markdown_editor:" + key)` 的动态拼接前缀，非真实 key，脚本已注明并排除）。

## 三、验证方式

- `python3 scripts/check-i18n-keys.py src` → `MISSING: 1`（仅上述动态前缀）
- 13 × 5 个 locale JSON 全部通过 `json.load` 合法性校验
- 线上 `/locales/zh-CN/dashboard.json` 与 `/locales/en-US/dashboard.json`
  逐键核对 `vas.priceStepHint`、`paymentOrders.userSearch` 取值正常

## 四、给维护者的提醒

若再次出现「locale 文件里有、页面却显示点分名」，先查
`__ASSETS_VERSION__` 是否随构建变化，再查 key 是否存在 ——
本仓库历史上有过两次同类误判。
