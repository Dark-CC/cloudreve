# OpenList API 对接可行性研究：读写、秒传、断点续传、挂载指定路径

> 研究日期：2026-11-01　|　机器：`centos-stream-server`（192.168.31.202，aarch64）
> 性质：**只读研究 + 临时实例实测**（未改本仓库代码、未部署、未发版、未触碰 15212/15213/生产与 15220 测试站）
> 结论来源：**OpenList v4.2.9 真实实例实测**（临时进程，端口 5244，独立数据目录）＋ v4.2.9 源码（tag 快照）逐条核对
> 姊妹文档：[189-enterprise-drive-feasibility.md](./189-enterprise-drive-feasibility.md)、[189-probe-report.md](./189-probe-report.md)、[139-driver-implementation.md](./139-driver-implementation.md)
> 凭据：本报告中**无任何真实凭据**；探测用的临时实例密码为一次性随机值，实例与数据已全部销毁（见 §8）

---

## 0. 一句话结论

**可以对接，且比 189 干净得多：OpenList 提供完整的 HTTP API（含分片断点续传与秒传），路径模型能稳定锚定「某个存储的某个子路径」。但「不中转」只能在下载侧部分成立 —— 上传侧要做到浏览器直传底层存储，只对 OpenList 88 个驱动中的 4 个有效；秒传与直传都是「按底层存储能力协商」的可选能力，不是通用保证。**

| 研究项 | 结论 | 关键依据 |
| --- | --- | --- |
| 1. API 契约完整度 | ✅ **全套齐全**，7 类操作全部实测通过；**无官方 OpenAPI/Swagger** | §2 |
| 2. 挂载指定路径 | ✅ **可稳定锚定**，支持多挂载点、嵌套挂载、同目录多路径别名 | §3 |
| 3. 秒传 | ⚠️ **存在但非通用**：接口是「种子/CAS 转存」模型，**88 个驱动中仅 12 个实现** | §4 |
| 4. 断点续传 | ✅ **完整支持**：分片、乱序、幂等重发、按 path+size 找回会话 | §5 |
| 5. 直连边界 | ⚠️ **下载可 302 到底层直链（视驱动与 `web_proxy` 开关）**；**上传直传仅 4 个驱动支持** | §6 |
| 6. 多站长模型 | ✅ 一个 OpenList = 一个策略可行；⚠️ 凭据失效告警**在本仓库需改一处硬编码** | §7 |
| 7. 能力差异 | ✅ OpenList 自带能力探测接口，可据此暴露能力位 | §4、§6 |

---

## 1. 测试环境与方法

| 项 | 值 |
| --- | --- |
| OpenList 版本 | **v4.2.9**（Commit `0497ee8`，前端 v4.2.9，构建 2026-10-25） |
| 获取方式 | GitHub Release `openlist-linux-arm64-lite.tar.gz`（35.6 MB），经代理 `192.168.31.200:1082` 下载 |
| 运行方式 | 临时进程 `openlist server --data /tmp/olprobe/data2`，监听 **5244**，独立数据目录 |
| 存储驱动 | `Local`（指向 `/tmp/olprobe/local`），用于验证接口语义（非目标生产驱动） |
| 出网 | `no_proxy=localhost,127.0.0.1,192.168.0.0/16,10.0.0.0/8`，所有 API 调用走 `127.0.0.1` 直连 |
| 源码核对 | `OpenListTeam/OpenList` tag `v4.2.9` 源码快照，逐条比对路由与 handler |
| 清理 | 进程已停、端口已释放、`/tmp/olprobe` 与 `/tmp/OpenList-4.2.9` 已删除（§8 有实测输出） |

> **说明**：本机无 docker/podman（`which docker podman` 均无输出），故使用 release 二进制而非容器（Spec 允许「容器/进程」二选一）。临时实例仅监听本机，未对外开放。

---

## 2. API 契约（研究项 1）

### 2.1 鉴权方式

