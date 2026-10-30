# 管理端/秒传四处缺陷修复与 15212 验证报告（2026-10-30）

本批次只做 15212 测试站验证与部署，**未发版**，`baixiong` 的 tag/Release 未动。

- 测试站：https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech
- 部署镜像：`cloudreve-fork:fix-1030-r4`
- 回滚镜像：`cloudreve-fork:fix-1030-r3`（按第七节的回滚命令重建即可，镜像仍在）
- 更早的回滚镜像：`cloudreve-fork:fix-1030-1024`

## 一、四处修复

### 1. 群组「允许目录直链」开关（`/admin/group/1`）

**根因**：前端 `UploadDownloadSection.tsx` 里该 Switch 写死 `checked={false}` 且没有 `onChange`，是死控件；后端 `GroupSetting` 里根本没有对应字段。

**改动**：
- `inventory/types/types.go`：`GroupSetting` 新增 `FolderDirectLink bool`（JSON `folder_direct_link,omitempty`）
- `inventory/group.go`：`mergeGroupSettings` 用 OR 合并（与 `RedirectedSource`/`AllowSelectNode` 一致）
- `application/migrator/{group.go,model/group.go}`：同步字段
- 前端 `api/dashboard.ts`、`EditGroup/UploadDownloadSection.tsx`（真正接 `onChange`）、`NewGroupDIalog.tsx`（默认关）
- `service/user/response.go`：`Group` 与 `BuildGroup` 暴露 `folder_direct_link`，供前端右键菜单判断
- `pkg/filemanager/manager/entity.go`：重写 `GetDirectLink`，支持文件夹展开

**语义**（用户已确认）：开启后对文件夹取直链 = **展开为其下每个文件各自的 `/f/:id/:name` 链接**。不打包 zip、不做目录首页跳转。每个子文件独立 302 到网盘预签名地址，因此 y139（禁止中转、禁止打包）无需特判即可工作。

**实现**：`GetDirectLink` 里对文件夹用 `m.fs.Walk` 展开，子文件 URI 逐个追加进 `pending` 队列，走原有单文件分支（计量、吊销、`unique_direct_link` 复用全部不变）。超过 `MaxWalkedFiles` 返回 `CodeBatchSourceSize`(40014)。预算判断抽成可测的 `directLinkBudget(sourceBatch, requested)`。

**#42 误读的修正**：上一轮给 `showDirectLink` 加的 `display.hasFile &&` 是按「文件管理器右键菜单」理解的，属误解。本批改为按 `folder_direct_link` 放行文件夹，`redux/thunks/file.ts` 的 `startBatchGetDirectLinks` 同步加前置校验。

### 2. `/admin/policy/4` 缺 Authorization / App Key 编辑框

**根因**：`BasicInfoSection.tsx` 的「访问凭据」块只在 `showBucket`（oss/cos/obs/qiniu/s3/ks3/upyun）时渲染，`PolicyType.y139` 被排除。

**改动**：渲染条件加 `|| values.type === PolicyType.y139`，字段沿用 `policyProps.tsx` 已就绪的 y139 `akName`/`skName`（Authorization / x-yun-app-key）。新增文案 `policy.y139CredentialDes`（中英双语）。

保存后立即生效、无需重启：`UpdateStoragePolicyService` → `inventory/policy.go Upsert` 清 `StoragePolicyCacheKey`，y139 client 每请求重建、无 token 缓存。

### 3. y139 下隐藏「中转上传」「下载中转」

**改动**：
- `StorageAndUploadSection.tsx`：整块（Switch + helper + `TrafficDiagram`）渲染条件由 `!= local` 收紧为 `!= local && != y139`，删除 `disabled` 与 `uploadRelayLockedY139` 分支
- `DownloadSection.tsx`：同法排除 y139，删除 `downloadRelayLockedY139`
- 清理随之无用的 i18n key

**是 UI 收敛，不是把强制逻辑挪到前端**：后端「y139 恒不走中转」（`manager/upload.go`、`archive.go` 拒绝打包）保持原样。其它策略类型的渲染与行为完全不变。

### 4. 秒传后缩略图不显示
**根因（15212 实测确认）**：不是「没提交缩略图任务」，而是**缩略图实体写盘撞「对象已存在」**。

