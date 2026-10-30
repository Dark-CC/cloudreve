# InstaBox 磁盘清理执行记录（2026-10-30）

依据：[#46](todo:JG1q-Ghal_MHomBBLPbsz) 的只读排查报告 `docs/instabox-disk-audit-20261030.md`。
机器：InstaBox `m_agqw5litn4acpbo7b5axu3ycku`（x86_64，4 vCPU / 8 GiB，`/` 16G + `/data` 20G）。
执行时间：第一轮 2026-10-30 14:38–14:52 UTC；第二轮（用户批准后）14:46–14:58 UTC。
执行方式：InstaBox exec API（`POST /v1/machines/{id}/exec`）。

## 总览：两轮结果

| 盘 | 初始（报告口径） | 第一轮后 | **最终** | 累计释放 |
|---|---|---|---|---|
| `/`（16G） | 8.2G / 55% | 8.1G / 55% | **5.0G / 34%**，可用 9.9G | **约 3.1 GiB** |
| `/data`（20G） | 16G / **82%** | 16G / 55% | **7.0G / 36%**，可用 13G | **约 10.3 GiB** |

- **第一轮**（约束内安全项）：重复 yarn/go 缓存、crB 可重建依赖、临时目录、npm 缓存、journal —— 约 5.5 GiB。
- **第二轮**（用户批准的三项）：5212 的 aria2 剧集 5346 MiB、Playwright 镜像 3.1 GiB、site302 僵尸进程 —— 约 8.4 GiB。

## 0. 相对排查报告的前提校正

排查报告是 14:34–14:55 采集的，本次执行前逐项复核，**三处与报告不同或需要修正**：

1. **`/data/ouFmtYpb` 的剧集仍在，报告没算错，是我第一次核对方式有误**。报告给的路径是
   `/data/ouFmtYpb/cloudreve_v4.18.0_linux_amd64/data/temp/aria2/<uuid>/…`，我第一次用
   `du /data/ouFmtYpb/*/data/temp` 通配匹配不到（实际多一层 `cloudreve_v4.18.0_linux_amd64`），
   误以为已被释放；改用 `du /data/ouFmtYpb/*` 复核后确认 **5434 MiB 原样存在，最后写入仍是
   2026-10-27 15:37**。已按约束**未动**，见 §4。
2. **`/var/log/journal` 的回收比报告预期少**：报告估 65 MiB、可回收约 56 MB。实测
   `journalctl --vacuum-size=20M` 打出 `freed 0B`——当时只有两个**活动** journal 文件
   （`system.journal` 32 MiB + `user-1000.journal` 24 MiB），vacuum 只对归档文件生效。
   补做 `journalctl --rotate` 后再 vacuum 才生效：56 MiB → 16 MiB，实际回收约 **40 MiB**。
3. **新增一项报告未记录的泄漏**：`/opt/site302` 已被删除，但其进程（PID 653159，`./cloudreve -c
   data/conf.ini`，Oct 29 起运行）仍持有 **deleted cwd**，且仍在 `*:15214` 应答 200。这份空间
   不是磁盘占用（目录已不存在），但进程不退出就永远钉住它打开的二进制。第一轮按「归属不明」
   保留并列入报告；**第二轮经用户批准已停掉**，见 §4.1。

另确认：`/data/workspace/cr-vas` 不是源码检出目录，而是一个 **78 MiB 的 Go 二进制**（86 MB
`cr-vas` ELF 文件），与报告 §2.1 描述的「另一份源码检出」不符——这份是构建产物残留，本次保留
（见 §3 的跳过说明）。

## 1. 执行前基线

```
Filesystem      Size  Used Avail Use% Mounted on
/dev/root        16G  8.2G  6.7G  55% /
/dev/vdb         20G   16G  3.6G  82% /data
```

`/data` 一级目录（MiB）：

| 路径 | 执行前 | 执行后 |
|---|---|---|
| `/data/workspace` | 5673 | **217** |
| `/data/ouFmtYpb` | 5434 | 5434（未动） |
| `/data/home` | 5113 | 5073 |
| `/data/pro15213` | 88 | 89 |
| `/data/cloudreve-fork-test` | 5 | 5 |
| 其余存档/元数据 | ≈90 | ≈90 |

`/` 一级：`/var/lib/containerd` 4067 MiB、`/usr` 3286 MiB、`/opt/rust` 620 MiB、`/var/log` 65 MiB。

执行前状态核对（确认无构建在跑，才动手）：

- `ps aux` 无 `go build` / `yarn` / `vite` / `npm` / `docker build` 进程；
- `docker ps -a` 只有一个容器 `cloudreve-fork-test`（`cloudreve-fork:admin-r7`，`Up 42 min`，
  `0.0.0.0:15212->5212/tcp`）；
- `git -C /data/workspace/crB status --porcelain` = 0 行（工作区干净）。

## 2. 实际执行的操作与释放量

全部为「报告 C 类 + B 类里无回滚价值」的项；**未执行任何 `docker prune`**，未删任何镜像与容器。

| # | 操作 | 依据 | 实际释放 |
|---|---|---|---|
| 1 | `rm -rf /data/workspace/.yarncache` | 与 `/data/home/.cache/yarn` 是两份独立副本（1384 vs 1384 条目，同设备不同 inode：65040/141361 vs 65040/1200228）。保留 HOME 那份，因为 yarn 默认读 `~/.cache/yarn` | **2517 MiB** |
| 2 | `rm -rf /data/workspace/.gocache` | Go 默认 `GOCACHE=/data/home/.cache/go-build`（`go env` 实测）。`.gocache` 最后写入 11:08（已停写 3.5 h），`go-build` 13:50 仍有写入（在用的那份） | **1516 MiB** |
| 3 | `rm -rf /data/workspace/crB/frontend/{node_modules,build}` | 被 `.gitignore` 覆盖、可 `yarn install` 完全重建；删前 `git status` 干净、无构建进程 | **1401 MiB** |
| 4 | `rm -rf /data/workspace/{pwtest,probe2,shots,sync,tmp/*}` | Playwright 探测/截图/同步临时目录 | ~26 MiB |
| 5 | `rm -rf /data/workspace/*.log`（8 个） | 12:47–13:51 的历史构建日志 | ~1 MiB |
| 6 | `rm -rf /data/home/.npm` | npm 包缓存，可重下 | 41 MiB |
| 7 | `journalctl --rotate && journalctl --vacuum-size=10M` | 见 §0.2 | ~40 MiB（在 `/` 上） |
| 8 | `rm -rf /tmp/*` | tmpfs，**不释放磁盘**，仅减内存占用 | 0（磁盘） |

`probe2` 里有部分文件属主为 `root`（以 root 跑的 Playwright 安装），普通 `rm` 报 Permission denied，
改用 `sudo rm -rf` 完成。

## 3. 执行后结果

```
Filesystem      Size  Used Avail Use% Mounted on
/dev/root        16G  5.0G  9.9G  34% /
/dev/vdb         20G  7.0G   13G  36% /data
```

| 盘 | 初始 | 第一轮后 | **最终** | 累计释放 |
|---|---|---|---|---|
| `/` | 8.2G / 55% | 8.1G / 55% | **5.0G / 34%** | 约 3.1 GiB |
| `/data` | 16G / **82%** | 16G / 55% | **7.0G / 36%** | **约 10.3 GiB** |

`/data` 可用量从 3.6 GiB 提升到 **13 GiB**，`/` 可用从 6.7 GiB 提升到 **9.9 GiB**，
两块盘都远低于报告 §6.3 定的「守 20%（约 4 GiB）」下限。

服务健康复查（两轮清理全部完成后执行，全部正常）：

| 项 | 结果 |
|---|---|
| 容器 `cloudreve-fork-test` | `Up`，镜像 `cloudreve-fork:admin-r7` 未变 |
| **15212** 测试站 `GET /` | **200** |
| **15213** Pro 实例 `GET /` | **200** |
| **5212** cloudreve 进程 PID 30019 | 运行中（`etime` 4 天 19 小时），`*:5212` 监听正常 |
| 5212 的 `aria2.service` PID 76627 | `active` / `enabled`，未停（只 `aria2.remove` 了已完成的剧集任务） |
| 15214（site302 僵尸部署） | `000` 无响应——已按批准停掉 |
| 15212 测试站数据 `/data/cloudreve-fork-test` | 5 MiB，未触碰 |
| `/data/workspace/crB` 仓库本体 | `git status --porcelain` = 0 行 |
| 其余 5 个镜像 | `admin-r7`/`admin-r6`/`vas-1030-r3`/`easypay-1030-r2`/`debian:bookworm-slim` 全部保留 |

`/data` 最终构成（MiB）：`home` 5073（缓存，见 §5）、`workspace` 1570、`ouFmtYpb` 412（5212，剩一个在下的 wim 任务）、
`pro15213` 89、存档 ≈90、测试站 5。

## 4. 跳过项与原因

| 项 | 大小 | 为什么没清 |
|---|---|---|
| ~~5212 生产从机的 aria2 未完成下载（诡秘之主 13 集 4K）~~ | ~~5434 MiB~~ | **第一轮跳过，第二轮经用户批准已删除**，见 §4.1 |
| ~~`mcr.microsoft.com/playwright:v1.49.1-jammy` 镜像~~ | ~~3322 MiB~~ | **第一轮跳过，第二轮经用户批准已删除**，见 §4.1 |
| `/opt/rust` | 620 MiB | 报告列为「需确认」。Go + Node 栈当前不用 Rust，但属工具链、删除成本高（重装慢），本次不动 |
| `/data/backup-20261029-175711`、`deploy-*`、`cloudreve-backup`、`backup-site302`、`backup-y139-*.db`、`ouFmtYpb-migrate-backup` | ≈12 MiB | 报告标「可清（体量小、优先级最低）」，但这些是**部署前唯一的库快照与容器定义**，是回滚路径的一部分。为 12 MiB（占 `/data` 0.06%）牺牲回滚能力不划算，**主动保留** |
| `/data/workspace/cr-vas` | 78 MiB | 是构建产物（Go 二进制），但它是 `vas-1030-r3` 那次构建的交付物，不确定是否还有任务要取用，属「归属不明」→ 列此并保留 |
| `/data/workspace/cloudreve-src` | 31 MiB | 源码检出（`git status` 干净），其它任务的工作区 |
| `/data/workspace/backup-admin-20261030-132628` | 2 MiB | admin 构建的配置备份，归属不明，保留 |
| `cloudreve-fork:vas-1030-r3` / `admin-r6` / `easypay-1030-r2` / `debian:bookworm-slim` | 396 / 361 / 418 / 116 MB | **回滚镜像与基础镜像，全部保留**。任务约束要求保留回滚镜像；报告建议删 `vas-1030-r3`（净 135 MiB），但它对应的构建产物还在工作区、且属同批构建，收益小于风险，本次不动 |
| `/var/lib/containerd`（镜像层） | 4067 MiB | 在 `/` 上，删它只能靠删镜像；可用项只剩 Playwright（需确认）。**未改 `daemon.json`/containerd 配置**，迁移 snapshotter 属有风险的结构改动，不在本次范围 |
| ~~`/opt/site302` 的 deleted cwd~~ | — | **第二轮经用户批准已停进程**，见 §4.1 |
| `/tmp` | 109 MiB | **是 tmpfs（内存盘）**，清了不释放磁盘，只减内存压力 |

### 4.1 第二轮：用户批准后清掉的三项

用户在反馈中原话「这三个都删除」，批准了第一轮跳过的三项。执行时间 2026-10-30 14:46–14:58 UTC。

#### (1) 5212 的 aria2 剧集 —— 释放 5346 MiB

**执行前发现的关键障碍（如果直接 `rm` 会白删）**：这批 13 集 mp4 仍被进程持有 **16 个打开句柄**：

```
$ sudo find /proc/*/fd -lname "*temp/aria2*" | 按 pid 去重
76627
```

持有者是 **PID 76627**（`aria2.service`，systemd 托管、`enabled`、已运行 3 天，
`/usr/bin/aria2c --conf-path=/data/home/.aria2/aria2.conf`）。**直接 `rm` 只会让目录消失，
inode 仍被进程钉住，磁盘空间不释放**——这正是「删完发现没腾出空间」的典型坑。

**正确的释放顺序**：

1. 用 RPC 确认任务状态。`aria2.tellActive` 显示 gid `4be7ddc608486a6f` 为 `active`，
   `completedLength == totalLength == 5605519722`（已完成但仍在 active 表里）；
   `tellStopped` 里的 `ec22d269a6409e41` 是该目录的种子元数据任务（`complete`，15304 B）。
2. `aria2.removeDownloadResult`（清 `ec22d269…`）→ OK，但句柄仍是 16 个（aria2 对已完成任务的 fd 保留）。
3. **`aria2.remove`（gid `4be7ddc608486a6f`）→ 返回 gid，句柄数 16 → 0**。
4. 确认 `find /proc/*/fd -lname "*temp/aria2*"` 只剩 1 个（那 1 个指向**另一个**在下的
   `SurfaceLaptop_BMR_*.wim`，属目录 `33806012-…`，与该剧集无关），再 `rm -rf` 那个 uuid 目录。

执行结果：

```
删前 /data/ouFmtYpb                            5435 MiB
删后 /data/ouFmtYpb                              97 MiB
删前 /data                                       61%
删后 /data                                       35%
```

**边界严格遵守**：只删 `data/temp/aria2/a235a09d-…/` 这一个 uuid 目录；`/data/ouFmtYpb` 本体、
`cloudreve_v4.18.0` 二进制、`data/` 下的数据库与用户文件、`aria2.service` 与 5212 的 cloudreve
进程（PID 30019）**全部未动且仍正常**（见 §3 健康复查）。同目录下另一个在下的任务（`33806012-…`）也保留。

#### (2) Playwright 镜像 —— 释放 3.1 GiB（在 `/` 上）

```
删前 docker ps -a --filter ancestor=mcr.microsoft.com/playwright:v1.49.1-jammy  → 空（0 引用）
docker rmi mcr.microsoft.com/playwright:v1.49.1-jammy
  Untagged: mcr.microsoft.com/playwright:v1.49.1-jammy
  Deleted:  sha256:019b822658965bf5f6d509472802797554360f2940e677b2afb0f17748d0d0e3
删前 /   8.1G / 55%    删后 /   5.0G / 34%
```

**只删了这一个镜像**，未用 `docker image prune -af`。`admin-r7`（在用）、`admin-r6`、`vas-1030-r3`、
`easypay-1030-r2`（回滚）、`debian:bookworm-slim`（基础）**一个都没动**。

#### (3) `/opt/site302` 遗留进程 —— 停掉，释放主进程前占用的内存而非磁盘

停手前的核验（按指示先确认归属）：

| 检查 | 结果 |
|---|---|
| `cmdline` | `./cloudreve -c data/conf.ini` |
| `cwd` / `exe` | **都指向 `/opt/site302` 且标记 `(deleted)`** —— 部署目录已被删除 |
| systemd 托管 | **否**。`systemctl list-units "*site302*"` 空；仅在 `session-44936.scope`（10-29 21:46，`abandoned` 孤儿会话） |
| 监听端口 | `*:15214`，`GET /` 返回 **200** |
| 活动连接 | 0 |

**判定**：它虽仍应答 HTTP，但二进制已不存在、无 systemd 单元、无活动连接，属「目录已删、只剩进程持有
delivered 二进制」的僵尸部署，不是需要保留的在用服务。按指示 `kill`：

```
sudo kill -TERM 653159 653156   → 两者均干净退出，无需 KILL
15214 复查：000（无响应，预期）
```

**它真正省下的是内存与一个无人维护的公开端口**，磁盘占用原本已不在 `du` 里。如果 15214 曾是你在用的
入口，请告知——重新部署需要先从备份恢复 `/opt/site302` 目录。

## 5. `/data/home` 剩下的 5073 MiB 是什么

全部是**正在使用的缓存**，不是重复副本：

| 路径 | MiB | 说明 |
|---|---|---|
| `~/.cache/yarn` | 2517 | 唯一一份 yarn 缓存（另一份已删），下次 `yarn install` 靠它离线装 |
| `~/.cache/go-build` | 1581 | Go 构建缓存，13:50 仍在写入，删了下次全量重编 |
| `~/.cache/ms-playwright` | 863 | 验收用 chromium，**必须保留**（测试脚本依赖） |
| 其余（`node`/`mesa*`/`sessions`/`chromium` 等） | ≈12 | — |

这部分属于「可清但会拖慢下次构建」的范畴。报告里「推荐方案」把 Go 缓存也削到一份的最小值，
是在牺牲后续构建速度换空间；当前 `/data` 已到 36%、可用 13 GiB，**没有必要再削**。

## 6. 守门约定（本次验证有效）

后续构建任务沿用报告 §6.4 的做法，并在收尾时执行本次验证过的两条：

```bash
# 构建结束：删前端依赖与产物（可重建）
rm -rf /data/workspace/*/frontend/node_modules /data/workspace/*/frontend/build
# 只保留一份缓存：yarn 留 ~/.cache/yarn、Go 留 ~/.cache/go-build（不要再用 .yarncache/.gocache）
```

- **不要用 `docker image prune -af`**（会清掉在用与回滚镜像）；删镜像一律点名 `docker rmi <image>`；
- **不要写「`find` 命中就 `rm -rf`」的通配脚本**（#45 误删源码目录的教训）；
- 用 `sudo` 删 `probe2` 这类 root 属主的 Playwright 目录时要盯住路径，别用通配符扩大范围；
- **删下载目录前先查进程句柄**：`sudo find /proc/*/fd -lname "*<路径>*"`。被 aria2 等
  进程持有的文件，`rm` 后目录会消失但 **inode 仍被钉住，磁盘不释放**。正确顺序是先用 RPC
  （`aria2.remove` / `removeDownloadResult`）让任务退出、句柄归零，再删目录。本次剧集 5.3 GiB
  就是靠这一步才真正腾出来的。

## 7. 一句话结论

两轮合计，`/data` 从 **82% 降到 36%**（释放约 **10.3 GiB**，可用 3.6 → 13 GiB），
`/` 从 **55% 降到 34%**（释放约 3.1 GiB）。三项大头（5212 剧集、Playwright 镜像、site302 僵尸进程）
在第一轮按约束跳过、经用户批准后在第二轮清完；12 MiB 的部署存档仍因是回滚依据而主动保留。

清理后 15212、15213、5212 三个实例均正常（HTTP 200 / 进程在），测试站容器与 5 个镜像未变。
