# 15212 测试站升级到 v4.23.7 部署报告

日期：2026-10-30
范围：发版 v4.23.7（GitHub Release + GHCR 镜像）并将 InstaBox 15212 测试站切换到正式产物。

## 1. 发版

- **tag**：`v4.23.7`（轻量标签，指向 `baixiong` tip `2e9b42f`，PR #141 合并提交）。
- **源码改动**：`application/constants/constants.go` 的 `BackendVersion` 由 `4.23.5` 改为 `4.23.7`（v4.23.6 只靠 CI ldflags 注入，本次落到源码）；新增 `docs/release-notes/v4.23.7.md`，与 tag 同提交（release workflow 的存在性校验要求）。
- **Release workflow**：run 40926183238… 实际为 [run 40926183258](https://github.com/Dark-CC/cloudreve/actions/runs/40926183258)，server job 全绿（desktop / android / updater 按仓库变量门控跳过）。QEMU arm64 未发生失联，无需重跑。
- **Release 产物核对**（api.github.com 实测）：
  - `checksums.txt` + 13 个平台归档（darwin/freebsd/linux/windows × amd64/arm64，linux 另含 armv5–v7、loong64）；
  - docker manifest `ghcr.io/dark-cc/cloudreve:v4.23.7` 为 multi-arch（linux/amd64 + linux/arm64）。
- **Release 正文**取自 `docs/release-notes/v4.23.7.md`，覆盖：移动云盘（139）策略、严格直连（上传/下载/两个中转开关锁定）、上传按客户端点名策略（限组允许集合）、右键单文件直连、未知策略类型降级、打包拒绝与错误体修复、40099 错误码、已知限制（不能打包、加密仍中转、历史 local 实体仍中转、分片建议 100MB）。
- **未发版改动核查**（开放问题）：`git log v4.23.6..HEAD` 全部为用户可见的功能/修复提交，无 dependabot 等额外依赖升级需要单独说明。

## 2. 测试站部署

- **部署前备份**（均在 InstaBox，不入库）：
  - `docker inspect cloudreve-fork-test` 存档 → `/data/backup-20261029-175711/container-inspect.json`；
  - 数据库 `sqlite3 .backup`（WAL 安全导出）→ `/data/backup-20261029-175711/cloudreve.db`、`pdb.db`。
- **部署方式**：停止并删除旧容器，用正式产物 `ghcr.io/dark-cc/cloudreve:v4.23.7` 重建，参数与归档一致——`-p 15212:5212`、`-v /data/cloudreve-fork-test:/cloudreve/data`、`--restart unless-stopped`、7 个 `CR_SETTING_DEFAULT_*` / `CR_ENABLE_ARIA2` 环境变量、镜像自带 entrypoint，**无单文件 bind-mount**。
- **保留回滚**：`cloudreve-fork:direct-only-v7`（上一运行镜像）与 `cloudreve-fork:rollback-pre-directdl` 均保留，一条 `docker run`（参数同上、换镜像名）即可回滚。

## 3. 部署后自检（实例内 127.0.0.1:15212 实测）

| 检查项 | 结果 |
|---|---|
| 站点首页 | `HTTP 200` |
| `GET /api/v4/site/ping` | `{"code":0,"data":"4.23.7"}` |
| 存储策略 | 6 个原样（1 Default local / 2 A s3 / 3 本地策略B local / 4 移动云盘直连 y139 / 5、6 y139-bad-token y139） |
| 文件总数 | 24（与部署前一致） |
| `crdirecttest` 目录 | 在，绑定策略仍为 4（hashid `NbI8`） |
| 上传直传 | `PUT /api/v4/file/upload`（policy_id=`NbI8`）返回 `upload_urls[]`，Host = `ykj-eos-wx2-01.eos-wuxi-3.cmecloud.cn`（云盘对象存储，非本站）；`PUT` 分片 → `200` |
| 完成回调 | `GET /api/v4/callback/y139/<sid>/<secret>` → `{"code":0}` |
| 下载直连 | `POST /api/v4/file/url` 返回云盘预签名地址（同 Host），`GET` → `200`，内容与上传文件逐字节一致（`cmp` 通过） |
| 测试文件清理 | `rt-verify.txt`、`cr-test-ctrl.png` 已删除，`crdirecttest` 现为空目录 |

说明：部署后第一次取下载地址时拿到一条已过期的缓存签名（`Request has expired`），带 `no_cache` 重取即得新签名——属预期行为，非缺陷。

## 4. 凭据暴露面检查表（用户红线：移动云盘 Authorization / x-yun-app-key 不外泄）

| # | 检查项 | 方法 | 结果 |
|---|---|---|---|
| 1 | 本次提交 diff（版本号 + 发版说明） | `git diff` 后 grep `Basic [A-Za-z0-9+/=]{16,}`、app-key 赋值模式 | 0 命中 |
| 2 | 全仓库 tracked 文件 | `git grep -E "Basic [A-Za-z0-9+/=]{16,}"` | 0 命中 |
| 3 | 既有 139 相关 docs（可行性/实现/e2e/直连报告） | 逐处人工核对 `Authorization` / `x-yun-app-key` 出现位置 | 仅请求头**名称**与占位式说明（如 `Basic base64("pc:<token>:<account>")`），无真实值；实现报告已声明日志/错误串不打凭据 |
| 4 | v4.23.7 发版说明正文 | 人工通读 + 关键字扫描 | 只出现「网页端登录凭据」「App Key（x-yun-app-key）」等描述性文字，无值 |
| 5 | GitHub Release 正文 | API 拉取 body 后凭据样式扫描 | clean |
| 6 | 提交信息与 PR 描述（v4.23.6..HEAD 全量） | `git log` + grep | 0 命中 |
| 7 | release workflow 日志 | server job 成功；y139 驱动的 `headers()` 只塞 `http.Header`、`decode()` 只读状态码，代码层面不打印请求头 | 通过 |
| 8 | 数据库与备份 | `cloudreve.db` / `pdb.db` 及 `/data/backup-20261029-175711/` 仅存于 InstaBox 本机，**未入 git**；凭据在库内以 `<redacted>` 记述 | 通过 |

结论：移动云盘凭据未出现在任何公开位置（git / 发版说明 / Release / docs / PR / 提交信息）。

## 5. 清盘汇报

| 机器 | 动作 | 释放量 |
|---|---|---|
| InstaBox（m_agqw5…） | `/tmp` 测试二进制（cloudreve-4235fix2/3、4236pre、b32m）+ builder 缓存 `docker builder prune` | ~270MB + 863.8MB ≈ **1.1GB** |
| Baixiong | 过期测试镜像 `cloudreve-fork:4.23.6-directdl` | **1.91GB** |

保留：当前部署镜像 `ghcr.io/dark-cc/cloudreve:v4.23.7`、回滚镜像 ×2（InstaBox）、部署前备份目录（InstaBox）、builder 缓存其余部分（加速后续构建）。Baixiong 无 `/tmp` 大文件、无 dangling 镜像。

## 6. 结论

发版与测试站更新全部完成，自检全过：**15212 现跑正式产物 v4.23.7，移动云盘策略下上传下载仍直连云盘**，可让用户验收。生产从机 :5212、aria2 :6800、15213 Pro 全程未动。
