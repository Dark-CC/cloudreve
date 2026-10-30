# InstaBox 磁盘占用实测与「挂 S3 顶硬盘」结论

采集时间：2026-10-30 14:34–14:55 UTC
机器：InstaBox `m_agqw5litn4acpbo7b5axu3ycku`（hostname `instabox-baixiong`，x86_64，4 vCPU / 8 GiB RAM）
采集方式：InstaBox exec API 逐条执行**只读**命令（`df`、`findmnt`、`du`、`docker system df`、`docker ps`、`docker inspect`、`find`、`journalctl --disk-usage`、`stat`、`ps`）

## 0. 只读声明（本任务硬约束）

本次排查**没有执行任何写操作**。全文所有命令均为读操作，未删除、未 prune、未重启、未改任何配置：

- 未执行 `rm` / `docker rmi` / `docker container prune` / `docker image prune` / `docker builder prune`；
- 未执行 `systemctl` 任何动词、未 `docker restart/stop`；
- 未改 `/etc/docker/daemon.json`、未改 fstab、未挂载任何东西；
- 未动 5212 生产从机（`/data/ouFmtYpb`，进程 `cloudreve_v4.18.0 -c config.ini` PID 30019，运行 416596 s）与 15213 Pro 实例（`/data/pro15213`，PID 268881，运行 185551 s）。

采集过程中对 5212/15213 目录只做了 `du`、`find -printf`、`ls`。

## 1. 两块盘的真实体型（先纠正一个前提）

这台机器不是「一块 20GiB 盘」，而是**两块盘**：

```
Filesystem      Size  Used Avail Use% Mounted on
/dev/root        16G  8.2G  6.7G  55% /
/dev/vdb         20G   16G  3.6G  82% /data
```

| 盘 | 设备 | 容量 | 已用 | 可用 | 使用率 | 里面是什么 |
|---|---|---|---|---|---|---|
| 系统盘 `/` | `/dev/root`（vda） | 15.6 GiB | 8.1 GiB | 6.6 GiB | **55%** | 系统本体 + **Docker 镜像层全部在这里** |
| 持久盘 `/data` | `/dev/vdb` | 19.5 GiB | 15.9 GiB | 3.6 GiB | **82%** | 测试站数据、workspace 检出、全部缓存 |

inode 都在安全区（`/` 11%、`/data` 30%），不是小文件耗尽型的问题。

**关键结构事实**：`/etc/docker/daemon.json` 写着 `{"data-root": "/data/docker"}`，但 `du /data/docker` 只有 **3 MB**——因为镜像层实际落在 **containerd** 的 snapshotter 下，仍在根分区：

```
/data/docker/rootfs/overlayfs/<id> overlay  lowerdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/960/fs:...
```

`/var/lib/containerd` = 4067 MB，其中：

| 子目录 | 大小 | 是什么 |
|---|---|---|
| `io.containerd.snapshotter.v1.overlayfs` | **2997 MB** | 镜像解包后的层（26 个 snapshot，正在用的容器是 960/959/958/957/863/862/708 + 上层 961） |
| `io.containerd.content.v1.content` | **1068 MB** | 拉取时的镜像 blob（压缩层） |
| 其余（bolt metadata / runtime task / tmpmounts 等） | 4 MB | 元数据 |

所以「把 data-root 挪到 /data」这个既有配置**并没有真正把镜像搬走**，镜像照样吃系统盘。这是本次排查的第一个新发现。

## 2. `/data` 占用构成（15.9 GiB，按大小降序）

`/data` 一级目录实测（`find -maxdepth 1 -exec du -sxm`，单位 MiB）：