| 项 | 实测值 |
| --- | --- |
| 登录端点 | `POST /api/auth/login`，body `{"username","password"}` |
| 返回 | `{"code":200,"data":{"token":"<JWT>"}}`，**裸 JWT 串，无 Bearer 前缀** |
| 携带方式 | 请求头 `Authorization: <token>`（**直接放 token，不加 `Bearer `**） |
| JWT 载荷 | `{"username":"admin","pwd_ts":0,"exp":...,"nbf":...,"iat":...}` |
| **有效期** | 实测 `exp - iat = 172800` 秒 = **48 小时**，来源 `config.json` 的 `token_expires_in: 48`（小时） |
| 持续使用策略 | 🚨 **无 refresh token 机制**。JWT 由 `jwt_secret` 签发，`token_expires_in` 可配；**到期即失效，必须重新登录** |

**对本站的直接含义**：策略里存的不能只是一次性 token —— 48 小时后必然失效。可行做法是**存用户名+密码，由本站按需登录换 token 并缓存**，在 `exp` 前主动续；或要求站长把 `token_expires_in` 调大（不推荐，属安全降级）。这一点与 139 的「贴一次登录态」形态不同。

### 2.2 后端实测的鉴权失败表现

| 场景 | 响应 |
| --- | --- |
| 无 token | HTTP 200，`{"code":401,"message":"Guest user is disabled, login please"}` |
| 无效 token | HTTP 200，`{"code":401,"message":"token is invalidated"}` |

> 🚨 **重要**：OpenList 把业务错误放进 **HTTP 200 的 body** 里，靠 `code` 字段区分。驱动的错误映射**必须读 body 的 `code`，不能只看 HTTP 状态码**，否则所有失败都会被当成成功。

### 2.3 文件操作端点全表（全部实测）

所有路径均为**虚拟路径**（挂载点下的路径），而非底层存储的真实路径。分页参数统一为 `page` / `per_page`。

| # | 操作 | 方法 | 端点 | 关键请求体 | 实测结果 |
| --- | --- | --- | --- | --- | --- |
| 1 | 列目录 | POST | `/api/fs/list` | `{"path","page","per_page","refresh"}` | ✅ `200` |
| 2 | 取文件信息 | POST | `/api/fs/get` | `{"path"}` | ✅ `200`，含 `raw_url` |
| 3 | 建目录 | POST | `/api/fs/mkdir` | `{"path"}` | ✅ `200` |
| 4 | 重命名 | POST | `/api/fs/rename` | `{"path","name"}` | ✅ `200` |
| 5 | 移动 | POST | `/api/fs/move` | `{"src_dir","dst_dir","names":[]}` | ✅ `200` |
| 6 | 复制 | POST | `/api/fs/copy` | `{"src_dir","dst_dir","names":[]}` | ✅ `200` |
| 7 | 删除 | POST | `/api/fs/remove` | `{"dir","names":[]}` | ✅ `200` |
| 8 | 上传（流式） | PUT | `/api/fs/put` | 头 `File-Path`，body 为文件内容 | ✅ `200` |
| 9 | 上传（表单） | PUT | `/api/fs/form` | multipart | 未测（流式已满足） |
| 10 | **分片上传** | POST/PUT | `/api/fs/multipart/*` | 见 §5 | ✅ 全流程 |
| 11 | 取下载链接 | POST | `/api/fs/link` | `{"path"}` | ✅ `200`，**但需 admin 权限** |
| 12 | 递归列子目录 | POST | `/api/fs/dirs` | `{"path","force_root"}` | ✅ `200` |
| 13 | **客户端直传** | POST | `/api/fs/get_direct_upload_info` | 见 §6 | 本地驱动返回 `not implement` |
| 14 | **秒传能力探测** | POST | `/api/fs/seed/capabilities` | 见 §4 | ✅ `200` |

**源码核对**：以上端点与 `server/router.go` 的 `_fs()` 路由组逐条一致（`v4.2.9`）。

### 2.4 分页语义（实测）

`{"path":"/local/docs","page":2,"per_page":2}` → `total=4`，本页返回 2 项（第 3、4 个文件）。
**注意**：`total` 是该目录条目总数，本页条数需按 `per_page` 自行切片；`refresh:true` 可强制绕过缓存。

### 2.5 错误码契约（实测，全部为 HTTP 200 + body `code`）

