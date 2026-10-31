# 天翼云盘（189）对接 Cloudreve 存储策略可行性：按移动云盘（139）严格直连口径核对

> 研究日期：2026-10-31　|　状态：**只读研究，未改代码、未部署、未实测真实账号**
> 姊妹文档：[139-cloud-drive-feasibility.md](./139-cloud-drive-feasibility.md)、[139-driver-implementation.md](./139-driver-implementation.md)、[y139-direct-link-site-302.md](./y139-direct-link-site-302.md)
>
> 路线目标：沿用 139（`pkg/filemanager/driver/y139/`）的口径 —— **用户自己的登录态**、**严格直连**（本站不转发任何文件字节）、**添加向导无 Endpoint**。
> 参考资料：用户给的 https://doc.oplist.org/guide/drivers/189 （SPA，静态抓取拿不到正文），因此协议细节以 AList/OpenList 的 `drivers/189/`、`drivers/189pc/` 源码为准（2026-10-31 读取 main 分支）。

---

## 0. 一句话结论

**可以对接，但不能照抄 139 的「粘贴登录态 + 预签名直链」两处关键口径，新增 y189 需要至少一处架构升级。**

| 研究项 | 结论 | 与 139 口径的偏离 |
| --- | --- | --- |
| 1. 登录态复用「用户账号登录态、非开放平台」 | ⚠️ **部分可行**：必须存 `sessionKey + sessionSecret` 一对（不是一串 token），且**过期后无法自动续期**——189 没有 139 那种用登录态换新登录态的接口 | **落地交互差**：需要浏览器脚本捞 cookie，且定期人工重贴；139 是「贴一次，驱动自己续到 15 天内」 |
| 2. 严格直连下载（本站不转发字节） | ✅ **可行**：`getFileDownloadUrl.action` → 再取 302 的真实地址，**最终地址不要求 SessionKey/Signature**，本站只需在签发时发一跳 302 | **多一跳服务端请求**：真实地址由本站解析出来再 302；与 139 的 `/file/getDownloadUrl` 一步到位不同，但字节仍然不经本站 |
| 3. 个人云 / 家庭云 / 共享云 | 建议**只做个人云**；家庭云是另一套签名密钥（`familySessionKey/Secret`）与另一套文件接口，成本翻倍 | 139 没有对应概念 |
| 4. 上传直连 | ❌ **不能做到 139 那样的浏览器直传**：`getMultiUploadUrls` 必须用 `SessionKey` 签名、**只能由持有 sessionSecret 的服务端调用**；拿到的切片 URL 才可裸 PUT | **根本性偏离**：要么本站中转字节（违反严格直连），要么新增「浏览器经本站签名网关上传」的中间形态 |
| 5. 凭据字段与添加向导 | 需要 2–3 个凭据字段（`sessionKey`、`sessionSecret`、可选 `cookie`），**同样不要 Endpoint** | 字段从「整行 Authorization + app key」变成「两个不透明串」 |
| 6. 续期机制 | ❌ **没有登录态级的续期**；只有 `accessToken`（登录票据）能刷出新的 session，而 `accessToken` 来自 RSA 密码登录（需验证码） | 139 有 `tellin/authTokenRefresh.do`；189 没有等价物 |

**判定**：下载侧可以守住「严格直连」；**上传侧守不住**。如果「严格直连」是不可谈判的前提，那么 y189 只能做**只读（下载/浏览）策略**，或接受一个纯服务端中继的上传实现。

---

## 1. 189 的接口家族：先分清四套东西

189 的接口不是一套，混用是踩坑的第一来源。AList 为此维护了两个驱动（`189` 与 `189pc`），正是它们对应不同家族：

| 家族 | 域名 | 鉴权 | 能力 | AList 驱动 |
| --- | --- | --- | --- | --- |
| **A. PC 客户端族（推荐）** | `api.cloud.189.cn` / `upload.cloud.189.cn` | `SessionKey` + `Signature`（HMAC-SHA1，密钥是 `sessionSecret`），参数 AES-ECB 加密 | 列目录、下载、分片上传、家庭云、批量任务（移动/复制/删除） | `189pc` |
| B. 网页族 | `cloud.189.cn/api/open/...`、`hb02.upload.cloud.189.cn` | 网页 cookie | 列目录、`getFileInfo.action` 取 `FileDownloadUrl`、建目录/改名/批量任务、老式整包上传 | `189` |
| C. 登录族 | `open.e.189.cn/api/logbox/oauth2/*` + `cloud.189.cn/api/portal/*` | 账号密码 RSA + 图形验证码 | 换取 `accessToken` | 两者都用 |
| D. 旧客户端上传族 | `hb02.upload.cloud.189.cn/v1/DCIWebUploadAction` | `sessionKey`（表单里甚至写死 `"??"`） | 老接口，AList 已标注「家庭云不支持」并降级为兼容路径 | `189` 的 `oldUpload` |

