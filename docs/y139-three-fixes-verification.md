# 移动云盘（139）三个问题：实机复现、修复与验证

测试站：InstaBox `15212`（容器 `cloudreve-fork-test`，仅映射 `15212:5212`）
版本：`4.23.7` fork 构建
验证提交：`746bfeb5`（`fix(uploader): skip parts the storage did not sign a URL for`）

三个问题都在 **旧构建（v4.23.7 / `8de9fd2186c7`）上先复现**，修完后在 **新构建** 上重跑同一场景验证。凭据一律记作 `<redacted>`。

---

## 问题 1：秒传后卡在「上传中」

### 复现（旧构建）

139 云盘侧已持有某内容、Cloudreve 本地无该 hash 实体时，驱动走「云盘已存在」分支：

```
PUT /api/v4/file/upload
→ {"rapid_uploaded": true, "upload_urls": [], "upload_id": ""}
```

前端 `base.ts:177` 见到 `rapid_uploaded` 就 `transit(Status.finished)` 并 **跳过完成回调**，于是：

- 会话残留，`entities.upload_session_id` 从不清除
- `files.size = 0` 而 `entities.size = 30`（文件行与实体行对不上）

这就是「卡在上传中」——浏览器以为完事了，后端从未收到完成通知。

### 根因

`pkg/filemanager/driver/y139/y139.go` 的「云盘已有内容」分支（`created.Exist || (created.RapidUpload && len(created.Parts) == 0)`）直接设了 `credential.RapidUploaded = true`。这个标志的语义是「服务端已把文件落库，浏览器什么都不用做」，而这里文件其实 **还没在 Cloudreve 侧完成**，仍需回调。

### 修复

驱动不再设 `RapidUploaded`，改用新语义的 `NoPartsUploaded`：

- `pkg/filemanager/fs/fs.go`：`UploadCredential.NoPartsUploaded`（json `no_parts_uploaded`）
- `pkg/filemanager/manager/upload.go`：`NoPartsUploaded && len(hash) > 0` 时置 `uploadSession.StorageSideDeduplicated`，否则清零（云盘没法对空 hash 去重）
- `pkg/filemanager/driver/y139/y139.go`：该分支置 `NoPartsUploaded = true`，不设 `RapidUploaded`
- `service/explorer/response.go`：把 `no_parts_uploaded` 透出到会话响应
- 前端 `chunk.ts`：见到 `no_parts_uploaded` 直接返回（不传任何分片），但 **绝不跳过** `afterUpload()`

**关键点**：`base.ts` 的 `transit(finished)` 快路径只认 `rapid_uploaded`，`no_parts_uploaded` 顺利走到 `upload()` → `afterUpload()` → 回调。

### 验证（新构建，真实 HTTP + 真实浏览器）

会话响应：

```
rapid_uploaded : absent
no_parts_uploaded: true
upload_urls    : 0
upload_id      : ''
```

浏览器（Puppeteer + 真实「New → Upload files」、真实 file chooser）发出的请求：

```
PUT  /api/v4/file/upload                          ← 建会话
GET  /api/v4/callback/y139/<session>/<secret>     ← 浏览器真的调了回调
```

界面「上传中」计数在 6 个采样周期（约 12 秒）内回到 0，文件立即可见。数据库侧：

```
entity_id | size | refs | upload_session_id cleared | hash
188       | 33   | 1    | yes                        | 64 chars
```

（同内容再次上传走云盘去重，复用同一内容，`size` 正确、会话已清。）

旧构建的对照：`rapid_uploaded: true`、无回调请求、`size = 0`、`upload_session_id` 残留。

---

## 问题 2：Authorization / App Key 失效后如何重配

### 现状复现（旧构建）

把策略 4 的 Authorization 换成语法合法但无效的值后：

```
PUT /api/v4/file/upload
→ 40099  "139 cloud drive login state expired, please paste a fresh Authorization header"
```

### 重配路径（实测可行，无需重启）

管理端更新存储策略：

```
PUT /api/v4/admin/policy/4
{"policy": { ...完整策略对象，access_key 换成新的 Authorization... }}
→ {"code": 0}
```

紧接着 **同一条策略** 上的下一次上传：

```
→ code 0，upload_urls: 1（正常签名分片）
```

**不需要重启实例。** 原因：`UpdateStoragePolicyService.Update` → `inventory/policy.go Upsert` 会清 `StoragePolicyCacheKey`；且 y139 client 每次请求新建，没有残留连接或 token 缓存。

### 操作要点（给使用者）