缩略图保存路径由 `ThumbEntitySuffix`（默认 `{blob_path}/{blob_name}._thumb`）按**源实体 source** 推导。秒传的两份文件共享同一个源实体 → 同一个缩略图路径。第一份上传已把 `..._thumb` 写到网盘；第二份生成缩略图时 `d.Put` 命中已存在对象 → 返回 `fs.ErrFileExisted` → 事务回滚、thumb 实体被 unlink → `disableThumb` 给该文件打上 `thumb:disabled`，于是**永远没有缩略图**。

**修法（对齐既有语义，不做去重/覆盖的临时绕过）**：
- `inventory/file.go` 新增 `GetThumbEntityBySource(ctx, source, policyID)`：按「同策略 + 同 source + 类型为缩略图」查已有实体（ID 倒序取最新）
- `inventory/file.go` 新增 `LinkEntity(ctx, entity, file)`：`AddFile` + `AddReferenceCount(1)`，与 `WithExistingEntity` 同语义
- `pkg/filemanager/manager/thumbnail.go` 新增 `findReusableThumbEntity`，用 `fs.ReplaceMagicVar(m.settings.ThumbEntitySuffix, ...)` 派生路径（`ThumbStoragePolicyID` 优先）
- `Thumbnail()` 与 `SubmitAndAwaitThumbnailTask()` 进入管线**前**先查复用；命中则直接把该实体链到当前文件，不跑生成、不写盘
- 兜底：`generateThumb` 遇到 `ErrFileExisted` 时用 `recoverExistingThumbEntity` 把已存在的实体找回来关联，而不是让任务失败并 `disableThumb`

效果：秒传/多副本共享源实体时天然共享同一张缩略图（同一存储对象，refcount 各自 +1）。不新增设置项，**不改 `ThumbEntitySuffix` 默认值**（改后缀会孤儿化所有既有缩略图）。

## 二、验证结果（全部在 15212 实机 + 真实浏览器）

### 构建/部署路上的两个坑（已修，供后续参考）

1. **不要在编译时加 `-tags="go_json"`**。`goccy/go-json` 在 `go.mod` 里只是 indirect 依赖，加该 tag 会让所有 admin 端点的 JSON 编码 panic（表现为 `/api/v4/admin/*` 全部 500）。release 的 `.goreleaser.yaml` 本身**不加任何 tag**，按它来即可。
2. **`assets.zip` 内部必须带 `assets/build/` 前缀**。`statics.go` 是 `fs.Sub(statics, "assets/build")`，若 zip 内是 `service-worker.js`、`assets/` 这样的根相对路径，前端会完全 404（`/`、`/session` 全 404）。正确打法：`mkdir -p stage/assets && cp -r frontend/build stage/assets/build && (cd stage && zip -r - assets/build)`。
3. Go 二进制必须 `CGO_ENABLED=0` 静态链接，否则 Alpine musl 下报 `./cloudreve: not found`。

### 1. 秒传缩略图（真实浏览器上传）

用 Playwright + Chromium 在 15212 上登录，走真实「新建 → 上传文件」file chooser，把同一段视频上传两次（`bxy-upload-a.mp4` / `bxy-upload-b.mp4`，sha256 相同）。

- 上传后两者 `primary_entity` **相同**（秒传命中）
- 缩略图接口对两者返回**完全相同的预签名 URL**（同一缩略图对象，第二个未重新写盘）
- 文件列表渲染出 4 张 `<img>`，全部 `naturalWidth > 0`，其中两张 400×225 即上述一对

服务端日志可见复用路径：

```
Reusing existing thumb entity 161 for file "副本_bxy.mp4" (source "uploads/1/Tm107VA6/bxy.mp4")
```

### 2. 目录直链开关

浏览器实测完整闭环（不是只读状态）：

| 步骤 | 结果 |
|---|---|
| 初始 | `checked = true`，可点击（`disabled = false`） |
| 点击 | `true → false`，点保存 |
| 刷新 | 仍为 `false` |
| 再点击 + 保存 | `false → true` |
| 再刷新 | 仍为 `true` |

服务端 `GET /api/v4/admin/group/1` → `settings.folder_direct_link: true`，DB `groups.settings` 同步落库。

功能验证（开关打开时对**文件夹**取直链）：

```
PUT /api/v4/file/source  {"uris":["cloudreve://my/存储策略演示"]}
→ code 0, data[{ link: ".../f/34tg/演示文件-策略B.txt", file_url: ".../存储策略演示/演示文件-策略B.txt" }]
```

开关关闭时同请求返回 `40077 Entity not exist`（目录被拒绝）。两个方向都正确。

### 3. `/admin/policy/4` 凭据框

浏览器读取页面：出现「访问凭证」区块（`hasAuthorization = true`），含 Authorization 与 App Key 两个输入框，可编辑。