**建议**：新增 `y189` 驱动以 **A 族（`189pc` 的协议）** 为蓝本。理由：

1. A 族是「PC 客户端也在用」的正式接口，字段稳定（`sessionKey`/`sessionSecret` 成对下发），不像 B 族是网页私有 `.action`；
2. 下载、上传、批量任务（删除/移动/改名）在同一套鉴权下完成，不用维护两套；
3. 家庭云支持只在 A 族里存在（`familySessionKey/familySessionSecret`）。

B 族只保留一个用途：**验证登录态是否还有效**（见 §3.3），因为它接受 cookie 且不需要签名密钥，适合做「凭据体检」。

---

## 2. 与 139 的机制逐项对照

### 2.1 鉴权材料

| 维度 | 139（已实现） | 189（本研究） |
| --- | --- | --- |
| 用户交什么 | `Authorization: Basic <base64(pc:账号:token)>` 整行 + `x-yun-app-key` | `sessionKey` + `sessionSecret`（成对，不透明串），可选 `cookie` |
| 请求头 | `Authorization`、`x-yun-app-key`、`x-yun-client-info` 等，body 参与签名（`Mcloud-Sign`） | `SessionKey`、`Signature`、`Date`、`X-Request-ID`、`PkId`、`EncryptionText`；查询串带 `clientType=TELEPC&version=6.2&channelId=web_cloud.189.cn&rand=...` |
| 签名算法 | `md5(md5(base64(排序后的 body)) + md5(ts:rand))` 大写 | `HMAC-SHA1(sessionSecret, "SessionKey=..&Operate=GET&RequestURI=/path&Date=..&params=<aes hex>")` 大写 |
| 参数加密 | 无（body 明文 JSON） | 有：`params` 为 AES-ECB(PKCS7) 后的 hex，密钥取 `sessionSecret[:16]`；签名覆盖的正是这串 hex |
| 传输密钥 | 无 | `EncryptionText` = RSA 加密本次随机 AES 密钥，`PkId` 指明用哪把公钥（`generateRsaKey.action`） |
| 账号密码是否入库 | 不入库 | **同样可以不入库**（见 §3） |

对驱动的直接影响：`pkg/request` 客户端支持自定义 header、query 与 cookie jar，实现上没有障碍；但 AES-ECB / RSA / HMAC-SHA1 三段密码学逻辑必须自写（139 只用了 md5，189 要新增 crypto 代码 + 单测）。

### 2.2 文件操作接口对照

| 能力 | 139 | 189（A 族） |
| --- | --- | --- |
| 列目录 | `POST /file/list`（游标分页 `nextPageCursor`） | `GET api.cloud.189.cn/listFiles.action`（`pageNum/pageSize` 页码分页，`folderId=-11` 为个人云根） |
| 时间格式 | RFC3339 带毫秒 | `"2006-01-02 15:04:05 -07"`（AList 按 `str + " +08"` 解析，说明服务端只给半截时区） |
| 元数据 | 父目录 listing 里读（无 HEAD） | 同左，无 HEAD |
| 下载 | `POST /file/getDownloadUrl` → 预签名（`X-Amz-Expires=900`） | `GET .../getFileDownloadUrl.action?fileId=&dt=3&flag=1` → 返回 URL，再 GET 拿 302 的 `location` |
| 上传 | `/file/create` 直接返回预签名分片 URL（S3 SigV4） | `GET /person/initMultiUpload` → 逐片 `GET /person/getMultiUploadUrls` → 裸 PUT → `GET /person/commitMultiUploadFile` |
| 秒传 | `contentHash`(SHA256) → `rapidUpload`/`exist` | `initMultiUpload` 带 `fileMd5`/`sliceMd5` → `fileDataExists=1`，再走旧接口 commit；**哈希算法是 MD5**（整文件 MD5 + 分片 MD5 列表的 MD5） |
| 删除 | `/recyclebin/batchTrash`（`fileIds[]`） | `POST batch/createBatchTask.action`（`type=DELETE` + `taskInfos` JSON）+ 轮询 `batch/checkBatchTask.action` 直到 `taskStatus=4`；永久删除需再 `CLEAR_RECYCLE` |
| 改名 | `POST /file/update` | B 族 `renameFile.action` / `renameFolder.action`，或批量任务 |
| 建目录 | `/file/create`（type=folder） | `createFolder.action`（B 族）或 `batch/createBatchTask` 的 `MOVE/COPY` 目标 |
| 缩略图 | 列表自带 `thumbnailUrls` | 列表带 `icon.smallUrl` / `icon.largeUrl`（`iconOption=5`），同样直接复用到 Cloudreve 缩略图 |

