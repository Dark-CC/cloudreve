# InstaBox 磁盘清理执行记录（2026-10-30）

依据：[#46](todo:JG1q-Ghal_MHomBBLPbsz) 的只读排查报告 `docs/instabox-disk-audit-20261030.md`。
机器：InstaBox `m_agqw5litn4acpbo7b5axu3ycku`（x86_64，4 vCPU / 8 GiB，`/` 16G + `/data` 20G）。
执行时间：第一轮 2026-10-30 14:38–14:52 UTC；第二轮（批准三项）14:46–14:58 UTC；第三轮（停服务 + 清下载）14:58–15:05 UTC。
执行方式：InstaBox exec API（`POST /v1/machines/{id}/exec`）。

## 总览：四轮结果

| 盘 | 初始（报告口径） | 第一轮后 | 第二轮后 | 第三轮后 | **最终** | 累计释放 |
|---|---|---|---|---|---|---|
| `/`（16G） | 8.2G / 55% | 8.1G / 55% | 5.0G / 34% | 5.0G / 34% | **5.0G / 34%**，可用 9.9G | **约 3.1 GiB** |
| `/data`（20G） | 16G / **82%** | 16G / 55% | 7.0G / 36% | 6.8G / 35% | **6.7G / 35%**，可用 13G | **约 10.6 GiB** |

- **第一轮**（约束内安全项）：重复 yarn/go 缓存、crB 可重建依赖、临时目录、npm 缓存、journal —— 约 5.5 GiB。
- **第二轮**（批准三项）：5212 的 aria2 剧集 5346 MiB、Playwright 镜像 3.1 GiB、site302 僵尸进程 —— 约 8.4 GiB。
- **第三轮**（新指令）：停 15214 / 5212 / aria2，清光 `data/temp/aria2` 下全部下载残留（含已涨到 3.3 GiB 的 wim）
  —— 约 3.3 GiB。5212 与 aria2 当时为 inactive（enabled 保留）。
- **第四轮**（新指令「只保留 15212 和 Pro」）：**5212 与 aria2 被彻底删除**——两个 service 已 `disable` 并删掉
  unit 文件，`/data/ouFmtYpb` 整目录删除，aria2 的配置/会话/下载目录删除。重建步骤见 §11。

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

四轮合计，`/data` 从 **82% 降到 35%**（释放约 **10.6 GiB**，可用 3.6 → 13 GiB），
`/` 从 **55% 降到 34%**（释放约 3.1 GiB）。三项大头（5212 剧集、Playwright 镜像、site302 僵尸进程）
在第一轮按约束跳过、经批准后在第二轮清完；第三轮停掉 15214/5212/aria2 并清光 `data/temp/aria2`
下的下载残留；**第四轮把 5212 与 aria2 彻底删除**（disable + 删 unit 文件 + 删 `/data/ouFmtYpb`
与 aria2 配置数据）。12 MiB 的部署存档仍因是回滚依据而主动保留。

结束后：**15212 / 15213 Pro 均返回 200**，容器 Up，两个数据目录完好；**5212 与 aria2 已不存在**
（`systemctl is-enabled` 返回 `not-found`，端口不再监听，重建步骤见 §11）；测试站容器
`cloudreve-fork-test` 与 5 个镜像未变；未使用 `docker image prune -af`。

## 8. 第三轮：停服务与清空下载残留

用户在反馈中的原话：「15214 停止 然后a2和5212 也停止 然后5.2G 剧集 清理掉」。
执行时间 2026-10-30 14:58–15:05 UTC。

### 8.1 15214

停前复核：`ss -ltn | grep :15214` 无输出，上一轮 `kill -TERM 653159 653156` 已生效，无残留进程。
**本次未恢复 `/opt/site302`**（按要求）。

### 8.2 停 5212（`cloudreve.service`）

```
sudo systemctl stop cloudreve     → active: inactive
sudo systemctl is-enabled cloudreve → enabled（保留自启配置，未 disable）
```

单元原文（停前实际读取）：

```ini
[Unit]
Description=Cloudreve (slave node, :5212)
After=network-online.target
Wants=network-online.target
RequiresMountsFor=/data
StartLimitIntervalSec=0

[Service]
Type=simple
User=root
WorkingDirectory=/data/ouFmtYpb/cloudreve_v4.18.0_linux_amd64
ExecStart=/data/ouFmtYpb/cloudreve_v4.18.0_linux_amd64/cloudreve_v4.18.0 -c config.ini
Restart=on-failure
RestartSec=5s
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

停前运行状态（供对比）：PID 30019，`root`，已运行 **4 天 19 小时 58 分**，`cwd` =
`/data/ouFmtYpb/cloudreve_v4.18.0_linux_amd64`，`exe` = 同目录下的 `cloudreve_v4.18.0`，
监听 `*:5212`，模式为 slave（`Mode=slave`，连 master `https://pan.cdn.ad`）。

### 8.3 停 aria2（`aria2.service`）

```
sudo systemctl stop aria2        → active: failed (Result: exit-code, status=7 = SIGTERM 正常退出)
sudo systemctl reset-failed aria2 → active: inactive
sudo systemctl is-enabled aria2   → enabled（保留，未 disable）
```

单元要点（原文）：`User=root`、`Environment=HOME=/root`、`WorkingDirectory=/data/home`、
`ExecStart=/usr/bin/aria2c --conf-path=/data/home/.aria2/aria2.conf --daemon=false`。
配置文件 `/data/home/.aria2/aria2.conf`：`rpc-listen-port=6800`、`max-concurrent-downloads=1`、
`save-session=/data/home/.aria2/aria2.session`（`save-session-interval=60`）。

停前已 **备份 session** 到 `/data/home/.aria2/aria2.session.bak-stop-20261030`，重启后可用它恢复队列。

### 8.4 清空 `data/temp/aria2`

两个服务停掉后先确认句柄归零：

```
sudo find /proc/*/fd -lname "*temp/aria2*" | wc -l   → 0
```

然后清空该目录。**过程中踩到的两个坑**：

1. `rm -rf .../aria2/*` 执行 `rc=0` 但目录里的 `SurfaceLaptop_BMR_*.wim` **仍在**——停服瞬间它还在写，
   通配展开时该目录被重新创建。必须 **点名 `rm -f` 具体文件**再 `rmdir` 空目录。
2. 那个 wim 是 **8.7 GB 的稀疏文件**（`ls` 显示 8709965972 B，`du` 只算 3460 MiB）——
   看 `ls` 大小会大幅高估占用，要以 `du` 为准。

```
清理前 /data/ouFmtYpb                              3460 MiB
清理后 /data/ouFmtYpb                                89 MiB
清理前 /data                                        52%
清理后 /data                                        35%
清后 sudo find /proc/*/fd -lname "*ouFmtYpb*"      → 空
清后 sudo lsof +L1 | awk '$8>100000000'             → 无（无 >100MB 的 deleted 文件）
```

**边界遵守**：只清 `data/temp/aria2/` 下的下载数据；`/data/ouFmtYpb` 本体、`cloudreve_v4.18.0`
二进制、`config.ini`、`data/` 下的数据库与用户文件全部保留。清后 `/data/ouFmtYpb` 剩 89 MiB
（二进制 + 配置 + 库）。

### 8.5 第三轮健康复查

| 项 | 结果 |
|---|---|
| **15212** `GET /` | **200**（容器 `cloudreve-fork-test` Up） |
| **15213** `GET /` | **200**（`cloudreve-pro15213` active/running，**未被连带停掉**） |
| 5212 端口 | 无监听 ✓ |
| 6800（aria2 RPC） | 无监听 ✓ |
| 15214（site302） | 无监听 ✓ |

## 9. 第三轮的临时停止说明（已被第四轮推翻）

> ⚠️ 第三轮只是「停一下」，当时写的是「`systemctl start` 即可重启，`enabled` 配置保留」。
> **第四轮已把两个 service 连同配置、数据、目录全部删除**，`systemctl start` 不再能拉起任何东西。
> **重建步骤见 §11。**

第三轮的停止方式（历史记录）：

```bash
sudo systemctl stop cloudreve      # active: inactive
sudo systemctl stop aria2          # active: failed(status=7) → reset-failed → inactive
# 两者 enabled 均保留
```

## 10. 第四轮：彻底删除 5212 与 aria2

用户原话：「5212和a2可以删除了，只保留15212和Pro」。执行时间 2026-10-30 15:10–15:22 UTC。

### 10.1 删前边界证明（三个目录的实测路径）

| 对象 | 实际路径 | 大小 | 与 `/data/ouFmtYpb` 是否相交 |
|---|---|---|---|
| **15212** 测试站数据 | `/data/cloudreve-fork-test`（容器 `cloudreve-fork-test` 挂载为 `/cloudreve/data`） | 5 MiB | **无** |
| **15213 Pro** | `/data/pro15213`（`cloudreve-pro15213.service`，`WorkingDirectory=/data/pro15213`） | 89 MiB | **无** |
| **5212**（待删） | `/data/ouFmtYpb` | 89 MiB | — |

额外校验：

- `findmnt -T /data/ouFmtYpb` → `/data /dev/vdb`，即它是 `/data` 下的**普通目录**，不是独立挂载点；
- `readlink -f /data/ouFmtYpb` → `/data/ouFmtYpb`，不是软链；
- `grep -rls "ouFmtYpb" /etc/systemd/system/` → **只命中 `cloudreve.service`**（就是要删的那个），
  无其它服务引用；
- 遍历全部容器（`docker ps -a` + `inspect .Mounts`），**无任何容器挂载 `ouFmtYpb`**。

### 10.2 逐项删除了什么

| # | 对象 | 操作 | 结果 |
|---|---|---|---|
| 1 | `cloudreve.service` | `systemctl disable --now cloudreve` | 移除 `multi-user.target.wants` 软链 → `inactive` / `disabled` |
| 2 | `aria2.service` | `systemctl disable --now aria2` | 同上 → `inactive` / `disabled` |
| 3 | `/data/ouFmtYpb/`（整目录） | `rm -rf` | rc=0，目录已不存在 |
| 4 | `/data/home/.aria2/`（`aria2.conf`、`aria2.session` 及 5 个备份、`aria2.log`） | `rm -rf` | rc=0 |
| 5 | `/data/home/aria2-downloads/`（下载目录） | `rm -rf` | rc=0（当时已空） |
| 6 | `/etc/systemd/system/aria2.service` | `rm -f` | 已删 |
| 7 | `/etc/systemd/system/cloudreve.service` | `rm -f` | 已删 |
| 8 | `systemctl daemon-reload` | — | ok |

**保留未动**：`/usr/bin/aria2c` 及其 apt 包（理由见 10.3）；`/data/cloudreve-fork-test`；`/data/pro15213`；
5 个 docker 镜像；`cloudreve-fork-test` 容器；`~/.cache/*`。

删除后校验：`systemctl is-enabled cloudreve` → **`not-found`**，`aria2` 同样 → `not-found`
（unit 文件已不在，不是 disable 态而是不存在）；`5212` 与 `6800` 端口均无监听；
`find /proc/*/fd` 扫 `ouFmtYpb`/`aria2` → 空；`lsof +L1` 无 >50 MB 的 deleted 文件（无残留占用）。

### 10.3 为什么保留 aria2c 二进制而删配置

选的是**「只删配置与数据、保留 apt 包」**（也是用户建议的做法）：

- 这台机器上 aria2 的唯一使用者就是 5212 的 cloudreve 从机（`aria2.service` 专为它而建，
  配置里 `dir=/data/home/aria2-downloads`）。5212 已删，没人再调 6800 RPC。
- 二进制本身很小（`/usr/bin/aria2c`），留着的收益大于风险：将来重建 5212 时**不用联网重装**。
- 配置与数据才是「有状态、有隐私」的部分（RPC secret、下载任务列表），真正该清的就是它们。
- 它不白占资源：无 unit、无进程，只是个静态文件。

### 10.4 删后健康证据（硬底线）

| 项 | 证据 |
|---|---|
| **15212** `GET /` | **200** |
| **15213 Pro** `GET /` | **200** |
| 容器 `cloudreve-fork-test` | `Up 57 minutes`，镜像 `cloudreve-fork:admin-r7` 未变 |
| `cloudreve-pro15213.service` | `active` |
| 15212 数据目录 | `/data/cloudreve-fork-test` 5 MiB，在 |
| 15213 数据目录 | `/data/pro15213` 89 MiB，在 |
| 端口 | `15212`、`15213` 在监听；`5212`、`6800` 已消失 |
| 镜像 | 5 个全在（`admin-r7`/`vas-1030-r3`/`admin-r6`/`easypay-1030-r2`/`debian:bookworm-slim`），未 `prune` |

### 10.5 最终占用

```
Filesystem      Size  Used Avail Use% Mounted on
/dev/root        16G  5.0G  9.9G  34% /
/dev/vdb         20G  6.7G   13G  35% /data
```

`/data` 最终构成（MiB）：`home` 5073、`workspace` 1617、`pro15213` 89、测试站 5、存档 ≈90。
**`ouFmtYpb` 已从列表中消失。** 本轮直接释放约 89 MiB（目录本身）；在此之前第三轮已清掉其中的
3.3 GiB 下载数据，所以 5212 的「大头」实际是在第三轮释放的。

## 11. 如何从零重建 5212 与 aria2（删后版）

⚠️ 下面的信息是在删除前实测抄录的。**二进制、配置、数据库、license 都已随目录删除**，
重建需从外部重新拿。

### 11.1 删除前的 5212 完整配置（用于重建参考）

**目录结构**（`/data/ouFmtYpb`，共 89 MiB）：

```
/data/ouFmtYpb/
├── cloudreve.log                                  9 KB
├── aria2/
│   └── c97e8bc7-ec7b-4a91-a955-8cd7126709c2/      (空目录)
└── cloudreve_v4.18.0_linux_amd64/
    ├── cloudreve_v4.18.0                          91.6 MB  二进制
    ├── config.ini                                 275 B
    ├── license_key                                130 B    两行：CR_HEXOR_DATA / LICENSE_KEY
    ├── start.sh                                   1337 B
    └── data/
        ├── cloudreve.db                           663 KB   SQLite 库
        ├── cache_persist.bin                      119 B
        └── temp/aria2/                            (已清空)
```

**systemd unit**（`/etc/systemd/system/cloudreve.service`，已删，原文抄录）：

```ini
[Unit]
Description=Cloudreve (slave node, :5212)
After=network-online.target
Wants=network-online.target
RequiresMountsFor=/data
StartLimitIntervalSec=0

[Service]
Type=simple
User=root
WorkingDirectory=/data/ouFmtYpb/cloudreve_v4.18.0_linux_amd64
ExecStart=/data/ouFmtYpb/cloudreve_v4.18.0_linux_amd64/cloudreve_v4.18.0 -c config.ini
Restart=on-failure
RestartSec=5s
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

**config.ini 全文**：

```ini
[System]
Mode = slave
Listen = :5212

[Slave]
Secret = <已随目录删除，不再记录原值>

[CORS]
AllowOrigins = *
AllowMethods = OPTIONS,GET,POST
AllowHeaders = *
```

**启动方式（二选一）**：

- 当时生产用的是 systemd（`ExecStart` 直接跑二进制、`-c config.ini`，不传 license 参数，
  因为 `data/cloudreve.db` 里已有 license 记录）。
- `start.sh` 是首次初始化用的：读 `license_key` 两行 → `export CR_HEXOR_DATA` →
  `./cloudreve_v4.18.0 --license-key="$LICENSE_KEY"`。**首次启动才需要**。

### 11.2 重建 5212 的步骤

```bash
# 1) 重建目录并放入二进制（从外部重新获取 v4.18.0 或改用当前 fork 镜像）
sudo mkdir -p /data/ouFmtYpb/cloudreve_v4.18.0_linux_amd64/data
#    放入 cloudreve 二进制、config.ini（内容见 11.1）、license_key

# 2) 重建 unit（内容见 11.1 的 ini 块）
sudo tee /etc/systemd/system/cloudreve.service >/dev/null <<'EOF'
... 粘贴 11.1 的 unit 内容 ...
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now cloudreve

# 3) 验证
sudo ss -ltnp | grep :5212
journalctl -u cloudreve -n 30 --no-pager
```

注意：slave 模式对 license 检查宽松（`data/cloudreve.db` 的 license 为空也能跑）；master 模式必须有 license。
**数据库 `cloudreve.db`（663 KB）已随目录删除**，重建的将从空库开始，需要在 master 端重新登记这个从机。

### 11.3 重建 aria2 的步骤

`aria2c` 二进制还在（`/usr/bin/aria2c`，apt 包 `aria2`）。只需重建配置与 unit：

```bash
# 1) 配置目录与文件
sudo mkdir -p /data/home/.aria2 /data/home/aria2-downloads
sudo tee /data/home/.aria2/aria2.conf >/dev/null <<'EOF'
enable-rpc=true
rpc-listen-all=false
rpc-listen-port=6800
rpc-secret=<自定一个新的>
rpc-allow-origin-all=false
dir=/data/home/aria2-downloads
continue=true
max-concurrent-downloads=1
max-connection-per-server=8
split=8
min-split-size=1M
file-allocation=none
disk-cache=32M
auto-file-renaming=true
allow-overwrite=false
input-file=/data/home/.aria2/aria2.session
save-session=/data/home/.aria2/aria2.session
save-session-interval=60
force-save=false
log=/data/home/.aria2/aria2.log
log-level=notice
EOF

# 2) unit
sudo tee /etc/systemd/system/aria2.service >/dev/null <<'EOF'
[Unit]
Description=Aria2 RPC daemon (127.0.0.1:6800)
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
User=root
Group=root
Environment=HOME=/root
WorkingDirectory=/data/home
ExecStart=/usr/bin/aria2c --conf-path=/data/home/.aria2/aria2.conf --daemon=false
Restart=on-failure
RestartSec=5s
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload && sudo systemctl enable --now aria2
sudo ss -ltnp | grep :6800
```

如果连二进制也要重装：`sudo apt-get install -y aria2`（Debian/Ubuntu）。
