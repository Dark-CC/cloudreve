# 天翼云盘（189）登录态实测报告 —— 凭据到位前的部分实测 + 完整探测方案

> 探测日期：2026-10-31　|　机器：`centos-stream-server`（192.168.31.202）
> 性质：**只读探测**　|　状态：**协议层基线与失效语义已实测确证；账号级 4 项仍待凭据**
> 关联：#56 可行性研究（`docs/189-enterprise-drive-feasibility.md`，分支 `tds/conv-01a191ec-5b1a-75d0-a5cc-2f99a057c14c`）
> 凭据：`CT189_LOGIN_COOKIE`　|　本报告中**一律占位符 `<COOKIE_REDACTED>`，无任何真实凭据、地址或响应原文**

---

## 0. 本轮结论速览

| # | #56 遗留问题 | 本轮状态 |
| --- | --- | --- |
| 1 | cookie 换 `sessionKey`/`sessionSecret` 的路径与有效期 | ⛔ **未完成**（凭据未到位）；**换取路径已实测收敛到两条，见 §3** |
| 2 | 真实 CDN 地址裸取是否可行（**最关键**） | ⛔ **未完成**（需账号内一个真实文件） |
| 3 | 真实地址 TTL | ⛔ **未完成** |
| 4 | 分片 URL 能否批量预签、一次几片 | ⛔ **未完成** |
| 5 | session 过期窗口与失效错误码 | 🟡 **错误码已实测确证**（§4.1）；**过期窗口仍需凭据观测** |
| 6 | 家庭云与个人云差异 | ⛔ 本轮未验证（报告 §6 给出低成本判据） |

**唯一阻塞原因**：环境变量 `CT189_LOGIN_COOKIE` 在本 agent 的构建 shell 与 `centos-stream-server` 远端 shell 中**均不存在**（`printenv` 长度 0，`/proc/self/environ` 中无该键，进程环境变量总数 34 且不含任何 `CT*` 变量）。密钥已在密钥库（id `w1u3wQEQMq_LpYWCCWM42`），授权变更按平台规则「下一个 Turn 起生效」，本轮开始时尚未注入。

> 因此本轮**没有**对账号发起任何带凭据的请求，也**没有**创建任何 `cr-test-*` 目录（无从创建），未触碰账号内任何数据。
>
> **复检记录**：用户在本轮中途回复「已单独开通这一个密钥的读取权限」，本轮内又复检 3 次（含 `/proc/self/environ` 直读），变量仍为空——说明单个密钥的白名单授权同样按「下一个 Turn 起生效」，与本轮的启动时刻无关。下一次任务启动时应先跑一条 `printenv CT189_LOGIN_COOKIE | wc -c` 确认非 0，再执行 §7 步骤。

---

## 1. 已实测确证的事实（不依赖账号，可复核）

### 1.1 网络与工具链

| 项 | 实测结果 |
| --- | --- |
| 直连 `cloud.189.cn` | ✅ `200`，1.02 s |
| 经代理 `192.168.31.200:1082` | ✅ `200`，0.51 s |
| `api.cloud.189.cn` 可达 | ✅ 返回业务错误（`400`），说明域名与接口存活 |
| 工具 | `curl` / `python3` / `openssl 3.5.9` / `jq` 齐备；无 `pycryptodome`、无 `cryptography` → **AES-ECB 改用 `openssl enc` 实现**，已往返校验 |

### 1.2 两套鉴权并存 —— 这是 #56 未点明的关键结构

实测发现天翼云盘**网站侧**与**客户端 API 侧**是两套完全独立的鉴权，任何「cookie → session」方案都必须跨过这道坎：

| 域名族 | 鉴权方式 | 无凭据时的错误（实测原文） |
| --- | --- | --- |
| `cloud.189.cn/api/*.action` | **Cookie 会话** | `{"errorCode":"InvalidSessionKey","errorMsg":"cookieUserSession is null or invalid, cookieUserSession=null"}` |
| `api.cloud.189.cn/*.action` | **HMAC-SHA1 签名**（`SessionKey`/`Signature`/`Date`） | `{"errorCode":"InvalidArgument","errorMsg":"date/signature is null"}` |