**删除语义要注意**：189 的删除是**异步批量任务**，有「初始化→冲突→执行中→完成」四态。Cloudreve 的 `Delete()` 期望同步返回成败，驱动必须自己轮询（AList 用 1 秒间隔死等）。若目标目录不存在同名冲突，通常直接到 4；但网络抖动下可能长时间停在 3，需要给轮询设上限，超时按失败返回（否则用户点删除会一直转圈）。

---

## 3. 研究项 1：能否复用「用户账号登录态、非开放平台」口径

### 3.1 「用户登录态」在 189 里到底是什么

189 真正的业务凭据是**一对值**：

```json
// GET https://api.cloud.189.cn/getSessionForPC.action?appId=8025431004&accessToken=<accessToken>&clientType=TELEPC&...
{
  "res_code": 0,
  "loginName": "<账号，占位>",
  "sessionKey": "<40 位十六进制，占位>",
  "sessionSecret": "<长十六进制，占位>",
  "familySessionKey": "<家庭云，占位>",
  "familySessionSecret": "<家庭云，占位>",
  "keepAlive": 300,
  "getFileDiffSpan": -60,
  "getUserInfoSpan": 3600
}
```

- `sessionKey`：身份标识，随请求头 `SessionKey` 发出，也参与签名串。
- `sessionSecret`：**签名密钥**，永远不出现在请求里，只在服务端算 HMAC 和派生 AES 密钥。
- 两者**必须成对**；只有 `sessionKey` 是没用的（这也是 AList 里 `sessionKey` 只在 upload 路径被取出来用、而 API 调用一律用 tokenInfo 的原因）。

> 推论：139 的「一串可粘贴的 Authorization」在 189 不存在。管理员要么交 cookie（驱动自己换 session），要么交 `sessionKey`+`sessionSecret`（驱动直接签名）。

### 3.2 有没有「不存明文账号密码」的路？

有，**三种，按推荐度排序**：

| 方案 | 用户操作 | 本站存什么 | 代价 |
| --- | --- | --- | --- |
| **A. 粘贴 cookie（推荐）** | 在 cloud.189.cn 登录后 F12 复制 `Cookie` 整行 | cookie（等同一段时间的登录态） | 驱动需要额外一步：拿 cookie 调登录跳转，再用它换 `accessToken`，再换 session。cookie 与账号密码**等效权限**，泄露风险与 139 的 Authorization 同级 |
| **B. 粘贴 sessionKey + sessionSecret** | 需要在控制台执行一段脚本，把 `getSessionForPC` 的响应里两个字段贴出来 | 这一对值 | 最贴合 139 的「贴不透明串」体验；但用户多半不知道怎么拿到，必须提供脚本 |
| **C. 账号密码入库** | 输账号密码 | 明文或可解密的账号密码 | **不建议**：等于把云盘账号托管给站点；还要处理验证码（见 §3.4） |

`y139` 的现有表单形态（`Authorization` 整行 + `x-yun-app-key`）可以**原样借用为方案 B**：两个输入框，`AccessKey` 存 `sessionKey`、`SecretKey` 存 `sessionSecret`，语义与 139 完全对应（`sessionSecret` 就是 189 的「app key」，都是签名必需但用户看不懂的串）。这也是最省事、最符合「按 139 一样的思路」的做法。

### 3.3 登录态是否过期？怎么判断

- 每次响应都可能带 `res_code`（int 或 string 两种形态）、`errorCode`、`code`（XML/JSON 混用），**过期/失效的信号是响应体里出现 `InvalidSessionKey` 或 `userSessionBO is null`**（AList 用字符串包含判断）。
- 对驱动意味着：**不能靠 HTTP 状态码**判断登录态失效，必须扫响应体，命中后映射到 Cloudreve 已有的 `serializer.CodeStorageAuthExpired`（40099，139 已在用）。
- 有效期：源码里只有 `keepAlive=300`、`getUserInfoSpan=3600` 这类心跳/同步间隔提示，**没有可读的绝对过期时间戳**。也没有 139 那种「token 内含过期毫秒」可以本地预判，只能「用到失效为止」。
- **无法本轮实测**（无 189 账号），过期窗口需实测确认。建议实现时把「首次失效时刻」记进策略的 `last_refresh_error`（139 已建好的机制），让管理员在面板上看到而不是靠用户投诉。

### 3.4 为什么不采纳「账号密码自动登录」