| 场景 | `code` | `message` |
| --- | --- | --- |
| 路径不存在 | `500` | `failed to get obj: object not found` |
| 挂载点不存在 | `500` | `failed get storage: storage not found; rawPath: /nomount` |
| **无任何存储** | `500` | `failed get storage: storage not found; please add a storage first` |
| 路径含 `..` | `403` | `using relative path is not allowed` |
| 重命名撞名 | `403` | `file [dedup.txt] exists` |
| 删除不存在的项 | `200` | `success`（**幂等，不报错**） |
| 未鉴权 | `401` | `Guest user is disabled, login please` |
| token 失效 | `401` | `token is invalidated` |
| 驱动不支持直传 | `500` | `not implement` |
| 分片会话不存在 | `404` | `multipart upload session not found` |

> 驱动需把这些映射到 Cloudreve 的 `serializer` 码。特别注意 `500` 被复用于「对象不存在」（OpenList 用 `code` 而非 HTTP 语义），**不能按 HTTP 500 一律当服务端错误**。

### 2.6 官方 OpenAPI / Swagger —— ❌ 没有

实测 6 个常见路径逐一探测：

```
/swagger.json  -> 200 (Content-Type: text/html)
/openapi.json  -> 200 (Content-Type: text/html)
/api/openapi.json -> 200 (Content-Type: text/html)
/api/docs /docs /swagger/index.html -> 200 (text/html)
```

**全部 200 但 `Content-Type: text/html`，返回的是前端 SPA 的 `<!doctype html>` 兜底页，不是 OpenAPI 文档**。结论：**OpenList 不提供官方 OpenAPI/Swagger 规范**，契约只能靠源码与实测建立。这增加了对接的维护成本——**API 变更无法从规范文件自动感知**。

---

## 3. 挂载指定路径（研究项 2）

### 3.1 路径模型

OpenList 是**虚拟文件系统**：管理员在 `/api/admin/storage/create` 里声明 `mount_path`（虚拟路径）与 `driver` + `addition`（底层驱动参数，如 `root_folder_path`）。用户 API 只见虚拟路径。

**实测验证的三种结构**：

| 结构 | 操作 | 实测结果 |
| --- | --- | --- |
| 单挂载 | `/local` → `/tmp/olprobe/local` | ✅ 列 `/` 见 `local` 一项 |
| **子目录挂载** | `/team` → `/tmp/olprobe/local/sub` | ✅ 列 `/team` 只见 `b.txt` 与 `docs`（子目录内容） |
| **嵌套挂载** | `/team/nested` → `/tmp/olprobe/local/docs` | ✅ 列 `/team/nested` 见 `a.txt`、`dedup.txt`、`empty.txt` |
| **同物理目录多别名** | `/team/docs/a.txt` vs `/local/sub/docs/a.txt` | ✅ 两条路径访问**同一文件**（`name=a.txt, size=21`） |

**结论**：
- ✅ **可以稳定锚定到「某个存储的某个子路径」**——站长在 OpenList 里建 `mount_path` 指向目标子目录即可，本站只需在策略里存这个 `mount_path` 前缀。
- ✅ **一个 OpenList 可承载多个挂载点**，因此「一个 OpenList = 一个策略」与「一个 OpenList = 多个策略（按挂载点区分）」都可行。
- ⚠️ **同一文件可能有多条虚拟路径**（别名）。若本站以路径作为文件唯一标识，站长假如同一个目录挂两次，**同一文件会以两个不同路径出现**，需在策略层约定「一个策略锚定一个挂载点前缀」来规避。

### 3.2 路径变更后的行为

- 虚拟路径就是 API 的唯一寻址方式，**底层存储路径变更（站长改 `addition`）会导致原虚拟路径指向新位置** —— 对本站是透明的，但文件「内容可能整个换了」而路径不变。
- 若站长**删除或改名挂载点**，原路径立刻失效，报 `storage not found`（实测）。
- 路径越界（含 `..`）被服务端拒绝：`403 using relative path is not allowed`（实测）。

> **对本站的建议**：策略里存「挂载点前缀」，并在保存时用 `/api/fs/list` 探测一次校验其存在；失效时给出可诊断的提示，而不是笼统的「存储不可用」。