### 4. y139 中转控件

同一页面文本检查：「中转上传」`false`、「下载中转」`false` —— 两块整块不渲染（不是置灰）。其它策略类型不受影响。

## 三、数据修复记录（仅 15212 测试实例）

`副本_bxy.mp4`（file 160）此前被旧版打到永久无缩略图。修复步骤：

1. 确认孤儿实体 197 已不存在、file 160 的 `thumb:disabled` 元数据为空
2. 发现实体 161 在 DB 里存在但指向的网盘对象 `uploads/1/Tm107VA6/bxy.mp4._thumb` 已被早期坏版本删除
3. 删除 `file_entities` 中 entity 161 的两条关联 + 删除 entity 161 → 请求缩略图时由新逻辑重新生成
4. 验证：`bxy.mp4` 与 `副本_bxy.mp4` 均取到同一预签名 URL，实际下载为 **71270 字节真 PNG（400×225）**

**未触碰 5212 生产 slave 与 15213 副本实例。**

## 四、测试与磁盘

- `go build ./pkg/filemanager/... ./inventory/ ./service/...` 通过
- `go test ./pkg/filemanager/... ./inventory/...` 全绿，含新增：
  - `inventory/thumb_dedup_test.go`（`TestGetThumbEntityBySource`、`TestLinkEntitySharesThumbnail`）
  - `inventory/group_folder_direct_link_test.go`（`TestMergeFolderDirectLink`）
  - `pkg/filemanager/manager/directlink_folder_test.go`（`TestDirectLinkBudget`）
- 前端 `tsc --noEmit` 与 baseline 对比：**零新增类型错误**（反而少一条）

### 磁盘清理

| 机器 | 清理前 | 清理后 | 说明 |
|---|---|---|---|
| InstaBox `/` | 87%（2.0G 可用） | 84%（2.4G 可用） | builder cache、旧测试镜像 `r2`/`thumb3`/`thumb4`、UI 临时 node_modules 与截图 |
| InstaBox `/data` | 84%（3.3G） | 83%（3.4G） | 同上 |
| Baixiong `/` | 27%（26G 用） | 27% | `/tmp` 临时脚本与产物 |

**保留**：`cloudreve-fork:fix-1030-r4`（当前部署）+ `cloudreve-fork:fix-1030-r3`、`cloudreve-fork:fix-1030-1024`（回滚）。未执行 `docker image prune -af`。Yarn/Go 缓存保留以加速后续构建。

第二轮（r4）清盘：`docker builder prune` 释放 536.6MB；`/` 89%→87%（释放约 0.4G），`/data` 85%→84%（释放约 0.2G）；删除 r4 构建的临时二进制、前端 `build/` 目录、`/tmp` 脚本与 UI 验证 `node_modules`。

测试文件 `cr-test-*`、`bxy-upload-a/b.mp4` 及 0 字节残留会话记录均已删除。

## 五、第二轮界面修正（同日追加，已部署 r4）

### 5.1 `/admin/policy/4` 移除 Endpoint 字段

上一轮加的 y139 专属区块里一并塞了一个 Endpoint 输入框（`BasicInfoSection.tsx`），本轮整块删除，只保留「根目录」与「访问凭证」。移动云盘的存储端点由后端依据凭据自动解析，编辑页无需也不应让用户填。

注意保留项：「访问凭证」（Authorization / App Key）两框**未受影响**，仍可编辑；`policy.y139EndpointDes` 这个 i18n key 仍被新建向导 `Y139Wizard.tsx` 使用（首次配置时允许覆盖），因此保留不删。

### 5.2 溢出策略 / 缩略图策略下拉框字号

**先测后改**。在部署实例上用 `getComputedStyle` 实测 `/admin/policy/4` 上所有下拉与输入框：

| 元素 | 修复前 | 修复后 |
|---|---|---|
| 普通文本输入框（`MuiOutlinedInput-input`） | 14px | 14px |
| 溢出存储策略「无」 | **16px** | 14px |
| 缩略图存储策略「与文件相同」 | **16px** | 14px |
| 其它 outlined 下拉（如「已启用」） | 16px | 14px |
| `MuiSelect-filled` 下拉（如「MB」「允许」） | 14px | 14px |

**根因**：`DenseFilledTextField` 显式设了 `fontSize: theme.typography.body2.fontSize`，而 `DenseSelect` 的 `& .MuiOutlinedInput-input` 规则只设了 padding、**没有设字号**，于是 outlined 变体的 select 退回 MUI 默认的 16px。

