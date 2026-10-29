# Baixiong 磁盘清理记录（2026-10-29）

机器：Baixiong（machineId `3Vpjw8E1s5xb6meuf6Qhf`，Linux arm64，2C/11G，根分区 `/dev/sda1` 96 GB）
执行方式：`remote_shell` 登录宿主机，只清构建残留，不动生产容器、数据卷与用户数据。

## 一、清理前盘点（只读）

```
Filesystem      Size  Used Avail Use% Mounted on
/dev/sda1        96G   79G   18G  82% /
```

### 分区内大头（`du -sxm`，单位 MB）

| 路径 | 占用 | 性质 | 处置 |
| --- | --- | --- | --- |
| `/var/lib/docker`（build cache 部分） | 43.2 GB | `docker system df` 中 Build Cache，83 条，0 条被引用 | **清空** |
| `/root/.tds/workspaces` | ~17 GB | 72 个任务工作区，其中 9 个 `frontend/node_modules` 各 1.1–1.4 GB | 见下方「保留项」 |
| `/root/.cache/go-build` | 2917 MB | Go 构建缓存 | 保留（加速构建） |
| `/root/.cache/ms-playwright` | 680 MB | Playwright 浏览器（验收脚本用） | 保留 |
| `/tmp` | 1425 MB | 构建残留 | **清 1.3 GB** |
| `/root/.gomodcache` | 1173 MB | Go module 缓存 | 保留 |
| `/tmp/node-compile-cache` | 83 MB | Node 编译缓存（自动再生） | 保留 |
| `/root/.cache/pnpm`、`/root/.npm`、`/root/.bun` | 417 / 394 / 1622 MB | 包管理器缓存 | 保留 |
| `/opt` | 805 MB | esim-shop、x-api-portal、new-api 数据 | 保留（生产） |
| `/root/shot` | 44 MB | 验收脚本与截图 | 保留（生产验收资产） |
| `/var/log` | 192 MB | 系统日志 | 保留 |
| `/var/tmp` | 64 KB | — | 无需处理 |

### Docker 状态（清理前）

```
TYPE            TOTAL     ACTIVE    SIZE      RECLAIMABLE
Images          10        1         2.686GB   2.196GB (81%)
Containers      1         1         4.096kB   0B (0%)
Local Volumes   3         1         1.224GB   1.221GB (99%)
Build Cache     83        0         43.2GB    2.46GB
```

镜像清单（含构建时间）：

| 镜像 | 大小 | 创建时间 | 处置 |
| --- | --- | --- | --- |
| `src-esim:latest` | 477 MB | 2026-10-22 | **保留**（运行中） |
| `01a163e9-…-esim:latest` | 477 MB | 2026-10-22 17:09 | 删除（旧代 test 构建标签，与 `src-esim` 同代重复） |
| `golang:1.26-alpine` | 357 MB | 2026-10-21 | 删除（构建用，基础镜像可重新拉取） |
| `postgres:16-alpine` | 411 MB | 2026-10-21 | 保留（esim 栈依赖） |
| `alpine:latest` | 26.6 MB | 2026-10-21 | 保留 |
| `ghcr.io/dark-cc/newapi-skin:latest` | 339 MB | 2026-10-21 | 保留（newapi 栈镜像，栈已下线但保留以便一条命令恢复） |
| `nginx:alpine` | 103 MB | 2026-10-14 | 保留（x-api-portal web） |
| `golang:1.25-alpine` | 324 MB | 2026-08-19 | 删除（旧构建基础镜像） |
| `node:20-alpine` | 194 MB | 2026-04-15 | 删除（旧前端构建基础镜像） |
| `golang:1.24-alpine` | 388 MB | 2026-02-04 | 保留（作为回滚可用的旧档基础镜像，本次磁盘已足够） |

容器：仅 `temporary-esim`（Up 6 days，healthy），无已停止容器需要清理。
卷：`e20a5061…`（dangling 匿名卷）、`gomodcache`、`src_esim-data`（esim 生产数据）。**一个未动**。

## 二、执行动作

### 1. `/tmp` 构建残留（约 1.3 GB → 111 MB）

删除前先把 `/tmp` 下零散脚本（`*.mjs`/`*.py`/`*.json`/`*.sh`/`*.log`）打包备份为 `/root/backup-tmp-scripts-20261029.tar.gz`（590 KB），避免误删调试脚本。