---

## 4. 秒传（研究项 3）

### 4.1 OpenList 的秒传是「种子 / CAS 转存」模型，不是简单的 md5 查询

秒传集中在 `/api/fs/seed/*` 组（源码 `server/router.go` + `server/handles/torrent.go`），**核心接口 `POST /api/fs/seed/capabilities` 实测可用**：

```
请求: {"paths":["/local/docs/a.txt"],"path":"/local/docs"}
响应: data.default_matrix = {"md5":{"whole":true,"pieces":false},
                             "sha1":{...}, "sha256":{...}}
      data.files[0] = {"available_hashes":[], "requires_download":true,
                       "requires_fetch":true, "streamable":true,
                       "estimated_traffic":21, ...}
      data.formats = {"cas":true,"oss":true,"torrent":true}
```

语义（源码核对 `SeedCapabilities`）：
1. 传入**已在 OpenList 里的文件路径**（`paths`），OpenList 算出其 md5/sha1/sha256；
2. 返回该文件**哪些哈希已存在**（`available_hashes`）、**是否需要下载内容才能算**（`requires_download`）；
3. 真正秒传走 `/api/fs/seed/rapid_upload`（`QuickSaveSeed`），把 seed/cas 数据交给**目标存储驱动的 `SeedRapidUploader` 接口**。

### 4.2 能力边界（源码统计，`v4.2.9`）

- **88 个驱动**中，实现 `SeedRapidUploader` 的只有 **12 个**：
  `115`、`123`、`123_open`、`189_tv`、`189pc`、`aliyundrive_open`、`baidu_netdisk`、`pikpak`、`quark_open`、`thunder`、`thunder_browser`、`thunderx`。
- 其余（含 `Local`、`S3`、`OneDrive`、`WebDAV` 等）**不支持秒传**。

**结论**：
- ⚠️ **秒传不是通用能力，是「按底层存储协商」的可选能力**。做「全套带秒传」时，**必须把秒传做成能力位**，不支持的存储自动降级为普通上传。
- ⚠️ **秒传的哈希语义由目标存储决定**（有的要 md5、有的要 sha1、有的要 sha256），本站不能预设单一哈希。
- ⚠️ 秒传的触发条件是**目标存储已有该哈希的索引**（通常意味着同一个 OpenList/同一存储里已有人传过，或站长侧已有索引）。**不是「本站在别处算过 md5 就能秒传」**。

> **对开放问题的回答倾向**：秒传的哈希应由 **OpenList 侧**掌握（通过 `/api/fs/seed/capabilities` 探测），本站只负责把「本地已有文件的哈希」传过去并读取结果。**本站自行计算哈希对跨 OpenList 实例没有意义**——哈希索引在各站长自己的存储里，互不相通。

---

## 5. 断点续传（研究项 4）

OpenList **原生提供完整的分片上传与断点续传**，这是本次研究最正面的一项。契约全部实测。

### 5.1 接口全表

| 步骤 | 方法 | 端点 | 关键参数 |
| --- | --- | --- | --- |
| 初始化 / 续传 | POST | `/api/fs/multipart/init` | 头：`File-Path`、`X-File-Size`（必填）、`X-Chunk-Size`（可选）、`X-File-Md5`/`Sha1`/`Sha256`（可选）、`Overwrite`（默认 true） |
| 上传分片 | PUT | `/api/fs/multipart/chunk` | 头：`X-Upload-Id`、`X-Chunk-Index`，body 为分片内容 |
| 完成 | POST | `/api/fs/multipart/complete` | 头：`X-Upload-Id` |
| 查询状态 | GET | `/api/fs/multipart/status` | `?upload_id=` **或** `?path=&size=` |
| 中止 | POST | `/api/fs/multipart/abort` | 头：`X-Upload-Id` |

### 5.2 实测结果

**初始化**：
```json
{"code":200,"data":{"upload_id":"ae2920ef-...","state":"receiving","attempt":0,
 "path":"/local/docs/mp.bin","size":3000000,"chunk_size":1048576,
 "total_chunks":3,"received":null,"received_bytes":0,"frontier":0,
 "storage_progress":0,"resumed":false}}
```