| # | 路径 | 大小 (MiB) | 占 /data | 是什么 | 性质 |
|---|---|---|---|---|---|
| 1 | `/data/workspace` | **5673** | 34.8% | 构建工作区（含 `.yarncache`、`.gocache`、`crB` 源码检出） | 构建产物 + 缓存 |
| 2 | `/data/ouFmtYpb` | **5434** | 33.3% | **官方 5212 Pro 从机数据目录**，几乎全是 aria2 未完成下载 | 他人数据 / 需确认 |
| 3 | `/data/home` | **5113** | 31.3% | sandbox 用户 HOME，其中 `.cache` 4969 | 构建缓存 |
| 4 | `/data/pro15213` | 89 | 0.5% | 15213 Pro 复制实例 | 不可清 |
| 5 | `/data/cloudreve-fork-test` | **5** | 0.03% | **15212 测试站数据目录**（bind 到容器 `/cloudreve/data`） | 不可清 |
| 6 | `/data/docker` | 3 | 0.02% | Docker 元数据（镜像层不在这） | 不可清 |
| 7 | `/data/backup-20261029-175711` | 3 | 0.02% | 4.23.7 部署前备份（容器 inspect + sqlite .backup） | 可清（回滚用，见 §4） |
| 8 | `/data/deploy-20261030-095200` | 2 | 0.01% | 部署存档 | 可清 |
| 9 | `/data/deploy-20261029-220915` | 2 | 0.01% | 部署存档 | 可清 |
| 10 | `/data/cloudreve-backup` | 2 | 0.01% | 备份 | 可清 |
| 11 | `/data/backup-site302` | 2 | 0.01% | 备份 | 可清 |
| 12 | `/data/backup-y139-20261029-120903.db` | 1 | — | 单库备份 | 可清 |
| 13 | `/data/ouFmtYpb-migrate-backup` | 1 | — | 迁移备份 | 可清 |
| 14 | `/data/config`、`/data/state`、`/data/lost+found` | 各 1 | — | 系统/空 | — |

**测试站数据本身几乎不占地方：5 MiB。** 这点很重要，直接决定了 §5 的结论。

### 2.1 `/data/workspace` 拆解（5673 MiB）

| 路径 | 大小 (MiB) | 是什么 | 是否被 git 跟踪 |
|---|---|---|---|
| `/data/workspace/.yarncache` | **2517** | 前端依赖 yarn 离线缓存（`v6/npm-*-integrity`） | 否 |
| `/data/workspace/.gocache` | **1516** | Go 构建缓存（`46/ c8/ ea/ …`） | 否 |
| `/data/workspace/crB` | **1506** | cloudreve 源码检出：`frontend/node_modules` **1354** + `frontend/build` **47** + `.git` 34 + 源码 | `node_modules`/`build` 被 `.gitignore` 覆盖，仓库本体干净（`git status --porcelain` = 0 行） |
| `/data/workspace/cr-vas` | 78 | 另一份源码检出（无 node_modules），`git status` 干净 | 仓库本体 |
| `/data/workspace/cloudreve-src` | 31 | 源码检出，`git status` 干净 | 仓库本体 |
| `/data/workspace/pwtest`、`probe2` | 各 12 | Playwright / 探测临时目录 | 否 |
| 其余（`tmp`/`sync`/`shots`/各类 `*.log`） | ≈9 | 构建日志与临时目录 | 否 |

### 2.2 `/data/home` 拆解（5113 MiB）

| 路径 | 大小 (MiB) | 是什么 |
|---|---|---|
| `.cache/yarn` | **2517** | **与 `/data/workspace/.yarncache` 内容完全相同的第二份拷贝** |
| `.cache/go-build` | **1581** | Go 构建缓存（与 `.gocache` 又是两套） |
| `.cache/ms-playwright` | **863** | Playwright 浏览器（验收脚本依赖） |
| `.config` | 89 | 配置 |
| `.npm` | **41** | npm 包缓存 |
| `npm-global` / `.npm-global` / `npmg` | 各 6 | 三处 npm 全局目录（重复） |
| `.yarn` | 1 | yarn 自身 |
| 其余（`.ssh`、`.profile`、`MACHINE.md`…） | ≈1 | — |

**重复盘点的证据**：两处 yarn 缓存是**两份独立副本**，不是硬链接——