- 前者吃 `COOKIE_LOGIN_USER`，后者**不吃 cookie**，只认 `sessionKey`+`sessionSecret` 签名。
- 这解释了为什么「有 cookie」不等于「能用 `api.cloud.189.cn`」——中间必须完成一次**换发**。

### 1.3 签名层实现已就绪并自测通过

按 AList `drivers/189pc/utils.go` 的规则实现并验证：

- **HMAC-SHA1**：`Signature = UPPER(HMAC_SHA1(sessionSecret, "SessionKey=..&Operate=GET&RequestURI=..&Date=..[&params=..]"))` —— 已与参考实现比对一致。
- **AES-ECB + PKCS7**：密钥取 `sessionSecret[:16]`，加密后转 hex 填入 `params` —— 已用 `openssl enc -d` 往返还原，原文完全一致，PKCS7 填充值校验通过。
- **脱敏**：cookie 整串、分号分隔的每一段、其值、以及任何 ≥32 位十六进制串在输出前一律替换为占位符 —— 已用含假凭据的字符串验证，凭据片段无一泄漏。

### 1.4 #56 的一条接口路径已过时（对实现的直接影响）

| 接口 | #56 记录 | 本轮实测 |
| --- | --- | --- |
| `open.e.189.cn/api/logbox/oauth2/unifyLoginForPC.action` | 密码登录第一步，取 `lt`/`reqId` | ❌ **404**（页面「天翼用户中心-页面没有找到」） |
| `open.e.189.cn/api/logbox/oauth2/qrcodeLogin.do` | — | ❌ **404** |
| `open.e.189.cn/api/logbox/oauth2/qrcodeGenerate.do` | — | ❌ **404** |
| `open.e.189.cn/api/logbox/oauth2/loginSubmit.do` | 密码登录 | ✅ `200` |
| `open.e.189.cn/api/logbox/config/encryptConf.do` | RSA 公钥 | ✅ `200` |
| `open.e.189.cn/api/logbox/oauth2/needcaptcha.do` | 验证码判定 | ✅ `200` |
| `cloud.189.cn/web/main` | 网页入口 | ✅ `200`，静态资源已迁至 **`cloud.dlife.cn`** |

> **影响**：这类私有接口变动频繁（本次已见 3 个 404 + 静态域名迁移）。对 y189 驱动的含义是——**不要把登录/换发路径写死**，并且 #56 §3.4「密码登录路线不推荐」的结论**本轮得到进一步支持**：入口连 `unifyLoginForPC` 都没了，密码路线成本只会更高。

---

## 2. 探测脚本（已写好，凭据一到即可一次跑完）

脚本 `p189.py` 的要点：

- 只从 `CT189_LOGIN_COOKIE` 读凭据，**不写入任何文件、不打印**；
- 全流程**只读**：`getUserInfo` / 换发 / `listFiles`（`pageSize=5`）/ `getFileDownloadUrl` / 对已有文件的 GET 与 Range 探测；
- **不建目录、不删文件、不上传**（原计划的上传分片测试若要执行，须先经用户同意在其账号建 `cr-test-*` 内容——见 §5）。

探测顺序与产出：

> 脚本位于 `scripts/189probe/p189.py`，已通过三项自测：HMAC-SHA1 与参考实现一致、AES-ECB/PKCS7 经 `openssl enc -d` 往返还原一致、脱敏对假凭据无泄漏。

