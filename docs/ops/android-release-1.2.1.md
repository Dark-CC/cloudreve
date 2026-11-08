# 安卓客户端 1.2.1 发版：界面固定简体中文 + 正式签名密钥（2026-11-08）

- 仓库：`Dark-CC/cloudreve`，工作分支 `tds/conv-01a1bafa-c66b-7858-8d41-046f4cf7f8f8`，发版提交 `e73fe3bc`
- 发版标签：`android-v1.2.1`（轻量标签，指向 `e73fe3bc`）
- 工作流：`Android Release`（`.github/workflows/android-release.yml`），run `42479478636`
- 作业性质：**仅动 `android/` 目录 + 新增 4 条 GitHub Actions secret**。未改服务端代码、未触碰任何生产机（香港站 155.117.85.143 / 国内站 120.220.76.88）、未影响 v4.23.10 的发版产物与标签。

---

## 一、结论（一句话）

安卓端已发布 `android-v1.2.1`：应用自身的全部界面文案（107 条）只在 `res/values/strings.xml` 里以简体中文存在，其余 12 个语言目录已删除，因此**任何系统语言下界面都是中文**，应用名保持 `Cloudreve Mobile` 不变；版本 `1.2.1 (4)`；APK 与 AAB 均由新建的正式 RSA-4096 发布证书签名（证书 SHA-256 `11:2B:07:…:BF:12:B2`），密钥已加密写入仓库 secrets。

---

## 二、界面语言：真实来源与改动方式

### 2.1 先查清文案到底存在哪

- `AndroidManifest.xml` 的 `android:label` 指向 `@string/app_name`（两处：应用与 `DocumentsProvider` 的 label），所以「应用名」来自资源文件。
- 资源目录共 13 个：`values/` 加 12 个 `values-<语言>/`（`ar de es fr it ja ko pl pt-rBR ru zh-rCN zh-rTW`）。除 `values/` 外，**每个语言目录只有 4~7 行**，`values-zh-rCN/` 里甚至没有 `app_name`。
- 结论：**绝大多数界面文案并不在 `strings.xml` 里，而是硬编码在 Compose / Kotlin 源码中**（登录页、文件列表、对话框、通知、磁贴、SAF 文档提供者）。只改 `strings.xml` 不可能让界面变中文——这就是必须改代码的原因。

### 2.2 怎么改的

1. 把全部用户可见文案集中写入 `android/app/src/main/res/values/strings.xml`，共 **107 条**，内容为简体中文（`app_name` 例外，仍为 `Cloudreve Mobile`）。
2. **删除 12 个 `values-<语言>/strings.xml`**：此后这些文案只有 `default` 一份，系统语言无论是什么，取到的都是中文。这是有意的行为变更——原先是「跟随系统语言 / 回落到英文」，现在是「固定中文」。
3. 硬编码英文改为资源引用（`stringResource(...)` / `context.getString(...)`），涉及文件：

| 文件 | 改动要点 |
| --- | --- |
| `ui/login/LoginScreen.kt` | 服务器地址、账号、密码输入框标签，登录按钮，错误区文案 |
| `ui/login/LoginViewModel.kt` | 构造新增 `appContext`，登录失败/校验错误信息本地化 |
| `ui/files/FilesScreen.kt` | 标题「文件」、搜索提示、所有无障碍描述、新建/重命名/删除对话框、离线文件弹窗、备份与同步弹窗（含同步文件夹）、文件菜单、空目录/错误/搜索无结果提示；`pathLabel` 改为 `@Composable` 以使用资源；日期时间统一用 `CN_LOCALE`（`Locale.SIMPLIFIED_CHINESE`）格式化，避免跟随系统区域漂移 |
| `ui/files/FilesViewModel.kt` | 构造新增 `appContext`，用于生成中文提示 |
| `data/FileRepository.kt` | 构造改为 `(appContext, apiClient)`，网络/接口错误信息本地化 |
| `work/UploadWorker.kt`、`work/CameraUploadWorker.kt`、`work/SyncFolderWorker.kt`、`work/TaskPollWorker.kt` | 前台通知标题、进度正文、任务完成/取消/失败通知、通知渠道名 |
| `provider/CloudreveDocumentsProvider.kt` | 文档提供者的根目录名「我的文件」、只读提示、缩略图缺失、未找到、下载 HTTP 错误 |
| `tile/SyncTileService.kt` | 快捷设置磁贴副标题「相机备份：开/关」 |
| `CloudreveApp.kt`、`MainActivity.kt` | 随之把 `Context` 传进新建的 Repository / ViewModel 工厂 |

### 2.3 产物里的实测结果（不是靠代码推断）

| 检查 | 结果 |
| --- | --- |
| `apkanalyzer resources value --type string --name app_name --config default` | `Cloudreve Mobile`（应用名未改） |
| `apkanalyzer resources value … --name files_title` | `文件` |
| 我方 107 条文案在 `ja` 资源配置中出现的条数 | **0**（即日语系统下也取中文） |
| `default` 配置里全部 string 名 | 134 条 = 我方 107 条 + 依赖库自带 27 条 |