```
$ stat -c "%d %i %n" /data/home/.cache/yarn /data/workspace/.yarncache
65040 141361   /data/home/.cache/yarn
65040 1200228  /data/workspace/.yarncache
```

同设备（65040）不同 inode，且两边能找到字节数完全相同的同名 tarball（例：`npm-@excalidraw-excalidraw-0.18.0`… 的 `.yarn-tarball.tgz` 均为 31234118 B）。**光是同一份 yarn 缓存就存了两遍，白占 2517 MiB。** Go 缓存同理（`go-build` 与 `.gocache`，合计 3097 MiB，两套并行）。

### 2.3 `/data/ouFmtYpb` 拆解（5434 MiB）——本次最大单块，且不是我们的数据

```
/data/ouFmtYpb/cloudreve_v4.18.0_linux_amd64/data/temp/aria2/a235a09d-.../【高清剧集网发布 www.TTHDTT.com】诡秘之主[全13集]…2160p.WEB-DL.H265.AAC-DeePTV/
```

16 个文件，13 个 4K 剧集 mp4（每个 358–503 MB）+ 3 个 png 广告图，**最后写入时间 2026-10-27 15:37**（已停摆 3 天）。这是官方 **5212 Pro 从机**的 aria2 下载临时目录残留，不是 fork 的东西，也不是测试站的东西。

按约束，5212 生产从机不可清 → 这一项归入「需确认」。

## 3. `/` 占用构成（8.1 GiB，按大小降序）

| # | 路径 | 大小 (MiB) | 占 / | 是什么 | 性质 |
|---|---|---|---|---|---|
| 1 | `/var/lib/containerd` | **4067** | 49.6% | **Docker 镜像层**（snapshotter 2997 + content 1068） | 需确认（含在用/回滚镜像） |
| 2 | `/usr` | **3286** | 40.0% | 系统软件包 | 不可清 |
| 3 | `/opt/rust` | **620** | 7.6% | Rust 工具链 | 需确认 |
| 4 | `/tmp` | 109 | 1.3% | tmpfs（`/tmp` 是内存盘，3.9G 的 tmpfs，占 109M） | 可清 |
| 5 | `/root` | 76 | 0.9% | root HOME | 需确认 |
| 6 | `/var/log` | 65 | 0.8% | 系统日志（journal 56 MB，`journalctl --disk-usage` 实测） | 可清 |
| 7 | `/var/cache` | 3 | — | apt 缓存（**已经很小**，无空间可腾） | 可清但无意义 |
| 8 | `/etc`、`/boot`、`/srv` 等 | 4 | — | 系统 | 不可清 |

（`/var/lib` 合计 4175 MiB = containerd 4067 + `/var/lib/apt` 91 + dpkg 18；`apt` 那 91 MiB 是索引，不是包缓存。）

各块相加 8230 MiB ≈ 8.0 GiB，与 `df` 的 8.2G 一致。

`/tmp` 是 tmpfs（`tmpfs 3.9G 109M 3.8G 3% /tmp`）——**它在内存里，不占盘**，清它对磁盘毫无帮助（这与 Baixiong 上 `/tmp` 是真磁盘的情况不同，不要套用旧经验）。

### 3.1 Docker 镜像逐个清点（6 个，4.252 GB）

`docker system df -v` 实测：

| 镜像 | 大小 | 唯一层 | 创建时间 | 容器引用 | 定位 |
|---|---|---|---|---|---|
| `cloudreve-fork:admin-r7` | 388 MB | 127.1 MB | 39 分钟前 | **1（`cloudreve-fork-test`，Up 39 min，`0.0.0.0:15212->5212`）** | **当前部署，不可清** |
| `cloudreve-fork:vas-1030-r3` | 396 MB | 135.3 MB | 39 分钟前 | 0 | 同批构建兄弟版本 |
| `cloudreve-fork:admin-r6` | 361 MB | 100.2 MB | 54 分钟前 | 0 | 上一版（回滚候选） |
| `cloudreve-fork:easypay-1030-r2` | 418 MB | 304.9 MB | 2 小时前 | 0 | 易支付版本（回滚候选） |
| `debian:bookworm-slim` | 116 MB | 2.362 MB | 3 天前 | 0 | 基础镜像（可重拉） |
| `mcr.microsoft.com/playwright:v1.49.1-jammy` | **3.32 GB** | **3.322 GB** | 22 个月前 | **0** | **Playwright 镜像，占了镜像总量的 78%** |

