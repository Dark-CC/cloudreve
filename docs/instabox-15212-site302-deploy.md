# 15212 测试站部署直链修复版 + v4.23.7 覆盖重发：实测报告

2026-10-30 实施。任务口径：「修复后的版本部署到测试站吧，然后重新编译 4.23.7 版本重新发版」→ 版本号「就用 v4.23.7 覆盖重发」。

修复行为见 [y139-direct-link-site-302.md](./y139-direct-link-site-302.md)；重发流程与基线对照见 [release-v4.23.7-rerelease.md](./release-v4.23.7-rerelease.md)。

## 1. 结论

| 目标 | 结果 |
| --- | --- |
| 15212 测试站跑上含修复的构建 | ✅ 现跑 `ghcr.io/dark-cc/cloudreve@sha256:8de9fd21…`（重发后的正式产物） |
| 直链改由本站下发、302 不中转 | ✅ `PUT /api/v4/file/source` → `https://15212-…instabox.tech/f/NmTe/bxy.mp4`；GET 为 302，响应体 696 字节；跟随跳转取回 10,244,355 字节，sha256 前缀 `bb9b91b0ef5862b3` |
| v4.23.7 同版本覆盖重发 | ✅ tag 移至 `bbef3e86`，Release `428461815` 与 GHCR `v4.23.7`/`latest`（`sha256:8de9fd21…`）均为新产物，正文含直链修复说明 |
| 界面无旧行为 | ✅ 「Get direct link」弹窗只显示本站 `/f/NmTe/bxy.mp4`，无 `cmecloud.cn`、无 `X-Amz-Signature`、无「直接下载」入口 |
| 两机清盘 | ✅ InstaBox 释放约 3.4 GB，Baixiong 约 30 MB |

## 2. 构建（InstaBox，重活不落 Baixiong）

仓库 `b95e0c77` 拉到 `/data/workspace/build-site302`，口径对齐 `.goreleaser.yaml` 的 server build：

- 前端：`.build/build-assets.sh 4.23.7` → `application/statics/assets.zip`（14.8 MB）
- 后端：`CGO_ENABLED=0`、linux/amd64、`-trimpath -ldflags "-s -w -X …constants.BackendVersion=4.23.7 -X …constants.LastCommit=b95e0c77"` → 86.8 MB
- 镜像：仓库自带 `Dockerfile`，`docker build --provenance=false` → `cloudreve-fork:v4.23.7-site302`

踩到的两个坑，已记入下面 §6：

- **OOM**：机器 7–8 GB 内存且初始无 swap，vite 构建被 OOM killer 杀掉（退出码 137）。`build-assets.sh` 设的 `--max-old-space-size=8192` 超过物理内存。加 6 GB swap（`/swapfile2`）后构建约 72 秒通过。
- **根分区撑爆**：`/` 只有 16 GB，构建缓存在 `/var/lib/docker`、临时件在 `/tmp`（两者同盘）。多次 `no space left on device`，清理 build cache、悬空镜像与 journal 后成功。

## 3. 部署

部署前备份到 `/data/deploy-20261029-220915/`：`container-inspect.json`、`conf.ini`、WAL 安全导出 `sqlite3 .backup`（1.38 MB；直接拷主文件会丢最新写入）。

旧容器 `cloudreve-fork-test` 停止并删除，新镜像以**完全相同参数**重建：`-p 15212:5212`、`-v /data/cloudreve-fork-test:/cloudreve/data`、`--restart unless-stopped`、原 6 个环境变量（`CR_ENABLE_ARIA2=0` 等）、镜像自带 entrypoint，不加单文件 bind-mount。

重发完成后测试站已切到正式产物，并留别名 tag `ghcr.io/dark-cc/cloudreve:v4.23.7-rerelease` 便于回滚操作。

## 4. 验收自检（实测输出）

