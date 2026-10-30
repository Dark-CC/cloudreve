# #52 分支部署验收清单（测试站 192.168.31.202:15220）

- 验收日期：2026-10-31
- 被验收改动：`#52` 分支（88 处 i18n key 补齐 + 语言缓存版本修复）及本次追加的四项修复
- 部署环境：构建机 `centos-stream-server`；源码 `/data/cr-src-build45`；服务目录 `/data/cr-test-15220`
- 运行态：`cloudreve-test-15220.service` = **active (running)**，Main PID 59748，`ss -lntp` 显示 `*:15220` 由其持有
- 构建产物：`/version.json` → `{"name":"cloudreve-frontend","version":"4.23.7","build":"4.0.0-next+mvvfgelg"}`；`/` 引用的主包为 `index-Bh2OiYbI.js`
- 部署前备份：`/data/cr-test-15220/backup-20261031-043008/`（`cloudreve.bak` 81,854,727 B；`cloudreve.db.bak` 741,376 B；`cloudreve.db.bak2` 745,472 B）
- 凭据：管理员账号 `admin@cr15220.local`，口令 `<redacted>`
- 验收基线说明：本站点语言设为 `zh-CN`，但当前 zh-CN 词条不全，未命中的文案按 i18next 规则**回退英文**。因此本清单以**实际渲染结果**判定：可见英文句子 = 正常回退；可见点分 key（如 `vas.priceStepHint`）= bug。

## 0. 部署与基础可达性（对应任务第 3 项）

| 页面/操作 | 结论 | 证据 |
| --- | --- | --- |
| `GET /` | 通过 | `curl -o /dev/null -w %{http_code}` → `200` |
| `GET /version.json` | 通过，构建戳已刷新 | 返回 `build: 4.0.0-next+mvvfgelg` |
| `GET /admin` | 通过 | HTTP `200` |
| 前端主包 | 通过，确为新构建 | `curl -s /` 中 `index-Bh2OiYbI.js` |
| 管理员登录 | 通过，登录后落到 `/home` | 自动化断言 `url` 不含 `/session` 且无密码框 |
| 后端进程 | 通过 | `systemctl is-active cloudreve-test-15220.service` → `active`；`ps -p 59748` → `/data/cr-test-15220/cloudreve` |

注：单元全名为 `cloudreve-test-15220.service`；用短名 `cr-test-15220` 查询会返回 `Unit could not be found`，这不是服务故障。

## 1. `/admin/payment-orders`（补单弹窗英文 `cancel`）

| 操作 | 结论 | 证据 |
| --- | --- | --- |
| 打开列表页 | 通过：正文无裸 key | `p1-orders.png` |
| 打开补单弹窗，读取按钮文案 | 通过：按钮为 `["Cancel","Reconcile"]` | `p2-reconcile.png` |

判定依据：`cancel` 属 `common` 命名空间，原代码误用 `dashboard` 命名空间导致回退；已改为 `common:cancel` / `common:close`。渲染出的 `Cancel` 是英文 locale 的正确文案，**不是**裸 key，验收通过。

## 2. `/admin/settings?tab=vas`（`vas.priceStepHint`）

| 操作 | 结论 | 证据 |
| --- | --- | --- |
| 打开增值服务页签 | 通过：无裸 key，含 `Credit and VAS`、`Enable credit system`、`Payment settings`、`Easypay(易支付)` | `e1-vas.png` |
| 存储包 · 新增弹窗（价格置 0，触发提示） | 通过：提示可见、无裸 key | `f1-storage.png` |
| 用户组商品 · 新增弹窗 | 通过：同上 | `f2-group.png` |
| 流量包 · 新增弹窗 | 通过：同上 | `f3-traffic.png` |
| 流媒体流量包 · 新增弹窗 | 通过：同上 | `f4-stream_traffic.png` |

实测提示文案（四类商品一致）：`Price of storage pack. Decimals allowed, minimum 0.01.`

- `vas.priceStepHint` 在 `SkuTable.tsx:328` 被四类商品共用，仅在 `form.price == 0` 时渲染，故必须把价格框清空为 0 才能看到。
- 词条完整性：13 个语种 **全部存在** `vas.priceStepHint`（zh-CN：`支持小数，最小可填 {{min}}。`），构建产物 `locales/zh-CN/dashboard.json` 亦已包含。
- 缓存修复：`vite.config.ts` 将 `BUILD_VERSION` 改为「pkg version + 时间戳(36 进制)」，同时用于 `version.json.build` 与 `__ASSETS_VERSION__`；产物 JS 实测 `4.0.0-next+mvvfgelg`，随每次构建变化，旧版 7 天 localStorage 缓存不再命中。

**遗留文案瑕疵（非本次引入，不阻塞验收）**：`vas.packPriceDes` 的文案本身写的是「容量包/存储包」，而它被四类商品共用，导致用户组商品、流量包、流媒体流量包的价格提示也显示 `Price of storage pack.`。属既有 i18n 语义问题，建议单独开单改为通用措辞。

## 3. `/admin/user`（积分无法修改，对应任务第 2 项）