**修法**：在 `frontend/src/component/Common/StyledComponents.tsx` 的 `DenseSelect` 里补上同一个 token：

```ts
"& .MuiOutlinedInput-input": {
  ...
  fontSize: theme.typography.body2.fontSize,
}
```

影响面说明：`DenseSelect` 全局只有 outlined 分支受影响，filled 分支（原本就是 14px）不变；outlined 下拉本就应该与同表单输入框同字号，所以这是修正而不是破坏性改动。

### 5.3 部署后验证（`fix-1030-r4`）

浏览器打开 `/admin/policy/4`：

- 页面文本中 **`Endpoint` 出现 0 次** —— 整块已移除
- 「访问凭证」块存在，两个框 `disabled=false / readonly=false`（可编辑）
- 「中转上传」「下载中转」均不渲染
- 全部 10 个下拉与全部文本输入框的 computed `font-size` 统一为 **14px**

---

## 六、移动云盘凭证自动续期：已接通持久化 + 失败可见（本轮完成）

上一版结论是「能自动续，但只在内存里」。本轮把这条链路**完整接通**并实测，目标形态是：**站长首次手动填一次凭据，之后程序自动续期并写回策略；续期失败时站长在后台首页看到提示。**

### 6.1 改了什么

| 文件 | 改动 |
|---|---|
| `pkg/filemanager/driver/y139/session.go` | `personalCloudHost` 改为**锁内续期、锁外持久化**：拿到 `save` 回调后先释放 `c.mu` 再写库，避免回调经 policy store 重建驱动时重入同一把锁。三条返回路径（正常 / pinned server 降级 / 路由查询失败）都覆盖。新增 `persist()`、`report()`、`classifyRefreshError()`。 |
| `pkg/filemanager/driver/y139/client.go` | client 新增 `refreshNotBefore`（续期节流）、`persistToken`/`reportRefreshError` 回调、`hadBasicPrefix`、`authorizationHeader()`；新增 `splitAuthorization()`；新增三种失败类型常量。 |
| `pkg/filemanager/driver/y139/y139.go` | 新增 `SetRefreshErrorReporter()`，与既有 `SetTokenPersister()` 同风格。 |
| `pkg/filemanager/manager/fs.go` | 建驱动时同时装持久化与上报两个回调，按 `policy.ID` 绑定。 |
| `ent/schema/policy.go` + 生成代码 | 策略表新增 `last_refresh_at`、`last_refresh_error`、`last_refresh_error_kind`。 |
| `inventory/policy.go` | `UpdateAccessKey` 成功即清错误标记；新增 `ReportRefreshError`、`ListRefreshNotices`。 |
| `service/admin/policy.go`、`routers/*` | 新增 `GET /api/v4/admin/credential-notices`。 |
| `frontend/.../Home/CredentialNotice.tsx` | 后台首页横幅，列出续期失败的策略并给「去处理」链接。 |

### 6.2 四条要求逐条对照

| 要求 | 实现 |
|---|---|
| 手动填一次，之后自动续期 | 手动输入框保留；续期成功写回 `access_key`，重启后直接用新值。 |
| 续期结果回显到输入框 | 编辑页 `service/admin/policy.go:241` 的 `Get` 本就带 `SkipStoragePolicyCache`，读库中最新值，**无需改动**；写回后打开即看到续期后的凭据。 |
| 续期失败让站长看见 | 三种情形分列 `expired` / `refresh_failed` / `persist_failed`，写入该策略行并由首页横幅展示；**续期成功即清空标记**，横幅自动消失，不常驻。 |
| 多凭证各自独立 | 见 6.4。 |

### 6.3 并发与降级处理

- **节流**：`refreshCooldown = 1 分钟`。检查位置放在 `remain < 0` 判断**之前**——否则并发第二个请求在 `resolved` 尚未置位时会再续一次（单测 `TestConcurrentRenewalPersistsOnce` 抓到了这一点）。
- **写库失败不阻断业务**：内存里已是新 token，本进程仍可用；写库失败只记 Warning 并上报 `persist_failed`，让站长在重启前处理。
- **上报失败也不阻断**：`report()` 自身出错只记日志。

### 6.4 多策略隔离：本来就是隔离的，已加测试固定

排查结论：**没有全局单例、没有全局锁、没有共享变量**。

