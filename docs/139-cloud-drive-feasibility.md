# 中国移动云盘（139 云盘）对接 Cloudreve 存储策略可行性

> 研究日期：2026-10-29　|　状态：**已完成（含实测）**
> 路线：**用户自己的账号登录态**（cookie + Authorization 头），不走开放平台开发者 API。
> 理由：fork 开源后任何用户用自己的云盘账号即可接入，无需逐个申请 app key 资质。

## 1. 结论

**可实现。下载和上传都能 302 直连云盘，Cloudreve 不承担文件流量。**

| 方向 | 结论 | 实测证据 |
| --- | --- | --- |
| **下载 302** | ✅ **可行** | `/file/getDownloadUrl` 返回 AWS SigV4 预签名 URL，**无需任何 Cookie / Authorization / Referer**，裸 `curl` 直接 `206 Partial Content` 拿到真实文件字节，支持 Range 断点续传 |
| **上传 302** | ✅ **可行** | `/file/create` 一次性返回预签名分片上传 URL，浏览器直连 `PUT` 返回 `200`，响应头 `Access-Control-Allow-Origin: *` + `Access-Control-Allow-Methods: PUT` —— **CORS 放行，浏览器可跨域直传** |
| 列目录 | ✅ 可用 | `/file/list` 返回完整元数据（fileId / name / size / type / contentHash / 缩略图） |
| 删除 | ✅ 可用 | `/file/batchDelete` |
| 授权方式 | ⚠️ **非标准 OAuth**，见 §4 | 需从浏览器抓 `Authorization` 头 + `x-yun-app-key` |
| 风控 | ⚠️ **高**，见 §4.3 | 登录需过阿里云滑块拼图，接口有频控 |

一句话卡点：**协议层全通，卡点在"用户如何把登录态交给 Cloudreve"，以及这套私有接口的稳定性与合规风险。**

## 2. 实测验证的完整链路

所有请求基线：

```
POST https://personal-kd-njs.yun.139.com/hcy/file/*      （个人网盘业务）
POST https://user-njs.yun.139.com/user/*                 （用户与登录）
```

必需请求头（缺任一即报错，实测）：

| 头 | 说明 | 缺失时 |
| --- | --- | --- |
| `Authorization` | `Basic base64("pc:<token>:<account>")` | `04000005 认证失败` |
| `x-yun-app-key` | 网页版内置的固定 app key | `01000004 请求头x-yun-app-key规则不正确` |
| `x-yun-api-version` | `v1` | — |
| `x-yun-app-channel` | `10000034`（网页端） | — |
| `x-yun-module-type` | `100` | — |
| `x-yun-client-info` | 客户端指纹串 | — |
| `Content-Type` | `application/json` | — |

### 2.1 列目录（实测通过）

```
POST /file/list
{"parentFileId":"root","pageSize":20,"orderBy":"updated_at","sortType":"desc",...}
```

- 根目录 id 用 `"root"`（也可以是 `/`），**空字符串会报 `父目录ID不允许为空`**
- 返回 `items[]`，含 `fileId` / `parentFileId` / `name` / `type`（`file`|`folder`）/ `size` /
  `category` / `fileExtension` / `contentHash`（sha256）/ `contentHashAlgorithm` / `revisionId` /
  `thumbnailUrls` / 审计状态字段
- 分页用 `nextPageCursor`（游标式，不是 offset 页码）
- 实测还返回了 `contentAuditInfo`（内容审核状态）、`metadataAuditInfo`——**说明云盘侧对文件有内容审核**，见 §6

### 2.2 下载直链（实测通过，这是 302 的核心）

```
POST /file/getDownloadUrl
{"fileId":"<fileId>"}
```

响应（节选，签名参数已脱敏）：

```json
{"success":true,"data":{
  "url":"https://<bucket>.obs.dualstack.cidc-rp-2006.joint.cmecloud.cn/<hash>?...
         &X-Amz-Algorithm=AWS4-HMAC-SHA256
         &X-Amz-Expires=900
         &X-Amz-SignedHeaders=host
         &X-Amz-Signature=<sig>",
  "expiration":"<签发时刻 + 15 分钟>",
  "size":257548159,
  "contentHash":"<sha256>"
}}
```

