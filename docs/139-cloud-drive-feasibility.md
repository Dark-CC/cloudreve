# 中国移动云盘（139 云盘）对接 Cloudreve 存储策略可行性

> 状态：**研究中**（本文档仍在补充实测数据，未定稿）
>
> 路线说明：本项目最终采用的路线是**"用户自己的账号登录态"**（cookie / token），
> 而不是开放平台的开发者 API（需 app key + secret + 资质审核）。理由：fork 开源后，
> 任何用户用自己的云盘账号即可接入，不需要为每个人申请开发者资质。
> 开放平台 API 的信息保留在文末附录，作为对照与备选。

## 1. 结论速览

| 问题 | 结论 | 依据 |
| --- | --- | --- |
| 下载能否 302 直连云盘？ | 待实测 | |
| 上传能否浏览器直传（302）？ | 待实测 | |
| 登录态用什么？ | 待实测 | |
| 登录态能维持多久？ | 待实测 | |
| 风控强度 | **高**（已实测证据见 §3） | |

## 2. Cloudreve fork 侧的对接点（代码事实）

已核对仓库代码，一个存储策略驱动需要实现的能力，以及 302 的完整链路：

### 2.1 302 下载链路

1. 前端请求文件下载地址 → `service/explorer/file.go` 的 `FileURLService`
2. 服务端取直链：`pkg/filemanager/manager/entity.go` 的 `GetDirectLink` / `GetEntitySource`
3. 直链由驱动给出：`EntitySource.Url()`
4. 若 `FileURLService.Redirect == true` 且只有单个 URL，则直接
   `c.Redirect(http.StatusFound, res[0].Url)` —— **浏览器 302 跳到云盘，不经过 Cloudreve 流量**
5. 是否需要中转由驱动能力位决定：`pkg/filemanager/manager/entitysource/entitysource.go` 的
   `ShouldInternalProxy()`，判断 `HandlerCapabilityProxyRequired` / `InternalProxy` / `Encrypted`

关键前提：直链必须是**浏览器可直接访问**的 URL。如果需要 Cloudreve 服务端带上
Cookie / Authorization 头去取，浏览器直连就会 401/403，`ShouldInternalProxy()` 会强制中转，
302 就不成立。

### 2.2 302 上传（直传）链路

1. 前端初始化上传 → 驱动 `Token()` 返回 `UploadCredential`
   （`pkg/filemanager/fs/fs.go`：`UploadURLs` / `SessionID` / `ChunkSize` / `Credential` /
   `UploadID` / `CompleteURL` / `Callback` 等字段）
2. 前端 `frontend/src/component/Uploader/core/uploader/` 下各 uploader 按策略类型处理
   `upload_urls`，**直接向云盘 PUT/POST 字节流**
3. 云盘必须对该直传地址放行 **CORS**（浏览器跨域），否则前端无法直传

S3 驱动是现成范例（预签名 PUT + CORS 配置）；OneDrive 驱动是"用户账号登录态"路线的范例。

### 2.3 驱动实例化位置（新增驱动需要改的地方）

- 策略类型常量：`inventory/types/types.go`（`PolicyTypeLocal` / `PolicyTypeS3` / `PolicyTypeOd` …）
- 驱动实例化：`service/admin/policy.go` 的 `switch service.Policy.Type`
- 驱动路由：`pkg/filemanager/manager/fs.go` 的 `GetStorageDriver`
- 驱动接口：`pkg/filemanager/driver/handler.go`（`Put` / `Delete` / `Open` / `LocalPath` /
  `Thumb` / `Source` / `Token` / `Meta` / `List` / `Capabilities`）
- 若需 302 下载，`Capabilities()` 不能返回 `HandlerCapabilityProxyRequired`

## 3. 登录与风控（已实测）

以下为本轮**真实访问 `https://yun.139.com/w/` 观测到的现象**，不是文档推断：

| 观测项 | 实测结果 |
| --- | --- |
| 登录页 | SPA（`/w/`），手机号输入框 `input.code-phone-input` + "获取验证码" |
| 协议勾选 | 必须勾选"我已阅读并同意…"，未勾选时按钮只打埋点不发短信（实测已验证） |
| 短信接口 | `POST https://user-njs.yun.139.com/user/sms/getSmsCode` |
| 请求体 | **整体加密**，不是明文 JSON（实测请求体为 base64 密文） |
| 响应体 | **整体加密**（实测响应为 base64 密文） |
| 滑块验证 | 发送验证码后触发 **阿里云滑块拼图验证**（`static-captcha.aliyuncs.com/qst/PUZZLE/online/903/<uuid>/back.png` 与 `shadow.png`） |
| 登录态 | 非标准 OAuth：`Authorization: Basic base64("pc:<token>:<account>")` + 一批 cookie + localStorage 里的 token |
| 请求头 | 额外 `x-yun-module-type` / `x-yun-app-channel` / `x-yun-client-info` 等自定义头 |

**这意味着什么**：这不是一个可以用账号密码换取 refresh token 的开放 OAuth 流程。网页版的
请求体/响应体是加密的，登录要过阿里云滑块。要做成 Cloudreve 驱动，需要逆向这套加密与签名，
并且每次登录都可能被滑块拦下——这对"用户自己登录一次、长期用"的体验是严重问题。

## 4. 待实测项（阻塞在登录态）

- [ ] 列目录：`/hcy/file/list` 请求/响应形态
- [ ] 下载直链：`/hcy/file/getDownloadUrl` 返回的 URL 形态、有效期（TTL）
- [ ] 直链是否需要 Cookie / Referer / UA 防盗链
- [ ] 上传：`/hcy/file/getUploadUrl` 是否返回预签名地址、是否支持浏览器 CORS 直传
- [ ] 登录态有效期

## 附录：开放平台 API（备选路线，非本项目主线）

来自 `https://open.yun.139.com` 文档站的静态抓取（未实测）：

- 认证：`accessToken1` 接口，POST uuid 换 accessToken
- 统一请求头：`appid` / `secretkey` / `accesstoken` / `deviceid` / `channelid`
- 上传：`/richlifeApp/devapp/pcUploadFileRequest` 两阶段（先取 URL 再 POST 二进制）；
  单文件限制普通 4G / 白银 8G / 黄金 20G / 钻石 50G；文件落在"我的应用收藏"
- 下载：`/richlifeApp/devapp/downloadRequest`，返回含 `code=` 的 `downloadURL`，支持断点续传；批量上限 50
- 频控与鉴权：均有
- 资质：需开发者资质（本轮未走此路线，未核实审核要求与费用）