镜像层共享：`cloudreve-fork` 四个 tag 共享 260.8 MB（admin-r7/vas-1030-r3/admin-r6）或 113.6 MB（easypay-1030-r2）。

**`docker system df` 报「可回收 3.865GB (90%)」——但这个数字会骗人**：它把 `playwright:v1.49.1-jammy`（3.32GB，无人引用）和三个旧 tag 都算成可回收。实际回收要按 §4 的判定来，绝不能照着 90% 全清。

### 3.2 容器运行时杂项

| 项 | 实测 | 说明 |
|---|---|---|
| 容器可写层 | 28.67 kB | 容器几乎不在层里写东西 |
| 容器日志（json-file） | **0.87 MiB** | `/data/docker/containers/39bbe…/…-json.log` = 914285 B |
| Build Cache | **4.096 kB（1 条）** | 几乎为空——历史上被清过 |
| Local Volumes | 0 个，0 B | 测试站数据是 bind mount，不用卷 |
| 容器数 | 1（全部，`docker ps -a` 只有这一个） | 无僵尸容器 |

**镜像层里混着 dpkg/apt 日志**（`snapshots/833/fs/var/log/dpkg.log` 391 KB 等）——那是 `debian:bookworm-slim` 构建时就有的，不是运行时日志。

## 4. 逐项「可清 / 需确认 / 不可清」判定与可释放量

判定依据统一为三条：(a) 是否有运行中的进程/容器引用；(b) 是否是回滚路径依赖；(c) 是否是他人的生产数据（5212/15213）。

### A. 不可清（动了就出事）

| 项 | 大小 | 依据 |
|---|---|---|
| `/data/cloudreve-fork-test`（15212 测试站数据，bind mount，`rw=true`） | 5 MiB | 容器 `cloudreve-fork-test` 的 `/cloudreve/data`，删了站点空库 |
| `cloudreve-fork:admin-r7` 镜像 | 388 MB | **正在运行容器的镜像**，删了无法重启 |
| `/data/pro15213`（15213 Pro 实例） | 89 MiB | 运行中 PID 268881（185551 s），独立 systemd 服务 |
| `/data/docker` 元数据 | 3 MiB | Docker 引擎本体 |
| `/usr` | 3286 MiB | 系统本体 |
| `/` 上的 `/etc`、`/boot`、`/var/lib/apt` | 95 MiB | 系统本体 |

**小计：不可动 ≈ 3.8 GiB**，其中真正「数据」只有 94 MiB（测试站 5 + 15213 89），其余是系统与在用镜像。这也说明：单靠清盘救不了系统分区，因为 `/` 的 8.1 GiB 里 3.3 GiB 是 `/usr`、4.0 GiB 是镜像层。

### B. 需确认（我的建议 + 代价）

