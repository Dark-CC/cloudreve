# 管理端/秒传四处缺陷修复与 15212 验证报告（2026-10-30）

本批次只做 15212 测试站验证与部署，**未发版**，`baixiong` 的 tag/Release 未动。

- 测试站：https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech
- 部署镜像：`cloudreve-fork:fix-1030-r3`
- 回滚镜像：`cloudreve-fork:fix-1030-1024`（旧容器已移除，按下面的回滚命令重建即可）

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

**保留**：`cloudreve-fork:fix-1030-r3`（当前部署）+ `cloudreve-fork:fix-1030-1024`（回滚）。未执行 `docker image prune -af`。Yarn/Go 缓存保留以加速后续构建。

测试文件 `cr-test-*`、`bxy-upload-a/b.mp4` 及 0 字节残留会话记录均已删除。

## 五、回滚方式

```bash
docker rm -f cloudreve-fork-test
docker run -d --name cloudreve-fork-test --restart unless-stopped -p 15212:5212 \
  -v /data/cloudreve-fork-test:/cloudreve/data \
  -e CR_SETTING_DEFAULT_media_meta_ffprobe=1 -e CR_SETTING_DEFAULT_thumb_libraw_enabled=1 \
  -e CR_ENABLE_ARIA2=0 -e CR_SETTING_DEFAULT_thumb_ffmpeg_enabled=1 \
  -e CR_SETTING_DEFAULT_thumb_vips_enabled=1 -e CR_SETTING_DEFAULT_thumb_libreoffice_enabled=1 \
  cloudreve-fork:fix-1030-1024
```

数据目录 `/data/cloudreve-fork-test` 不受影响。

## 六、范围内自有判断（按授权说明）

1. **目录直链语义**选「展开为文件直链列表」而非 zip/跳转：契合 `GetDirectLink` 同步返回一组 URL 的既有契约，且天然避开 y139 禁止打包的限制。
2. **秒传副本共用同一个缩略图实体**（而非每文件另存一份）：与秒传本身共享源实体的模型一致；另存会让实体数随副本数膨胀，违背秒传意义。
3. **`directLinkBudget` 语义**：`sourceBatch <= 0 || requested > sourceBatch` 时返回 `requested`（service 层已拒绝超限请求），避免二次报错。
4. **已损坏的测试文件只做一次性数据修复**，不写进迁移或启动逻辑。