`open.e.189.cn/api/logbox/oauth2/loginSubmit.do` 前必需：

1. `unifyLoginForPC.action` 拿 `lt` / `reqId` / `paramId` / `captchaToken`；
2. `encryptConf.do` 拿 RSA 公钥，用户名密码 RSA 加密；
3. `needcaptcha.do` —— **可能要求图形验证码**（AList 挂 OCR 服务识别，或把图片 base64 回给前端让用户填）。

这与 139 当初拒绝密码登录的理由完全一致（当初 139 是短信 + 阿里云滑块）。结论：**密码路线会把「验证码识别」和「明文密码托管」两个负担同时带进开源 fork**，不建议走。

---

## 4. 研究项 2：严格直连下载是否可行

### 4.1 链路

```
浏览器 GET /f/<本站直链>              ← 本站地址（沿用 y139 直链 302 方案）
   ↓ 302                                 本站签发阶段（服务端，不是浏览器）：
云盘真实下载地址                        GET api.cloud.189.cn/getFileDownloadUrl.action?fileId=X&dt=3&flag=1
   ↓                                      → {"fileDownloadUrl": "http://.../..."}
文件字节                                GET 该 URL（禁止跟随）→ 302 Location = 真实地址
```

关键点：

1. **真实地址不需要 SessionKey/Signature**。AList 拉 302 时用的是同一个客户端（会带上 session 头），但那是顺手的，`getFileDownloadUrl.action` 返回的 `fileDownloadUrl` 本身就是一个**带自己令牌的下载地址**，跟随得到的是 CDN/对象存储地址；AList 的注释与实现都把它当作可直接交给播放器/下载器的 `link.URL`。
   → **必须实测确认**（开放问题），确认项：裸 `curl`（不带任何头、不带 Referer）能否拿到 200/206。
   → 若实测发现必须带 Referer/UA，Cloudreve 的 302 只能下发 URL、**不能附加请求头**，那就只能退回「本站中转」（违反严格直连）或改用 `?download` 页面跳转，与 139 的 `Source()` 契约冲突。
2. **地址是 `http://`**，AList 强制替换为 `https://`。驱动必须做同样替换（否则浏览器会拦混合内容）。
3. **HTML 实体**：响应里的 URL 可能带 `&amp;`，必须反转义后再用。
4. **有效期未知**：139 是明确 15 分钟（`X-Amz-Expires=900`，可本地读）；189 的真实地址 TTL 需要实测（对照 AList 里被注释掉的 `expire=` 正则解析，说明它曾尝试从中解析但放弃了）。
   → 驱动里 `Capabilities().MaxSourceExpire` 不能照抄 13 分钟。建议**保守取 5 分钟**，并保留策略级覆盖项；实测后再调。
5. **本站 302 的形态已就绪**：`docs/y139-direct-link-site-302.md` 已经把 y139 从「直链下发云盘地址」改成「本站 `/f/:id/:name` 302」，这个方案对 189 直接适用，不需要再改 `entity.go`。

### 4.2 结论

**下载可以严格直连**，且本站只多做一次「签发时的 302 探测」（每次拿直链多 1 个请求，几百字节）。与 139 的偏离是：

- 139：一次 API 调用直接拿到预签名地址；
- 189：两次（取 URL → 探 302），且要解析 `Location`；
- 139 的签名里带 TTL，可以精确预算缓存；189 未知，只能保守。

因此 `y139` 里那个「URL 上的 query 不能追加」的约束在 189 **可能不成立**（189 允许替换/重排），但也没有必要动它。

---

## 5. 研究项 3：个人云 / 家庭云 / 共享云

### 5.1 三者的协议差异（按源码能确认的）

| 项 | 个人云 | 家庭云 | 共享云（他人分享给我的） |
| --- | --- | --- | --- |
| 根目录 ID | `-11` | `""`（空串）或 `familyId` 下的根 | 不在 189 驱动里实现 |
| 业务路径 | `api.cloud.189.cn/{listFiles,getFileDownloadUrl,...}.action` | `api.cloud.189.cn/family/file/*`、`family/manage/getFamilyList.action` | 无 |
| 签名密钥 | `sessionKey` / `sessionSecret` | `familySessionKey` / `familySessionSecret` | 无 |
| 查询参数 | `dt=3&flag=1`、`recursive=0` | 额外 `familyId=<数字>` | 无 |
| 上传路径 | `upload.cloud.189.cn/person/*` | `upload.cloud.189.cn/family/*` | 无 |
| 列表字段 | 同名 | 同名 + 家庭云排序枚举不同（`toFamilyOrderBy`） | 无 |
| 额外能力 | — | 「转存到个人云」（`COPY` 批量任务）、家庭云中转上传 | 需先「转存到我的云盘」，属另一套分享接口 |