| 项 | 大小 | 建议 | 代价 / 前提 |
|---|---|---|---|
| `/data/ouFmtYpb/…/data/temp/aria2/…诡秘之主…`（13 集 4K mp4 + 3 png） | **5434 MiB** | **清**（占比第一，2026-10-27 后无写入，属未完成下载残留） | **但这是 5212 Pro 从机的数据**，按既有约束「不动 5212 生产从机」，须先让用户确认：这 13 集是放弃的下载，还是还要续传。清掉后 aria2 任务需重新开始 |
| `mcr.microsoft.com/playwright:v1.49.1-jammy` 镜像 | **3322 MiB** | **清**（0 容器引用，22 个月前构建；本机已有 `/data/home/.cache/ms-playwright` 的浏览器 863 MiB 可直接用） | 代价：以后若要在容器里跑 Playwright 需重新拉 3.3 GB。本机现有验收脚本用的是 HOME 里的 playwright 缓存，不依赖这个镜像 |
| `/data/workspace/.yarncache` + `/data/home/.cache/yarn` 其中一份 | **2517 MiB** | **清一份**（两份独立副本，内容相同） | 代价：下次 `yarn install` 少一份离线包，需联网重下 |
| `/data/workspace/.gocache` 或 `/data/home/.cache/go-build` 其中一份 | **1516–1581 MiB** | **清一份**（两套并行 Go 缓存） | 代价：下次 Go 编译变慢（首次全量） |
| `/data/workspace/crB/frontend/node_modules` | **1354 MiB** | **清**（`git status` 干净，`node_modules` 被 `.gitignore` 覆盖，可完全重建） | 代价：**若该工作区上还有任务在跑**，`yarn install` 需重跑 ~2–5 分钟。当前无构建进程（`ps` 无 yarn/node 构建） |
| `/data/workspace/crB/frontend/build` | 47 MiB | 可清 | 同上，可重建 |
| `cloudreve-fork:vas-1030-r3`、`admin-r6`、`easypay-1030-r2` | 396+361+418 = **1175 MiB**（共享层，删一个只净得它的 UNIQUE SIZE） | **至少保留 1 个回滚镜像**。建议留 `admin-r6`（当前部署的上一版，最贴近的回滚点）+ `easypay-1030-r2`（功能版本回滚），删 `vas-1030-r3`（与其他同批重复） | 删 `vas-1030-r3` 净释放约 **135 MiB**（其 UNIQUE SIZE）。删多了就失去回滚能力 |
| `/opt/rust` | 620 MiB | 若确实没有 Rust 构建需求可清 | 本仓库 `desktop/` 无 Rust 工具链依赖（Go + Node 栈）。清掉后如有需要重装 |
| `/data/backup-20261029-175711`、`deploy-*`、`cloudreve-backup`、`backup-site302`、`backup-y139*.db`、`ouFmtYpb-migrate-backup` | ≈12 MiB | 可清（旧的部署存档） | 失去 4.23.7 部署前的容器定义与库快照。体量小，优先级最低 |

### C. 可清（安全、无回滚价值）

| 项 | 大小 | 依据 |
|---|---|---|
| `/data/home/.npm` | 41 MiB | npm 包缓存，可重下 |
| `/data/workspace/pwtest`、`probe2` | 24 MiB | Playwright / 探测临时目录 |
| `/data/workspace/tmp`、`sync`、`shots`、各 `*.log` | ≈9 MiB | 构建日志 |
| `/var/log`（journal） | 65 MiB | `journalctl --vacuum-size=` 可回收，约 56 MB |
| `/tmp` | 109 MiB | **但它是 tmpfs，清了不释放磁盘**，别指望它 |
| `/var/cache` | 3 MiB | apt 缓存已很小 |

### 预估可释放总量

| 场景 | 释放量 | 分摊 |
|---|---|---|
| **保守**（只清 C 类 + 一份重复 yarn + crB 的 node_modules/build + Playwright 镜像 + `vas-1030-r3`） | **≈ 7.3 GiB** | `/data` 约 3.9 GiB、`/` 约 3.4 GiB |
| **推荐**（保守 + 5212 的 aria2 mp4 + 删一份 Go 缓存 + `/opt/rust`） | **≈ 14.8 GiB** | `/data` 约 10.7 GiB、`/` 约 4.1 GiB |
| **推荐但不动 5212 数据、也不删 Playwright 镜像**（只清重复缓存 + node_modules/build + `vas-1030-r3` + 日志） | **≈ 8.1 GiB** | `/data` 约 7.9 GiB、`/` 约 0.2 GiB* |

推荐方案后：`/data` 从 15.9 GiB → 约 **1.1 GiB（约 6%）**，`/` 从 8.1 GiB → 约 **4.7 GiB（约 30%）**。这才是真正「不再卡住」的状态。

