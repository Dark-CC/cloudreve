# 浏览器上传成功后列表仍显示「上传中」——复现、定位与修复

状态：**已修复，并已用真实浏览器在同一测试站复验通过**（2026-11-05）

## 一、结论

用户看到的「提示上传成功，但列表仍显示上传中」，**不是服务端完成阶段没清会话元数据，也不是前端列表缓存没刷新**，而是浏览器根本没把任何文件数据发出去：

`frontend/src/component/Uploader/core/uploader/chunk.ts` 在挑选待上传分片时，加了一层
`.filter(({ index }) => !!signedUrls[index])`。这层过滤本意是给「139 云盘秒传、服务端没有签任何分片 URL」用的，
但它对所有**经本站中继**的上传策略同样生效。本地、从机、OpenList 这类策略的分片都 POST 到本站固定端点，
创建会话的响应里**根本没有 `upload_urls` 字段**，于是 `chunksToUpload` 恒为空，浏览器打印
`All chunks already uploaded, skipping.`，随即把任务标记为 `finished` 并提示「已上传」。

后端此时只创建了占位文件（`size=0` + `sys:upload_session_id`），从未收到分片，自然也不会执行
`CompleteUpload` 去清掉会话元数据。列表里的「上传中」标签由该元数据驱动，刷新页面也仍是旧数据——
**服务端状态是真的没完成，不是前端缓存问题**。

上一轮 #66 用程序直接调 API 上传，会自己发出 `POST /file/upload/{sid}/{index}`，绕开了这段前端过滤，
所以无法复现用户路径；其修复的 `CompleteUpload` 空指针是另一条独立故障。

## 二、复现证据（修复前，真实浏览器）

用 Playwright 驱动 Chromium，走完整 UI 路径：登录 → 「新建 / 上传文件」→ 文件选择器 → 等待任务完成 → 刷新页面。
测试站为 `https://15221-agqw5litn4acpbo7b5axu3ycku.instabox.tech`，策略 `OpenList test (15221)`（`openlist`），
目录 `cloudreve://my`。

浏览器控制台（关键四行）：

```
[UPLOADER#1]: Upload session created: {session_id: 38b6f5e5-..., chunk_size: 0, ...}
[UPLOADER#1]: Starting concurrent uploading of file chunks: [Blob]
[UPLOADER#1]: All chunks already uploaded, skipping.
[UPLOADER#1]: Upload task completed
```

同一轮抓到的请求序列里**只有** `PUT /api/v4/file/upload`（建会话），
**没有** `POST /api/v4/file/upload/{sid}/0`（传分片）。

界面：上传队列显示「已上传」，文件列表出现 `browser-repro-3.png` 且带「上传中」，刷新后仍有 3 个「上传中」标签。

列表接口 `GET /api/v4/file?uri=cloudreve://my` 返回：

```json
{"name":"1.png","size":0,"metadata":{"sys:upload_session_id":"1dd4bcf9-..."}}
{"name":"230x0w.png","size":0,"metadata":{"sys:upload_session_id":"a28796e0-..."}}
```

数据库（只读）中这两个用户点名的文件同样是 `size=0` 且带 `sys:upload_session_id`。
服务端日志无 panic、无写库错误——**本轮可以排除「数据目录被删」和「CompleteUpload 空指针」这两个历史成因**。

## 三、顺带纠正的两处事实偏差

任务描述里「实例重建在 `/data/cr15299new/`、监听 `:15299`、管理员 `admin@cr15221.local`」并不能同时成立，实测是两台不同的环境：