**乱序 + 幂等**（这是断点续传可靠性的核心）：

| 动作 | 返回 `received` | `frontier` |
| --- | --- | --- |
| 发 chunk 0 | `[[0,0]]` | 0 |
| 发 chunk 1（跳过顺序无关） | `[[0,1]]` | 1 |
| **重发 chunk 0** | `[[0,1]]`（幂等，未重复计数） | 2 |

**断点续传找回会话**：`GET /api/fs/multipart/status?path=/local/docs/mp.bin&size=3000000` 返回**同一个 `upload_id`** 及已收分片区间 —— 客户端崩溃后无需本地保存 id 即可恢复。

**完整上传验证**（3 MB，服务端 chunk 10 MB → `total_chunks=1`）：
```json
{"code":200,"data":{"state":"completed","received_bytes":3000000,"frontier":1,"storage_progress":100}}
```
落盘校验：`/api/fs/get` 返回 `size=3000000` ✅ 字节数一致。

### 5.3 分片大小语义（重要的坑）

源码 `multipartChunkSize()`：
- **管理员设置 `MultipartChunkSize`（默认 10 MB）是上限**；
- 客户端可通过 `X-Chunk-Size` 请求**更小**的分片，但**不能更大**；
- 下限 **1 MB**。

实测印证：请求 `X-Chunk-Size: 1048576`（1 MB）时生效；另一次不指定时服务端给了 `10485760`（10 MB），把 3 MB 文件算成 1 块。

> 🚨 **对本站的含义**：Cloudreve 的策略里有 `ChunkSize` 配置。若本站的 `ChunkSize` **大于** OpenList 侧上限，分片会被服务端合并处理（不报错但违背预期）；若小于 1 MB 会被抬到 1 MB。**策略里不能自由设定分片大小，必须以 OpenList 的 `chunk_size` 为准**（`init` 响应里返回实际值）。

### 5.4 并发与流控

源码注释明确（`MultipartChunk`）：
- 分片**可并发、可乱序**发送；
- **`429`** = 接收窗口已满（流控，稍后重试）；
- **`409`** = 同一分片正在另一连接上传；
- **`404`** = 会话不存在；**`403`** = 非属主。

> 这是干净的、可直接映射到 Cloudreve 重试逻辑的语义（类似 S3 分片上传）。

### 5.5 不支持分片的存储如何降级

分片是 **OpenList 服务端**的能力，**与底层存储是否支持分片无关** —— OpenList 自己在本地组片后再交给底层驱动 `Put`。因此：
- ✅ **对所有存储都能用分片/断点续传**（这是 OpenList 带给本仓的最大价值）；
- ⚠️ 但**断点续传只在 OpenList 会话存活期内有效**（会话有 GC，`multipartPkg.DefaultManager.StartGC()`）；**跨重启不保证**。

---

## 6. 直连可行性（研究项 5，最关键）

### 6.1 下载：能否拿到底层直链？

**取决于驱动是否 `MustProxy()` 与存储的 `web_proxy` 开关**。源码 `server/handles/fsread.go` 的决策：

```go
if storage.Config().MustProxy() || storage.GetStorage().WebProxy {
    rawURL = <OpenList 自身的 /p/ 代理地址>
} else {
    // 优先用驱动给出的直链，否则 fs.Link() 解析
    if url, ok := model.GetUrl(obj); ok { rawURL = url }
    else { link, ... := fs.Link(..., Redirect: true); rawURL = link.URL }
}
```

**实测（Local 驱动 + `web_proxy:false`）**：
```
/api/fs/get  →  raw_url = http://127.0.0.1:5244/p/local/docs/a.txt?sign=...:0
GET 该地址   →  HTTP/1.1 200 OK（Accept-Ranges: bytes, Content-Length: 21）
```
- Local 驱动是**由 OpenList 本机文件系统直接出流**，因此 `raw_url` 指向 OpenList 自身 —— 这是 **200 直出，不是 302**，字节经 OpenList。
- 对**云盘类驱动**（139/189/115 等），`rawURL` 会是底层存储的真实直链，此时可以 302 让浏览器直取。

