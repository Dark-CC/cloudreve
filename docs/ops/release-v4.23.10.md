# 发布 v4.23.10 正式版：tag、流水线与产物验证

- 日期：2026-11-08
- 仓库：`Dark-CC/cloudreve`，发版分支 `baixiong`
- 结果：**`v4.23.10` 已作为轻量 tag 打在主干 tip 上，`Release` 流水线一次跑通并已发布正式 Release（13 个附件 + GHCR 多架构镜像）；未部署、未重启任何生产机、未改动任何生产数据。**

## 1. 发布前复核

| 检查项 | 结论 |
| --- | --- |
| `v4.23.10` 是否已存在 | **不存在**（`git ls-remote --tags origin` 只有 v4.23.5~v4.23.8；GitHub Tags/Releases API 同样无该 tag），因此按任务书继续发版，未移动/删除任何既有 tag |
| 主干 `baixiong` tip | `7910700dec2b4c7a84253e9c5150a50256ba685c`（Merge PR #188），与 GitHub API `branches/baixiong` 的 `commit.sha` 一致 |
| 本地工作区一致性 | 本地 `HEAD` = `7910700d…`，工作区干净，无需先合并或同步 |
| `docs/release-notes/v4.23.10.md` | 在 tip 的树里（未改动其内容），流水线第一步 `Verify release notes exist` 通过 |

### 一处与任务书事实的差异（按实际复核为准）

任务书的「已发布 tag」列表包含 `v4.23.9`。**实际上 `v4.23.9` 从未打过 tag、也从未发过 Release**：远端只有 `v4.23.5/v4.23.6/v4.23.7/v4.23.8` 四个 tag，Releases 也只有这四个，`Release` 流水线的历史运行记录里同样没有 v4.23.9 的条目。v4.23.9 只以两种形式存在过：仓库里的发版说明 `docs/release-notes/v4.23.9.md`（提交 `3b5da976`）和手工构建、直接部署到两台生产机的二进制。

因此本次 `v4.23.10` 是**继 `v4.23.8` 之后仓库里的第一个正式发布**，其发版说明正文里「自 v4.23.9 以来的全部变更」实际覆盖了两次未发版的改动（`3b5da976` 之后的：#183 设置缺失补种、公告受控 CSS，以及若干 ops 文档），这正是发布说明要写的那部分内容，无内容缺失。

## 2. 打 tag

| 项 | 值 |
| --- | --- |
| tag 名 | `v4.23.10` |
| 目标 | `baixiong` → `7910700dec2b4c7a84253e9c5150a50256ba685c` |
| 类型 | **轻量标签**（GitHub `git/ref/tags/v4.23.10` 的 `object.type` = `commit`；`git ls-remote` 只有一条 ref、没有 `^{}` 剥离行），符合「tag 用轻量标签」的发版政策 |
| 打 tag 工具 | 平台 `create_tag`（仓库写操作走平台工具，本机无 repo 写凭据） |

## 3. 流水线运行

| 项 | 值 |
| --- | --- |
| run id | `42473652241`（Release #6，attempt 1） |
| 触发 | `push`，ref `v4.23.10`，head `7910700d…` |
| 开始 / 结束（UTC） | 10:58:14 → 11:24:51 |
| 运行时长 | **26.6 分钟**（tag 推送 10:54 到 Release 正式发布 11:23:38，端到端约 29 分钟，含 runner 排队约 3.5 分钟） |
| 结论 | **success**，全程 attempt 1，无失败步骤、无重试 |
| 各 job | `server` success（26.6 min）；`desktop` / `android` / `updater-manifest` skipped |

- `desktop` / `android` 被跳过是**预期行为**：这两个 job 由仓库变量 `vars.DESKTOP_RELEASES` / `vars.ANDROID_RELEASES` 门控，本仓库未开启（与 v4.23.5~v4.23.8 的历史发布一致）。所以本次附件里没有桌面安装包与 APK，属正常。
- 历史坑（QEMU 构建 arm64 镜像吃光 runner 内存导致 `hosted runner lost communication`，v4.23.7 曾因此重跑）**本次未复现**，第一次就通过。
- 构建过程中前端资源重新生成：`.build/build-assets.sh` 在 CI 内 `yarn version --new-version 4.23.10` 后构建并打包 `application/statics/assets.zip`（该文件被 `.gitignore`，不依赖仓库里是否提交），因此 `go:embed` 不缺资源。

## 4. 产物验证

### 4.1 Release 页