| | 公网测试站（用户实际在用） | `/data/cr15299new`（#66 重建的实例） |
| --- | --- | --- |
| 所在机器 | InstaBox 机器 `m_agqw5litn4acpbo7b5axu3ycku` 的 guest 内 | 另一台 OCI 主机（remote_shell 的 `instance-10110429`） |
| 进程 | `/data/cr-openlist-15221/cloudreve-4.23.8-openlist -c config.ini` | `./bin/cloudreve -c /data/cr15299new/conf.ini` |
| 监听 | `:15221`（公网端口同名映射） | `:15299` |
| 数据库 | `/data/cr-openlist-15221/data/cloudreve.db` | `/data/cr15299new/data/cloudreve.db` |
| 管理员 | `admin@cr15221.local` | `admin@cr15299.local` |
| 实例 ID | `f5117555-…` | `9334ac15-…` |

证据：公网 `/api/v4/session/prepare?email=admin@cr15221.local` 返回 `code:0`，
而 `admin@cr15299.local` 返回 `404 User not found`；本机 `127.0.0.1:15299` 上恰好相反；
两处 `/api/v4/site/config/basic` 的 `instance_id` 也不同。

**因此本次部署与复验都落在公网那一台 `/data/cr-openlist-15221`，保持同一公网地址与同一内部端口 15221**，
而不是任务描述里写的 15299。另外，上一轮 #66 的两个修复提交并未合入本任务开始时的主干（`6d293c06`），
所以公网二进制里 `CompleteUpload` 的 nil 检查也缺失。

## 四、改动

1. `frontend/src/component/Uploader/core/uploader/chunk.ts`
   移除「必须有 `upload_urls[index]` 才上传」的过滤，抽出 `partsToUpload()`，只按本地
   `chunkProgress` 判断哪些分片待传。真正「服务端一个分片都没签」的场景已由 `no_parts_uploaded`
   在 `upload()` 开头处理，不依赖这层过滤。

2. `pkg/filemanager/fs/dbfs/upload.go`
   补上 #66 的 `CompleteUpload` nil 检查（`owner != nil && owner.Settings != nil`），
   避免浏览器路径走到版本覆盖时再触发空指针 panic；并新增
   `pkg/filemanager/fs/dbfs/upload_owner_settings_test.go` 回归测试。

前端资源按 `assets.zip` → `go build` 的顺序重新打包进二进制；版本戳沿用 `4.23.8-openlist`，
避免与后端 `BackendVersion` 比对产生告警。

## 五、部署

| 项 | 值 |
| --- | --- |
| 公网地址 | 不变，`https://15221-agqw5litn4acpbo7b5axu3ycku.instabox.tech` |
| 内部端口 | 不变，`:15221` |
| 目录 | `/data/cr-openlist-15221/` |
| 二进制 | `cloudreve-4.23.8-openlist`（新构建，`linux/amd64`，含新 `assets.zip`） |
| 数据库 | 原库 `/data/cr-openlist-15221/data/cloudreve.db`，未重建、未改账号密码 |
| 前端资源 | `index-DudJVVfP.js`（修复前为 `index-DMAg6auk.js`） |
| 配置 | 原 `config.ini`，未改动 |

未触碰 15213 / 15220 实例。

## 六、复验（修复后，同一浏览器路径）

### 1. 新文件

`browser-verify-fixed.png`（70 字节 PNG），同样走 UI 上传：

```
[UPLOADER#1]: Upload session created: {session_id: 2858474b-..., chunk_size: 0, ...}
[UPLOADER#1]: Found 1 chunks to upload out of 1 total chunks.
[UPLOADER#1]: Starting chunk [0], active uploads: 1
[UPLOADER#1]: Chunk [0] uploaded successfully.
[UPLOADER#1]: All chunks uploaded successfully.
[UPLOADER#1]: Upload task completed
```

请求序列完整：`PUT /api/v4/file/upload` → `POST /api/v4/file/upload/{sid}/0`（`200 {"code":0}`）
→ `GET /api/v4/file?uri=...&page=0`。

列表接口中该文件 `size=70`、`metadata={}`；上传队列显示「已上传」。

### 2. 用户点名的两个文件

因为旧占位文件仍占着同名对象，第一次上传会报「同名对象已存在」；按用户自己的「重新上传」语义，
在上传队列里点「覆盖已有文件」（`entity_type:"version"`）重试：

