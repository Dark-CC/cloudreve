# 右键下载直连云盘 —— 实现与验证报告

## 1. 结论

右键菜单里的「下载」现在请求云盘的直连地址，浏览器直接向上游发起请求，站点不再中转内容。

改动落在 `baixiong` 分支体系，不涉及 `main`。共 6 个文件、+89/−19。

## 2. 「关闭了中转却依然不 302」的真正原因

不是中转开关没生效，是右键下载这条路径**根本没有请求过直连 URL**。

调用链：

```
右键「下载」 → downloadFiles() → downloadSingleFile()
           → POST /api/v4/file/url  (FileURLService)
           → FileManager.GetEntityUrls → entitysource.Url → ShouldInternalProxy()
```

`ShouldInternalProxy()` 的判据里有一项是 `f.e.ID() == 0`（空实体）。而 `GetEntityUrls` 取的实体来自
`File.PrimaryEntity()`（`pkg/filemanager/fs/dbfs/file.go:309`），它的实现是：

```go
primary, _ := lo.Find(f.Model.Edges.Entities, func(item *ent.Entity) bool {
    return item.Type == int(types.EntityTypeVersion) && item.ID == f.Model.PrimaryEntity
})
if primary != nil { return fs.NewEntity(primary) }
return fs.NewEmptyEntity(f.Owner())
```

只有当实体 `type == EntityTypeVersion`（枚举值 **0**）**且** `id == primary_entity` 时才返回实体，否则一律返回空实体。

本次排查中遇到过 `primary_entity` 为 NULL、以及实体 `type` 被写成 1 的两种情况，都会让 `PrimaryEntity()`
返回 `ID() == 0` 的空实体 —— 于是 `ShouldInternalProxy()` 恒为真，无论策略上是否勾选了内部中转，都走站点中转。
这正是「我已经关了中转，却依然没有 302」的来源。

> 注：这条判据本身也是有意义的——空实体意味着没有可用的上游对象，只能由站点自己提供内容。问题在于
> 之前的排查把「为什么是空实体」当成噪声跳过了。

## 3. 改动

| 文件 | 改动 |
| --- | --- |
| `pkg/filemanager/fs/fs.go` | `FsOption` 新增 `NoInternalProxy bool` 与 `WithNoInternalProxy(b bool)` |
| `pkg/filemanager/manager/entity.go` | `GetEntityUrls` 计算 `directEntitySource := o.NoInternalProxy && meterClaim == ""`，仅此时给 `source.Url` 追加 `WithNoInternalProxy()`；URL 缓存 key 追加该标志，避免直连与中转结果互相串号 |
| `pkg/filemanager/manager/entitysource/entitysource.go` | `ShouldInternalProxy()`：加密实体恒为真（见 3.1）；其余情况下 `NoInternalProxy` 可越过策略的 `InternalProxy` |
| `service/explorer/file.go` | `FileURLService` 新增 `Direct bool`；`Get` 传 `fs.WithNoInternalProxy(s.Direct && s.Download)` |
| `frontend/src/api/explorer.ts` | `FileURLService` 接口新增 `direct?: boolean` |
| `frontend/src/redux/thunks/download.ts` | 单文件右键下载传 `direct=true`；批量（StreamSaver）与 `pickDownloadRoute` 保持原样 |

### 3.1 加密实体不参与直连

改动过程中发现一处必须堵住的漏洞：加密文件在存储上是**密文**，上游 URL 交给浏览器只会得到无法解密的字节，
且会让密文脱离站点鉴权。因此 `ShouldInternalProxy()` 里把加密判断提到最前：

```go
if f.e.Encrypted() {
    return true   // 加密实体永远走中转
}
return f.e.ID() == 0 || handlerCapability.StaticFeatures.Enabled(...) ||
    f.policy.Settings.InternalProxy && !f.o.NoInternalProxy
```

原来的写法是 `(InternalProxy || Encrypted()) && !NoInternalProxy`，`NoInternalProxy` 会把加密这一点也一并绕过。
现在加密实体与本地策略一样，保持站点中转。

### 3.2 计费会话不参与直连

`directEntitySource := o.NoInternalProxy && meterClaim == ""`：有 serve-time 计费 claim 的会话无法把 claim 挂到上游
URL 上，直连会绕过计费，故此时强制中转。`EntityUrl.Metered` 也是由 `MeterClaim != "" && ShouldInternalProxy()`
推出，两者一致。

### 3.3 CDN 池不改写直连 URL

云盘/对象存储的预签名 URL 是最终地址，`pickDownloadRoute` 对跨域地址不做 CDN 池改写，否则签名会失效。
批量 StreamSaver 下载需要 CORS 可读的字节流，因此仍走中转。

## 4. 验证

环境：InstaBox `m_agqw5litn4acpbo7b5axu3ycku`，站点 `https://15212-...instabox.tech`，容器 `cloudreve-fork-test`
（回滚镜像 + 挂载替换二进制），版本串 `4.23.6-directdl3`。

### 4.1 API 层：`direct` 越过策略的中转开关

策略 4 临时指向本地 S3 兼容探针并**显式开启 `internal_proxy`**，同一文件、同一账号：