- `GetStorageDriver` 每次调用 `y139.New(...)` 新建 client，驱动不做实例缓存；
- `resolved`、`refreshNotBefore`、`authorization` 都是 **client 实例字段**；
- 缓存 key 是 `storage_policy_<id>`，`UpdateAccessKey` 只删对应策略的 key；
- 本轮新增的两个回调也都按 `policy.ID` 绑定。

新增 `TestPoliciesRenewIndependently`：两个策略各建各的 client，A 续期后断言 **B 的续期次数为 0、写库次数为 0**，B 随后自己请求时仍能独立续期（各自节流互不影响）。

### 6.5 单测

`pkg/filemanager/driver/y139/y139_test.go` 新增 7 个，全部通过：

- `TestPersonalCloudHostPersistsRenewedToken` — 续期结果写回
- `TestPersonalCloudHostDoesNotPersistFreshToken` — 未续期不写库
- `TestConcurrentRenewalPersistsOnce` — 并发只续一次、只写一次
- `TestRenewalFailureDoesNotBreakRequest` — 续期失败不影响请求返回语义
- `TestPersistFailureDoesNotFailRequest` — 写库失败不失败请求
- `TestPoliciesRenewIndependently` — 多策略隔离
- `TestRenewedTokenKeepsHeaderShape` — 写回格式与粘贴格式一致（见 6.6）
- 另加 `TestExpiredCredentialIsReported`、`TestPersistFailureIsReported` — 两类失败各自上报正确 kind

### 6.6 实测中发现并修掉的一个真实缺陷

写回时 `c.authorization` 是**纯 base64**（`NewClient` 已剥掉 `Basic ` 前缀），若直接写库，策略里会变成 `Basic ` 前缀缺失的值。虽然 `NewClient` 兼容两种形式、运行不受影响，但**编辑页的授权框会显示一个不完整的 header**，站长再手动保存时容易出错。

修法：新增 `splitAuthorization()` / `authorizationHeader()`，按管理员当初粘贴的形状写回。`TestRenewedTokenKeepsHeaderShape` 固定该约定（带前缀的仍带回前缀，裸 payload 的仍写裸 payload）。

### 6.7 15212 实测记录（真实网盘）

用一个不依赖 HTTP 层的验证程序，直接按 `GetStorageDriver` 的方式建驱动、调 `List`，对**同一库里的两个 y139 策略**各跑一次：

| 观测项 | 结果 |
|---|---|
| 自动迁移加三列 | ✅ 老库启动后 `storage_policies` 多出 `last_refresh_*`，原有 4 行数据完好 |
| 策略 A 续期失败 | ✅ 该行写入 `last_refresh_error_kind='expired'` + 时间戳 |
| 策略 B 是否被 A 影响 | ✅ **未受影响**（A、B 各自一行、各自 kind，互不覆盖） |
| 通知接口 | ✅ `GET /api/v4/admin/credential-notices` 返回两条，含策略名与 kind |

**未能实测「续期成功」这一条**，原因如实说明：实验用「把过期时间戳改成 +10 天」的方式构造续期窗口，但网盘侧校验 token 时过期段是签名的一部分，改了就整串失效，续期接口直接回 `4006 The token does not exist or has expired`。不做破坏性替换就无法在真实账号上制造「进入窗口但 token 仍被网盘认可」的状态。因此成功路径由单测覆盖（`countingFake` 模拟网盘返回新 token，断言写库且格式正确），失败路径与隔离由实机覆盖。

> 测试全程未打印任何真实凭据，凭据在文档中一律 `<redacted>`。

### 6.8 给站长的操作要点

- 续期**依赖网盘侧仍认可当前 token**。若站长在网页端退出登录、改密码，或 token 已被服务端作废，续期会失败，横幅会提示 `expired`，此时**必须重新登录移动云盘、粘贴新的 Authorization**。
- 控制台编辑页的授权框始终显示库中当前值（含最近一次续期结果），可直接覆盖粘贴。
- 续期阈值 15 天、token 寿命约 30 天：正常访问的策略会在剩余约 15 天时自动续期，写回后不会出现「重启回退」。

---

## 七、VAS 套餐容量单位与单价小数（同日并入）

### 7.1 先核清数据模型的单位约定