| 项 | 值 |
| --- | --- |
| Release id | `438198899` |
| tag / 标题 | `v4.23.10` / `4.23.10` |
| 状态 | 正式版（`draft=false`、`prerelease=false`） |
| 发布时间（UTC） | 2026-11-08T11:23:38Z |
| `target_commitish` | `7910700dec2b4c7a84253e9c5150a50256ba685c` — **与主干 tip 完全一致** |
| 正文 | 取自 `docs/release-notes/v4.23.10.md`（与仓库内容逐字一致） |
| 附件数 | 13（与 v4.23.5~v4.23.8 相同） |

### 4.2 附件清单

| 附件 | 大小（字节） | sha256（取自 Release API 的 digest） |
| --- | --- | --- |
| `checksums.txt` | 1,254 | `e9a3b38d…d04b1` |
| `cloudreve_v4.23.10_linux_amd64.tar.gz` | 40,501,293 | `46ebb244…1fce45` |
| `cloudreve_v4.23.10_linux_arm64.tar.gz` | 37,913,316 | `05bbf0ab…ae3dc95` |
| `cloudreve_v4.23.10_linux_armv5.tar.gz` | 39,069,727 | `e067cd39…ed9b63` |
| `cloudreve_v4.23.10_linux_armv6.tar.gz` | 39,013,274 | `eb7f6d18…b3c5d` |
| `cloudreve_v4.23.10_linux_armv7.tar.gz` | 38,987,099 | `b28a464a…b03c48` |
| `cloudreve_v4.23.10_linux_loong64.tar.gz` | 38,990,145 | `ddfb9ee9…e78dc01` |
| `cloudreve_v4.23.10_darwin_amd64.tar.gz` | 41,001,511 | `31fe3073…418cf06c` |
| `cloudreve_v4.23.10_darwin_arm64.tar.gz` | 39,113,358 | `7090b354…72eca379` |
| `cloudreve_v4.23.10_freebsd_amd64.tar.gz` | 40,444,169 | `14a7eb52…fdfffac2` |
| `cloudreve_v4.23.10_freebsd_arm64.tar.gz` | 37,880,887 | `bff489bf…03becb` |
| `cloudreve_v4.23.10_windows_amd64.zip` | 41,006,911 | `07a19c18…adec979` |
| `cloudreve_v4.23.10_windows_arm64.zip` | 37,980,096 | `99111c78…cfb87da4` |

下载校验：从 Release 下载 `linux_amd64` 与 `linux_arm64` 两个包，`sha256sum` 均与 `checksums.txt` 中对应行及 API digest 一致。

### 4.3 GHCR 镜像

| 镜像 tag | manifest digest |
| --- | --- |
| `ghcr.io/dark-cc/cloudreve:v4.23.10`（多架构索引） | `sha256:e4ffa7948fa8eaff21c7082a5dedbea067230f03b3c240e8c65d574181c260d4` |
| `:v4.23.10-amd64` | `sha256:53fb025683d726eddbde02084dc1f5af702203fe280f50ce1369b43ad878da8f` |
| `:v4.23.10-arm64` | `sha256:1608a984bcc01dd631456e1dc1682ffed758c1fcba3bf15733ba7caf809c54d6` |
| `:v4.23.10-slim`（多架构索引） | `sha256:a8fc8f80edd4b6d23bf2e79d9f2fddc06068c11e3e247be257787e1b177df141` |
| `:v4.23.10-slim-amd64` | `sha256:19dc6d1b8ccd0884cb899a0c00a17d488e4a8e936686c2d569e3c3a120ac5082` |
| `:v4.23.10-slim-arm64` | `sha256:7a863aa988e7a10e8ba6e2bf88300ffc28842c7cefb91c8ad325e7e0127c671a` |
| `:latest` / `:v4` | 均指向 `sha256:e4ffa794…c260d4`（与 `:v4.23.10` 同一索引） |

索引内两个平台：`linux/amd64` → `53fb0256…`，`linux/arm64` → `1608a984…`，与各自的单架构 tag 一致。

### 4.4 二进制自检：版本号真的是 4.23.10

本机是 `aarch64`，`linux_amd64` 产物无法直接执行（`Exec format error`），因此**用同一次发布里的 `linux_arm64` 产物实跑**，并单独校验 `linux_amd64` 产物的 sha256：