```
direct=true  → {"url":"http://127.0.0.1:19000/probe-bucket/q.txt"}
direct=false → {"url":"https://15212-.../api/v4/file/content/JAC6/0/q.txt?download=true&sign=..."}
```

开启中转的策略下，`direct=true` 依然拿到上游地址 —— 这就是「关闭中转仍不 302」的修复路径。

### 4.2 真实浏览器点击（非单测）

Playwright 以真实账号登录，对文件右键 → 点「下载」，监听全部网络请求：

```
GET http://127.0.0.1:19000/probe-bucket/q.txt
```

页面没有发出 `/api/v4/file/content/` 请求，内容请求直接落到存储上游。

### 4.3 构建与测试

- `go build ./...`、`go vet ./pkg/filemanager/...` 通过
- `go test ./pkg/filemanager/...` 全部 ok（local / remote / s3 / fs / dbfs / manager / workflows）
- `yarn build` 通过（需 `NODE_OPTIONS=--max-old-space-size=6144`，本机 3GB 堆会 OOM）
- `tsc --noEmit`：`download.ts` 的两处 `showDirectoryPicker` 报错在改动前就存在（基线 127 条），非本次引入

### 4.4 与 139 驱动分支的合并验证

把 `tds/conv-01a18784...`（139 驱动）的全部改动叠加到本分支后：`fs.go` 两处改动互不重叠、`git apply --3way`
零冲突；合并树 `go build ./...` 通过、`go test ./pkg/filemanager/driver/y139/...` ok、`go test ./pkg/filemanager/...` 全部 ok。

## 5. 对 139（移动云盘）上传路径的核查

按另一份要求核查了「不上站中转」与「哈希传递」两点，结论：**驱动侧已满足**。

- **分片直传**：`Token()` 返回的 `upload_urls[i]` 直接取云盘 `/file/getUploadUrl` 返回的 `uploadUrl`，原样透传，
  站点侧不重写、不做任何代理。超过 100 片时后端分批补签，按下标对应分片。
- **合并完成**：`CompleteUpload` 由后端调云盘 `/file/complete`，本身不传内容；回调路由 `GET /api/v4/callback/y139/:sessionID/:key`
  只做完成确认，不接收数据。
- **前端直发**：`y139UploadChunk` 用的是不带 `baseURL` 的裸 `request`，PUT 直接发往云盘给出的绝对地址。
- **哈希**：前端在 `upload_dedup && !encryption` 时以流式 SHA-256 计算，经 `createUploadSession({hash})` →
  `fs.UploadProps.Hash` → 落库到上传会话，并作为 `CreateRequest.ContentHash` 交给云盘；`CompleteUpload` 也带
  `session.Props.Hash`。秒传命中时 `Token()` 直接置 `RapidUploaded`，`UploadURLs` 为空、零字节。
- **唯一发现的实际缺口**：`service/admin/policy.go` 未强制 y139 策略 `Relay=false`，且前端
  `dispatchUploader` 里 `if (task.policy.relay) return new Local(...)` 在 `PolicyType.y139` 分支之前。
  即**管理员若开启上传中转，y139 会退回 `Local` 组件、内容经站点传输**。这是需要单独决策的问题，
  本任务不擅自扩大范围去改它。

## 6. 需要用户决定的事项

1. **是否强制 y139 不上传中转**（第 5 节末尾的缺口）。两个选项：后端在策略保存时强制 `Relay=false`；
   或保留开关但在前端向导上明确标注「开启后内容将经站点转发」。两者都需要动 `service/admin/policy.go`。
2. **分片默认值与文档冲突**：代码默认 50MB，`docs/139-*` 写「≤30GB 用 100MB」。是否统一到文档值。
3. **适用范围**：本次 `direct` 对**全云盘统一生效**（s3/oss/cos/obs/ks3/qiniu/upyun/onedrive 等），
   而不是只对 139 放开。理由：这些驱动都能直接给出上游 URL，语义一致；`HandlerCapabilityProxyRequired`
   的驱动（如 remote）仍强制中转，加密实体与计费会话也仍中转。若希望只对 139 放开，改动点集中在
   `FileURLService.Get` 里加一个按策略类型判断的条件，风险更低但行为不一致。

## 7. 测试环境残留与清理

- 策略 4（`y139-test`）已还原：`server` 置空、`bucket_name=root`、`is_private=1`、`s3_path_style=false`、
  `internal_proxy` 已移除
- S3 兼容探针容器已删除
- 15212 磁盘：99% → **48%**（`docker image prune` 回收 2.03GB，并清理探针构建产物与临时备份）
- 本机构建副本与前端 staging 目录已删除，磁盘 40%
- 回滚二进制保留在 `/data/home/probe/cloudreve-directdl.bak-v2`

## 8. 未能验证的部分

- **139 真实直传**：15212 上两个 y139 策略只有占位假 token（`access_key` 32 字符），备份库里也没有真实凭据，
  无法复现真实账号的直传/秒传。此前的 `docs/139-driver-e2e-report.md` 记录过真实账号验证通过。
- **真实 CDN/对象存储 302**：本机与 15212 均无法拉取 minio 等镜像，用自写 Go S3 兼容探针替代。探针给出的
  是 `http://127.0.0.1:19000/...` 形态的地址，与真实预签名 URL 的跨域跳转语义一致，但不是真实 CDN 链路。