| 步骤 | 动作 | 回答哪一项 |
| --- | --- | --- |
| 1 | `cloud.189.cn/api/portal/getUserInfo.action` 带 cookie | cookie 是否有效；网站侧是否顺带下发 session 字段 |
| 1b | cookie → `accessToken` → `getSessionForPC.action` | 问题 1 的换取路径 |
| 2 | 带签名调 `listFiles.action`（`folderId=-11`，根目录取 5 条） | 签名逻辑是否正确（同时验证 §1.3） |
| 3 | `getFileDownloadUrl.action`（取列表里第一个文件） | 问题 2 的前提 |
| 4 | 对返回地址**禁止跟随**发，读 `Location` | 拿真实 CDN 地址 |
| 5 | **裸取**：无 Cookie、无 Referer、仅 UA、再加 Range | **问题 2（最关键）** |
| 6 | 间隔 +0/60/300/900 s 重复取 | **问题 3 TTL** |

---

## 3. 问题 1：cookie 换 sessionKey/sessionSecret —— 已收敛到两条路径

#56 只写了「cookie → accessToken → `getSessionForPC`」一条。本轮实测确认**存在两条候选**，且第二条此前未被记录：

| 路径 | 形态 | 实测依据 |
| --- | --- | --- |
| **A. `/api/portal/getUserInfo.action` 直读** | 若响应体直接含 `sessionKey`/`sessionSecret`，则一步到位 | 该接口已确认吃 cookie 会话（错误信息为 `cookieUserSession`），是否下发 session 字段**待实测** |
| **B. cookie → `accessToken` → `getSessionForPC.action`** | 两跳；`getSessionForPC` 必须走 `params`(AES) 加密，**裸调只报 `params is null`** | 已实测：`getSessionForPC.action` 无论补多少签名头，缺 `params` 一律 `InvalidArgument: params is null` |

> **对 #56 的修正**：#56 §3.1 把 `getSessionForPC` 描述成「GET 带 `accessToken` 查询参数即可」。实测表明它属于 A 族签名接口，**必须携带 AES 加密的 `params`**，裸查询串不够。这意味着「cookie 自动换发」的实现复杂度**高于** #56 的估计，而不是简单地拼一个 URL。

换取后的**有效期**：仍待实测（脚本会记录首次换发时刻，并在后续每一步记录是否出现 `InvalidSessionKey`，以此夹逼窗口）。

---

## 4. 问题 5：失效错误码（已确证）与过期窗口（待观测）

### 4.1 失效错误码 —— ✅ 已实测确证

用伪造 `SessionKey` + 合法格式的签名头打两个业务接口，返回**完全一致**的错误：

```json
{"errorCode":"InvalidSessionKey","errorMsg":"userSessionBO is null or fail to get sessionsecret by sessionkey","success":null}
```

- `listFiles.action` 与 `getFileDownloadUrl.action` 都是这个响应；
- HTTP 状态为 `400`；
- **与 #56 §3.3 的判断吻合**：失效信号是响应体里的 `InvalidSessionKey` + `userSessionBO is null`，**不能靠 HTTP 状态码**判断（正常业务错误、参数错误也都是 400）。

> 对驱动的直接含义：错误映射必须**扫响应体**，命中 `InvalidSessionKey` / `userSessionBO is null` → `serializer.CodeStorageAuthExpired`（40099）。这一点现在有了实测样本，可以写进单测的固定用例。

### 4.2 过期窗口 —— 待凭据观测

本轮的探测设计：换到 session 后，按 `+0 / +N` 间隔反复调用 `listFiles`，记录**首次出现 `InvalidSessionKey` 的时刻**。若单轮时间不够跨越整个窗口，则在报告中给出「已确认有效时长下限」，并把上限留给更长周期的观测。

#56 记录的 `keepAlive=300`、`getUserInfoSpan=3600` 只是心跳/同步间隔，**不是过期时间**——本轮仍然确认：接口响应里没有可直接读出的绝对过期时间戳。

---

## 5. 问题 2/3/4：仍必须用真实账号才能定论

这三项**无法**用无凭据探测替代，因为都需要账号里的真实文件或真实上传会话：

