# 移动云盘（139）存储驱动端到端实测报告

- 日期：2026-10-29
- 被测分支：`tds/conv-01a18760-097f-76b3-a765-51da83285df3`（commit `6f53052a`）+ 本次修复
- 实测镜像：`cloudreve-fork:4.23.6-y139test4`（最终），`-y139test`/`test2`/`test3` 为中间版本
- 实例：InstaBox `m_agqw5litn4acpbo7b5axu3ycku`，容器 `cloudreve-fork-test`，`15212 -> 5212`
- 账号：真实移动云盘账号（凭据一律 `<redacted>`，未写入本仓库任何文件）

## 结论速览

| 项 | 结论 | 证据 |
| --- | --- | --- |
| a. 列目录 | 通过 | 文件/文件夹、大小、时间、hash 与云盘一致 |
| b. 下载 302 | 通过 | `302` 到 `*.eos-wuxi-3.cmecloud.cn`，跟随字节 sha256 一致 |
| c. 上传直传 | 通过 | 浏览器直 PUT 预签名 URL，云盘侧 size + contentHash 一致 |
| d. 秒传 | 通过 | 同内容新文件名 → `rapid_uploaded: true`，零字节传输 |
| e. 删除 | 通过 | 软删除→回收站→清空后云盘侧 2 秒内消失 |
| f. token 续期 | 通过（单元测试） | 15 天窗口内触发刷新，写回策略；见下文说明 |
| g. 错误提示 | 通过 | 失效凭据返回 `40099` + 可读文案，非 500 |

## 本次实测修出的三个缺陷

这三个都是**真实代码缺陷**，不是测试脚本问题，已修复并重新构建验证。

### 1. `authHeader()` 自死锁（严重）

**症状**：任何合法 URI 的 `PUT /api/v4/file/upload` 永久挂起（130s+ 无响应），而空 body / 坏 URI 立即返回。本地策略同样挂起，说明与 139 无关，是共性缺陷。

**取证**：`kill -ABRT` 抓 goroutine dump，得到：

```
goroutine 216 [sync.Mutex.Lock]:
  client.go:194 authHeader
  client.go:289 headers
  client.go:352 post
  session.go:108 queryRoutePolicyLocked
  session.go:59  ...
  client.go:331 personalCloudHost   <-- 已持有 c.mu
```

`personalCloudHost()` 持锁调用 `queryRoutePolicyLocked()` → `post()` → `headers()` → `authHeader()`，而 `authHeader()` 又去 `c.mu.Lock()`。Go 的 `sync.Mutex` 不可重入，永久自锁。

**修复**（`pkg/filemanager/driver/y139/client.go`）：
- `authHeader()` 去掉加锁，改为在锁下安全读取，并加注释说明调用链已在锁内；
- `accountName()` 拆出 `accountNameLocked()` 供锁内路径使用；
- 删除死代码 `tokenExpiry()`。

**回归测试**：`TestHeadersDoNotRelock`——持锁调用 `authHeader()`/`headers()`，5s 超时判失败。已验证该测试**能抓住旧缺陷**（还原旧行为 → FAIL 5.00s；恢复 → PASS）。

修复后 `PUT /api/v4/file/upload` **3.2s** 返回。

### 2. 上传完成阶段拿不到云盘 fileId

**症状**：分片 PUT 到 OBS 全部 200，回调 `/file/complete` 却报 `file not found: "<name>"`。

**根因**：`CompleteUpload()` 用 `ResolvePath()` 按**路径名**反查 `fileId`。但 139 的占位文件在 `/file/complete` 成功之前根本不对外列目录，所以永远查不到。`Create()` 明明返回了 `FileID`，`Token()` 却没往下传。

**修复**：
- `pkg/filemanager/fs/fs.go`：`UploadProps` 新增 `UploadFileID` 字段，注释说明用途；
- `y139.go Token()`：把 `created.FileID` 存进 `uploadSession.Props.UploadFileID`（此时 session 尚未落缓存，会一并持久化，回调时能读回）；
- `y139.go CompleteUpload()`：优先用该字段，仅在其为空时回退到路径解析。

### 3. 会话里没有内容哈希 → `/file/complete` 被云盘拒绝

**症状**：修完上一条后，`/file/complete` 报 `04000002 文件内容hash值不允许为空`。

**根因**：`session.Props.Hash` 从未被赋值。`req.Props.Hash` 有值，但构造 `UploadProps` 时漏掉了，`CompleteUpload` 传给云盘的 `ContentHash` 也就是空。同时秒传分支的 `UploadProps` 也漏了同一字段。

**修复**（`pkg/filemanager/fs/dbfs/upload.go`）：普通上传与秒传两条构造路径都补上 `Hash: req.Props.Hash`。

### 附带：token 续期回归测试

新增 `TestRenewalRewritesCredential` 与 `TestRefreshSkippedWhenTokenIsFresh`，覆盖：
- 15 天窗口内的凭据会调用刷新接口，新 token 落到 `client.authorization` 并经 `persistToken` 写回策略；
- 窗口外的凭据完全不动（不刷新、不改值）。

两个测试用假 `request.Client` 注入响应，不触网。

## 各项实测细节

### a. 列目录

`GET /api/v4/file?uri=...` 返回 `code=0`，`storage_policy.type=y139`。上传后云盘侧 `size=21600` 与写入字节数相等，`contentHash` 与本地 sha256 一致。

### b. 下载 302（不是 200 中转）

`POST /api/v4/file/url {"redirect": true}` 直接返回：

```
HTTP 302
Location: https://ykj-eos-wx2-01.eos-wuxi-3.cmecloud.cn/85ed04fc...086
```