1. 后台 → 存储策略 → 「移动云盘直连」→ 编辑
2. `Authorization`（`access_key`）粘贴新的 `Basic base64(账号:token)`
3. `App Key`（`secret_key`，即 `x-yun-app-key`）填配套值
4. 保存即可，无需重启；再上传一次确认

报错文案本身已经指明动作：「please paste a fresh Authorization header」。

### 注意

- 策略必须与该用户所属组有关联（`group_allowed_policies`），否则不会出现在 `/file/policy` 的允许列表里
- 目录级偏好存在 `metadata` 的 `sys:preferred_policy`（hashid），会 **覆盖** 组的默认策略——排查「明明改对了还是走旧策略」时先查它

---

## 问题 3：目录也能点「获取直链」，点了报错

### 复现（旧构建）

```
PUT /api/v4/file/source  对目录
→ 40077  Entity not exist
```

后端 `manager.GetDirectLink` 对非 file 类型显式拒绝，这是**正确行为**——目录没有可直链的实体。问题在前端：`useActionDisplayOpt.ts` 的 `showDirectLink` 缺少「目标必须是文件」的判断，于是目录的右键菜单也渲染「获取直链」。

### 修复

`frontend/src/component/FileManager/ContextMenu/useActionDisplayOpt.ts`：

- `showDirectLink` 增加 `display.hasFile &&` 条件
- `showDirectLinkManagement` 改为 `display.showDirectLink && targets.length == 1`（去掉重复的 `hasFile` 判断）

后端不动。

### 验证（新构建，真实浏览器右键菜单）

| 目标 | 右键菜单 |
|---|---|
| 文件夹 `cr-test-e2e` | Enter / Download / Share / Rename / Copy / Tags / Organize / More actions / View details / Delete —— **没有「获取直链」** |
| 文件 `cr-test-first.txt` | Open / Open with / Download / Share / Rename / Copy / **Get direct link** / Tags / … |

文件直链点开仍正常出弹窗并给出可用链接：

```
Get direct link
https://15212-<machine>.instabox.tech/f/RbSo/cr-test-first.txt
Show file name | Force download
```

---

## 部署与回滚

- 备份：`/data/deploy-20261030-095200/`（`inspect.json`、`images.txt`、`cloudreve.db`、`conf.ini`）
- 数据库备份：部署前 `sqlite3 ".backup"` 快照（WAL 库必须用 `.backup`，直接拷主文件会 corrupted）
- 当前镜像：`cloudreve-fork:fix-1030-1024`
- 回滚镜像：`ghcr.io/dark-cc/cloudreve:v4.23.7-rerelease`（`8de9fd2186c7`）
- 容器参数与原部署一致（env 5 项 thumb/media + `CR_ENABLE_ARIA2=0`，bind `/data/cloudreve-fork-test:/cloudreve/data`，仅 `-p 15212:5212`）

## 清理

- Cloudreve 侧：`cr-test-*` / `ui-up-*` / `cr-auth-*` 文件与实体全部删除，临时管理员 `cr-e2e-admin@fork-test.local` 删除
- 云盘侧：上述对象经驱动 `Delete` 移入云盘回收站
- 策略：临时 `y139-bad-token` / `cr-test-y139-auth` 删除，`group_allowed_policies` 无悬空行；`Admin` 组指回策略 4
- 两台机器清盘（均未使用 `docker image prune -af`）：InstaBox 删旧镜像层与构建缓存、构建产物、测试文件；Baixiong 删临时脚本
- 回归确认：`/` 与 `/home` 均 200，`/api/v4/site/ping` → `4.23.7`，策略 4 凭据完好（长度 294 / 16，内容 `<redacted>`）

## 构建踩坑记录

1. **`assets.zip` 的目录结构必须是 `assets/build/...`**。`statics.go` 用 `fs.Sub(statics, "assets/build")` 取前端资源；用 `cd build && zip -r assets.zip .` 打出来的包条目是根级（`./index.html`），Go 能编译通过、二进制也「有前端」的字符串，但运行时 `/` 和 `/home` 全 404。正确做法是先 `mkdir -p stage/assets/build && cp -r build/* stage/assets/build/` 再 `zip -r assets.zip assets`。
2. `npx vite build` 在 8G 机器上默认堆会 OOM（`Aborted`），要加 `NODE_OPTIONS=--max-old-space-size=6144`。
3. 磁盘只有 16G，Alpine 运行镜像的 `apk add` 层 1.28GB，`docker commit` 与 `docker build` 都会把盘吃满；空间不足时可用 `docker cp` 替换运行中容器的二进制并 `docker container commit` 固化，比整镜像重建省得多。