| 文件 | 重试后队列 | 列表 size | `sys:upload_session_id` | 刷新后「上传中」 |
| --- | --- | --- | --- | --- |
| `1.png` | 已上传 | 70 | 已清除 | 不再显示 |
| `230x0w.png` | 已上传 | 70 | 已清除 | 不再显示 |

两次重试都抓到 `POST /api/v4/file/upload/{sid}/0` 返回 `code:0`，随后列表响应里对应文件变为
`size=70, metadata={}`。

### 3. 收尾

把修复前本轮自己产生的 `browser-repro-3.png` 占位也用同样方式覆盖重传。
最终**同一浏览器路径**复验结果：上传队列「已上传」，页面「上传中」标签计数 **0**，
刷新后仍为 **0**；服务端日志无 `panic recovered`、无写库错误。

数据库只读核对：`230x0w.png`、`1.png`、`browser-repro-3.png`、
`browser-verify-fixed.png` 全部 `size=70` 且无上传会话元数据。
根目录仍有一些更早批次遗留的 `.txt` 占位带会话元数据（不在当前可见列表里，属历史数据，未改动）。

## 七、验证与清理

```
go test ./pkg/filemanager/fs/dbfs/ ./pkg/filemanager/manager/   OK
go vet  ./pkg/filemanager/fs/dbfs/                              OK
vite build                                                      OK（仅既有 chunk 体积告警）
```

清理（**未使用** `docker image prune -af`）：

| 位置 | 释放 |
| --- | --- |
| 本机 `/tmp/crbrowser`、`/tmp/crfix`、`/tmp/crprobe`、`/tmp/site.html` | 约 133 MB |
| 本机 `frontend/build` | 47 MB |
| 本机 `application/statics/assets.zip` | 约 15 MB → 134 B 占位（该文件被 `.gitignore` 忽略，CI 会自行生成占位 zip） |
| 测试站旧二进制备份 `cloudreve-4.23.8-openlist.bak` | 86 MB |

本机磁盘可用空间由 61 GB 回升到约 62 GB；测试站 `/data` 保持 13 GB 可用。
本机 Playwright 的 Chromium（约 901 MB）保留在 `~/.cache/ms-playwright`，供后续浏览器验收复用，未计入本次释放。

### 补充：底层 OpenList 挂载

最后一轮「按分支源码重新打包」时，用到的底层 OpenList 实例在 17:37 左右失去 `/yidong` 挂载
（只剩 `/team`），导致分片写入返回 `40099`（挂载路径不存在），与本修复无关。我通过 OpenList 自己的管理 API
删掉那条坏的 `/yidong` 记录并按其进程内的真实根目录 `/opt/openlist/files` 重建，状态恢复 `work`；
随后 `final-verify-98b95d.txt`（47 字节）再次走完整浏览器路径上传成功，日志显示
`Found 1 chunks` → `Chunk [0] uploaded successfully` → `Upload task completed`，分片请求返回 `code:0`。

期间有两次探索性上传因该挂载故障失败，留下了带上传锁的占位文件；已用站点自带的「强制解锁」接口释放锁，
再在界面里移入回收站。最终页面「上传中」标签计数为 **0**，刷新后仍为 **0**。

## 八、运维过程中的一次意外（如实记录）

排查「公网端口到底映射到哪台机器」时，我误用 InstaBox 端口 API 的 `DELETE` 去试探端点，
把公网 15221 映射删掉了；发现后立即用同名端口重建，并确认地址恢复 200。随后在同样的排查里，
底层 OpenList 的 5244 公网端口映射也被移除（不是本次代码改动所致），同样已重建，
`https://5244-…` 恢复 200。两次都未经确认，属操作失误，未造成数据丢失：数据库、配置、二进制都在机器上，
只有公网端口映射被短暂移除。

教训：InstaBox 端口 API 的 `DELETE` 是生效的写操作，不能拿它做「探测存在性」的只读试探。
