# v4.23.7 同版本号覆盖重发（含直链修复）

2026-10-30 实施。用户口径：「修复后的版本部署到测试站，然后重新编译 4.23.7 版本重新发版」→「就用 v4.23.7 覆盖重发」。

被覆盖的行为改动见 [y139-direct-link-site-302.md](./y139-direct-link-site-302.md)：移动云盘「获取直链」改回本站 `/f/:id/:name`，访问时 302 到云盘预签名地址，本站不中转字节。

## 1. 覆盖前基线（回溯用）

覆盖前先用 GitHub / GHCR API 抓一遍现状，任何一项都能凭下面的值找回旧产物。

| 项 | 旧（2026-10-29 首发） |
| --- | --- |
| tag `v4.23.7` 指向 | `2e9b42f5e151e1792d65a68e3aa0f8f9209e20ac`（轻量标签，PR #141 合并提交） |
| GitHub Release id | `428235271`，发布于 `2026-10-29T17:57:01Z`，正文 8105 字节 |
| Release 附件 | 13 个：`checksums.txt` + darwin/freebsd/linux/windows 各架构归档（linux 含 armv5–v7、loong64） |
| `cloudreve_v4.23.7_linux_amd64.tar.gz` | sha256 `26280cfc5446ccfbda965a90a10fa6203454e09fcc686aea5db5d2bd41b79f7e`，40,199,245 字节 |
| `cloudreve_v4.23.7_linux_arm64.tar.gz` | sha256 `c72ce60fc76dd7b82b6a2fad0a019ed280c0720a21fb5c869ed2a3f4fbc4203d`，37,642,078 字节 |
| `checksums.txt` | sha256 `9b9606acb2ebd33a7c8addcab9941f4c4a9e533ece830d80d1e81f4d81491c39`，1242 字节 |
| GHCR `cloudreve:v4.23.7` manifest | `sha256:9f0cdac0b7a9b7eea2e87ec7b688d4de0932c268fdd4ca762857f5c56655254d`（multi-arch: linux/amd64 + linux/arm64） |
| Release workflow run | [40926183258](https://github.com/Dark-CC/cloudreve/actions/runs/40926183258)，server job 全绿 |

旧镜像仍可从 GHCR 按 digest 取回：`docker pull ghcr.io/dark-cc/cloudreve@sha256:9f0cdac0…`（manifest 被新构建覆盖后该 digest 依然可寻址，因为 digest 即内容地址；万一被 GC 清掉，回滚就退回 v4.23.6 镜像 `ghcr.io/dark-cc/cloudreve:v4.23.6`）。

## 2. 覆盖方式

tag 是轻量标签，移动即覆盖，不需要重建注释对象：

```bash
git tag -f v4.23.7 <含直链修复的主干提交>
git push -f origin refs/tags/v4.23.7
```

`release.yml` 由 `push: tags: ['v*']` 触发，因此重新推同名 tag 会重跑整个 `Release` workflow：

- **发版说明**取自 `docs/release-notes/v4.23.7.md`（该 tag 提交里必须存在，workflow 第一步就校验），本次已补记直链修复与「同版本号重发」说明；
- **二进制版本号**由 GoReleaser 的 ldflags 注入 `{{.Version}}`，与源码里的 `application/constants/constants.go` 无关，所以不重新改版本号、`BackendVersion` 保持 `4.23.7` 即可，`/api/v4/site/ping` 仍返回 `4.23.7`；
- **GHCR 镜像**按 tag 名推送，同名 tag 被新 digest 覆盖，`latest` / `v4` / `v4.23.7-slim` 等派生 manifest 一并重建。

### 已存在 Release 时的行为（本任务待查明项，实测结论）

`release.yml` 里 GoReleaser 走 `goreleaser release --clean`，其默认 `release.mode` 为 `replace`（本仓库 `.goreleaser.yaml` 未覆盖该字段），所以**已存在的 Release 会被就地覆盖，不需要先删 Release 或 GHCR tag**：正文换成新发版说明，附件同名覆盖（清单里的 13 个文件全为同名同架构），target_commitish 更新为新 tag 指向。

若实测出现「asset already exists」类失败，处理方式是删掉该 Release 与对应 GHCR tag 后重跑，无需改 workflow——见 §6 的实测记录。

## 3. 部署到测试站（先于重发）

用户要求先部署、再重发，所以测试站用的是**本地按该提交构建的镜像**，标签 `cloudreve-fork:v4.23.7-site302`，不依赖 GHCR 是否已重建：

- 与 `.goreleaser.yaml` 的 server build 同口径：`CGO_ENABLED=0`，linux/amd64，ldflags 注入 `BackendVersion=4.23.7`、`LastCommit=<短哈希>`；前端用 `.build/build-assets.sh 4.23.7` 产出 `application/statics/assets.zip` 后 embed；
- 运行时镜像用仓库自带 `Dockerfile`（alpine + tini/supervisor/aria2/ffmpeg 等），与 GHCR 镜像同一构建路径；
- 容器参数与原部署完全一致：`-p 15212:5212`、`-v /data/cloudreve-fork-test:/cloudreve/data`、`--restart unless-stopped`、原有 6 个环境变量、镜像自带 entrypoint，**不加单文件 bind-mount**；
- 重发完成后回到测试站把镜像换成正式产物 `ghcr.io/dark-cc/cloudreve:v4.23.7@<新 digest>`，确保测试站跑的与用户下载到的是同一份字节。

### 部署前备份与回滚

- `docker inspect cloudreve-fork-test` → `/data/deploy-<时间戳>/container-inspect.json`；
- `sqlite3 data/cloudreve.db ".backup '<备份>'"`（WAL 安全导出，主文件单独拷会丢最新写入）；
- 回滚镜像保留旧 `ghcr.io/dark-cc/cloudreve:v4.23.7`（覆盖前 digest `sha256:9f0cdac0…`）与本次运行的新镜像，一条 `docker run` 换镜像名即可回滚。

## 4. 部署后自检

| 检查项 | 期望 |
| --- | --- |
| `GET /api/v4/site/ping` | `{"code":0,"data":"4.23.7"}` |
| 存储策略 | 6 个原样（1 Default local / 2 A s3 / 3 本地策略B local / 4 移动云盘直连 / 5、6 y139-bad-token） |
| `crdirecttest` 目录 | 在，绑定策略 4（hashid `NbI8`） |
| `PUT /api/v4/file/source`（注意：是 `file/source`，不是 `file/url`） | 返回 `https://15212-…instabox.tech/f/<hash>/<name>` |
| `GET /f/<hash>/<name>` | **302**，`Location` 为云盘预签名地址，响应体几百字节，不中转字节 |
| 跟随跳转取回字节 | 与源文件等长、逐字节一致 |
| 界面「获取文件直链」弹窗 | 本站地址，无 `cmecloud.cn` / `X-Amz-Signature`，无「直接下载」入口 |

## 5. 清盘

`docker image prune -af` 禁止使用（会连带删掉可回滚镜像）。按名字逐个删本次失败/中间镜像，保留：当前运行镜像、覆盖前的 `v4.23.7`（回滚）、部署前备份目录。Baixiong 与 InstaBox 两机都要清，并汇报释放量。

## 6. 实测记录

见本次部署报告 [instabox-15212-site302-deploy.md](./instabox-15212-site302-deploy.md)。
