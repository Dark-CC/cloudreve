# 中国移动云盘（139）存储策略实现说明

本文件记录 `y139` 存储策略（`PolicyTypeY139 = "y139"`）的协议依据、实现范围、已知风险，
以及与 OpenList（原 AList，AGPL-3.0）139 驱动实现的对照结论。

## 1. 目标与边界

- 浏览器**直连**云盘：下载走 302 到预签名 URL，上传走预签名分片直传。
- Cloudreve 只承担元数据、签名与状态机，不承载文件流量。
- 驱动包：`pkg/filemanager/driver/y139/`。

为什么必须这样：`entitysource.ShouldInternalProxy()` 会在策略声明
`HandlerCapabilityProxyRequired` 时强制中转，302 随即失效。因此
`Capabilities()` **只**声明 `HandlerCapabilityUploadSentinelRequired`。

## 2. 凭据形态

一条策略只需要三样东西，全部来自用户自己的浏览器会话：

| 策略字段 | 含义 | 来源 |
| --- | --- | --- |
| `AccessKey` | `Authorization: Basic …` 整行（驱动兼容带/不带 `Basic ` 前缀） | yun.139.com 任意 `/file/*` 请求的请求头 |
| `SecretKey` | `x-yun-app-key` | 同一请求的请求头，**必填**（缺失时云盘返回 `01000004`，驱动在建策略时就拒绝） |
| `BucketName` | 根目录 `fileId`，留空为 `root` | 可选，用于把策略限制在某个文件夹 |

`Authorization` 解码后是 `pc:<账号>:<token>`，其中 `<token>` 形如
`a|b|c|<过期毫秒时间戳>`。驱动从第 2 段取账号、第 4 字段取过期时间。

**不保存密码**，也不需要手机号或短信验证码。密码登录链路（mail.10086.cn →
`get_single_token` → `third_party_login`）在本实现中不采纳，理由见 §5。

## 3. 会话维持

- 每次建驱动时惰性解析业务域名，并在需要时刷新 token。
- **token 自动续期**：剩余有效期 < 15 天时调用
  `POST https://aas.caiyun.feixin.10086.cn:443/tellin/authTokenRefresh.do`，
  body 为 XML：`<root><token>…</token><account>…</account><clienttype>656</clienttype></root>`，
  响应 `<root><return>0</return><token>新token</token></root>`。
  `return != 0` 或 token 为空视为登录态失效（`CodeStorageAuthExpired`）。
- 刷新成功后通过 `inventory.StoragePolicyClient.UpdateAccessKey` **写回策略库**
  （同时清理 KV 缓存），因此进程重启后仍然使用新 token，而不是管理员当初粘贴的旧值。
- token 已过期（`remain < 0`）不再尝试刷新，直接报 `ErrAuthExpired`，提示重新粘贴。

## 4. 接口与实现要点

### 4.1 动态业务域名

`POST https://user-njs.yun.139.com/user/route/qryRoutePolicy`

```json
{"userInfo":{"userType":1,"accountType":1,"accountName":"<账号>"},"modAddrType":1}
```

取 `routePolicyList` 中 `modName == "personal"` 的 `httpsUrl`。若策略里显式配置了
`Server`，解析失败时保留该值并只记 warning，便于排障与临时固定。

### 4.2 公共请求头与签名

请求头：`Authorization`、`x-yun-app-key`、`x-yun-api-version: v1`、
`x-yun-app-channel`/`x-yun-channel-source: 10000034`、`x-yun-module-type: 100`、
`x-yun-client-info`（web=9 / pc=13，`Settings.Region == "pc"` 时用 13）、`Content-Type: application/json`。

签名（`Mcloud-Sign: <ts>,<rand>,<sign>`）：

1. body 先做 encodeURIComponent 变体（`+`→`%20`，还原 `! ' ( ) *`），再按字符排序；
2. `md5(base64(排序后的字符串))`；
3. 拼接 `md5("<ts>:<rand>")` 后整体 md5 转大写；
4. `ts` 格式 `2006-01-02 15:04:05`。

### 4.3 列目录与路径解析

`POST /file/list`，body 含 `parentFileId`、`pageInfo{pageCursor,pageSize:100}`、
`orderBy:"updated_at"`、`orderDirection:"DESC"`、`imageThumbnailStyleList:["Small","Large"]`。

按 `nextPageCursor` 循环至空。时间戳为
`2006-01-02T15:04:05.999-07:00`。

路径 → fileId：从根逐级在父目录 listing 里按 `name` 精确匹配，中间段必须是
folder。云盘没有 HEAD，`Meta` 同样靠父目录 listing 读取大小。