跟随该 302 拿到的字节 `21600` 字节，sha256 与上传内容一致。驱动特地在 `init()` 里**不设** `HandlerCapabilityProxyRequired`，就是为了保住这个 302（设了就会强制走 Cloudreve 中转，302 消失）。

### c. 上传直传

完全按浏览器行为复现：

1. `PUT /api/v4/file/upload` 建会话，拿到 `session_id / upload_urls / callback_secret`；
2. 客户端**直 PUT** 到 `upload_urls[i]`（OBS 预签名），带云盘要求的头：
   `content-type: text/plain;name=<name>`、`contentSize`、`range`、`uploadtaskID`、`rangeType: "0"`；
3. 回调 `GET /api/v4/callback/y139/<session_id>/<callback_secret>`，由后端跑 `/file/complete`。

分片 PUT 返回 `200` 且带 `ETag`。后端**只签名，不中转内容**。

### d. 秒传

同内容换文件名再建会话，后端直接返回 `rapid_uploaded: true`，`upload_urls` 为空，零字节传输。云盘侧走 `exist` / `rapidUpload` 短路。

### e. 删除

删除语义分两段，实测都验过：

- `DELETE /api/v4/file` → `code=0`，云盘对象**仍在**（这是对的：此时只是进了 Cloudreve 回收站）；
- `DELETE /api/v4/file/trash` 清空回收站后，驱动调 `/recyclebin/batchTrash`，云盘侧 **2 秒内**消失。

> 附带发现（**非缺陷**）：实测中一度以为删除没生效，排查后确认是 Cloudreve 去重语义——相同内容被秒传复用同一 entity，只删其中一份不会让 entity 失效，entity 活着对象就不会被回收。用**唯一内容**重测即符合预期。报告里记一笔，避免后续误判。

### f. token 自动续期

真实账号当前 token 还有约 29.9 天有效期，**无法在不破坏账号的前提下安全模拟"即将过期"**，因此生产实例上只验证了"有效期内不折腾"：

- 策略里 294 字符的 `Authorization` 在多次请求后**完全未变**，`updated_at` 也未变 → 没有无谓刷新/写回。

续期**行为本身**用单元测试覆盖（见上），确认刷新调用、新 token 落值、`persistToken` 写回三者都正确。真实端到端触发留待 token 自然进入 15 天窗口时观察，本次如实记「单元测试通过、线上触发未验证」。

### g. 失效凭据的错误提示

把策略的 `Authorization` 换成结构中合法但 token 已过期的值，再触发上传：

```
HTTP 200  code=40099
msg: 139 cloud drive login state expired, please paste a fresh Authorization header
```

- **不是 500**，是 Cloudreve 规整过的业务错误码 `CodeStorageAuthExpired = 40099`；
- 文案可读，直接告诉管理员该做什么；
- 响应体**不回显**凭据；
- 还原凭据后立即恢复正常（`code=0`）。

## 协议层澄清（踩过的坑，供后来者）

- **`fileRenameMode`**：`auto_rename` 会返回 `04000002`；驱动用 `force_rename`，无此问题。最初的失败是探针脚本写错，不是驱动缺陷。
- **token 刷新接口**：`https://aas.caiyun.feixin.10086.cn:443/tellin/authTokenRefresh.do`，POST XML（不是 JSON 信封），成功返回 `<return>0` 并给新 token、+30 天。**叠加非轮换**——旧新 token 同时可用，所以驱动写回是安全的。
- **上传完成必须直连 OBS**：Cloudreve 后端只签名，不存在 `/upload/:sessionId/:index` 中转内容这条路；走它会拿到 `40006`。
- **公网网关会切断 PUT**：`https://15212-...instabox.tech` 对 PUT 会 502/超时，所有实测改走实例内部 `127.0.0.1:15212`（28~33ms）。这是测试环境限制，不是产品问题。

## 环境与复现

- 构建机 arm64，目标实例 x86_64：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0` 交叉编译，二进制上传到实例后机上 `FROM 现有镜像 + COPY` 烘焙（本机无 qemu binfmt，不能直接 `docker build` amd64）。
- 前端：`yarn install`（`npm ci` lock 不同步会失败），手工 `zip` 打包 `application/statics/assets.zip`。
- 策略字段映射：`AccessKey` → `Authorization`，`SecretKey` → `x-yun-app-key`，`BucketName` → 云盘根目录，`Server` → 主机。
- 挂载方式：`PUT /api/v4/file/policy {"uri": "cloudreve://my/<dir>", "policy": "<hashid>"}`。

## 清理

- 云盘侧：所有 `cr-test-*`、`badtoken-*`、`cr-y139-e2e` 测试文件已全部删除并清空回收站，复查云盘根目录只剩用户原有 8 项（`AI空间`、`应用目录`、`ChangAnAuto`、`手机备份`、`手机视频`、`手机图片`、`猫影视.rar`、`和彩云产品功能.jpg`）。**未触碰用户已有文件。**
- Cloudreve 侧：测试目录内文件全部清空，回收站已空。

## 测试实例状态与恢复

- 当前：`cloudreve-fork:4.23.6-y139test4`，容器 `cloudreve-fork-test`，`-p 15212:5212 -v /data/cloudreve-fork-test:/cloudreve/data --restart unless-stopped`。
- 回滚镜像保留：`cloudreve-fork:4.23.6-full2`（2.48GB）、`4.23.6-full`（2.35GB）。
- 数据库备份：`/data/backup-y139-20261029-120903.db`、`/data/backup-y139-<ts>-pre-fix.db`。
- 策略 `y139-test`（id=4, hashid=`NbI8`）保留，可继续用于观察；不再需要时在后台删掉策略并卸载 `cr-y139-e2e` 目录即可。

## 待办

- token 自然进入 15 天窗口时，观察一次线上续期是否按预期写回（单元测试已覆盖逻辑，缺线上实证）。
