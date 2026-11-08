# 桌面客户端 0.3.13：界面默认中文 + 自动更新签名密钥 + 发版

日期：2026-11-08
标签：`desktop-v0.3.13`（轻量标签）
工作流：Desktop Release（`.github/workflows/desktop-release.yml`）
运行记录：见本文「四、三端构建与产物」

## 一、界面默认中文

目标：**界面默认简体中文**，不随系统语言变化；应用名称保持 `Cloudreve` 不动（用户 2026-11-08 明确）。

桌面端语言有两层，两层都改了，否则会互相拉扯：

| 层 | 文件 | 改动 |
| --- | --- | --- |
| 前端 UI（i18next） | `desktop/ui/src/i18n.ts` | 新增导出 `DEFAULT_LANGUAGE = "zh-CN"`；`fallbackLng` 由 `en-US` 改为它；语言检测顺序由 `querystring → navigator → htmlTag` 收窄为**只保留 `querystring`**，即不再看系统语言与 `<html lang>` |
| 原生外壳（rust-i18n：托盘菜单、系统通知、对话框） | `desktop/src-tauri/src/lib.rs` | 新增 `pub const DEFAULT_LOCALE = "zh-CN"`；`i18n!` 的 fallback 与 `normalize_locale()` 的兜底改为它；`init_i18n()` 与 `get_effective_locale()` 在配置里没有语言时改用 `DEFAULT_LOCALE`，不再读 `sys_locale` |
| 首次绘制前的 `<html lang>` | `desktop/ui/index.html` | `lang="en"` → `lang="zh-CN"` |

关键原因（为什么不能只改前端）：主窗口与弹窗的 webview 地址由 `get_url_with_lang()`（`desktop/src-tauri/src/commands.rs`）拼成 `<页面>?lng=<get_effective_locale()>`，前端 i18next 的 `querystring` 检测优先级最高。只改 `i18n.ts` 的话，系统是英文机器时 URL 仍带 `?lng=en-US`，界面还是英文。所以「默认语言」的真正源头在 Rust 的 `get_effective_locale()`。

`sys-locale` 依赖随之不再被 `src-tauri` 直接使用（`tauri-plugin-os` 仍需要它，故 `Cargo.lock` 里的 crate 条目保留），已从 `desktop/src-tauri/Cargo.toml` 移除。

### 语言切换器与「自动」选项

- 右上角/设置页的语言下拉（`desktop/ui/src/pages/settings/GeneralSection.tsx`）走 `invoke("set_language")` 写配置 + `i18n.changeLanguage()`；写配置后主窗口会重载并带上新的 `?lng=`，行为未变，切走再切回正常。
- 下拉里的「自动」项对应的就是配置里的 `null`，过去含义是「跟随系统」。现在 `null` 解析为简体中文，所以该选项的显示文案一并改了（11 个语言包 `settings.languageAuto`），例如中文为「默认（简体中文）」，英文为「Default (Simplified Chinese)」——避免出现「选了跟随系统却是中文」的误导。
- 语言不做持久化（`detection.caches: []` 未变），重启后回到默认中文；要长期显示其它语言，请在设置里显式选择。

验证方式：用 jsdom 复刻同一套 i18next 配置跑三种情形——系统语言为 `ja-JP` 且 URL 无 `lng` → 解析为 `zh-CN`；URL 带 `?lng=ko` → `ko`；`changeLanguage("zh-TW")` / `("en-US")` → 立即生效。另 `yarn build`（含 `tsc -b`）通过。

## 二、版本号

三处必须同步，漏一处的典型症状是构建产物版本与 tag 不一致、`latest.json` 里的版本对不上：

| 位置 | 改前 | 改后 |
| --- | --- | --- |
| `desktop/src-tauri/tauri.conf.json` `version` | 0.3.12 | 0.3.13 |
| `desktop/src-tauri/Cargo.toml` `version` | 0.3.12 | 0.3.13 |
| `desktop/Cargo.lock`（`cloudreve-desktop` 包条目） | 0.3.12 | 0.3.13 |

`desktop/ui/package.json` 的 `version` 是 `0.0.0`，与桌面应用版本无关，未改。应用名（`productName`、窗口标题、安装包名）保持 `Cloudreve`。

## 三、自动更新签名密钥

`tauri.conf.json` 的 `bundle.createUpdaterArtifacts: true` 强制要求签名私钥，缺失时构建直接失败；`plugins.updater.pubkey` 必须是与之配对的公钥。

- 密钥对在构建机本地用 `tauri signer generate`（minisign 格式，带密码）现场生成，公钥写入 `desktop/src-tauri/tauri.conf.json` 的 `plugins.updater.pubkey` 并随本次提交进仓库，私钥与密码**没有**进入仓库、chat、报告或日志。
- 私钥与密码通过 GitHub API 加密上传（libsodium sealed box，使用仓库 public-key 接口）写入仓库 secrets：
  - `TAURI_SIGNING_PRIVATE_KEY`
  - `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`
  - 调用凭据：团队密钥 `GITHUB_ADMIN_TOKEN`（Boss 授权的仓库管理员令牌，值不在此记录）。