### 4.4 下载

`POST /file/getDownloadUrl` → `{url, cdnUrl, cdnSwitch, expiration}`。
`cdnSwitch` 为真且有 `cdnUrl` 时优先 CDN。链接有效期 15 分钟
（`X-Amz-Expires=900`，支持 Range/206）。签名只覆盖 host，因此
`Source()` 返回的 URL **不能追加任何 query 参数**。

`MaxSourceExpire` 设为 `15m - 2m = 13m`，让 Cloudreve 的 URL 缓存总在有效期内。

### 4.5 上传

三接口：`/file/create` → `/file/getUploadUrl`（分批）→ `/file/complete`。

`create` body 关键字段：

- `contentHash` + `contentHashAlgorithm: "SHA256"`（全量 SHA256）；
- `partInfos[]`：每项 `partNumber`（从 1 起）、`partSize`、
  `parallelHashCtx.partOffset`；
- `fileRenameMode: "auto_rename"`、`commonAccountInfo{account, accountType:1}`。

响应 `{fileId, fileName, partInfos[{partNumber, uploadUrl}], exist, rapidUpload, uploadId}`。
`exist` 或 `rapidUpload` 为真且无分片地址 → 秒传，直接完成，不再传字节。

**一次 `create` 最多签 100 个分片地址**。超过时按每 100 个调用
`/file/getUploadUrl` 补齐，直到拿到全部分片 URL。这一步在
`Token()`（浏览器直传）与 `Put()`（服务端中转）两条路径上都做，保证前端拿到的
`upload_urls` 始终是完整的。

分片 PUT 到预签名 URL，**签名绑定 content-length**，因此必须设置精确长度、
`Content-Type: application/octet-stream`；前端另带
`contentSize` / `range` / `rangeType:0` / `uploadtaskID` 供云盘审计。

`complete` body：`{contentHash, contentHashAlgorithm, fileId, uploadId}`。

### 4.6 删除与重命名

- 删除：`POST /recyclebin/batchTrash`，body `{fileIds:[…]}`，每 100 个一批。
  （注意**不是** `batchDelete`。）已不存在视为删除成功。
- 重命名：`POST /file/update`，body `{fileId, name, description:""}`。
  用于将来处理同名冲突。

### 4.7 错误映射

| 条件 | 处理 |
| --- | --- |
| `04000005` / HTTP 401 | `ErrAuthExpired` → `CodeStorageAuthExpired` (40099) |
| HTTP 403 / 审计 code 前缀 `2100` | `ErrContentRejected` → `CodeContentAuditRejected` (40098) |
| HTTP 429 / 5xx / `02000003`、`02000004` | `backoff.RetryableError`（含 `Retry-After`） |

`CodeStorageAuthExpired` 与 `CodeContentAuditRejected` 的文案已加到
13 个语言的 `common.json` `errors` 段。

### 4.8 日志与脱敏

任何地方都不写入真实凭据。日志只允许 `redact()` 的结果：保留前 6 位，
其余替换为 `***`；空值输出 `<empty>`，短于 6 位输出 `***`。
日志与错误字符串里不出现完整的 `Authorization` 或 `x-yun-app-key`。

## 5. 与 OpenList 实现的对照

参照源码：`OpenListTeam/OpenList` 的 `drivers/139/{driver,util,types,meta}.go`。
**只借鉴协议与实现思路，不复制代码**（OpenList 为 AGPL-3.0，本项目为 GPL-3.0；
本驱动全部代码独立编写，因此不构成 AGPL 代码传染）。

### 采纳

| 项 | 说明 |
| --- | --- |
| token 静默续期 | 确认存在 `/tellin/authTokenRefresh.do`，只需 token + 账号，无需密码；剩余 < 15 天时刷新。这是原可行性报告中"只能重新粘贴"结论的更正 |
| 刷新后持久化 | 新 token 写回策略（本实现走 `UpdateAccessKey`），避免重启回退到旧 token |
| 动态业务域名 | `qryRoutePolicy` 取 `personal` 的 `httpsUrl`；另加"策略里可覆盖"的逃生通道 |
| `contentHashAlgorithm` | `create`/`complete` 显式声明 SHA256 |
| `parallelHashCtx.partOffset` | 分片偏移一并上报，符合云端并行哈希预期 |
| `exist` / `rapidUpload` 秒传 | 有则跳过传输，置 `RapidUploaded` |
| 100 分片一批 | `create` 只签前 100 个；其余按批 `getUploadUrl` 补齐 |
| `/recyclebin/batchTrash` | 删除接口用回收站而非 `batchDelete` |
| 分片请求头 | 前端 PUT 带 `contentSize`/`range`/`rangeType`/`uploadtaskID` |
| 上传分片大小规则 | ≤30GB 用 100MB、>30GB 用 512MB；本实现默认 100MB，上限 512MB，可由策略 `chunk_size` 覆盖 |
| 同名冲突改名 | 接口层提供 `Rename`，用 `auto_rename` + 随机后缀 + 删旧的思路（当前仅在重置时按需使用） |
| `cdnUrl` / `cdnSwitch` | 下载链接优先 CDN |