1. 解包 `cloudreve_v4.23.10_linux_arm64.tar.gz`，在 `/tmp` 下的临时目录里以默认配置（sqlite，监听 `127.0.0.1:5212`）启动，与生产无关，验证后立即停止并删除临时文件。
2. `GET /api/v4/site/ping` → `{"code":0,"data":"4.23.10","msg":""}`。
3. `GET /version.json` → `{"name":"cloudreve-frontend","version":"4.23.10","build":"4.23.10+mw7q2wi1"}`——前端与后端版本号字面相等。
4. 启动横幅：`V4.23.10  Commit #7910700  Pro=false`——提交号前缀与 tag 目标 `7910700d…` 对应。
5. 启动日志中 `Static resource version mismatch` 出现 **0 次**（历史上手工构建漏注入版本号时会打这条）。

## 5. 失败与重试记录

- 本次无失败、无重试（attempt 1 一次通过）。
- 未触发同版本覆盖重发流程（该流程需先删 Release 再移动 tag，见 `v4.23.9` 之后的历史做法）。

## 6. 一句话回滚

若需要撤销本次发布：先删 Release（`DELETE /repos/Dark-CC/cloudreve/releases/438198899`）再删 tag（`git push origin :refs/tags/v4.23.10` 或平台删除 tag），即回到「无 v4.23.10」的状态；`latest` / `v4` 两个浮动镜像 tag 会仍指向 4.23.10 的索引 digest，如需一并回退须重新把这两个 tag 指回旧版本索引。**两台生产机当前运行的二进制不受影响**，本次发布没有改动任何生产环境。

## 7. 线上版本号与发布版本号的关系

现状（本次只读核对，未改任何东西）：

| 站点 | 地址 | 架构 | 二进制 sha256 | `ping` 返回 |
| --- | --- | --- | --- | --- |
| 香港站 | `https://pan.cdn.ad`（155.117.85.143，容器 `cloudreve-fork`） | x86_64 | `eca8ce5d85824c60deb868b82abd2f2d7b15b3a8ee2d0dfb79200f7ed2a0db90` | `4.23.9` |
| 国内站 | `https://pan.qzyun.net`（120.220.76.88，systemd `cloudreve-fork.service`） | x86_64 | `eca8ce5d…`（与香港站逐字节相同） | `4.23.9`（源站回环核对） |

**代码内容与本次发布的 v4.23.10 相同，差别只是版本号字符串。** 依据：两台在用的二进制是公告受控 CSS 那一版，构建自分支 `0cae11ab`；`git diff 0cae11ab 7910700d` 输出为空，即该分支树与本次 tag 目标树逐字节一致。版本号不同只是构建时注入的 `BackendVersion` 不同（手工构建注入了 `4.23.9`，官方产物是 `4.23.10`），发布说明里 v4.23.9 段落所述内容也已包含在两台线上代码中。

**一句判断：不需要为了版本号再动生产。** 现在对齐到 `4.23.10` 的唯一实质收益是「线上二进制可与 GitHub Release 附件、GHCR 镜像 digest 对上，便于追溯和校验」；功能、数据、迁移状态与 `v4.23.10` 完全一致，没有必须升级的修复。若确实要对齐，正确的做法是等下一次计划内维护窗口，用官方 `cloudreve_v4.23.10_linux_amd64.tar.gz` 产物按既有流程替换（原子 `mv` 改名、只重启 `cloudreve-fork`，香港站停机约 30 秒、国内站约 2 秒，改前后各校 sha256 与 `ping`），**本任务不执行**。附带好处是版本号统一后，下次排查不用再区分「手工 4.23.9」与「官方 4.23.10」两个同内容不同号的产物。

## 8. 顺带观察到的生产情况（未处理、不属于本任务）

国内站 `https://pan.qzyun.net` 从外网经 CDN 访问当前返回 **HTTP 522**（edge 报 `error=connection_timeout`，即 CDN 连不到源站），而源站本身是健康的：`cloudreve-fork.service` 为 `active`，nginx 监听 `0.0.0.0:443`，源站本机回环 `http://127.0.0.1:5212/api/v4/site/ping` 正常返回 `4.23.9`，用 `--resolve` 打本机 443 也返回 200。香港站同时刻外网访问正常。判断是 CDN→源站的链路/回源问题（源站 IP 或回源策略），与本任务无关，未做任何改动，建议另开任务核查。

## 9. 本次未做的事

- 未部署、未重启任何生产机，未改动任何生产数据或配置（仅只读核对两台生产机的二进制 sha256 与回环 `ping`）。
- 未改动 `docs/release-notes/v4.23.10.md` 的内容。
- 未触碰 `v4.23.9` 的 tag/Release 状态（它本来就不存在，未补建）。