实测验证项：

| 验证项 | 结果 |
| --- | --- |
| 裸 GET（**不带任何 Cookie / Authorization / Referer / UA**） | ✅ `206 Partial Content`，返回真实文件字节 |
| `Range: bytes=0-1023` | ✅ `Content-Range: bytes 0-1023/257548159` |
| 中段 `Range: bytes=1000000-1000499` | ✅ 断点续传可用 |
| 直链有效期 | **15 分钟**（`X-Amz-Expires=900`） |
| 资源位置 | 移动云 OBS（`*.cmecloud.cn`）**公网对象存储**，非需鉴权的应用层 |
| CORS | `Access-Control-Allow-Origin: *` |

**结论**：这个 URL 可以原样作为 302 的 `Location`，浏览器跳过去直接下载，Cloudreve 零流量。

> 另外：列表接口返回的 `thumbnailUrls` 也是同一套 SigV4 预签名机制，但 `X-Amz-Expires=86400`（24 小时）。
> 说明云盘对不同场景签发不同 TTL 的签名 URL。

### 2.3 上传直传（实测通过，这是上传 302 的核心）

移动云盘的上传是 **S3 风格的多段预签名上传**，分三步：

**第 1 步 `/file/create`** —— 建占位元数据，**直接返回预签名分片上传 URL**：

```
POST /file/create
{"parentFileId":"root","name":"x.txt","type":"file","size":12,
 "fileRenameMode":"auto_rename","contentHash":"<sha256>","contentHashAlgorithm":"sha256",
 "partInfos":[{"parallelHashCtx":{"partOffset":0},"partNumber":1,"partSize":12}]}
```

响应含 `fileId`、`uploadId`、`rapidUpload`（秒传标记）、`partInfos[].uploadUrl`：

```
https://ykj-eos-wx2-01.eos-wuxi-3.cmecloud.cn/<hash>?partNumber=1&uploadId=<uploadId>
    &X-Amz-Algorithm=AWS4-HMAC-SHA256
    &X-Amz-SignedHeaders=content-length%3Bhost
    &X-Amz-Expires=7200
    &X-Amz-Signature=<sig>
```

实测验证项：

| 验证项 | 结果 |
| --- | --- |
| 浏览器式 `PUT` 直传（无任何鉴权头） | ✅ `200 OK`，返回 `ETag` |
| 上传地址有效期 | **2 小时**（`X-Amz-Expires=7200`） |
| CORS 响应头 | ✅ `Access-Control-Allow-Origin: *`、`Access-Control-Allow-Methods: PUT`、`Access-Control-Expose-Headers: ETag` |
| `Content-Length` 是否参与签名 | ✅ **参与**（`SignedHeaders=content-length;host`）——声明 `size` 必须与实际字节数严格一致，否则 `SignatureDoesNotMatch` |

**第 2 步** 浏览器向 `uploadUrl` 直接 `PUT` 分片字节。
**第 3 步 `/file/complete`** 提交 `{fileId, uploadId, contentHash, contentHashAlgorithm}` 完成合并。

实测完整跑通：create → PUT 12 字节 → complete 返回 200，文件正确落盘（`size:12`）。

> 注意：`/file/getUploadUrl`（带 `moduleType=5`）是**另一条**按需补签分片 URL 的路径，
> 当 `create` 返回的分片 URL 过期或做多段续传时使用。单文件小上传走 `create` 即可。

### 2.4 清理

测试产生的临时文件已用 `/file/batchDelete` 删除，根目录复查恢复为原有 8 项，回收站无残留。

## 3. 对照 Cloudreve fork 的改造量

### 3.1 302 下载：几乎零改造

fork 现成链路（已核对代码）：

```
FileURLService.Redirect  →  GetEntitySource  →  driver.Source()  →  c.Redirect(302, url)
```

驱动 `Source()` 直接返回 §2.2 的预签名 URL 即可。唯一约束：
`Capabilities()` **不能**返回 `HandlerCapabilityProxyRequired`，
否则 `entitysource.ShouldInternalProxy()` 会强制走 Cloudreve 中转，302 作废。