### 5.2 建议

- **默认只做个人云**（与 139「个人网盘」对齐）。
- **家庭云列为二期**：它需要①多一份密钥对、②每个请求多一个 `familyId`、③另一套文件/上传路径、④一套 `getFamilyList` 选择逻辑。工作量约等于再做半个驱动。
- **共享云不做**：189 的分享链接接口（分享码换取、转存）是独立协议，139 侧也没有对应能力，加进来会让「存储策略」变成「网盘聚合器」，与 fork 的定位不符。

若用户确实要家庭云，建议**做成第二个策略类型**（`y189_family`）而不是同一个策略里的开关：139 的经验表明，一个策略里塞两种语义会让凭据、根目录、错误映射三处都变模糊。

---

## 6. 研究项 4：上传直连（这是唯一的硬伤）

### 6.1 189 的上传流程（A 族 stream 方式）

```
1. GET upload.cloud.189.cn/person/initMultiUpload
     params: parentFolderId, fileName, fileSize, sliceSize, lazyCheck=1
     → data.uploadFileId, data.fileDataExists
2. 对每一片 i：
   GET upload.cloud.189.cn/person/getMultiUploadUrls
     params: uploadFileId, partInfo="i-<base64(md5(切片))>"
     → uploadUrls["partNumber_i"] = {requestURL, requestHeader}
3. 裸 PUT requestURL，带 requestHeader 里给的头部（不签名、无 SessionKey）
4. GET upload.cloud.189.cn/person/commitMultiUploadFile
     params: uploadFileId, fileMd5, sliceMd5, lazyCheck=1, opertype=3(覆盖)/1(不覆盖)
```

### 6.2 为什么浏览器不能自己干第 1、2、4 步

第 1/2/4 步都需要 `SessionKey` + `Signature` + `params`(AES) + `EncryptionText`(RSA) + `PkId`：

- `Signature` 用的密钥是 **`sessionSecret`**，它**绝不能给浏览器**（给了等于把账号凭据发给每个访客）；
- `params` 是 AES-ECB 加密的，密钥 `sessionSecret[:16]`——同理不能外发；
- `EncryptionText` 是 RSA 公钥加密的随机 AES 密钥，`PkId` 必须与服务端公钥轮换同步。

也就是说，**第 1、2、4 步必须由本站服务端代理**（只发元数据，不发文件字节），**第 3 步（真正传字节）浏览器可以直连**。

### 6.3 三种可选形态

| 形态 | 浏览器 | 本站 | 是否「严格直连」 | 改造成本 |
| --- | --- | --- | --- | --- |
| **① 逐片签名网关**：驱动在 `Token()` 里把 uploadFileId 与**全部分片 URL** 提前取好，塞进 `UploadCredential.UploadURLs`，浏览器 PUT 各片，回调时本站 commit | 直传字节 | 签发时 N 次 `getMultiUploadUrls`（每片一次，或按批） | ✅ 字节不经本站 | **中高**：`UploadCredential` 要新增「每片额外请求头」字段（189 的 `requestHeader` 必须原样带上） |
| **② 服务端中继**：`Settings.Relay=true`，Cloudreve 收分片再转发 | 传本站 | 转发全部字节 | ❌ | 低（但不能满足前提） |
| **③ 旧接口 `createUploadFile.action` + 断点续传 PUT** | 仍要 SessionKey 签名（`oldUpload` 的 PUT 是签过名的） | 转发 | ❌ | 低 |

**形态①的可行性取决于三件事，都必须实测**：

1. `getMultiUploadUrls` 能否一次签多片（AList 的 `partInfo` 是**逗号分隔的多值**，函数签名 `partInfo ...string` 支持批量；但注释写着「对 http body 有大小限制，分片信息太多会出错」，且 `StreamUpload` 里其实是**每片一次**调用）→ 若只能单片签，一个 10 GB 文件（每片 10 MB）= 1000 次签署请求，签发阶段会非常慢。
2. 分片 URL 的**有效期**未知（139 是 2 小时并写在 `X-Amz-Expires`）。
3. `requestHeader` 里到底有什么（AList 把它 `&` 切开逐条 `Header.Set`，说明是 `k=v&k=v` 形态；可能是 `Content-Type`/`Content-MD5` 之类）——若其中含**浏览器不允许设置的受限头**，形态①直接失败。

分片大小也有外部约束（AList `partSize()`）：

