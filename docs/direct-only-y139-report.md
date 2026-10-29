# 移动云盘（139）策略严格直连改造与 15212 验收报告

日期：2026-10-30
依据：用户明确指令「不要中转，上传和下载全部都 302 到移动云盘」（2026-10-29 23:34）。

## 1. 结论

15212 测试站已切换到严格直连版本，**上传与下载全程不经过本站中转**，实测证据见第 4 节。用户可直接在测试站目录 `crdirecttest` 下上传/下载任意文件验证。

## 2. 代码改动

站点原有三处会把内容拉回本机，本次全部封死，另外把两个中转开关在界面上锁死。

| 文件 | 改动 |
|---|---|
| `pkg/filemanager/manager/upload.go` | `CreateUploadSession` 计算 `unrelayed` 时追加 `Policy.Type == types.PolicyTypeY139`：移动云盘策略一律走驱动签名直传，无视库里的 `settings.relay`。`ConfirmUploadSession`（中转数据入口）对 y139 返回 `CodePolicyNotAllowed`，即使有客户端硬塞分片也进不来。 |
| `pkg/filemanager/manager/archive.go` | 新增 `rejectArchiveOnDirectOnlyPolicy`，`CreateArchive` / `spoolEntry` / `compressFileToArchive` 三处调用；y139 返回 `ErrNotSupportedAction`。新增 `AssertArchivable` 供上层提前校验。 |
| `pkg/filemanager/manager/manager.go` | 接口补充 `AssertArchivable`。 |
| `service/explorer/file.go` | `GetArchiveDownloadSession` 在**创建 zip 会话之前**调用 `AssertArchivable`，打包下载直接返回 JSON 错误，不再出现「先回一个 zip 链接、拉流时才失败」。 |
| `pkg/filemanager/manager/entity.go` | `GetDirectLink` 保留 `DirectLink` 记录（流量统计与撤销），但 URL 改为上游云盘地址，不再返回本站 `/api/v4/s/...`。 |
| `frontend/src/redux/thunks/download.ts` | 浏览器侧打包（StreamSaver）与目录流式写盘加守卫，y139 抛本地化错误，绝不退回中转。 |
| `frontend/src/component/Admin/StoragePolicy/.../StorageAndUploadSection.tsx` | 上传中转开关对 y139 `disabled` + 锁定说明文案。 |
| `frontend/src/component/Admin/StoragePolicy/.../DownloadSection.tsx` | 下载中转开关（`internal_proxy`）同上。 |
| `frontend/src/api/explorer.ts` | `FileResponse` 补 `storage_policy?` 字段（仅实体列表返回）。 |
| `frontend/public/locales/{zh-CN,en-US}/dashboard.json` | `policy.uploadRelayLockedY139` / `policy.downloadRelayLockedY139`。 |
| `frontend/public/locales/{zh-CN,en-US}/application.json` | `directOnlyBatchDownloadUnsupported`。 |

### 顺带修掉的一个错误串问题

上一版打包下载被拒时返回体是：

```
Not supported action: archiving (zip) is disabled on the 139 cloud drive policy, download the files individually%!(EXTRA <nil>)
```

成因：拒绝发生在 zip **已经开始写流**之后，错误只能塞进响应体，此处格式化把带 `RawError` 的 `AppError` 当格式串处理，`fmt` 补了 `%!(EXTRA <nil>)`。修法是新增 `AssertArchivable` 把校验提前到创建会话之前，正常 JSON 错误分支不再经过那条路径。现在返回：

```json
{"code":403,"msg":"Not supported action","error":"archiving (zip) is disabled on the 139 cloud drive policy, download the files individually"}
```

## 3. 15212 实例配置

- 容器：`cloudreve-fork-test`，镜像 `cloudreve-fork:direct-only-v4`，`-p 15212:5212`，`--restart unless-stopped`，数据卷 `/data/cloudreve-fork-test:/cloudreve/data`。
- 策略 4 改为 `y139` 类型，名称 `移动云盘直连`，`bucket_name` = 云盘根，`settings.relay` / `internal_proxy` / `encryption` 全部关闭。凭据以 `<redacted>` 记。
- Admin 组（id=1）默认策略 = 4。
- 测试目录 `cloudreve://my/crdirecttest` 已绑定策略 4，留给用户测试。
- 失效策略处理：原 5、6 号 `y139-bad-token`（token 无效、从未绑定任何目录）已删除；现存 4 个策略：1 Default(local)、2 A(s3)、3 本地策略B(local)、4 移动云盘直连(y139)。

## 4. 实测证据（均在实例内部 `127.0.0.1:15212` 完成）