\* 第三行不动 `/` 上的两项（Playwright 3.3 GiB + `/opt/rust` 620 MiB），所以只润 `/data`：15.9 GiB → **约 8.0 GiB（41%）**，`/` 仍在 8.0 GiB（55%）。这也说明**只清 `/data` 救不了 `/` 的 55%**——那 4 GiB 镜像层要么删无用镜像（Playwright 3.3 GiB）才能降，要么把 containerd 目录改到 `/data`（见 §6.3）。

明细（推荐方案，MiB，每项只计一次）：

```
5212 aria2 mp4 残留            5434   ← 需先与用户确认
playwright:v1.49.1-jammy       3322   ← 需先与用户确认（在 / 上）
重复的 yarn 缓存（留一份）        2517
重复的 Go 缓存（留一份）          1581
crB/frontend/node_modules      1354
/opt/rust                       620   （在 / 上）
vas-1030-r3 净释放              135
/var/log (journal)               65   （在 / 上）
crB/frontend/build               47
.npm                             41
临时目录/日志 ≈                    33
─────────────────────────────────
合计                         15149 MiB ≈ 14.8 GiB
```

> 注：两项「需确认」占了 8556 MiB（8.4 GiB）。**如果用户不想动 5212 的数据、也不删 Playwright 镜像，可释放量降到约 8.1 GiB**（见上表第三行），`/data` 从 82% 降到约 41%——够用，但不宽裕。

## 5. 「挂 S3 顶本地硬盘」对这台机器到底能不能解

### 5.1 先用数据说话：占用大头是什么

`/data` 15.9 GiB 的构成，按「会不会自己长」分类：

| 类别 | 大小 | 占比 | 会不会自己长 |
|---|---|---|---|
| **构建缓存与依赖**（两处 yarn 2517+2517、两处 Go 缓存 1581+1516、node_modules 1354、build 47、.npm 41） | **≈ 9.35 GiB** | **58.6%** | **会**，每跑一次构建就长 |
| **他人下载残留**（5212 的 aria2 mp4） | 5.31 GiB | 33.3% | 不会（已停 3 天），但会再发生 |
| **测试站数据**（`/data/cloudreve-fork-test`） | **0.005 GiB** | **0.03%** | 几乎不长 |
| **其他实例数据**（pro15213 + Docker 元数据） | 0.09 GiB | 0.6% | 缓慢 |

结论很硬：**可持续增长的那 58.6% 全是构建缓存与依赖，而用户问的「测试站数据」只有 5 MiB——占 0.03%。**

### 5.2 所以「挂 S3 当本地硬盘」解决不了这台机器的问题

按项对照，为什么挂载顶不了：

| 想外置的东西 | 挂 S3 能不能顶 | 为什么 |
|---|---|---|
| **Docker 镜像层**（`/var/lib/containerd` 4067 MiB，在 `/` 上） | **不能** | overlayfs 的 `lowerdir/upperdir` 必须是本机块设备上的真实目录。S3 是对象存储、不是 POSIX 文件系统，overlayfs 需要 hardlink、`renameat2`、xattr，s3fs/goofys/rclone mount 都不支持（s3fs 明确不支持 hardlink，`link()` 返回 ENOSYS）。社区的「Docker 上 S3」方案全是靠 block device 或 loopback 块设备，等于是把数据存到 S3 上的一个块设备文件，随机写延迟上百毫秒，`docker build` 会超时，且任何一次网络抖动都会让镜像层损坏 |
| **构建缓存**（yarn/go cache 约 8 GiB） | **技术上可以，但不该做** | yarn/go cache 是写多读多的随机小 I/O（Go cache 每次编译写几百个小文件）。放 S3 挂载上，`go build` 从 40 秒变十几分钟，且网络一断构建直接失败。**正确的做法是「清掉重复的那一份」而不是「搬到 S3」**——S3 不是缓存，缓存本来就该可无痛丢弃 |
| **node_modules**（1354 MiB） | **不建议** | `.bin` 里全是符号链接、esbuild 等包带原生二进制，需要 exec 权限。对象存储挂载无 exec、符号链接语义也不完整，`yarn install` 会失败或产出坏包。而且 node_modules 本就该 `yarn install` 重建，不该长期存 |
| **5212 的 aria2 下载临时目录**（5434 MiB） | **部分可以，但不是最优** | 这个目录的内容是「下载中的文件」，aria2 需要随机写，S3 挂载写会极慢且中断后分片校验失败。真正合适的做法是让 5212 的目的策略直接是 S3 对象存储（cloudreve 自己就支持，这也是 fork 已经在做的方向），而不是「先下到本地再搬」 |
| **测试站用户文件**（`/data/cloudreve-fork-test`，5 MiB） | **能，但毫无意义** | 5 MiB 的数据放 S3 顶不上任何东西。而且测试站已经配了 S3 / 移动云盘策略（策略 2 `s3`、策略 4/5/6 `y139`），用户文件本来就该落对象存储——**这个机制已经生效了**，不需要再挂一次 S3 |