| 删除对象 | 大小 | 是什么 |
| --- | --- | --- |
| `/tmp/cr-4235` | 366 MB | 4.23.5 构建/调试目录（含 src/tree/out、build*.log、verify 脚本） |
| `/tmp/cr-bin` | 166 MB | **测试二进制** `cloudreve-linux-amd64`（89 MB）+ `cloudreve-linux-arm64`（84 MB），2026-10-28 构建 |
| `/tmp/cr-out` | 86 MB | 本地后台编译产物 `cloudreve`，2026-10-26 |
| `/tmp/cr-out2` | 86 MB | 同上第二代产物 |
| `/tmp/cr-policy-e2e` | 83 MB | 存储策略 e2e 测试目录 |
| `/tmp/cr-local` | 82 MB | 本地源码 checkout |
| `/tmp/cr-local-bin` | 81 MB | 本地编译二进制 |
| `/tmp/cr-build` | 71 MB | 构建用源码 checkout |
| `/tmp/assets` + `/tmp/assets-4236pre.zip` + `/tmp/assetsstub` | 77 MB | 前端资产目录与资产 zip |
| `/tmp/fork-src.tar.gz`、`/tmp/fork-src2.tar.gz` | 84 MB | 源码传输包 |
| `/tmp/fork-part-*`（6 段） | 48 MB | 源码分片传输包 |
| `/tmp/pwtest` | 15 MB | Playwright 临时浏览器目录 |
| `/tmp/bunx-0-eslint@latest` | 15 MB | bunx 一次性 eslint 包 |
| `/tmp/y139` | 14 MB | 测试数据目录 |
| `/tmp/node_modules` | 8.5 MB | 临时 node_modules |
| `/tmp/f2-*`、`/tmp/crv3` | ~48 MB | 前端资产分片传输目录 |
| `/tmp/pf-*.js` | ~12 MB | 导出的前端 chunk 样本 |

### 2. Docker

- `docker builder prune -af`：清空 **Build Cache 43.2 GB**（83 条记录，全部 0 引用，可完全再生）。
- `docker rmi` 删除 4 个构建用/重复镜像：`01a163e9-…-esim:latest`(477 MB)、`golang:1.26-alpine`(357 MB)、`golang:1.25-alpine`(324 MB)、`node:20-alpine`(194 MB)，合计约 1.35 GB。

### 3. 未触碰（按约束）

- `/data` 下生产数据、所有 docker volume、运行中的 `temporary-esim` 容器；
- `/opt/esim-shop`（esim-shop 生产）、`/opt/x-api-portal` + `/opt/new-api/data`（newapi 栈及其数据，已下线但保留以便 `docker compose up -d` 一键恢复）；
- `/root/shot` 验收脚本、`/root/.tds` 与各任务 workspace 的 git 仓库（仅列占用，未删任何仓库）；
- Go/Node/Playwright 构建缓存（`/root/.cache/go-build` 2.9 GB、`/root/.gomodcache` 1.2 GB、`/root/.cache/pnpm` 417 MB、`/root/.cache/ms-playwright` 680 MB）。

## 三、清理后复核

```
Filesystem      Size  Used Avail Use% Mounted on
/dev/sda1        96G   71G   26G  74% /
```

```
TYPE            TOTAL     ACTIVE    SIZE      RECLAIMABLE
Images          6         1         1.731GB   1.241GB (71%)
Containers      1         1         4.096kB   0B (0%)
Local Volumes   3         1         1.224GB   1.221GB (99%)
Build Cache     0         0         0B        0B
```

- 可用空间：**18 GB → 26 GB（+8 GB）**，使用率 82% → 74%。
- 生产健康检查：`temporary-esim` 仍 `Up (healthy)`，`http://127.0.0.1:3100/` 返回 200。
- 镜像剩余 6 个：`src-esim`、`postgres:16-alpine`、`alpine`、`newapi-skin`、`nginx:alpine`、`golang:1.24-alpine`。

## 四、后续建议

1. **最大的一块是任务工作区的 `frontend/node_modules`**：9 个 workspace 各 1.1–1.4 GB，合计约 11.5 GB（另含 `frontend/build` 4×47 MB）。这些属于 workspace 内的构建产物（不是 git 仓库本体），但都是 PI 任务工作区，平台随时可能在其中续跑任务，本次按约束未删。若要回收，建议在平台侧把已"完成/关闭"的 todo 对应 workspace 清理，或统一执行 `find /root/.tds/workspaces -maxdepth 2 -name node_modules -type d` 后逐个确认。
2. **本次已清空 Docker Build Cache 43.2 GB**——这也是本次释放量的主要来源（`df` 的 8 GB 反映的是宿主已回收的块；build cache 记录中的 43.2 GB 含大量未实际落盘的元数据膨胀）。下次 Go 镜像构建会变慢一次，属预期代价。
3. 后续常设：每次构建完清 `/tmp` 下 `cr-*`、`assets*`、`fork-*` 与 `docker builder prune -f`，可把磁盘稳定维持在 70% 以下。