| 项目 | 结果 |
|---|---|
| 站点首页 | `HTTP 200` |
| `site/ping` | `{"code":0,"data":"4.23.5"}` |
| 上传会话 | 返回 `upload_urls[]`，Host = `ykj-eos-wx2-01.eos-wuxi-3.cmecloud.cn`（云盘对象存储，非本站） |
| 直传 | `PUT` 到云盘 URL → `200`，上传 24 字节 |
| 完成回调 | `GET /api/v4/callback/y139/<sid>/<secret>` → `{"code":0}` |
| 下载 | `POST /api/v4/file/url {"download":true}` → URL Host = 云盘域名，`GET` 返回 `200`，`Content-Length` 与文件一致 |
| 打包下载 | `{"code":403,...,"archiving (zip) is disabled..."}`，无 `%!(EXTRA ...)` |
| 反向验证（relay 强制置 true） | 重新上传仍返回云盘 `upload_urls`，即中转开关对 y139 无效 |
| 秒传 | 同名同 hash 上传返回 `rapid_uploaded: true`、零字节传输，属预期直连结果，不算中转 |

测试文件 `cr-test-*` 全部用 `DELETE /api/v4/file` 删除，目录 `crdirecttest` 现已为空并保留。

## 5. 部署过程踩到的两个坑（供后续部署参考）

1. **单文件 bind-mount 换二进制的陷阱**：容器用 `-v <宿主二进制>:/cloudreve/cloudreve:ro` 挂单文件，`mv`/`cp` 替换宿主文件会换 inode，容器仍挂已删除的旧 inode，报 `./cloudreve: not found`。正确做法是先删容器再替换文件，或者干脆把二进制烘进镜像（本次采用后者，已彻底去掉单文件挂载）。
2. **动态链接的二进制在容器里跑不起来**：`go build` 默认产出动态链接，镜像里没有对应动态链接器，同样表现为 `not found`。必须 `CGO_ENABLED=0` 静态编译。
3. **前端资源包目录结构**：`application/statics/assets.zip` 内路径必须是 `assets/build/...`，曾打成根目录结构导致站点首页 404，已修正。

## 6. 磁盘清理

| 机器 | 清理前 | 清理后 | 释放 |
|---|---|---|---|
| InstaBox（`/data`，20G） | 94.8% 使用，约 1G 可用 | 59% 使用，8.1G 可用 | 约 7G |
| Baixiong（`/`，96G） | 44G 使用 | 42G 使用 | 约 2G |

清理内容：InstaBox 上删除了 8 个旧镜像（仅保留 `direct-only-v4` 与回滚镜像 `rollback-pre-directdl`）、临时构建产物、Go 缓存、前端缓存；Baixiong 上删除 `frontend/node_modules`（1.3G）与 `/tmp` 下的构建包与二进制。

## 7. 回滚

一条命令切回改造前镜像：

```bash
docker rm -f cloudreve-fork-test
docker run -d --name cloudreve-fork-test --restart unless-stopped \
  -p 15212:5212 -v /data/cloudreve-fork-test:/cloudreve/data \
  -e CR_ENABLE_ARIA2=0 -e CR_SETTING_DEFAULT_thumb_ffmpeg_enabled=1 \
  -e CR_SETTING_DEFAULT_thumb_vips_enabled=1 -e CR_SETTING_DEFAULT_thumb_libreoffice_enabled=1 \
  -e CR_SETTING_DEFAULT_media_meta_ffprobe=1 -e CR_SETTING_DEFAULT_thumb_libraw_enabled=1 \
  cloudreve-fork:rollback-pre-directdl
```

注意：回滚镜像**不含 y139 驱动**，回滚后策略 4 会是孤儿行（前端已对未知类型做降级，不会白屏）。

## 8. 已知边界

- **加密策略**：实体加密时 `ShouldInternalProxy` 恒真，必然中转。y139 策略的 `encryption` 已保持关闭。
- **秒传**：`rapid_uploaded: true` 时浏览器零字节传输，已是零中转。
- **分享页/旧直链**：旧 `/api/v4/s/...` 链接仍 302 到云盘；新签发的直链直接是云盘地址。
- **打包/批量下载**：y139 策略下不支持（含浏览器侧 zip），前端与后端都会给出明确提示，只能逐个下载。

## 9. 用户如何测试

登录 https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech （管理员 `123@qq.com`），进入 **`crdirecttest`** 目录：

1. 上传任意文件 —— 浏览器 Network 里应看到 `PUT` 直接打到 `*.eos-wuxi-3.cmecloud.cn`，本站只收到一条下发凭证的 JSON。
2. 下载该文件 —— 地址栏/Network 直接跳到云盘域名，没有 `/api/v4/file/content/...`。

> 公网网关会切断 `PUT`，若网页上传在公网域名下失败，请在实例内部或 SSH 隧道下测试（既有实测结论）。