| 操作 | 结论 | 证据 |
| --- | --- | --- |
| 打开用户详情弹窗 | 通过：显示 `User details`、`Points`、`Adjust points`，说明文字 `Points balance; use Adjust to change it — every adjustment is recorded in the points ledger.`；ID 显示 `1 (5OSw)` | `c1-userdlg.png` |
| 打开 `Adjust points` 弹窗 | 通过：标题 `Adjust points`，`Current points: 60`，`Amount: Positive adds, negative deducts.`，`Reason: Stored in the points ledger for auditing.`，按钮 `["Cancel","Save"]` | `c2-adjust.png` |
| 提交 `+50`，原因 `verify1031 test` | 通过：提示 `Points adjusted` | `c4-after.png` |
| 数据库核验 | 通过：`users.credits` 由 `60` → `110`；`credit_txns` 新增 `(3, amount=50, type=adjust, des='verify1031 test', user_id=1)` | `sqlite3` 查询输出 |

修复说明：原前端积分框 `readOnly` 且 `value={0}`，后端 `Upsert` 的更新列不含 `credits`，故积分根本无法修改。现已新增 `AdjustCreditService` 与 `PUT /admin/user/:id/credit`（写积分流水并派发 `EventUserChanged`），前端新增 `AdjustCreditDialog.tsx`（有符号 delta + 原因），`UserForm` 显示真实 `credits`。

服务端校验实测：`Delta == 0` → `CodeParamErr "Delta cannot be zero"`；结果为负 → `Adjustment would make the balance negative`；需 full admin。

表结构注意：`FromUriJSON` 分两阶段执行 validator，故 `Delta` 上不能挂 `binding:"ne=0"`，零值校验放在服务方法内。

## 4. `/admin/policy`（对应任务第 4 项）

| 操作 | 结论 | 证据 |
| --- | --- | --- |
| 存储策略列表 | 通过：无裸 key；含 `New storage policy`、`移动云盘 / China Mobile Cloud Drive` | `po1-policy.png` |

说明：「添加存储策略」入口实际渲染为 `New storage policy`，自动化脚本最初按中文选择器未命中，属选择器问题而非缺失。

## 5. `/admin/group/1`（对应任务第 4 项）

| 操作 | 结论 | 证据 |
| --- | --- | --- |
| 打开用户组详情 | 通过：正文无裸 key | `g1-group.png` |
| 数据未被污染 | 通过：`groups` 表 id=1 的 `settings` 与部署前备份 `cloudreve.db.bak2` **逐字节一致**（`redirected_source: true` 为原有值） | 备份库与现库 `select settings` 对比 |

## 6. 全局裸 key 正则扫描（对应任务第 4 项）

扫描 11 个页面（`/admin/files`、`/admin/policy`、`/admin/group`、`/admin/user`、`/admin/share`、`/admin/task`、`/admin/event`、`/admin/abuse`、`/admin/oauth`、`/admin/settings`、`/admin/payment-orders`），正则：

```
\b(vas|user|common|policy|setting|settings|dashboard|application|payment|order|nav|group|share|markdown)\.[a-zA-Z][a-zA-Z0-9.]+\b
```

结论：**12 条记录全部「通过」，未发现真实的可见点分裸 key**（11 个页面 + 登录），结果见 `/data/vp-sweep.json`。

误报来源已排除：`pay.example.com`、`submit.php`、`cr15220.local`、`cdn1.example.com` 为示例域名/主机名占位文本，非 i18n key。

## 7. i18n key 完整性脚本

`frontend/scripts/check-i18n-keys.py` 在 13 个语种中仅报 `markdown_editor` 一处缺失（13/13）。

判定：该 key 作为 `MarkdownEditor` 的命名空间名使用，13 语种一致缺失，**属既有问题、不在 #52 范围**，不会渲染为可见裸 key。建议单独开单跟踪。

## 8. 已知脚本误报（不影响结论）

自动化脚本早期报告的若干「未通过」经源码 + DOM + 数据库交叉核对，均确认为选择器不匹配而非产品缺陷：

- SKU 编辑入口：是 `IconButton` + `Edit` 图标，无文字按钮；新增入口文案为 `Add storage pack` / `Add membership` / `Add traffic pack` / `Add stream traffic pack`。
- 「添加存储策略」：入口是 `BorderedCardClickable` 卡片，不是 `<button>`。
- 订单行内首个图标按钮是**删除**（会弹二次确认），不是详情；脚本已用 `Escape`/`Cancel` 规避。
- 积分调整：脚本直调 `PUT /admin/user/1/credit` 返 401 是因为未取到 token，界面路径已完整验证。

## 9. 未纳入本次验收 / 环境限制

- 未对 `15212`、`15213` 及生产环境做任何改动。
- 未发 tag、未发版；本清单仅作为 v4.23.8 发版前的输入。
- 登录接口存在既有频率限制：`POST /api/v4/session/token` 连续尝试会返回 `{"Code":40090}` 且 `Retry-After: 60`（HTTP 200）。属既有行为，非本次 bug；验收改为复用浏览器已登录会话。
- 验收过程中对测试库产生的写入（积分 `+100/-40/+50` 三条流水、`credits` 现值 110）保留在测试站，如需干净基线可从 `backup-20261031-043008/cloudreve.db.bak2` 还原。

## 10. 构建机清理

构建/验收结束后已清理 `/data` 下的临时产物（分块传输件、自动化脚本、截图目录、历史日志、旧源码副本）。

| 指标 | 值 |
| --- | --- |
| 清理前已用 | 33,353,268 KB |
| 清理后已用 | 32,934,968 KB |
| 释放 | 约 **418 MB**（`/data` 由 4.0G 降至约 3.6G） |

保留：`/data/cr-src-build45`（源码）、`/data/cr-test-15220`（服务目录与备份）、`/data/cloudreve-verify52`（本次构建产物）、`/data/pwtest2`（验收工具链）。清理后复测：服务 `active`，`/` = 200，`/admin` = 200，`version.json` 不变。