### 3.2 302 上传：需要前端配合

fork 的 `UploadCredential`（`pkg/filemanager/fs/fs.go`）已有 `UploadURLs` / `SessionID` /
`ChunkSize` / `UploadID` / `CompleteURL` 等字段，S3 驱动已跑通同样的模式。
139 驱动 `Token()` 需要：

1. 调 `/file/create` 拿到 `fileId` + `uploadId` + `partInfos[].uploadUrl`
2. 把这些填进 `UploadCredential` 返回给前端
3. 前端 PUT 完成后回调 Cloudreve，驱动再调 `/file/complete` 收尾

**难点**：139 的签名 URL 签了 `content-length`，而 fork 的分片大小由服务端 `ChunkSize` 决定。
分片大小必须两端一致，否则签名不匹配——这是实现中最容易踩的坑。

### 3.3 改造清单

| 改动点 | 文件 | 工作量 |
| --- | --- | --- |
| 新增策略类型常量 | `inventory/types/types.go` | 极小 |
| 新增驱动（List/Put/Delete/Source/Token/Meta/Thumb） | `pkg/filemanager/driver/y139/*` | **大（主体）** |
| 驱动实例化 | `service/admin/policy.go` 的 `switch` | 小 |
| 驱动路由 | `pkg/filemanager/manager/fs.go` `GetStorageDriver` | 小 |
| 前端 uploader（若复用 S3 形态则不需新增） | `frontend/src/component/Uploader/core/uploader/` | 中 |
| 策略设置项（Authorization / app-key / 根目录） | 策略表单 | 小 |

估算：**驱动主体 3–5 天，前端与联调 2–3 天**（熟手，单文件直传；不含多段续传打磨）。

## 4. 授权方式与风控（这是真正的成本所在）

### 4.1 没有 OAuth，只有"抄浏览器里的头"

移动云盘网页版**不提供**类似 OneDrive 的 refresh token 换取流程。实测确认可用的是：

- `Authorization: Basic base64("pc:<token>:<account>")`
- `x-yun-app-key: <网页版内置的固定值>`

两者都是**从浏览器里拿的**，不是给第三方接入设计的。`x-yun-app-key` 是网页前端写死的常量
（不在静态 JS 明文里，但可在运行时从请求头捕获）。

> 对照 OneDrive 驱动（`pkg/filemanager/driver/onedrive/oauth.go`）：
> OneDrive 有标准 OAuth2，`refresh_token` 存 `policy.AccessKey`，过期前用
> `Credential.Refresh()` 静默换新 token。139 **没有这一层**，只能存一个长期 token，
> 失效后必须让用户重新登录并重新粘贴。这是体验上的根本差异。

### 4.2 登录态持久性

| 项 | 实测/观察 |
| --- | --- |
| token 形态 | base64 内含 `account` + 一串签名材料 + 时间戳（`...|1795862156518|<长签名>`） |
| 有效期 | 未测出明确上限（token 内含过期时间戳，需长期观察） |
| 刷新机制 | **无 refresh token**；失效后 `04000005 认证失败`，只能重新登录 |
| 失效后的恢复 | 用户重新从浏览器抓一次 Authorization 与 app-key |

### 4.3 风控（实测到的硬证据）

| 观测 | 实测结果 |
| --- | --- |
| 短信接口 | `POST https://user-njs.yun.139.com/user/sms/getSmsCode`，**请求体整体加密**（非明文 JSON） |
| 响应体 | **整体加密**（base64 密文） |
| 图形验证 | 发码后触发 **阿里云滑块拼图**（`static-captcha.aliyuncs.com/qst/PUZZLE/online/903/<uuid>/`） |
| 协议勾选 | 必须勾选用户协议，否则按钮只打埋点不发短信（实测确认） |
| 接口频控 | 云盘侧有；本研究中 3 次连续 create 未被限流，但未见明确配额文档 |
| 内容审核 | 文件元数据带 `contentAuditInfo` / `metadataAuditInfo`，**云盘侧会对内容做审核** |

### 4.4 合规与项目风险（不粉饰）