**一处需要知情、但不影响目标的残留**：APK 里仍存在 `ca / da / fa / ja / ka …` 等语言配置，它们全部来自 AndroidX / Material 等依赖库自带的字符串（`ja` 配置下 26 条），我方文案一条都不在其中。也就是说：应用自己的界面文案在任何系统语言下都是中文；若有第三方控件或系统级弹窗引用库自带的资源，其语言仍可能随系统。彻底消除只能改依赖库行为，不属于本次范围。

---

## 三、版本号

`android/app/build.gradle.kts`：`versionCode = 3 → 4`，`versionName = "1.2.0" → "1.2.1"`。

产物实测（`apkanalyzer`）：`package = com.tdvorak.cloudreve`，`versionName = 1.2.1`，`versionCode = 4`。

---

## 四、正式签名密钥

### 4.1 生成方式

- 在构建机上用 Android 工具链自带的 JDK 17 执行 `keytool -genkeypair`，**PKCS12** 密钥库，**RSA 4096**，签名算法 **SHA384withRSA**，有效期 **10000 天**（2026-11-08 12:08 UTC → 2054-03-26 12:08 UTC）。
- `alias = cloudreve-mobile`；`DN = CN=Cloudreve Mobile, OU=Community Fork, O=Dvorinka, L=Unknown, ST=Unknown, C=CN`。
- 库口令与密钥口令使用同一条 **32 位随机串**（PKCS12 结构本身只用库口令校验，密钥口令为其别名口令）。
- 全程在构建机本地完成；口令与密钥库内容**没有**出现在聊天、提交、报告或任何日志里。

### 4.2 写入仓库的方式

用 GitHub REST API 的 Actions secrets 接口，先把库文件做 base64，再用仓库公钥做 **libsodium sealed box** 加密，逐个 `PUT`（四次均返回 201）：

| Secret 名 | 内容 |
| --- | --- |
| `ANDROID_KEYSTORE_BASE64` | 整个密钥库文件的 base64 |
| `ANDROID_KEYSTORE_PASSWORD` | 库口令 |
| `ANDROID_KEY_ALIAS` | `cloudreve-mobile` |
| `ANDROID_KEY_PASSWORD` | 密钥口令 |

工作流 `.github/workflows/android-release.yml` 已内置解码逻辑：`ANDROID_KEYSTORE_BASE64` 非空时把它落成 `$RUNNER_TEMP/release.keystore` 并注入其余三个变量，`build.gradle.kts` 的 `releaseEnv` 签名配置据此签名；本次 run 的 `Decode release keystore` 步骤为 success，产物也确为正式证书（见 §6），说明这条链路已真正生效。

### 4.3 保管位置

- **GitHub Actions secrets**：写入后不可读回，是构建期的权威副本（仓库 Settings → Secrets and variables → Actions）。
- **构建机本地备份**：`/root/cloudreve-android-keys/cloudreve-release-1.2.1.keystore` 与 `credentials.env`，目录 700、文件 600。这是仓库之外唯一的一份可用副本。
- **不在**仓库里、不在任何提交里、不在本报告里——公钥证书可公开，私钥与口令不可。

### 4.4 证书指纹（可公开核对）

| 项目 | 值 |
| --- | --- |
| 证书 SHA-256 | `11:2B:07:6D:03:D2:96:96:E2:C8:F1:FC:B9:5C:E2:3F:FF:B0:21:8F:C8:F4:AA:58:CE:5D:FC:A1:82:BF:12:B2` |
| 证书 SHA-256（紧凑） | `112b076d03d29696e2c8f1fcb95ce23fffb0218fc8f4aa58ce5dfca182bf12b2` |
| 证书 SHA-1 | `2d73b9f25e2f44ba932748d55d059a562246bdee` |
| 公钥 SHA-256 | `ff56ea06fe70dab12fcdfdef5c33603f50881b937affc1c408ed28b0b1c4aeae` |
| 签名校验 | `apksigner verify` → Verifies，APK Signature Scheme **v2**，签名者 1 个 |

### 4.5 密钥遗失会怎样（务必知情）

应用 ID `com.tdvorak.cloudreve` 与证书是**强绑定**的。私钥或口令一旦永久丢失：

1. 无法再发布能“覆盖安装”的新版本——Android 只接受同一证书签名的升级包，否则报签名不一致，用户必须先卸载（本地离线文件、登录态、设置全部丢失）。
2. 已经装 1.2.1 的用户只能卸载重装新证书的包；无法平滑升级。
3. 若将来上架 Google Play，需要走「上传密钥重置」（且 Play App Signing 已托管应用签名时，影响面才可控）——当前未启用 Play App Signing。
4. GitHub secret 一旦被覆盖或仓库重建，也同样无法恢复旧证书，只能换新证书并承受上面的后果。