- 上传前做了配对自检：用私钥+密码对测试文件签名，再用 `minisign -Vm` 以写入 `tauri.conf.json` 的公钥验签成功——确保「仓库里的公钥」与「CI 里的私钥」是同一对，否则所有自动更新都会验签失败。
- 旧的 `pubkey`（上游作者那一对）因无对应私钥无法签署本仓库更新，已替换。

⚠️ 该私钥目前**只存在于 GitHub Actions secrets**。丢失后无法再为已发布版本签更新包，老用户只能重新下载安装包。建议团队管理员把私钥与密码另存一份到团队密钥（团队 Secrets 页面），不要贴进对话。

另：仓库现有 secrets 里没有 `SPARSE_SIGNING_PFX_*`。本次同时修了 `desktop-release.yml` 的一个隐患——Windows 的签名稀疏身份包步骤原本无条件执行，缺证书时必然失败；现改为按 `release.yml` 的思路以 job 级 `env` 判空跳过（`if: runner.os == 'Windows' && env.SPARSE_SIGNING_PFX_BASE64 != ''`）。后果：本版 Windows 安装包不含 `Cloudreve-Identity.msix`，即「资源管理器同步右键菜单」所需的包身份没有预装（安装器可容忍缺失，安装与同步不受影响；需要时可在有证书后补发）。

## 四、三端构建与产物

标签 `desktop-v0.3.13` 指向提交 `bbc4c36`（本分支「默认中文 + 0.3.13 + 新公钥」那次提交；标签之后若再提交只增文档与工作流说明，不影响已构建的应用）。工作流 **Desktop Release** 运行 `42477863670`，共 2 次尝试：

| 平台 | 结果 | 耗时 | 说明 |
| --- | --- | --- | --- |
| macOS（`macos-latest`，Apple Silicon） | 成功 | 11 分 50 秒 | 首次即通过 |
| Windows（`windows-latest`） | 成功 | 15 分 14 秒 | 首次即通过 |
| Linux（`ubuntu-latest`） | 第 1 次失败 → 第 2 次成功 | 8 分 59 秒 / 8 分 10 秒 | 失败原因是打包 AppImage 时下载 `AppRun-x86_64` 得到 **HTTP 504**（GitHub 二进制发布端点瞬时故障），编译本身已成功；用「重跑失败作业」重跑后通过，未改任何代码 |

整轮墙钟时间约 29 分钟（11:55 触发 → 12:24 三端全部完成）。

### 产物清单（Release `desktop-v0.3.13`，已正式发布，非草稿）

| 文件 | 平台 | 大小 | sha256 |
| --- | --- | --- | --- |
| `Cloudreve_0.3.13_x64-setup.exe` | Windows x64（NSIS） | 9.15 MB | `e615ab7b…0381` |
| `Cloudreve_0.3.13_aarch64.dmg` | macOS Apple Silicon | 12.78 MB | `12b811f4…bae9` |
| `Cloudreve_aarch64.app.tar.gz` | macOS 自动更新包 | 13.08 MB | `3e54e3d2…2cfd` |
| `Cloudreve_0.3.13_amd64.deb` | Linux x64（Debian/Ubuntu） | 15.51 MB | `1a86cb4f…8696` |
| `Cloudreve_0.3.13_amd64.AppImage` | Linux x64（通用） | 93.91 MB | `cc93d20f…52d7` |
| `latest.json` | 自动更新清单 | 4.4 KB | `23552de0…6dca` |

每个安装包/更新包都有同名 `.sig` 签名文件（共 10 个附件，合计 144.4 MB）。

### 版本与签名核验（已实测，非推断）

- `latest.json`：`"version": "0.3.13"`，含 `windows-x86_64`、`darwin-aarch64` 与 Linux 条目，签名值为本次新密钥所签。
- `.deb` 元数据：`Package: cloudreve`、`Version: 0.3.13`、`Architecture: amd64`、`Depends: fuse3, libayatana-appindicator3-1, libwebkit2gtk-4.1-0, libgtk-3-0`——与 tag 一致。
- 用仓库里提交的公钥 + `minisign -Vm` 验签：`Cloudreve_0.3.13_x64-setup.exe`、`Cloudreve_0.3.13_amd64.deb` 均返回 `Signature and comment signature verified`，可信注释里带 `version:0.3.13`。即「仓库公钥 ↔ CI 私钥 ↔ 产物」三方配对无误，客户端自动更新的验签链成立。
- 下载回来的安装包 sha256 与 GitHub 记录的附件摘要一致。
- 二进制内嵌的前端资源名为 `assets/index-D0uxY4gu.js`，与本地用改后的源码构建出的产物**同名同内容**（同哈希），可确认发布出去的界面就是「默认中文」这一版。
- 未覆盖：Intel（x86_64）Mac 安装包。`macos-latest` 现为 Apple Silicon，本次矩阵没有 Intel 机型，所以 macOS 只出了 `aarch64` 两个包。要支持 Intel Mac 需在矩阵里加 `macos-13` 之类的 x86_64 机型重发一版（建议作为后续待办，M1 之前的机器目前只能自行编译）。