**结论**：
- ✅ **下载侧「不中转」可以实现到「OpenList 不转发字节」的程度** —— 前提是该 OpenList 的驱动实现了直链且 `web_proxy=false`。
- ⚠️ 对 Local/部分驱动，OpenList 本身就是文件服务，**必然经它转发**（它本来就是存储）。
- ⚠️ `/api/fs/link` 端点**要求 admin 权限**（源码 `g.POST("/link", middlewares.AuthAdmin, ...)`）。若本站用**普通用户 token**，只能用 `/api/fs/get` 的 `raw_url`。这是策略设计要决定的：**用 admin 账号（权限过大）还是普通账号（拿不到 link 接口）**。

### 6.2 上传：能否浏览器直传底层存储？

**这是本次研究最重要的否定结论。**

`POST /api/fs/get_direct_upload_info`（契约已实测）返回：
```go
type HttpDirectUploadInfo struct {
    UploadURL string            // 直传地址
    ChunkSize int64             // 0 = 不分片
    Headers   map[string]string
    Method    string            // 默认 PUT
}
```
- 实测 **Local 驱动**返回：`{"code":500,"message":"not implement","data":null}` —— 不支持直传。
- 源码统计：**88 个驱动中仅 4 个实现 `DirectUploader`**：`onedrive`、`onedrive_app`、`onedrive_sharelink`、`s3`。

**结论**：
- ⚠️ **「浏览器直传 OpenList/底层存储」只在极少数驱动上成立**。绝大多数情况（含 Local、大部分云盘）**上传必然经 OpenList 中转**。
- ✅ 但「不中转」的第二种可能仍然存在：**浏览器 → OpenList 的分片接口**（`/api/fs/multipart/*`）也是直连 OpenList，**不经本站**。这是「不中转**本站**」但「经 OpenList」的形态。

### 6.3 直连边界的三种形态（务必与用户对齐）

| 形态 | 字节路径 | 可行性 |
| --- | --- | --- |
| A. 全中转 | 浏览器 ↔ **本站** ↔ OpenList ↔ 底层 | 永远可行，但违背用户「不中转」意图 |
| B. 经 OpenList | 浏览器 ↔ **OpenList** ↔ 底层，本站只签发参数 | ✅ **推荐**，上传/下载都成立；本站不碰字节 |
| C. 真直连底层 | 浏览器 ↔ **底层存储**，OpenList 只签发 | ⚠️ 下载视驱动；**上传仅 4 个驱动** |

> 用户原话是「不使用 OpenList 的代理，让站长自己去搭建 OpenList，我们只是帮用户对接好 OpenList 的接口」。按此口径，**形态 B 就是用户要的**：本站只调 API、不转发字节、站长自建 OpenList 担流量。形态 C 是加分项而非必需。**本报告按形态 B 为设计基线**，C 作为能力位暴露。

---

## 7. 多站长模型（研究项 6）

### 7.1 一个 OpenList 对应一个还是多个策略？

三种都可行（实测依据 §3.1）：

| 模型 | 可行性 | 说明 |
| --- | --- | --- |
| 一个 OpenList = 一个策略 | ✅ 最简单 | 策略锚定 `mount_path`（如 `/team`） |
| 一个 OpenList = 多个策略 | ✅ 可行 | 不同策略锚定不同挂载点前缀；**注意别把同一挂载点配两遍**（会产生重复路径） |
| 一个策略跨多个 OpenList | ❌ 不建议 | 路径空间会冲突，无法统一 |

**建议**：**策略 = (OpenList 地址, 认证凭据, 挂载点前缀)** 三元组。这样一个站长多个目录可建多个策略，互不干扰。

### 7.2 凭据存取

必须存：
1. `Server`（OpenList 地址，可用 Cloudreve 已有的 `Policy.Server` 字段）
2. 认证凭据 —— 因 **token 48 小时过期且无 refresh**（§2.1），建议存**用户名 + 密码**，由本站登录换 token 并在接近 `exp` 时主动重登；token 缓存到内存/DB，不落日志。
3. `MountPath`（挂载点前缀，建议存入 `Policy.Settings` 扩展字段）