1. **非官方接口**：这套 `/hcy/*` 接口是网页版私有接口，没有任何公开文档或稳定性承诺。
   移动云盘前端一改版，驱动就可能整体失效。
2. **app-key 属于云盘客户端**：把网页版内置的 app-key 硬编码进开源驱动，等于把这个常量
   公开传播。云盘方随时可以更换或封禁，届时所有用户同时失效。
3. **存在被判定为"非授权客户端"的风险**：大规模第三方调用可能触发风控、限速甚至封号。
   这是每个使用该驱动的用户自担的账号风险，**报告必须在使用文档里明示**。
4. **内容审核**：云盘会对上传内容做审核（元数据里可见 `auditStatus` / `auditLevel`），
   被判定违规的文件可能被限制访问——作为 Cloudreve 后端存储时，用户会遇到"文件在，
   但下载 403"的情况，需要驱动层识别并给出可读错误。
5. **登录流程无法自动化**：短信 + 滑块意味着用户首次接入必须手动在浏览器登录再抄 token，
   无法做成"点一下授权"。

## 5. 与 OneDrive 驱动对照

| 维度 | OneDrive（现成） | 139 云盘（本研究） |
| --- | --- | --- |
| 授权方式 | 标准 OAuth2（授权码） | 抄浏览器 `Authorization` 头 + 内置 app-key |
| token 存储 | `refresh_token` 存 `policy.AccessKey` | 存整个 `Basic` 头字符串 |
| token 刷新 | `Credential.Refresh()` 静默自动刷新 | **无自动刷新**，失效需人工重登 |
| 直链获取 | Graph API 返回预认证下载 URL | `/file/getDownloadUrl` → SigV4 预签名，**15 分钟** |
| 直链鉴权 | 预认证，无需额外头 | 预签名，**无需额外头**（实测裸 GET 通过） |
| 上传路径 | Graph 上传会话（session）分片 | `/file/create` → SigV4 预签名分片 PUT → `/file/complete` |
| 上传 CORS | 由 Graph 支持 | **`*` 放行 PUT**（实测） |
| 掉线处理 | 自动刷新，用户无感 | 返回 `04000005`，需用户重新粘贴登录态 |
| 稳定性契约 | 官方 API，有版本承诺 | 私有接口，无任何承诺 |

## 6. 要求的凭据与资质

走本文路线**不需要**开发者资质、不需要审核、不需要付费。需要的是：

| 项 | 获取方式 |
| --- | --- |
| 移动手机号 | 用户自有（云盘账号） |
| `Authorization` 头 | 浏览器登录 yun.139.com 后从请求头复制 |
| `x-yun-app-key` | 同上，从请求头复制（网页版内置常量） |
| 根目录 fileId | `"root"` |

占位符（本仓库实现时请留空，由用户填写）：

```
Authorization: Basic <base64("pc:<token>:<account>")>
x-yun-app-key: <web app key>
```

## 7. 附录：开放平台 API（备选路线，未实测）

若将来走开发者路线，开放平台（`open.yun.139.com`）提供的是另一套接口，与本文 `/hcy/*` 完全不同：

- 认证：`accessToken1` 换 accessToken，需 appid + secretkey
- 上传：`/richlifeApp/devapp/pcUploadFileRequest` 两阶段；文件落在"我的应用收藏"
- 下载：`/richlifeApp/devapp/downloadRequest`，返回含 `code=` 的 downloadURL
- 频控与鉴权：均有
- 需开发者资质（个人/企业）与审核，未核实费用

**本路线的问题**：需逐个申请资质，与"fork 开源后人人可用"的目标冲突，故未采用。
另外它的上传目标目录固定为"我的应用收藏"，不是用户自由目录，做存储后端不合适。

## 8. 建议

1. **可以做**，技术上无障碍：下载 302 与上传直传都已实测跑通。
2. 但要在驱动文档里**明确告知用户风险**：非官方接口、随时可能失效、账号可能被风控。
3. 实现时优先解决「分片大小与签名 `content-length` 对齐」这个坑。
4. 登录态获取建议做成"粘贴 Authorization + app-key"的表单，而不是尝试自动化登录
   （滑块 + 加密请求体，自动化成本高且更容易触发风控）。
