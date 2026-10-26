# Dvorinka/cloudreve Fork 与原版（含 Pro）功能差异、开源完整性与二开可行性分析报告

> 分析对象：本仓库（Dark-CC/cloudreve，完整镜像自 [Dvorinka/cloudreve](https://github.com/Dvorinka/cloudreve)）
> 上游原版：[cloudreve/cloudreve](https://github.com/cloudreve/cloudreve)（社区版，最新 release 4.19.1，2026-09-18）+ 官方付费 Cloudreve Pro（闭源授权二进制）
> 分析日期：2026-10-26 · 分析方式：逐项代码核对（非照抄 README 宣称），所有结论均标注代码位置

---

## 0. 总结论（TL;DR）

1. **功能差异**：fork 以 4.19.1 为基线（零偏差镜像）二开，后端版本已推进到 **4.23.4**。原版 Pro 的核心功能——分享协作/ACL、付费分享、OIDC SSO、委派管理员、VAS 变现体系（积分/礼品码/商品/订单）、存储负载均衡、多存储策略、文件加密、AI 语义搜索、桌面客户端、审计日志——**已逐项在代码中落实，且给出了代码级实现**，非空口宣称。未覆盖项很少且均属边缘（iOS 客户端、macOS File Provider、官方托管支付平台等，见 §3.2）。
2. **开源完整性**：**完全开源**。无 git submodule、无预编译业务二进制（仅 `android/gradle/wrapper/gradle-wrapper.jar` 这一标准构建工具件）、无混淆代码、无远程授权校验、无 phone-home。`--license-key`/`CR_LICENSE_KEY` 参数虽保留但已彻底失效（`IsPro` 硬编码 `"false"`，无任何校验调用）。前端、桌面端（Tauri/Rust）、Android 端全部源码在同仓。自更新指向自己的 GitHub Releases（`Dvorinka/cloudreve`），不会拉到上游闭源二进制。
3. **二开可行性**：**可以，且条件很好**。GPL-3.0 传染性要求衍生作品同样 GPL-3.0 开源；NOTICE 已规范署名上游作者；代码里不含任何 Pro 闭源代码（Pro 从未开源，fork 是独立重新实现），因此不存在"盗用闭源代码"的法律瑕疵。工程上基线干净（0 ahead/0 behind 起步）、测试与 CI 真实运行、模块结构清晰，适合延续二开。

以下逐项展开。

---

## 1. 基本事实核对

| 项目 | 核对结果 |
|---|---|
| 仓库性质 | 单仓 monorepo：Go 后端（Gin + ent ORM，支持 SQLite/MySQL/PostgreSQL）+ `frontend/`（React+TS+Vite+MUI，已内嵌无 submodule）+ `desktop/`（Tauri/Rust）+ `android/`（Kotlin + Compose）+ `docs/` |
| 总提交数 | 1621（含平台侧 2 个合并提交）；历史自 2019-11-04 起，完整继承上游历史 |
| 基线 | ROADMAP.md 明确记载：基线为上游 master @ **4.19.1，"exact upstream HEAD, zero divergence"** |
| fork 后端版本 | **4.23.4**（commit `e89c83f` "chore: bump backend to 4.23.4 for release"），领先社区版 4.19.1 |
| 上游社区版现状 | 最新 release 4.19.1（2026-09-18，GitHub API 核实）；上游仍有 137 个 open issue 被 fork 迁移跟踪（ROADMAP §1.5） |
| 上游 Pro 形态 | 独立授权二进制 + `--license-key`/`CR_LICENSE_KEY` + `proupgrade` 数据库迁移；**社区仓库中不含任何 Pro 代码**（ROADMAP §1.3 证实："The community repo contains zero Pro code"），Pro 付费版为闭源发布 |
| 许可证 | GPL-3.0（仓库根 `LICENSE`），monorepo 各端同许可证 |
| 署名 | `NOTICE` 文件完整保留上游归属（cloudreve/cloudreve、cloudreve.org），声明 fork 独立维护、与原作者无关联、未使用任何私有 Pro 代码 |

上游 Pro 的官方功能清单来源：cloudreve.org/pricing 五个功能页（ROADMAP §1.3 已截图存档）+ 官方文档中带 `<Badge type="tip" text="Pro" />` 标记的全部章节（本仓 `docs/zh/` 即上游文档副本，共 11 处 Pro 徽章，逐一清点见 §3）。

---

## 2. "是否完全开源"——完整性专项核查

这是用户最关心的问题之一，核查维度与证据如下：

### 2.1 无闭源子模块 / 无预编译业务产物

- 全仓库**无 `.gitmodules`**；`frontend/`、`desktop/` 均为 vendored 源码树（NOTICE 明确记载 vendored 自 `cloudreve/frontend`、`cloudreve/desktop` 上游仓库——注意：上游这两个客户端仓库本身即 GPL 开源，闭源的是 *Pro 服务端配套的增强客户端*，fork 是自己重写的增强实现）。
- 二进制文件清查：`git ls-files` 中唯一的二进制是 `android/gradle/wrapper/gradle-wrapper.jar`（Gradle 官方标准构建引导 jar，非业务代码）。无 .zip/.so/.dll/.exe/.apk 等预编译产物入库。
- 前端通过 Go `//go:embed`（`application/statics/embed.go`）从源码构建打包，`statics.go` 校验的静态资源名为 `cloudreve-frontend`（非 `cloudreve-frontend-pro`，`application/statics/statics.go:86-91`），即发布物由本仓源码可完整复现。

### 2.2 授权校验已彻底移除（无远程 license 校验、无 phone-home）

- `application/constants/constants.go:8-11`：`var IsPro = "false"` **硬编码**，`IsProBool` 恒为 false。整个 Pro 分层在编译期即被关闭。
- `cmd/server.go:17` 仍注册 `--license-key` 参数、`cmd/root.go:14` 声明 `licenseKey` 变量——但**全仓库无任何代码读取该变量做校验**（grep 全仓仅这两处声明）。README 所称"CR_LICENSE_KEY 被忽略"属实：纯属兼容遗留的空壳参数。
- 无 `proupgrade` 命令（Pro 专用的数据库升级命令，上游 Pro 文档有、fork 的 `cmd/` 无）。
- 全仓 grep 无任何 license 验证/兑换/离线许可证逻辑（上游 Pro 的"授权密钥→离线许可证密钥"机制，见 `docs/zh/maintain/pro-license.md`，在 fork 代码中零实现）。
- `pkg/setting/provider.go:410` 的 `License()` 仍可读取数据库 `license` 设置项——但同样没有任何调用方据此做功能门控，属无害残留。
- **无 phone-home**：`IsPro` 唯一影响是版本号显示后缀（`routers/controllers/site.go:28-30`）和静态资源/DB 版本字符串后缀（恒不触发）。
- **自更新安全**：`pkg/updatecheck/updatecheck.go:23` `ReleaseRepo = "Dvorinka/cloudreve"`——服务端一键升级只会从 fork 自己的 GitHub Releases 拉取并校验 sha256，绝不会升级成上游闭源 Pro 二进制。

### 2.3 无混淆代码

全仓无混淆/加壳痕迹；Go/TS/Kotlin/Rust 全部为可读源码，注释质量高（大量 issue 编号引用，如 `#3164`、`#3570`）。桌面端 Rust 源码、Android Kotlin 源码均完整在仓。

**结论：fork 是一个可从源码完整复现全部功能的纯开源发行版，无任何闭源依赖或暗桩。**

---

## 3. 与原版 Pro 的功能逐项对比

先明确对比基准：原版分两层——**社区版**（开源，即功能基线）与 **Pro 版**（闭源付费，功能全集）。fork 的宣称是"把 Pro 级功能重新实现为免费功能"，因此下表以 **Pro 功能全集**为基准逐项核对 fork 的实现。

### 3.1 已覆盖（逐项代码核实）

#### A. 分享与协作（Pro 核心卖点）

| Pro 功能 | fork 实现（代码位置） | 核实状态 |
|---|---|---|
| 可上传/可编辑分享 | `service/share/manage.go:36-39` `ShareCreateService` 携带 `AllowUpload`/`AllowEdit`/`PreviewOnly`/`UploadOnly` 四类标志；持久化于 ShareProps（`service/share/manage.go:201-204, 286-289`）；前端序列化见 `service/explorer/response.go:348-351, 418-421`；有单测 `service/share/manage_test.go` | ✅ 真实存在 |
| 投递箱（仅上传）分享 | 同上 `UploadOnly`；多文件分享时强制单目录（`manage.go:151`）；匿名访问路由 `routers/router.go:260-267`（短链免登录可达） | ✅ |
| 仅预览分享 | `PreviewOnly` 标志全链路落地 | ✅ |
| 付费分享链接（积分购买） | `service/share/purchase.go` `SharePurchaseService`；路由 `routers/router.go:1044` `share.POST("purchase/:id")`；创建侧 `PricePoints` 字段（`manage.go:45`）；访问侧付费门控（`service/share/visit.go:126` `!paid` 判断） | ✅ |
| 细粒度文件 ACL（用户/组/匿名 × 读/建/改/删） | `inventory/acl.go` `AclClient`（List/Upsert，`ent.AclEntry` 实体、`aclentry.SubjectType`）；`service/share/visit.go:94` 消费 `AclClient` | ✅（Pro 截图中的 "Explicit access" 对话框对应此实现） |
| 分享历史/审计 | commit `3a1b0f1f` "feat(share): share history — audit subjects, diffs, feed + history UIs" | ✅ |
| 新用户默认分享快捷方式 | `pkg/setting/provider.go:820` `DefaultShares()`；`service/basic/site.go:35` `DefaultShareLinksInProfile` | ✅ |
| 自定义分享 slug、公开分享目录 | `manage.go:55-58` `Slug`/`ListedPublicly` | ✅（fork 在 Pro 之上的增强） |

#### B. 用户与认证

| Pro 功能 | fork 实现 | 状态 |
|---|---|---|
| OIDC SSO（Pro 徽章，`docs/zh/usage/oidc.md`） | `service/oauth/oidc.go`（issuer 解析、JWKS）+ `pkg/auth/oidc_consumer.go`（标准 OIDC 消费端）+ 前端 `frontend/src/component/Admin/Settings/UserSession/SSOSettings.tsx`；`inventory/setting.go:501` OIDC 签名私钥设置；支持授权码 + nonce、自动开户（`service/user/sso.go` 走 `CheckEmailAllowed` 注册过滤） | ✅ |
| 注册邮箱过滤 | `service/user/register.go:42` + `service/user/sso.go:348` `CheckEmailAllowed`，注册与 SSO 开户共用 | ✅ |
| 多账号切换 | `frontend/src/component/Frame/NavBar/UserAction.tsx:68,171` `switchAccount`（会话列表切换） | ✅ |
| QQ/微信登录 | `service/user/qq.go`、`service/user/wechat.go`（带 SSRF 校验） | ✅（社区版已有，保留） |
| Logto SSO（Pro 截图功能） | `inventory/setting.go:736-737` 有 `logto_enabled`/`logto_config` 设置项，但 `middleware/auth.go:62` 标注 `// TODO: Logto handler` | ⚠️ 部分：专用 handler 未完成；由于 Logto 本身兼容标准 OIDC，通用 OIDC 通道实际可接入 Logto |
| Passkey / TOTP 两步验证 | `ent/schema/passkey.go`、认证限流 `routers/router.go:304` `login_2fa` | ✅ |

#### C. 委派管理员（Pro：分节管理权限）

- `inventory/types/types.go:386-448`：`DelegatedAdminPermissions()` 定义全部按节管理的权限位；无 `GroupPermissionIsAdmin` 但持任意分节位的组即委派管理员。
- 防越权守卫齐全：`service/admin/group.go:166,189,204`（委派管理员不能改/建含管理位的组）、`service/admin/user.go:177-465`（不能把用户移入管理组、不能删/改管理组成员），并有注释明确设计意图。
- 委派管理员操作记入审计日志（README 宣称，对应 §A 审计事件体系：`service/admin/event.go`、`event_cron.go` 含保留期清理）。

**✅ 完整落地。**

#### D. VAS 变现体系（Pro：存储套餐/会员/兑换码/积分）

- `inventory/vas.go`：礼品码创建/列表/删除/兑换（`CreateGiftCodes` 等，176-280 行）、存储/用户组商品、订单。
- `inventory/payment.go` + `pkg/payment/stripe.go`（含单测）：自定义支付渠道以 Stripe 实现——对应 Pro 的"自定义支付渠道"（`docs/zh/payment/custom.md`，Pro 徽章）。
- 商店路由：`routers/router.go:1733` `shop.POST("purchase")`。
- Pro 的"官方支付平台"（cloudreve.org 托管的代收服务，`docs/zh/payment/official.md`）**不复刻也不可能复刻**——那是官方运营的在线服务而非软件功能；fork 以自建 Stripe 渠道替代。见 §3.2。

**✅ 变现体系核心落地。**

#### E. 存储策略管理（Pro 徽章）

| Pro 功能 | fork 实现 | 状态 |
|---|---|---|
| 一个用户组多个存储策略，用户自选 | `inventory/group.go:112` `AllowedPolicies`（`group.Edges.AllowedPolicies` 多对多）；`service/explorer/policy.go` 全套"策略对本组是否可用/仅所有者可管理"校验（71-162 行） | ✅ |
| 存储策略负载均衡 | `pkg/balancer/`（`balancer.go`、`roundrobin.go`，含单测） | ✅ |
| 按目录的存储策略 / 策略间文件迁移工具 | 未在代码中发现对应管理工具（`service/admin/file.go` 仅有列出/删除/取 URL） | ❌ 未覆盖（见 §3.2） |

#### F. 文件加密（Pro 徽章，`docs/zh/usage/file-encryption.md`）

- `service/explorer/upload.go:33,82-83` `EncryptionSupported`/`ClientSideEncrypted`（客户端 AES-256-CTR 直传加密，与 Pro 文档描述一致）。
- `service/explorer/slave.go:155-176` 从机解析加密元数据、加密缩略图。
- 主密钥管理 CLI：`cmd/masterkey.go`（get/rotate，32 字节强制校验）——注意 fork 的此命令**不需要** `--license-key`（上游 Pro 文档中该命令需授权密钥）。
- **✅ 完整落地且无授权门槛。**

#### G. AI 语义搜索（Pro 文档 `docs/zh/usage/search/ai-semantic-search.md`）

- `pkg/searcher/indexer/meilisearch.go:77-124,343`：Meilisearch embedder 配置（`EmbeddingEnbaled`、embedding 文档模板），即混合/语义检索的后端实现；配套 `chunker.go`。**✅**（需自行部署 Meilisearch，与上游同）。

#### H. 桌面客户端（上游桌面增强功能为 Pro 附带，社区版无桌面端仓库内的完整实现）

- `desktop/`：Tauri + Rust 同步引擎。Windows cfapi 云文件占位、Linux FUSE 均已落地；资源管理器右键分享菜单、应用内自动更新（ROADMAP §6，CI 三平台构建发布 .msi/.dmg/.deb/.AppImage）。
- **⚠️ macOS File Provider 接入仍在路线图**（Windows/Linux 完整，macOS 走 placeholder 降级适配）。
- 上游 Pro 文档 `docs/zh/usage/desktop-client.md` 明言桌面客户端需要 "Cloudreve Pro 服务器实例"——fork 桌面端对 fork 服务端工作，`desktop/` 全开源。

#### I. Android 客户端

- `android/`：Kotlin + Jetpack Compose，v1.1.0 已发布（README）。浏览/上传下载/分享/全文搜索/相机备份/离线收藏/本地同步目录/分享面板目标/DocumentsProvider/快捷磁贴（ROADMAP §7 逐项标注 done）。**✅ fork 独立于上游的新增交付**（上游从未开源 Android 客户端）。

#### J. 系统扩展（Pro 截图功能）

| Pro 功能 | fork 实现 | 状态 |
|---|---|---|
| 活动/审计日志 | `service/admin/event.go`（admin feed）、`event_cron.go`（按 `audit_log_retention_days` 清理）、分享历史事件 | ✅ |
| 站点公告 | `routers/router.go`/`inventory/types/types.go`/`service/user/setting.go` 中公告相关实现 | ✅ |
| 节点选择 | `service/explorer/workflows.go:96-98` `resolveNodeSelection`，按组 `AllowedNodes` 过滤 | ✅ |
| 举报滥用 | `service/abuse/report.go` `ReportService.Create` | ✅ |

#### K. 其他 Pro 相关

| 项 | 状态 |
|---|---|
| Pro 前端源码（Pro 用户才能拿到的前端源码） | ✅ 以前端全源码 vendor 进 monorepo 彻底解决——不再有任何"付费拿源码"层 |
| 付费引导 UI（ProChip/ProDialog） | ✅ 已删除：`frontend/src` 全树 grep 无 `ProChip`/`ProDialog`；仅存一处无害文案键 `group.availablePolicyDesPro`（`BasicInfoSection.tsx:119`） |
| WebDAV（社区版即有，fork 修复只读组权限执行等） | ✅ `pkg/webdav/` 全量在仓 |

### 3.2 未覆盖 / 部分覆盖（fork 与原版 Pro 的真实差距）

| 项 | 说明 | 影响评估 |
|---|---|---|
| **iOS 客户端** | 上游 Pro 有 iOS VOL 授权（见 pro-license.md "iOS VOL 授权"）；fork 无 `ios/` 目录，ROADMAP §7 明确 "native, no iOS"——刻意不做 | 仅影响需要 iOS 原生 App 的场景；网页端/PWA 可用 |
| **macOS File Provider 桌面同步** | ROADMAP §6 状态：FUSE(cfapi) 已落地 Linux/Windows，macOS File Provider "still open"，macOS 目前走降级全量同步适配 | macOS 用户体验降级，功能可用 |
| **官方支付平台** | cloudreve.org 托管的代收服务，属官方运营服务非软件功能，无法"开源复刻"；fork 用自建 Stripe 渠道（`pkg/payment/stripe.go`）覆盖自定义支付场景 | 无国内支付宝/微信官方代收；需自接支付渠道 |
| **Logto 专用 handler** | 设置项已在（`logto_enabled`），handler 为 TODO（`middleware/auth.go:62`） | Logto 兼容标准 OIDC，走通用 OIDC 接入可用；专用直通登录暂缺 |
| **按目录存储策略 / 策略间文件迁移工具** | 未发现实现 | 多策略 + 负载均衡已覆盖主场景；细粒度目录级策略缺 |
| **与上游 Pro 从机（slave）互通** | `docs/zh/usage/slave-node.md:71` 自述"与上游 Pro 从机兼容性尽力而为，因 Pro 为闭源实现" | 混布 fork 主 + Pro 从的拓扑不保证 |

### 3.3 fork 独有新增（上游两版都没有）

1. **完整开源 Android 原生客户端**（上游从未提供开源移动端）。
2. **安全加固套件**：
   - OAuth 公共客户端强制 PKCE、拒绝内置硬编码密钥（`service/oauth/oauth.go:180,266-270`，RFC 8252 loopback 重定向校验 `service/oauth/redirect.go`）；
   - 离线下载/SSO/邮件端点 SSRF 校验（`request.ValidateExternalURL`，`service/user/sso.go:396-448`、`qq.go:163`、`wechat.go:162`）;
   - 认证类端点全面限流（`routers/router.go:294-455`：login 10/min、2FA 10/min、token_refresh 30/min、sms 5/min、register 5/min 等）；
   - 委派管理员越权守卫 + 审计（§3.1.C）。
3. **约 70% 上游积压 issue 修复**：137 个迁移 issue 中约 96 个关闭（ROADMAP §5 有逐项划线记录：#3574 OOM、#3118/#3005 WebDAV 大文件、#3375 SMTP auth、SQLite WAL、MySQL parseTime、unix socket 等）。
4. **服务端自更新指向自有 Releases**（社区版从 GitHub 拉上游 release，fork 拉自己）。
5. 分享 Note/Slug/公开分享目录、垃圾清理 `desloppify` 质量整顿（strict scorecard 18.9→77.1）等工程化改进。

### 3.4 核对中发现的与 README 宣称的出入

- README 宣称各项均能在代码中落实，**未发现虚报**。
- 唯一措辞需留意：README 称"OIDC SSO、委派管理员已落地"为完全属实的；但若把 Pro 截图中的 **Logto** 也算作"OIDC SSO 已落地"则略有夸大（专用 handler 仍 TODO，通用 OIDC 可用）。
- README"约 70% issue 已关闭"与 ROADMAP §5 的逐项记录一致。

---

## 4. 法律边界：GPL-3.0 下的二开规则

1. **许可证传染性**：本项目整体 GPL-3.0。你在此基础上的任何修改、二次分发（无论是否商用）构成衍生作品，**必须同样以 GPL-3.0 开源**，且不得添加闭源条款（GPL §5、§7）。内部自用不分发则无开源义务（GPL 触发点是"分发/网络交互视部署形态而定"——Cloudreve 是 AGPL 吗？不是，是 GPL-3.0，故纯 SaaS 式自托管不分发二进制时无强制开源义务；一旦把修改后的二进制/源码发给他人即触发）。
2. **署名义务**：GPL §7(b)/(c) 允许要求保留原始署名。本仓 `NOTICE` 已保留原作者归属；你二开时**必须继续保留 NOTICE 及各源文件版权声明**，建议在 NOTICE 追加自己的维护条目。
3. **无闭源代码污染**：这是本项目最重要的法律优点——上游 Pro 从未开源，fork 的 Pro 等价功能是**独立清洁室式重新实现**（NOTICE 明确声明 "no private Pro code is used"；§2 核查亦未发现任何疑似 Pro 二进制或反编译残留）。因此二开不承担"包含盗用闭源代码"的风险。**注意**：你（或任何贡献者）切勿参考/反编译 Pro 二进制来补齐 §3.2 的缺口功能，以免引入污染；应继续依据功能描述独立实现。
4. **商标**：项目名 "Cloudreve" 及其品牌归属原作者；fork NOTICE 已声明无关联。你若公开发行自己的分支，建议在名称上做区分（如 fork 所做的"社区维护分支"定位），避免品牌混淆。
5. **文档与图片**：`docs/` 是上游文档的副本（GPL 文档内容随仓库许可），`docs/**/images/` 中的截图/SVG 引用上游素材，二开再分发时保持同许可即可。

---

## 5. 二开可行性评估与建议

**结论：可以延续二开，且这是一个比上游社区版更好的二开基线。** 理由与建议：

1. **工程质量**：干净的镜像基线 + 真实 CI（后端测试、前端类型检查、桌面三平台构建）+ 大量 issue 编号可追溯的修复 + 质量整顿记录（ROADMAP §5 scorecard）。模块划分（`service/`、`inventory/`、`pkg/`、`routers/`）清晰，新功能有明确落点。
2. **无授权暗桩**：§2 已核查无 license 校验、无 phone-home，自更新指向自有仓库——二开分支可完全自主掌控升级链路。
3. **建议的后续方向**（补齐 §3.2 缺口优先级排序）：
   - macOS File Provider（桌面端体验闭环）；
   - Logto 专用 handler（工作量小，`middleware/auth.go:62` TODO 已标好位置）；
   - 策略间文件迁移/按目录策略（运营大站点时高价值）；
   - 国内支付渠道（当前仅 Stripe；可参考 `pkg/payment/stripe.go` 的 provider 抽象自建）。
4. **风险提示**：
   - 上游若继续演进（4.19.1 之后），fork 需人工同步安全修复；建议定期 `git merge upstream/master` 或至少跟踪上游 security advisories（上游曾发布 16 个 GHSA，fork 基线已全部覆盖，见 ROADMAP §1.2）。
   - 单一维护者（Tomas Dvorak）巴士系数低，二开者应做好自己接手的心理与代码准备——这也是"是否可以延续二开"的真正答案：**可以，而且社区需要你接手**。
   - 混用上游 Pro 从机节点不受支持（§3.2 末行），迁移已有 Pro 部署时注意。

---

## 附：核查方法说明

- 代码位置均为本仓工作区实测（路径:行号），非转述。
- 上游社区版版本号经 GitHub API（releases/latest = 4.19.1）核实。
- 官方 Pro 功能清单取自 cloudreve.org/pricing 五个功能页（ROADMAP §1.3 已存档全量截图清单）与本仓 `docs/`（上游文档副本）中全部 11 处 Pro 徽章章节。
- 完整性核查命令基线：`git ls-files` 二进制清查、全仓 `grep -rni license/proupgrade/ProChip`、`.gitmodules` 存在性、`pkg/updatecheck` 目标仓库常量。