| 问题 | 为什么必须要账号 | 脚本对应步骤 |
| --- | --- | --- |
| **2. 裸取能否成立**（最关键） | 真实 CDN 地址由 `getFileDownloadUrl` 针对**具体 fileId** 签发 | 步骤 3–5 |
| **3. TTL** | 地址带自有令牌，只能实测其存活 | 步骤 6 |
| **4. 分片批量预签** | `initMultiUpload` 会**在账号里创建上传会话**，属写操作 | **本轮不做**，见下 |

### 关于问题 4 的一条红线

原 Spec 允许「只建 `cr-test-*` 前缀临时目录并事后删除」。本轮我的处理是：**凭据到位后先只跑只读的 1/2/3/5，问题 4 单独确认后再做**。理由——上传测试即便事后清理，仍会在账号里产生真实上传会话与回收站记录，且 `initMultiUpload` 之后若 `commit` 失败会留下孤儿会话；这与「不得删除账号内任何既有文件」的约束相邻，值得由用户点头再动。

---

## 6. 问题 6：家庭云 vs 个人云 —— 低成本判据（待执行）

#56 称家庭云是另一套 `familySessionKey/Secret` 与另一套路径。本轮的验证成本很低：**换发响应体里本来就带 `familySessionKey` / `familySessionSecret`**（#56 §3.1 的响应样本可见）。

- 若换发响应**含**这两个字段 → 家庭云凭据与个人云**同源下发**，「另一套账号」的说法不成立，只是密钥不同；
- 若**不含** → 说明该账号未开通/未加入家庭云，需单独授权。

这一步不需要任何额外的写操作，脚本的步骤 1/1b 会顺带记录字段是否存在（只记 `<present,len=N>`）。

---

## 7. 对 #56 结论的修订汇总

| #56 原结论 | 本轮实测 | 修订 |
| --- | --- | --- |
| §3.1 `getSessionForPC` = GET 带 `accessToken` 即可 | 缺 `params`(AES) 一律 `params is null` | **必须按签名接口调用**，换发实现成本上调 |
| §3.4 密码登录路线不推荐 | `unifyLoginForPC.action`、两个 qrcode 接口均已 **404**，静态资源迁至 `cloud.dlife.cn` | 不推荐的结论**更强**；且证明接口漂移快，驱动不可硬编码 |
| §4.1 真实地址不需 SessionKey（推测） | 未验证 | **仍是开放问题，且是最关键的** |
| §9.1「下载真实地址 TTL 未知」 | 未验证 | 仍开放 |
| §3.3 失效信号靠响应体字符串 | ✅ 实测得到 `InvalidSessionKey` + `userSessionBO is null` | **确证**，可直接写单测 |

---

## 8. 下一步

1. 待 `CT189_LOGIN_COOKIE` 注入本 agent shell（授权已开通，下一轮生效）；
2. 执行 `scripts/189probe/p189.py`，产出问题 1/2/3/5 的实测数据，尤其是**问题 2**（决定「严格直连」是否成立）；
   - 脚本无凭据时直接 `exit 2` 中止，不会发起任何请求；
   - 需要经代理时：`PROBE_PROXY=http://192.168.31.200:1082 python3 scripts/189probe/p189.py`；
   - 输出经 `redact()` 处理，可直接贴进报告；
3. 问题 4 与问题 6 的写操作部分，经用户确认后再做；
4. 回填本报告，并据此判定 §10「只读策略 / 签名网关 / 中转」三条路线中的可行项。

---

## 附录：本轮执行的命令类别（全部只读）

- `curl -s -o /dev/null -w '%{http_code}'` 探测域名/接口存活（`cloud.189.cn`、`api.cloud.189.cn`、`open.e.189.cn`）
- `curl` 无凭据调用业务接口，采集错误码与错误文案（§1.2、§4.1）
- 拉取 `cloud.189.cn/web/main` 及其 JS 静态资源，确认前端域名迁移（§1.4）
- 本地 `openssl enc` 与 `python3` 自测签名/加密/脱敏实现（§1.3）

**未执行**：任何带真实凭据的请求、任何写操作、任何目录创建或删除、任何仓库代码改动、任何部署或发版。