### 不采纳

| 项 | 理由 |
| --- | --- |
| 旧接口 `MetaPersonal`（`/orchestration/personalCloud/...`） | 新接口 `/file/*` 已覆盖全部所需能力；维护两套签名与路径没有收益 |
| `family` / `group` 家庭与群组分支 | 与个人盘是两套 `X-SvcType`、`/dynamic/file/*` 路径；当前需求只做个人盘，避免未验证代码进入 |
| `share` 分享链路 | 需求未涉及 |
| 密码 + 短信验证码登录（`step1/2/3`、`SmsCode`、`MailCookies`） | 需要用户密码和短信通道，凭据形态危险且不稳定；`Authorization` 粘贴 + token 自动续期已能满足长期使用。若将来要做，必须在表单里作为独立选项并明确标注"保存密码"的风险 |
| 12 小时 cron 定时刷新 | 本项目驱动是短生命周期对象（按请求创建），没有常驻调度器；改为"按过期时间在访问时刷新 + 写回策略"，效果等价且无需后台任务 |
| 直接复制 AGPL 源码 | 许可证隔离，避免 AGPL 传染争议 |

### 与原可行性报告的差异（报告已过时处）

1. **域名**：`personal-kd-njs.yun.139.com` 只是默认值，真实域名按账号路由，可能不同。
   现为"解析 + 可覆盖"。
2. **续期**：报告称只能重新粘贴。实际有静默续期接口，现已自动续期并持久化。
3. **删除**：报告写 `batchDelete`，实际新接口是 `/recyclebin/batchTrash`。
4. **分片**：报告未提 100 分片上限、`parallelHashCtx.partOffset`、
   `contentHashAlgorithm`、`exist`/`rapidUpload` 秒传。

## 6. 前端接入

- `src/api/explorer.ts`：`PolicyType.y139`。
- `src/component/Admin/StoragePolicy/StoragePolicySetting.tsx`：`PolicyPropsMap` 条目。
- `src/component/Admin/StoragePolicy/Wizards/Y139/Y139Wizard.tsx`：凭据表单，
  内含 F12 抓值指引与风险提示。
- `src/component/Uploader/core/index.ts`：`dispatchUploader` 分支。
- `src/component/Uploader/core/uploader/y139.ts`：分片直传 + 完成回调。
- `src/component/Uploader/core/api/index.ts`：`y139UploadChunk`（带云盘要求的分片请求头）。
- `frontend/public/static/img/y139.svg`：策略图标。
- 文案：13 个语言的 `dashboard.json`（`policy.*`）与 `common.json`（`errors.40098/40099`）。

回调路由：`GET /api/v4/callback/y139/:sessionID/:key` →
`middleware.UseUploadSession(types.PolicyTypeY139)` → `ProcessCallback`。

## 7. 后端接入点

- `inventory/types/types.go`：`PolicyTypeY139`。
- `pkg/serializer/error.go`：`CodeStorageAuthExpired = 40099`。
- `pkg/filemanager/manager/fs.go`：`GetStorageDriver` 分支 + 注入 token 持久化回调。
- `service/admin/policy.go`：`CreateStoragePolicyCorsService` 中该类型直接返回（无需 CORS）。
- `routers/router.go`：`callback/y139/:sessionID/:key`。

## 8. 风险

使用前请明确：

- **非官方接口**：走网页版私有 API，不是开放平台。云盘改版即可能失效，届时需重新适配。
- **风控**：异常流量、并发过高或异地登录可能触发限流、验证或封禁。建议限制并发与分片大小。
- **内容审核**：云端内容受运营商审核，违规文件可能被拒（40098）或事后删除；
  Cloudreve 侧的内容与云端不一致时以云端为准。
- **凭据**：`Authorization` 等同于该账号的登录态，能读写云盘。请仅在受信任的
  Cloudreve 实例上使用，并把策略限制在专用文件夹。
- **容量**：容量由云盘账号自身决定，Cloudreve 的配额限制是另一层，不改变云端占用。