### 运行环境说明

- 构建机不参与编译：三端均在 GitHub 官方 runner 上完成，本机只做密钥生成、公钥改造、界面校验与产物验证。
- 未能在本机真机启动客户端（本机无 GUI/WebKit），「默认中文」的验收依据是：源码改动 + 与线上完全相同的 i18next 配置在 jsdom 中的语言解析结果 + 发布二进制内嵌的正是本地构建产物。安装后打开设置页看一眼语言即可最终确认。

## 五、用户安装时可能遇到的系统提示

产物**没有商业代码签名证书**（Windows 无双签、macOS 无 Apple Developer ID），因此会有安全提示，属预期。

### Windows（`Cloudreve_0.3.13_x64-setup.exe`，NSIS）

- 运行时可能出现蓝色 **Windows SmartScreen**：「Windows 已保护你的电脑」。点「更多信息」→「仍要运行」。
- 若文件名/发布者被拦截更严格，右键安装包 → 属性 → 勾选「解除锁定」→ 确定，再运行。
- 安装过程中出现「未知发布者」是同一原因。
- 更新时（应用内自动更新）安装器以 passive 模式静默推进，一般不再弹SmartScreen；首次安装受限最明显。

### macOS（`Cloudreve_0.3.13_aarch64.dmg`）

- 本次只有 Apple Silicon（M 系列）包，Intel Mac 装不了（见第四节末尾）。
- 首次打开提示「**无法验证开发者**」或「已损坏，无法打开」。二选一：
  - 图形方式：系统设置 → 隐私与安全性 → 安全性 → 底部「仍要打开」；或在 Finder 中按住 Control 点应用图标 → 「打开」→ 「打开」。
  - 命令行：`xattr -dr com.apple.quarantine /Applications/Cloudreve.app`
- Apple 芯片包直接安装即可；`.app.tar.gz` 是自动更新用的包，普通用户不用手动下载。
- 自动更新包（`.app.tar.gz`）由 Tauri 验签（本次写入的公钥），不走 Gatekeeper 的开发者签名校验路径。

### Linux（`Cloudreve_0.3.13_amd64.AppImage` / `Cloudreve_0.3.13_amd64.deb`）

- AppImage：需要 `chmod +x` 后运行，部分发行版需 `fuse3`。
- `.deb`：`sudo dpkg -i Cloudreve_0.3.13_amd64.deb`，缺依赖时 `sudo apt -f install`（已声明依赖 `fuse3`、`libwebkit2gtk-4.1-0`、`libgtk-3-0`、`libayatana-appindicator3-1`）。

### 自动更新

- 客户端通过 `https://github.com/Dark-CC/cloudreve/releases/latest/download/latest.json` 检查更新，签名不匹配会拒绝安装（防中间人）。
- 本次更换了签名密钥对：**0.3.12 及更早的版本没有可直接沿用的信任链**（旧公钥无对应私钥），因此老用户需要手动安装一次 0.3.13，此后自动更新才可用。

## 六、回滚方式

1. **只回滚应用**：归档/删除 GitHub Release `desktop-v0.3.13`（可加 `--prerelease` 或 `isDraft`），客户端将读不到 `latest.json` 而停止升级；已安装的 0.3.13 可手动装回 0.3.12 安装包（若该版本存在）。
2. **回滚代码**：撤销本分支对 `desktop/` 的改动（`git revert`），并把 `tauri.conf.json` 的 `version` 与 `pubkey` 一并退回——**注意**：若已发出 0.3.13，退回旧公钥会让 0.3.13 客户端拒绝后续更新，因此正确做法是保留新公钥、只回退中文化相关提交。
3. **回滚密钥**：私钥/密码在 GitHub secrets 里，重新生成一对后必须同时更新 `pubkey` 并提高版本号再发版（密钥轮换无法对已发布版本透明生效）。
4. 移动标签重发时用 GitHub API `PATCH /repos/Dark-CC/cloudreve/git/refs/tags/desktop-v0.3.13`（`force: true`），重跑前先删除同名 Release，否则附件会因 `already_exists` 失败。
5. 构建失败最常见的两种情形：
   - Linux 打包阶段 `failed to bundle project: http status: 504`——Tauri 打包器需从 GitHub 下载 `AppRun-x86_64` 等二进制，端点偶发 504。**不需要改代码**，等本轮三端全部结束后调 `POST /repos/Dark-CC/cloudreve/actions/runs/<run_id>/rerun-failed-jobs` 重跑失败作业即可（本次即如此，第二次成功）。只要有作业还在跑，该接口会返回 403「This workflow is already running」。
   - Windows 的「签名稀疏身份包」步骤：仓库没有 `SPARSE_SIGNING_PFX_*` 时该步骤会跳过（本次已把原本的无条件执行改为判空跳过），不会阻断安装包构建。

## 七、影响面

- 仅触及 `desktop/`（桌面客户端）与 `.github/workflows/desktop-release.yml`。
- 未改服务端代码、未触碰生产机 155 / 120.220.76.88、未影响已发布的 `v4.23.10`、未改任何用户账号与密码。