> 🚨 **凭据安全**：`password` 必须加密存储（复用 `Policy.AccessKey`/`SecretKey` 的现有加密路径）。**任何情况下不写进日志或错误信息** —— OpenList 的 `code:401` 消息已足够诊断，无需回显凭据。

### 7.3 失效告警 —— 本仓库有一处硬编码必须先改

`service/admin/policy.go:254`：
```go
policies, err := storagePolicyClient.ListRefreshNotices(ctx, string(types.PolicyTypeY139))
```
而 `inventory/policy.go:359` 的 `ListRefreshNotices(ctx, policyType)` **按类型过滤**。

**后果**：新策略类型（如 `openlist`）若不是 `y139`，则**即使 `LastRefreshErrorKind` 有值，也永远不会出现在管理员仪表盘的刷新告警里** —— 凭据失效静默。

**必须的改动**（新策略落地的前置条件）：
- 把 `policy.go:254` 的类型硬编码改为**类型列表**（如 `[]string{PolicyTypeY139, PolicyTypeOpenList}`），或把参数改为可空（`""` = 全部类型）；
- 同时 `service/admin/policy.go:442` 的 `case types.PolicyTypeY139:` 的 CORS 分支需加新类型（139 是「驱动自签，无需 CORS 配置」，OpenList 同理）。

> 这两处不写，新策略在管理侧就是「半残」的。**这是接入前必须先修的技术债**，不是可选优化。

---

## 8. 能力差异如何暴露（研究项 7）

三类能力差异，来源不同：

| 能力 | 差异来源 | 如何探测 |
| --- | --- | --- |
| 秒传 | 底部驱动是否实现 `SeedRapidUploader`（88 中 12） | `POST /api/fs/seed/capabilities`（实测可用） |
| 浏览器直传 | 底部驱动是否实现 `DirectUploader`（88 中 4） | `POST /api/fs/get_direct_upload_info`，`data:null`/`not implement` = 不支持 |
| 上传支持 | 存储 `NoUpload` 标志 | `init` 时服务端报 `405 UploadNotSupported` |
| 重命名/移动 | 底部驱动是否实现 `Rename`/`Move` 接口 | 无统一探测接口，**只能按驱动名硬编码**或运行时试错 |

**建议形态**：策略层暴露一组能力位（沿用本仓 `driver.Capabilities` 范式，如 `pkg/filemanager/driver/handler.go` 的 `StaticFeatures *boolset.BooleanSet`）：
- `openlist_cap_seed`（秒传）
- `openlist_cap_direct_upload`（浏览器直传底层）
- `openlist_cap_rename` / `openlist_cap_move`

其中**秒传与直传可自动探测**（有接口），**重命名/移动不可探测**（需要站长在策略配置里勾选，或按驱动名维护白名单）。

---

## 9. 与本仓库现有范式的对照

| 维度 | 139 驱动（`pkg/filemanager/driver/y139/`） | OpenList 建议形态 |
| --- | --- | --- |
| 鉴权 | 管理员粘贴 `Authorization` + `x-yun-app-key` | 用户名+密码，本站登录换 48h JWT 并自动续 |
| 上传 | 驱动签每片 URL，浏览器直传对象存储（**真直连**） | `/api/fs/multipart/*` 分片直传 **OpenList**（不直连底层，但不过本站） |
| 分片/续传 | 驱动自己签分片 | **OpenList 服务端负责**，本站只管发块与重试 |
| 秒传 | 有（内容已存在时 `UploadID` 为空） | 接口存在但是可选的；能力位控制 |
| 下载直链 | `/file/getDownloadUrl` 一步到位 | `/api/fs/get` 的 `raw_url`（或 admin 的 `/api/fs/link`） |
| Relay 强制 | `manager/upload.go` 需对 y139 强制 `unrelayed` | ✅ **同理**：OpenList 策略也必须强制不经本站中转 |
| 关键接线点 | `fs.go:96` + `router.go:646` + `policy.go:254/442` | 同样四处（见 §7.3） |