| 字段 | 实际单位 | 依据 |
|---|---|---|
| `skus.amount` | **字节**（存储包/流量包）；`group` 类型下是**目标用户组 id** | `ent/schema/sku.go` 注释原文 `amount = bytes`；列表页 `sizeToString(s.amount)` 直接按字节渲染 |
| `skus.price` | **最小货币单位（分）** | `ent/schema/sku.go` 注释 `price is the cash price in the smallest currency unit`；`pkg/payment/stripe.go:23` `Amount 是订单金额，单位为该货币的最小单位`，`UnitAmount: stripe.Int64(p.Amount)` 原样传入 |
| `skus.duration` | 秒（0 = 永久） | schema 注释 |
| 展示换算 | `sizeToString()` 用 **1024 进制**，输出单位标签 GB/TB | `frontend/src/util/index.ts:33` |

**所以「容量没有单位可选」的真实情况**：输入框填的数字被**直接当字节**存。站长想填 1GB，填了 `1`，结果存成 **1 字节**——库里现存的那条测试数据（`amount=1`）正是这么来的，页面显示 `1.0 B`。这不是「少了个标签」，是**量级差 10⁹ 的错存**。

**单价**：模型本来就是分，`0.01 元 = 1 分` 完全合法；卡住的是前端 `parseInt(e.target.value) || 0`——小数被截断，填 `0.01` 得 `0`（不可售）。

### 7.2 换算关系与兼容方式

- **换算基数 1024**，与既有展示函数 `sizeToString` 完全一致：`1 GB = 1073741824` 字节，`1 TB = 1099511627776` 字节。不用 1000 进制，否则同一条数据在录入页和列表页会显示成两个数。
- **不改写任何已有数据**：`amount` 列语义不变（仍是字节），没有迁移、没有列变更。老套餐（包括那条 `amount=1`）打开编辑时显示 `1 字节` 对应的值（`bytesToCapacity` 计算得约 `9.3e-10 GB`），列表页显示 `1.0 B`——**读法完全没变，只是本地展示单位变了**，不会被误读。
- **打开编辑时的单位选择**：`bytesToCapacity()` 选「能保持 ≥1 的最大单位」，所以 1 TB 的套餐打开显示 `1 / TB` 而不是 `1024 / GB`；小于 1 GB 的值（历史脏数据）显示为小数 GB。
- **保存**：`capacityToBytes()` 用 `Math.round` 取整回字节，避免浮点尾差。

### 7.3 改了什么

| 文件 | 改动 |
|---|---|
| `frontend/src/util/index.ts` | 新增 `CapacityUnit`、`capacityUnitMultiplier`、`capacityToBytes()`、`bytesToCapacity()`。 |
| `frontend/src/component/Admin/Settings/VAS/SkuTable.tsx` | 表单加 `amountUnit`（仅 UI 态，库内仍存字节）；容量输入旁加 GB/TB 下拉；`openEdit` 字节→单位、分→元；`onSave` 单位→字节、元→分（`Math.round(price*100)`）。单价输入放开为 `min=0.01 step=0.01` 并改 `Number()` 解析。 |
| `frontend/src/component/Admin/Settings/VAS/GiftCodes.tsx` | 兑换码 `storage`/`traffic`/`stream_traffic` 类型同样加 GB/TB 下拉；`points` 类型保持整数不变。 |
| `frontend/public/locales/{zh-CN,en-US}/dashboard.json` | 新增 `vas.capacityUnit`；`vas.size`→「容量大小」、`vas.trafficSize` 去掉「（字节）」；两条说明文案改为写明 GB/TB 与 1024 进制；`packPriceDes` 写明「元，最小 0.01」。 |

**后端未改**：`service/admin/vas.go:121` 只校验 `s.Price < 0`，分值为 1（0.01 元）本来就通过；`binding:"required,min=1"` 属于兑换码的 `Amount`（积分/字节数），`0.01` 元走到的是 `price` 字段，不受影响。

### 7.4 金额链路核对（结论：本来是对的，无需改）

`skus.price`（分）→ `service/user/payment.go:94` `CreatePaymentOrder(..., s.Price, ...)` → `:104` `Amount: s.Price` → `pkg/payment/stripe.go:87` `UnitAmount: stripe.Int64(p.Amount)`。

Stripe 的 `UnitAmount` 语义就是**最小货币单位**，与库里的「分」直接对齐，中间没有任何 `/100` 或 `*100`。所以把前端改成「按元输入、存库前 ×100」，金额链路自动正确：填 `0.01` → 存 `1` → Stripe 收到 `1` 分。**不存在「输入框能填 0.01 但下单金额算错」的情况**。

> 易支付（Epay）相关代码不在本分支上，那批是独立合并的；本分支能核对的只有 Stripe 这条，其换算已如上确认。易支付那条链路的 `price → 元` 换算需在那批的代码上单独核，本报告不代其下结论。