一句话：**这台机器 82% 的 `/data` 里，58.6% 是可以随手清掉的构建缓存、33.3% 是别人没下完的剧集、0.03% 才是测试站数据。挂 S3 只能顶住那 0.03%，却要为其余 92% 的性能买单。**

### 5.3 如果一定要外置，技术的代价与边界（针对本机）

- **能外置、且划算的只有一类**：5212 的下载落地盘与测试站的用户文件——两者都已可用 cloudreve 自己的 S3 策略解决（测试站的策略 2/4/5/6 已指向对象存储），**不需要在 OS 层挂载**。
- **不要用 s3fs/rclone mount 挂到 `/data` 的子目录上当构建盘**：本机 4 vCPU，构建已经吃满 CPU；再叠一层 FUSE 会把 I/O 等待从毫秒级拉到几十毫秒，`go build` / `vite build` 的并行读写会互相放大，实测类场景常见的是 3–10 倍耗时，且一旦 InstaBox 网络抖动（该机本身有 30 分钟闲置挂起策略，网络更不保证），正在写的缓存会写出半截文件，反而制造脏缓存。
- **如果将来真的需要更大本地盘**，正确做法不是挂 S3，而是**在网站的资源页改机器规格（加盘/换大规格）**。本次排查无法从机器内部确认 InstaBox 的资源页是否提供「加盘」选项，也不做任何承诺——这需要用户在网站的机器资源页确认。
- **loopback 块设备 + S3 后端**（把 S3 当块设备）：本机根分区只剩 6.6 GiB，持久盘剩 3.6 GiB，要先在本地腾出块设备文件的空间，本末倒置；而且 4C 机器的 `io_uring` 队列深度不够撑高延迟块设备的吞吐。

## 6. 长期建议（可执行）

### 6.1 保留几个镜像：**3 个**

当前 6 个 → 保留 3 个，删除 3 个：

| 保留 | 作用 |
|---|---|
| `cloudreve-fork:admin-r7` | 当前部署（运行中） |
| `cloudreve-fork:admin-r6` | 上一版回滚（最贴近的回滚点） |
| `cloudreve-fork:easypay-1030-r2` | 功能回滚（易支付） |

删除：`mcr.microsoft.com/playwright:v1.49.1-jammy`（3.32 GB，无引用）、`cloudreve-fork:vas-1030-r3`（135 MB 净）、`debian:bookworm-slim`（2.4 MB 净，可重拉）。

**回滚约定的写法**（写进本仓库 `docs/` 的执行约定，比「记得别删」可靠）：每次部署只新增一个 `-rN` 标签并**同时**删除 `-r(N-2)`；`docker images --filter=reference='cloudreve-fork:*'` 的结果恒为 2–3 条。

### 6.2 构建完成即清（每次构建的最后一步，5 条命令）

在构建任务的收尾里固定执行（这属写操作，本次未执行，仅给出清单）：