**结论**：既有 139 驱动是本项目「非 S3 存储驱动」的成熟范式，OpenList 驱动可完全套用，**差异集中在「鉴权续期」与「分片由服务端托管」两点**。

---

## 10. 风险与未决项

| # | 风险 | 影响 | 应对 |
| --- | --- | --- | --- |
| R1 | **无 OpenAPI 规范**（实测证实） | 上游 API 变更无法自动感知 | 按版本锁定（v4.2.9 实测）；驱动内集中维护端点常量 |
| R2 | JWT 48h 过期无 refresh | 站长改密码/重启 OpenList（`jwt_secret` 变）都会失效 | 存密码自动重登 + 接入 §7.3 告警 |
| R3 | 错误用 HTTP 200 + `code` | 驱动若只看 HTTP 码会把失败当成功 | 错误映射必须读 body |
| R4 | 秒传/直传能力分驱动而异 | 「全套带秒传」对多数存储不成立 | 能力位 + 自动降级 |
| R5 | 同一物理目录多路径别名 | 文件可能出现两次 | 策略锚定单一挂载点前缀 |
| R6 | 分片大小上限由 OpenList 决定 | 本站 `ChunkSize` 可能被无视 | 以 `init` 返回的 `chunk_size` 为准 |
| R7 | `/api/fs/link` 需 admin | 用 admin 权限过大；用普通号拿不到 link | **默认用 `/api/fs/get` 的 `raw_url`**，普通账号即可 |
| R8 | AGPL-3.0 | 源码传染风险 | **仅调 HTTP API、不并入其代码**，可规避（本报告未复制任何 OpenList 源码进本仓） |

**未与法律意见核对的判断（R8）**：AGPL-3.0「通过网络交互」条款约束的是**派生作品**。本站仅通过 HTTP 调用 OpenList 的公开 API、不修改不链接其代码，属于正常使用受许可软件的系统，不构成派生作品。**此为工程判断，若正式商用建议法务复核。**

---

## 11. 环境清理证据

```
$ pgrep -af openlist
无 openlist 进程
$ ss -lntp | grep 5244
5244 已释放
$ ls -d /tmp/olprobe /tmp/OpenList-4.2.9
临时目录已清理
```

- 未执行 `docker image prune -af`（本机无 docker；按团队约束禁用）。
- 未触碰 15212/15213/15220 与线上服务；临时实例仅监听本机 5244。
- 临时实例使用一次性密码 `OlProbe2026tmp`（**非任何真实系统凭据**），实例销毁后该密码无意义。

---

## 12. 结论汇总

| 研究项 | 判定 |
| --- | --- |
| 1. API 契约 | ✅ 全套可用；**无 OpenAPI**；鉴权为 48h JWT 无 refresh；错误走 body `code` |
| 2. 挂载指定路径 | ✅ 可稳定锚定子路径；支持多挂载与嵌套；注意多别名 |
| 3. 秒传 | ⚠️ 有接口（`/api/fs/seed/*`），**但仅 12/88 驱动支持**，须做能力位 |
| 4. 断点续传 | ✅ **完整**：乱序、幂等、按 path+size 找回；分片上限由服务端定 |
| 5. 直连 | ⚠️ 下载可 302 直链（视驱动/`web_proxy`）；**上传直传仅 4/88 驱动**；推荐形态 B（浏览器↔OpenList，不过本站） |
| 6. 多站长 | ✅ 一个 OpenList 一个策略可行；**必须先修 `policy.go:254` 硬编码告警** |
| 7. 能力差异 | ✅ 秒传/直传可自动探测；重命名/移动需白名单 |

**总体可行性：可行，且优于 189 方案**（无 IP 绑定、API 完备、站长自担流量与合规）。主要成本在「鉴权续期」「能力位协商」「两处管理端硬编码」。

---

## 附：待用户确认的两个开放问题（沿用 Spec，不预设做法）

1. **站长端 OpenList 部署说明**：是否需要我们出一份面向非技术站长的最小部署文档（下载 release、加存储、建账号、开 API）？
2. **秒传哈希由谁算**：本报告倾向「由 OpenList 侧通过 `/api/fs/seed/capabilities` 探测，本站不自行建哈希索引」。是否认可？