```
10 MiB   ≤ 999 片（≤ ~9.7 GiB）
20 MiB   ≤ 999 片（→ 实际 999..1998 片区间用 20 MiB）
≥ 50 MiB，片数上限 1999（更大文件按 size/1999 向上取整到 10 MiB 的倍数）
```

Cloudreve 的分片大小是策略级 `Settings.ChunkSize`（139 默认 50 MB），与 189 的这些档位不冲突，但驱动必须在创建上传时把 `sliceSize` 明确告知并校验，别让两边算出的片数不一致。

### 6.4 结论

**上传是 y189 与 y139 口径分歧最大的一项**。139 能一把 `create` 拿到全部预签名 URL（S3 风格），是因为移动云盘把签名逻辑下沉到了对象存储；189 把签名留在自己的 API 上、且密钥不能外发，所以「浏览器直传」只能靠**本站当签名网关**实现。

若「严格直连」是硬前提，形态①可以做，但要把风险写清：
- 签发阶段的请求数 = 片数（除非批量签实测可用）；
- 一旦 `getMultiUploadUrls` 加严（比如要求来源 IP 一致或加时间窗），形态①会整段失效。

> 备选：**只做下载策略**（`y189` 只读）。这在 Cloudreve 里是可行的——上传会在 `Token()`/`Put()` 处直接返回 `ErrNotSupported`，前端表现为「此策略不可上传」。如果用户主要用途是「把天翼云盘当只读资源库」，这条路的性价比最高。

---

## 7. 研究项 5：凭据字段与添加向导

### 7.1 字段建议（对齐 139 表单的两框、无 Endpoint）

| 策略字段 | 存入内容 | 必填 | 来源 |
| --- | --- | --- | --- |
| `AccessKey` | `sessionKey` | ✅ | cookie 方案下由驱动换取，B 方案由用户粘贴 |
| `SecretKey` | `sessionSecret` | ✅ | 同上 |
| `BucketName` | 根目录 folderId，留空 = `-11`（个人云根） | ❌ | 与 139 的 `root` 语义一致 |
| `Server` | **不展示**（留作运维覆盖） | ❌ | 189 的域名是固定的 `api.cloud.189.cn` / `upload.cloud.189.cn`，不像 139 需要 `qryRoutePolicy` 动态解析，所以连「运维覆盖」都不太必要 |
| cookie（若走方案 A） | 建议放 `policy.Settings` 之外——139 的 `Settings` 是公开序列化的，**不能塞凭据** | — | 若要存 cookie，需要独立字段或并入 `AccessKey`（`sessionKey|sessionSecret|cookie` 三合一） |

> 关于「第三方凭据放进 `Settings`」：`y139` 的 `Settings` 只用于 `ChunkSize` / `Region` / `Relay`，凭据一律走 `AccessKey`/`SecretKey`（这两个字段在 API 返回时被脱敏）。189 的 cookie 若必须持久化，**建议同样并入 `AccessKey`**（例如 `v1|<sessionKey>|<sessionSecret>|<cookie>`），保持一个可脱敏字段，避免在设置里多开一个泄露面。

### 7.2 添加向导

与 `Y139Wizard.tsx` 同构，改动点：

- 类型枚举加 `y189`（`frontend/src/api/explorer.ts` 的 `PolicyType`）；
- `policyProps.tsx` 增加 `[PolicyType.y189]`：`akName`/`skName`/`bucketName`、`chunkSizeMin/Max`、**不写 `endpointName`**；
- 风险提示文案要写清「天翼云盘的登录态无法自动续期，失效后需重新粘贴」——这是与 139 最大的体验差异，不能藏。
- 帮助链接指向 `cloud.189.cn`，并提供「怎么拿到 sessionKey/sessionSecret」的一小段控制台脚本说明（139 是「F12 里抄两个头」，189 需要多一步）。

### 7.3 后端与前端改动清单（与 139 的实际落点对齐）

