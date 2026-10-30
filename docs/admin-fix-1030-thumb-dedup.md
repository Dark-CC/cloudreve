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

## 六、移动云盘凭证能否自动更新：**能，但目前只在进程内存里，不落库**

结论先行：**驱动的 token 自动续期逻辑完全实现且被真实调用，续期成功；但续期结果只写进程内存，从不回写存储策略的 `access_key`。所以严格答案是「能自动续，但不能持久化」——重启后回到旧 token，再续一次。**

### 6.1 链路实测

| 环节 | 位置 | 状态 |
|---|---|---|
| 续期触发 | `y139/session.go:55` `personalCloudHost` → `refreshTokenLocked` | ✅ 已被真实调用 |
| 续航条件 | token 第 4 段（过期毫秒）距现在 **≤ 15 天**（`refreshMargin`） | ✅ |
| 续期接口 | `POST https://aas.caiyun.feixin.10086.cn:443/tellin/authTokenRefresh.do`，XML body，clienttype 656 | ✅ |
| 新 token 生效 | 更新 `c.authorization` 内存字段，同进程后续请求即用新 token | ✅ |
| 回写策略 | `refreshToken()`（持久化版）→ `SetTokenPersister` → `inventory.UpdateAccessKey` | ⚠️ **生产路径从不调用** |

### 6.2 关键发现：持久化那条路是死代码

`session.go` 里有两个入口：

- `refreshTokenLocked()` —— 只更新内存，**不持久化**
- `refreshToken()` —— `refreshAndCollect` + 回调 `save()` 写库 + 清 `StoragePolicyCacheKey`

全仓 grep 的结果：

```
生产代码调用 .refreshToken( : 0 处  （仅 y139_test.go 里调用）
生产代码调用 refreshTokenLocked(: 3 处，全部在 session.go 内部
```

也就是说 `pkg/filemanager/manager/fs.go:104` 精心接好的 `SetTokenPersister` 回调 **在生产中永远收到不到调用**。

### 6.3 实测记录

1. 读出策略 4 的 `access_key`，解码得 token 过期时间 `2026-11-28 10:35:56`，**剩余 28.95 天**（> 15 天，不触发续期）。
2. 将该值第 4 段（过期毫秒）改为 `+10 天`，**token 本体字节完全不变**，写回 DB。
3. 重启容器清掉进程内状态，再发一次真实 y139 列表请求 —— **请求成功**（说明续期跑了，否则 10 天内也会用旧 token 照常工作，但无法区分）。
4. 复查 DB：`expires` **仍是 10 天**，未变成新 token。
5. 日志里没有任何 `139 login token refreshed` 行（该行为 Debug 级，正常日志级别下不可见）。
6. 已把策略 4 的 `access_key` 恢复成原始过期时间（`2026-11-28 10:35:56`，剩 28.62 天 = 原始 28.95 天减去流逝时间）。

> 说明：测试全程未打印任何真实凭据；只修改了过期时间戳这一个数字，token 本体从未被替换或外泄。

### 6.4 如果要做成真正自动更新，需要改什么

**改动范围（一处，约 10 行）**：

`pkg/filemanager/driver/y139/session.go` 的 `personalCloudHost` 中，把

```go
if err := c.refreshTokenLocked(ctx); err != nil {
```

改成先解锁再调持久化版本，或改用 `refreshAndCollect` + 自行 `save()`（注意：不能在持 `c.mu` 时调回调，回调会触碰 policy store 并可能重入本 client —— 代码注释已明确警告过这一点）。

**风险**：

1. **锁顺序**：`persistToken` 回调会走 `inventory` → DB → 清 KV，期间若另一协程正在建驱动/读策略，可能短暂阻塞。现有代码结构已用「锁内计算、锁外持久化」规避，改造时必须保持这个模式，否则死锁。
2. **写放大**：每次驱动重建（每请求一次，无缓存）都会检查续期条件；因为不改过期时间戳就不会重复续，实际写入只在真正续期时发生一次，风险低。
3. **并发续期**：多请求同时跨越阈值会各自续一次，最后一次写库覆盖前者。token 续期接口本身语义上允许这样，但建议加一个「同一进程内只续一次」的短窗去重。
4. **失败降级**：续期接口挂了不能阻断业务——现有 `refreshTokenLocked` 失败即返回 `ErrAuthExpired` 让调用方报错；若改成持久化路径，写库失败只应记 Warning（现有 `refreshToken` 已这样处理）。

**未改动**：本轮按要求只给结论，没有动这段代码，等确认后再改。

### 6.5 续期前提与 token 寿命

- **前提**：token 必须带过期段（`token` 按 `|` 拆分至少 4 段），且续期请求要用 **与获取时相同的客户端身份**（`clienttype 656` 即 PC 端）。实测当前策略里存的就是 `pc:` scheme（`Basic base64("pc:<账号>:<token>")`），即 PC 端 token，满足续期前提。
- **若用 Web 端 token**：驱动里 `clientType()` 会根据 `policy.Settings.Region` 判断 `pc` / `web`，但续期请求固定写死 `refreshClientTyp = "656"`。Web 端 token 走这个续期端点是否被接受，**未实测**（不想拿真实账号冒险）；代码上无区分，这是潜在坑。
- **寿命**：实测当前 token 剩余 **约 29 天**，即签发有效期约 30 天。因为续期阈值是 15 天，一个被正常访问的策略会在剩余 15 天时自动续到新的 ~30 天 —— 只要**续期结果被持久化**，理论上可以永久不过期。反之（现状），重启后回到 DB 里的旧 token，但那个旧 token 自己也会再续，直到它真正过期（因为 DB 里那个值不会随续期前进，**约 30 天后：DB 值已过期 → `remain < 0` → `ErrAuthExpired`，整个策略失效**）。

就现状看，**现在这套自动续期对「不重启」的单进程是有效的，但对「重启」是无效的，且存在约 30 天后的必然失效窗口**。这是建议优先修的原因。

---

## 七、回滚方式

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

## 八、范围内自有判断（按授权说明）

1. **目录直链语义**选「展开为文件直链列表」而非 zip/跳转：契合 `GetDirectLink` 同步返回一组 URL 的既有契约，且天然避开 y139 禁止打包的限制。
2. **秒传副本共用同一个缩略图实体**（而非每文件另存一份）：与秒传本身共享源实体的模型一致；另存会让实体数随副本数膨胀，违背秒传意义。
3. **`directLinkBudget` 语义**：`sourceBatch <= 0 || requested > sourceBatch` 时返回 `requested`（service 层已拒绝超限请求），避免二次报错。
4. **已损坏的测试文件只做一次性数据修复**，不写进迁移或启动逻辑。