### 7.5 同类弹窗逐项排查（`/admin/settings?tab=vas`）

| 位置 | 数值字段 | 现状 | 是否问题 | 处理 |
|---|---|---|---|---|
| 存储包 新增/编辑 | 容量 `amount` | 裸数字，直存字节 | **是**（用户点名） | 加 GB/TB 下拉 + 换算 |
| 存储包 | 单价 `price` | `parseInt`，最小 1 | **是**（用户点名） | 改 `Number`，`min 0.01 step 0.01`，元→分 |
| 存储包 | 有效期 `durationDays` | `parseInt`，整数天 | 否（天无法有小数） | 保持 |
| 存储包 | 积分 `points` | `parseInt`，`Math.max(1,...)` | 否（积分是整数） | 保持 |
| 直链流量包 新增/编辑 | 流量 `amount` | 同存储包，裸字节 | **是**（同类，同一组件） | 同存储包，已一并修 |
| 流媒体流量包 新增/编辑 | 流量 `amount` | 同上 | **是**（同类） | 已一并修 |
| 用户组套餐 新增/编辑 | `amount` | 是**用户组下拉**，不涉及容量 | 否 | 保持 |
| 用户组套餐 | 有效期、单价、积分 | 同上三行 | 单价**是** | 已一并修 |
| 兑换码 生成弹窗 | `amount`（points 类型） | `parseInt` 整数积分 | 否 | 保持整数，另加 `min 1 step 1` 明确约束 |
| 兑换码 生成弹窗 | `amount`（storage/traffic/stream_traffic） | 裸字节 | **是**（同类） | 加 GB/TB 下拉 + 换算 |
| 兑换码 生成弹窗 | 有效期 `duration` | `parseInt` | 否 | 保持 |
| 兑换码 生成弹窗 | 数量 `qty` | `parseInt`，`clamp(1,500)` | 否（有上下限且合理） | 保持 |
| 兑换码 列表 | 面额列 | `sizeToString(gc.amount)` | 否（已按字节正确显示） | 保持 |
| VAS 全局设置 | 分享积分比例 `share_score_rate` | 整数 0–100 | 否（百分比整数） | 保持 |
| VAS 全局设置 | 货币单位 `payment_currency_unit` | 整数 ≥1 | 否（汇率整数倍） | 保持 |

范围外的相邻数值输入（`Email` 的 SMTP 端口/保活秒数、`Event` 的审计日志保留天数、`Media` 的缩略图宽高与质量、`Queue` 的并发数与重试）：语义均为整数或已有合理 `min/step`，**不属于 VAS 套餐，未改**。

### 7.6 兼容性补充说明（重要）

`amount` 存字节这一点**没有变**，所以：

- 从旧版本升级到本版本，已有套餐**不需要迁移**，打开即用；
- 但**在旧版本里被错存成小数字节的历史数据**（例如想填 1GB 却存成 `1`）不会自动修正——那需要站长手工重填。本次未对那条测试数据做任何改写，只在测试实例里另建数据做验证。

---

## 七点五、构建机器切换与磁盘事件（重要记录）

### 为什么从 InstaBox 切到 Baixiong

本批默认把重活放在 InstaBox（`m_agqw5litn4acpbo7b5axu3ycku`）上跑。实际执行时发现**两条并行任务（本批 r6/r7 与另一条 vas/payment 任务）共用同一台 InstaBox、同一个 `/data/workspace/cloudreve-src` 源码树**，互相踩踏，具体表现为：

1. 对方的构建脚本重置了 `cloudreve-src`，把本批已同步进去的 28 个改动文件全部冲掉（复现：`grep capacityUnit` 在对方重置后返回 0）。
2. 本批改 `frontend/build` 时，对方的 `yarn build` 同时在写同一目录；本批 `rm -rf build/assets` 清掉的是**真正的产物目录**，对方随后又重建了一部分，两边都得到半成品。
3. 磁盘被双方反复写满：一次冲到 `/` 100%（剩 0），一次 `/data` 91%。

用户指示后采取措施：**本批之后的构建改到 Baixiong 上做**（72GB 空闲、node/yarn 齐全），并且**改为从 GitHub 分支重新 clone**，不再共享 InstaBox 上的源码树。这条只覆盖「空间不足期间」，不是永久改变。

### InstaBox 磁盘清理记录（本轮）