| 改动点 | 文件 |
| --- | --- |
| 策略类型常量 | `inventory/types/types.go`（`PolicyTypeY189 = "y189"`） |
| 驱动新包 | `pkg/filemanager/driver/y189/*`（client/session/list/upload/y189.go + 单测；189 还需一个 `crypto.go` 放 AES-ECB/HMAC-SHA1/RSA） |
| 驱动实例化 | `pkg/filemanager/manager/fs.go` 的 `switch`（含 `SetTokenPersister` / `SetRefreshErrorReporter` 接线） |
| CORS 豁免 | `service/admin/policy.go` 的 `case`（189 也无需 CORS，直接 `return nil`） |
| 上传回调路由 | `routers/router.go`：`callback.GET("y189/:sessionID/:key", middleware.UseUploadSession(types.PolicyTypeY189), ...)` |
| 强制直传/禁中转 | `pkg/filemanager/manager/upload.go` 两处 `== types.PolicyTypeY139` 与 `archive.go` 的打包拦截、`frontend .../download.ts` 的 `assertNoDirectOnlyPolicy` |
| 前端上传器 | `frontend/src/component/Uploader/core/uploader/y189.ts` + `core/index.ts` 的 `switch`（形态①要额外带每片请求头，`api/index.ts` 新增 `y189UploadChunk`） |
| 向导与文案 | `Wizards/Y189/`、`policyProps.tsx`、`locales/*/dashboard.json`（22 个语言包，139 那次是全量补的） |
| 续期提示 | `service/admin/policy.go` 的 `ListRefreshNotices` 现在硬编码 `PolicyTypeY139`，**必须改成接受类型列表**，否则 189 的失效提示不会出现在面板 |

> 最后一行的坑值得单独强调：`RefreshNoticeService.List` 里写的是 `ListRefreshNotices(ctx, string(types.PolicyTypeY139))`，只查 139。加 189 时若照抄，凭据失效将**永远不在后台告警**——而 189 恰恰是不能自动续期的那个。

---

## 8. 研究项 6：续期机制

| 项 | 139 | 189 |
| --- | --- | --- |
| 登录态刷新接口 | `POST aas.caiyun.feixin.10086.cn/tellin/authTokenRefresh.do`（XML，用旧 token 换新 token） | **无登录态级等价物** |
| 189 唯一的刷新 | — | `GET api.cloud.189.cn/getSessionForPC.action?appId=8025431004&accessToken=<accessToken>`，用 **accessToken** 换新 session |
| accessToken 从哪来 | — | 密码登录后 `loginSubmit.do` 返回的 `toUrl`，再 `getSessionForPC` 得到。**accessToken 本身是登录票据，不是长期 refresh token** |
| 需要密码/验证码才能重登 | 否 | **是**（`loginSubmit.do` + `needcaptcha.do`） |
| 客户端侧应对 | 提前 15 天刷新，失败写回策略并告警 | `refreshSession()` 用 accessToken 刷；`UserInvalidOpenToken` 时只能 `login()`（=要密码，驱动里没有密码时此路不通） |
| 结论 | 自动续期，用户几乎无感 | **被动失效 + 人工重贴** |

对实现的直接影响：

1. `y189` **不能承诺**「贴一次长期有效」。策略表单与文档必须写明「登录态失效后需重新粘贴」。
2. 驱动仍应实现 `refreshSession()`（accessToken 有效期内可静默续 session），但必须处理「没有 accessToken 或 accessToken 也失效」的降级路径：直接 `ErrAuthExpired` → `serializer.CodeStorageAuthExpired`（40099），前端提示重贴。
3. 若走 cookie 方案，可做「用 cookie 自动换 accessToken → 换 session」的**准续期**，这是 cookie 方案相对 sessionKey 方案唯一实质优势，也是推荐 cookie 的另一理由（但要接受 cookie 也是会过期的）。

---

## 9. 风险与成本

### 9.1 技术风险

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| 下载真实地址的 TTL 未知 | 直链可能提前失效 | 实测；`MaxSourceExpire` 保守值；本站 302 每次重签（已具备） |
| 下载真实地址可能需要 Referer/UA | 302 无法带头 → 严格直连失败 | 必须先做 2 小时内的 curl 实测（§11 开放问题 2） |
| 上传需要服务端网关 | 与 139 口径不一致 | 形态① 或 只读策略 |
| `getMultiUploadUrls` 单片签署 | 签发慢、请求量大 | 实测批量；否则限制单文件大小或在签发阶段限流 |
| 响应体错误码格式不统一（`res_code` int/string、`errorCode`、`code`、XML/JSON） | 错误映射易漏 | 抄 AList `RespErr.HasError()` 的四路判定，加单测 |
| 域名/主机可能变体（如分片上传域名） | 硬编码失效 | 以 `initMultiUpload` 返回的 `uploadHost` 为准（AList 有 `UploadHost` 字段未用，可作为余量） |
| 非官方私有接口 | 云盘改版即失效 | 与 139 一样在使用文档里明示风险 |

### 9.2 工作量估算（对照 139 的实际情况）

| 阶段 | 估算 |
| --- | --- |
| 协议验证（真实账号，curl + 小脚本） | 1–2 天（**没有它就什么都定不下来**） |
| `y189` 驱动骨架：鉴权（HMAC/AES/RSA）+ 列目录 + 路径解析 + 下载 | 3–4 天 |
| 删除/改名/建目录（异步批量任务 + 轮询） | 1–2 天 |
| 上传（形态①：分片签署网关 + 前端 uploader + 回调） | 4–6 天 |
| 向导/文案/22 语言包/后台提示改造 | 1–2 天 |
| 联调 + 单测 + 真机端到端 | 2–3 天 |
| **合计（含上传）** | **约 12–19 人日** |
| **合计（只读下载策略）** | **约 7–10 人日** |