```
GET /api/v4/site/ping                 → {"code":0,"data":"4.23.7"}
GET /api/v4/file?uri=cloudreve://my   → 存储策略演示 / cr-y139-e2e / crdirecttest / bxy.mp4
                                        storage_policy = {id:NbI8, name:移动云盘直连, type:y139}
PUT /api/v4/file/source               → {"link":"https://15212-…instabox.tech/f/NmTe/bxy.mp4"}
GET /f/NmTe/bxy.mp4（不跟随）          → 302, size=696
                                        Location: https://ykj-eos-wx2-01.eos-wuxi-3.cmecloud.cn/…
GET 跟随跳转                           → 200, size=10244355, sha256 前缀 bb9b91b0ef5862b3
```

库中 6 个策略原样（1 Default local / 2 A s3 / 3 本地策略B local / 4 移动云盘直连 / 5、6 y139-bad-token）。

界面验证用 puppeteer-core + 系统 chromium（界面语言为英文），注入 `cloudreve_session` 后右键 `bxy.mp4` → "Get direct link"：弹窗只有本站地址，无 `cmecloud.cn`、无 `X-Amz-Signature`，也无「直接下载」按钮（仅原有 Show file name / Force download 两个开关）。

## 5. 覆盖重发

顺序：发版说明进主干（PR #144，`baixiong` → `bbef3e86`）→ 移动轻量 tag → 重跑 Release workflow。

第一次 run [40964151064](https://github.com/Dark-CC/cloudreve/actions/runs/40964151064) **失败**：GoReleaser 的 `replace` 模式不覆盖既有附件，13 个文件全部 `422 already_exists`。失败点在最后一步，因此 GHCR 镜像当时其实已被新构建覆盖（`9f0cdac0…` → `eca5ceea…`），只有 Release 停在旧内容——这个不一致状态已修好，处理方式是**先删旧 Release 再重跑同一 workflow**，attempt 2 成功。

最终：Release `428461815`（旧 `428235271` 删除），tag `v4.23.7` → `bbef3e86`，GHCR `v4.23.7`/`latest` = `sha256:8de9fd2186c7d657ccf56340694cb1bd1181f76aa960d136bc33008bc373f860`，13 个附件 sha256 全部与旧产物不同。完整新旧对照表见 [release-v4.23.7-rerelease.md](./release-v4.23.7-rerelease.md) §1。

## 6. 清盘

按名逐个删，未使用 `docker image prune -af`（会连带删掉可回滚镜像）。

| 机器 | 清理内容 | 释放 |
| --- | --- | --- |
| InstaBox | `cloudreve-fork:v4.23.7-site302`（本地构建镜像）、`/data/workspace/build-site302`（1.6 GB）、`/tmp` 测试件与 geetest 截图 | `/` 873 MB → 2.7 GB，`/data` 5.6 GB → 7.2 GB，合计约 3.4 GB |
| Baixiong | `/tmp` 探测二进制与 workflow 日志 | 约 30 MB（磁盘本就只用 24%） |

保留：当前运行镜像（含 digest 引用与 `v4.23.7-rerelease` 别名）、部署前备份目录 `/data/deploy-20261029-220915/`。

## 7. 可复用教训

- **同版本覆盖重发必须「先删 Release 再重跑」**：GoReleaser 的 `replace` 只替换 Release 元信息，不替换同名附件；且失败发生在编译与推镜像之后，别用镜像 digest 变了来判断整轮成功。
- **GoReleaser 失败后 Release 与 GHCR 会分叉**，验收要同时查两处：`/releases/tags/<tag>` 的 target/正文/附件 digest，和 GHCR manifest digest。
- `.build/build-assets.sh` 的 8 GB heap 在 7–8 GB 机器上必然 OOM，先加 swap 再构建。
- 部署自检走 `PUT /api/v4/file/source`（**不是** `/api/v4/file/url`，后者给的是中转下载地址），返回字段是 `data[].link`；测 302 必须用真实域名而非回环地址。
- 数据目录相对**可执行文件所在目录**解析，容器里挂 `/cloudreve/data` 才对得上；直接拷贝 sqlite 主文件会丢 WAL 里的最新数据，必须 `.backup`。