因此：请把 §4.3 提到的本地备份**立即转存**到密码管理器或离线介质；在备份确认安全之前，不要重装/重建这台构建机。

---

## 五、发版记录

| 项目 | 值 |
| --- | --- |
| 标签 | `android-v1.2.1`（轻量标签，无附注对象），指向提交 `e73fe3bcfd48f011d0dc73b94fd0102e269b8c2d` |
| 工作流 run | `42479478636`（`Android Release`，由 tag push 触发） |
| 起止（UTC） | 2026-11-08 12:16:44 → 12:20:20，**总耗时 3 分 36 秒**（作业本体 12:16:47 → 12:20:20，3 分 33 秒） |
| 结果 | success（`Decode release keystore`、`Build release APK + AAB`、`Publish GitHub release` 三步均成功） |
| Release | id `438227256`，标题 `Cloudreve Android android-v1.2.1`，说明 `Signed with the release key.` |
| 产物 | `cloudreve-android-v1.2.1.apk` 3,663,617 字节<br>`cloudreve-android-v1.2.1.aab` 4,428,818 字节 |
| sha256 | APK `d544a7e97a811af83bc5a78f3c3184ac00e34720371af0d6ee737da5977587bb`<br>AAB `dc034837014632c2f51fbbd59fa793863fa3c432722389af1e52fec6d4bc0cf9` |

过程插曲（供以后判断“失败”的含义）：首次 CI 校验 run `42478909819` 在 `./gradlew :app:assembleDebug` 步骤失败，日志是 Gradle 发行包下载超时（`SocketTimeoutException: Read timed out`，10 秒读超时），属 runner 网络抖动，**不是代码错误**；同一提交重跑后 android 作业 success，随后才打标签发版。

---

## 六、产物验证（下载 Release 附件后实测）

| 检查 | 结果 |
| --- | --- |
| APK 签名 | `apksigner verify --print-certs` → `Verifies`，v2 方案通过，签名者 1 |
| APK 证书 | `CN=Cloudreve Mobile, OU=Community Fork, O=Dvorinka, …`，SHA-256 与 §4.4 一致 |
| APK 版本 | `apkanalyzer apk summary` → `com.tdvorak.cloudreve 4 1.2.1` |
| APK 应用名 | `app_name = Cloudreve Mobile` |
| AAB 签名 | `jarsigner -verify -verbose -certs` 显示同一张 `CN=Cloudreve Mobile, …` 证书 |
| AAB 版本 | 包内 `base/manifest/AndroidManifest.xml` 含 `versionName` 与 `1.2.1`、包名 `com.tdvorak.cloudreve`（与 APK 同一 Gradle 配置、同一次构建产出） |
| 附件完整性 | 下载后 sha256 与 Release 记录的 digest 完全一致 |

---

## 七、回滚方式

1. **撤下这次发版（最快，秒级）**：删除 Release 与标签即可，用户端不再看到 1.2.1。
   `DELETE /repos/Dark-CC/cloudreve/releases/438227256` → 再删引用 `refs/tags/android-v1.2.1`。仓库中已安装的用户不受影响（他们手机上还是 1.2.1）。
2. **退回中文改动**：`git revert e73fe3bc`（或恢复到改动前的 `values-*` 目录 + 硬编码英文），再走同样的 tag 流程重发。
3. **要注意的一点**：仓库此前**没有** `android-v1.2.0` 之类的历史安卓标签，也就是说 1.2.1 是第一条被打标签的安卓版本。若要给用户一个“旧版”用于降级，只能在 1.2.0 对应的提交上用 `workflow_dispatch` 重跑 `Android Release`；而那条老配置没有签名 secret 生效路径，会回落 debug 证书 → 与 1.2.1 的证书不同，**不能覆盖安装**，必须卸载后装。
4. **密钥相关事故**：证书不可更换（换了就等于新应用），因此不存在“换密钥回滚”；若私钥疑似泄露，正确处置是停止分发并评估是否需要以新应用 ID 重发，这属于需要单独立项的决策。

---

## 八、影响面与遗留

- 代码改动只在 `android/`（含新增本报告），服务端、前端、桌面端零改动。
- 仓库 secrets 新增 4 条，未覆盖任何已有项（原有 `TAURI_SIGNING_PRIVATE_KEY`、`TAURI_SIGNING_PRIVATE_KEY_PASSWORD` 未动）。
- 遗留提示：依赖库自带的多语言字符串（`ja` 配置下 26 条，我方文案 0 条）仍随系统语言变化（见 §2.3），如需彻底中文需另行评估。
- 构建机临时文件已放在 `/root/android-verify/`（下载的 APK/AAB）与 `/root/cloudreve-android-keys/`（密钥备份），均不在 `/tmp`；前者可在验收后删除，后者**务必保留并转存**。