---

## 10. 建议（给决策的三条路）

1. **只读策略（推荐先做）**：`y189` 只支持浏览 + 下载（严格直连、本站 302 下发），上传返回不支持。收益：蹲实「严格直连」这个前提，成本最低，风险面最小。
2. **完整策略（形态①）**：下载同 1，上传走「本站签署 + 浏览器直传分片」。代价是新增一个 `UploadCredential` 字段（每片请求头）、签发阶段请求数随片数增长、且依赖「分片 URL 可预签署」这一未经实测的假设。
3. **暂不做**：若用户的核心诉求其实是「备份到天翼云盘」，那么「本站中转 + 单个 `Put()` 调用」的实现量远小于 1/2，与 Cloudreve 其它策略（`cos`/`oss`/`obs` 等中转模式）一致——**放弃严格直连，换取两周内的可用实现**。

我的判断：**先做 1**（把下载与凭据口径钉住，真实体验一遍「登录态会失效」的痛点），**再按实测结果决定 2 还是 3**。

---

## 11. 开放问题（需要用户/实测回答）

1. **个人云 / 家庭云 / 共享云，要哪个？** 研究建议默认个人云；家庭云建议二期单独类型；共享云不建议。
2. **是否有可用的 189 账号做端到端实测**（如 139 当初 #32 那样）？**没有账号，下面 4 条都无法定论**：
   - 裸 `curl`（无任何头）能否直接下载 302 后的真实地址；
   - 真实下载地址的有效期；
   - `getMultiUploadUrls` 能否批量签、`requestHeader` 内容、URL 有效期；
   - `sessionKey`/`sessionSecret` 的实际过期窗口（决定「多久要重贴一次」）。
3. **「严格直连」是否是不可谈判的前提**？若是，上传只能走形态①（或只读）；若否，形态②的实现量小得多。
4. **凭据形态选哪一种**：cookie（可准续期，但cookie 等效密码）还是 `sessionKey`+`sessionSecret`（最像 139，但不能续期）？

---

## 12. 附录：本轮核对到的源码事实（可复核）

- AList/OpenList `drivers/189pc/utils.go`：`request()` 用 `State` 头 `SessionKey/Signature/X-Request-ID/Date`，`Signature=HMAC-SHA1(sessionSecret, "SessionKey=..&Operate=..&RequestURI=..&Date=..[&params=..]")`；`EncryptParams` 用 `AES-ECB(PKCS7)`、密钥 `sessionSecret[:16]`；`Link()` 用 `getFileDownloadUrl.action` 再取 302 的 `location`；`StreamUpload` 逐片 `getMultiUploadUrls` → 裸 PUT → `commitMultiUploadFile`；`refreshSession()` 用 `accessToken` 换 session，`UserInvalidOpenToken` 才回落到密码登录。
- AList/OpenList `drivers/189pc/meta.go`：`Type: personal|family` 开关，`DefaultRoot: "-11"`，`UploadMethod: stream|rapid|old`。
- AList/OpenList `drivers/189/driver.go`：B 族 `getFileInfo.action` 取 `downloadUrl` 再探 302；`createFolder.action`/`renameFile.action`/`createBatchTask.action`。
- AList/OpenList `drivers/189/help.go`：`AesEncrypt`、`hmacSha1`、`RsaEncode`、`qs()`（注明 `fileName` 需 `url.QueryEscape`）。
- 本仓库 `pkg/filemanager/driver/y139/*`：`Capabilities()` 只声明 `HandlerCapabilityUploadSentinelRequired`（不声明 `ProxyRequired`，否则 302 作废）；`MaxSourceExpire = 15m-2m`；`Token()` 一次 `create` 拿 100 片签名，超出用 `getUploadUrl` 补齐。
- 本仓库 `pkg/filemanager/manager/upload.go`：`Type == types.PolicyTypeY139` 两处强制直传/禁止中转分片；`archive.go` 打包拦截；`frontend .../download.ts` `assertNoDirectOnlyPolicy`。
- 本仓库 `service/admin/policy.go`：`ListRefreshNotices(ctx, string(types.PolicyTypeY139))` —— 加新类型时必须一并改。
- 本仓库 `docs/y139-direct-link-site-302.md`：本站 `/f/:id/:name` 302 方案已落地，189 可复用。