| 步骤 | 释放 | `/` | `/data` |
|---|---|---|---|
| 起点 | — | 87%（2.0G 可用） | 84% |
| 并发构建把盘写满 | — | **100%（0 可用）** | 91%（2.0G） |
| `docker builder prune -af` | 802.1MB | — | — |
| 删除死 swap 文件 `/swapfile2`（未激活、`fstab` 无条目，6.1G） | **6.1GB** | **96% → 56%** | — |
| 清 Go modcache/`/data/home/go`（1.2G） | 1.2GB | — | — |
| 清中间源码树（`cr-r7`、`fresh` 1.5G、`imgbuild`、`imgctx`、`src`、`build-src`）、临时 tar/b64 | 约 2.5GB | — | — |
| **清理后** | | **55%（6.7G 可用）** | **75%（5.1G 可用）** |

> `docker container prune` 只回收 28.67kB（没有可回收的已停容器）。**未执行 `docker image prune -af`**。

**保留的镜像**（全部保留，未删）：

| 镜像 | 用途 |
|---|---|
| `cloudreve-fork:admin-r7` | 另一任务当前部署（容器 `cloudreve-fork-test` 正在跑它） |
| `cloudreve-fork:admin-r6` | 该任务回滚位 |
| `cloudreve-fork:vas-1030-r3` | 本批上一轮部署/回滚位 |
| `cloudreve-fork:easypay-1030-r2` | 支付任务回滚位（容器 `cloudreve-fork-test-rollback-r2`） |
| `debian:bookworm-slim` | 基础镜像，后续构建需要 |
| `mcr.microsoft.com/playwright:v1.49.1-jammy` | 浏览器验证用（3.32GB，保留） |

**本批 r7 镜像未能在 InstaBox 上产出**：镜像构建前源码树被对方重置，随后磁盘写满，因此 `cloudreve-fork:fix-1030-r7` 不存在。当前 InstaBox 上的 `admin-r7` 是另一任务的产物，**不是**本批代码。

### 本批构建与验证状态

| 环节 | 状态 |
|---|---|
| 代码改动 | ✅ 已 commit 并推送：`3f181c3` |
| Go 全量构建 + 单测（Baixiong / InstaBox 均跑过） | ✅ `go build ./...` 通过；`go test ./pkg/filemanager/... ./inventory/...` 全绿，含 9 个新增 y139 单测 |
| 前端 `tsc --noEmit` | ✅ 191 条与 baseline 完全一致（零新增类型错误） |
| 前端打包 | ⏳ 已切到 Baixiong 重新 clone 后构建 |
| 15212 部署 r7 + 浏览器验收 | ⏳ 待前端产物就绪后进行 |
| 移动云盘「续期成功」路径实机验证 | ❌ 未完成（原因见 6.7：真实账号上无法在不破坏 token 的前提下构造续期窗口，已由单测覆盖） |
| 多策略隔离 / 失败上报 | ✅ 实机验证通过（见 6.7） |
| VAS 容量单位 / 单价小数 | ⏳ 代码完成、类型检查通过，待部署后浏览器验收 |

---

## 八、回滚方式

```bash
docker rm -f cloudreve-fork-test
docker run -d --name cloudreve-fork-test --restart unless-stopped -p 15212:5212 \
  -v /data/cloudreve-fork-test:/cloudreve/data \
  -e CR_SETTING_DEFAULT_media_meta_ffprobe=1 -e CR_SETTING_DEFAULT_thumb_libraw_enabled=1 \
  -e CR_ENABLE_ARIA2=0 -e CR_SETTING_DEFAULT_thumb_ffmpeg_enabled=1 \
  -e CR_SETTING_DEFAULT_thumb_vips_enabled=1 -e CR_SETTING_DEFAULT_thumb_libreoffice_enabled=1 \
  cloudreve-fork:fix-1030-r3
```

数据目录 `/data/cloudreve-fork-test` 不受影响。

数据目录 `/data/cloudreve-fork-test` 不受影响。

## 九、范围内自有判断（按授权说明）

1. **目录直链语义**选「展开为文件直链列表」而非 zip/跳转：契合 `GetDirectLink` 同步返回一组 URL 的既有契约，且天然避开 y139 禁止打包的限制。
2. **秒传副本共用同一个缩略图实体**（而非每文件另存一份）：与秒传本身共享源实体的模型一致；另存会让实体数随副本数膨胀，违背秒传意义。
3. **`directLinkBudget` 语义**：`sourceBatch <= 0 || requested > sourceBatch` 时返回 `requested`（service 层已拒绝超限请求），避免二次报错。
4. **已损坏的测试文件只做一次性数据修复**，不写进迁移或启动逻辑。