```bash
# 1) 前端依赖与产物（可重建，git 忽略）
rm -rf /data/workspace/*/frontend/node_modules /data/workspace/*/frontend/build
# 2) 只留一份 yarn 缓存（两处副本择一）
rm -rf /data/workspace/.yarncache      # 依赖 /data/home/.cache/yarn
# 3) 只留一份 Go 缓存（择一）
rm -rf /data/workspace/.gocache        # 依赖 /data/home/.cache/go-build
# 4) 临时目录与日志
rm -rf /data/workspace/pwtest /data/workspace/probe2 /data/workspace/tmp /data/workspace/*.log
# 5) 收割构建产物后从工作区删掉（二进制 → 立即 push 分支 / 传 GHCR / scp 走）
```

**关键约定：构建产物（测试二进制 89 MB 级、`assets.zip`、镜像 tar）不要留在 `/data`，产出后立刻传走并且从工作区删除。** 历史上 99% 满卡上传就是二进制留在本机导致的。

**另外**：构建时用 `DAEMON=1` 之外不要并发跑多条前端构建——`node_modules` 与 yarn 缓存会被多个任务同时写，是「互相清盘」的根源。三个任务排队跑（用户已要求的「依次完成」）本身就是正确的调度。

### 6.3 要不要换盘 / 加盘

| 盘的现状 | 结论 |
|---|---|
| `/` 15.6 GiB、55% 已用，其中 4.0 GiB 是镜像层 | **够用**，但 `data-root` 的配置没把镜像搬走（见 §1），镜像仍吃系统盘。建议把 containerd 的 snapshotter 也指到 `/data`（`/etc/containerd/config.toml` 的 `root`），这样 `/` 只剩系统本体，腾出 4 GiB |
| `/data` 19.5 GiB、82% 已用 | 按 §4 推荐方案清完约 25%，**不必加盘**。若长期跑多任务并发构建，可用量下限建议守 20%（约 4 GiB） |

关于扩容：**机器规格只能在网站的机器资源页改**，本任务不承诺任何扩容。若确实需要更大持久盘，入口是 InstaBox 网站该机器的资源/规格页（本次未从机器内部确认是否存在加盘选项）。

### 6.4 设一条「磁盘守门」，避免再次满盘

把这项写进 `docs/` 的构建约定（建议每次构建任务开头/结尾各查一次）：

```bash
df -h / /data | awk 'NR>1 {gsub("%","",$5); if ($5+0 > 80) {print "DISK>80%: "$0; exit 1}}'
```

超过 80% 就先按 §4 的 C 类清单清，再开始构建；**永远不要用 `docker image prune -af`**（会清掉在用的和回滚的镜像，#40 的教训），也不要写「`find` 命中就 `rm -rf`」的通配脚本（#45 误删源码目录的事故）。

## 7. 一句话回答用户

- **总占用**：`/` 8.1 GiB / 15.6 GiB（55%）；`/data` 15.9 GiB / 19.5 GiB（82%）。
- **大头**：构建缓存与依赖 9.35 GiB（58.6%，两处重复的 yarn 缓存 + 两套 Go 缓存 + node_modules）、5212 从机的 aria2 未完成下载 5.31 GiB（33.3%）、测试站数据仅 5 MiB（0.03%）。
- **可释放**：保守 ≈ 7.3 GiB、推荐 ≈ 14.8 GiB（其中 8.4 GiB 需用户点头：5212 的剧集 + Playwright 镜像）；连那两项都不动、只清安全项也有 **≈ 8.1 GiB**。
- **挂 S3 的结论**：**解决不了这台机器的问题**。真正的增长源是构建缓存（60%），而缓存放 S3 只会让构建慢 3–10 倍、网络一断就失败；镜像层用 overlayfs 根本无法挂 S3。唯一适合对象存储的两块——测试站用户文件与 5212 的下载落地——**cloudreve 自己的存储策略已经能做**（测试站策略 2/4/5/6 已指向 S3 / 移动云盘），不需要在 OS 层挂载。要更大本地盘就改机器规格，别挂 S3。
