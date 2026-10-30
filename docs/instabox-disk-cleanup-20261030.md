# InstaBox 磁盘清理执行记录（2026-10-30）

依据：[#46](todo:JG1q-Ghal_MHomBBLPbsz) 的只读排查报告 `docs/instabox-disk-audit-20261030.md`。
机器：InstaBox `m_agqw5litn4acpbo7b5axu3ycku`（x86_64，4 vCPU / 8 GiB，`/` 16G + `/data` 20G）。
执行时间：2026-10-30 14:38–14:52 UTC。
执行方式：InstaBox exec API（`POST /v1/machines/{id}/exec`）。

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
   data/conf.ini`，Oct 29 起运行）仍持有 **deleted cwd**。这份空间不是磁盘占用（目录已不存在），
   但进程不退出就永远不释放它执行中的句柄；属 site302 历史部署的遗留服务，**本次不动**，
   记录待用户决定是否停掉该进程。

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
/dev/root        16G  8.1G  6.7G  55% /
/dev/vdb         20G   11G  9.0G 55% /data
```

| 盘 | 执行前 | 执行后 | 变化 |
|---|---|---|---|
| `/` | 8.2G / 55% | 8.1G / 55% | −约 40 MiB（journal） |
| `/data` | 16G / **82%** | 11G / **55%** | **−约 5.0 GiB** |

`/data` 可用量从 3.6 GiB 提升到 **9.0 GiB**，越过报告 §6.3 定的「守 20%（约 4 GiB）」下限。

服务健康复查（清理后立即执行，全部正常）：

| 项 | 结果 |
|---|---|
| 容器 `cloudreve-fork-test` | `Up`，镜像 `cloudreve-fork:admin-r7` 未变 |
| 15212 测试站 `GET /` | **200** |
| 15213 Pro 实例 `GET /` | **200** |
| 5212 生产从机进程 PID 30019 | 运行中，未触碰 |
| 15212 测试站数据 `/data/cloudreve-fork-test` | 5 MiB，未触碰 |
| `/data/workspace/crB` 仓库本体 | `git status --porcelain` = 0 行 |

`/data` 剩余构成（MiB）：`ouFmtYpb` 5434（33%，属 5212）、`home` 5073（缓存，见 §5）、
`workspace` 217、`pro15213` 89、存档 ≈90、测试站 5。

## 4. 跳过项与原因

| 项 | 大小 | 为什么没清 |
|---|---|---|
| **5212 生产从机的 aria2 未完成下载**（诡秘之主 13 集 4K） | **5434 MiB** | 属 **5212 生产从机数据**，按任务约束「不动 5212」；且这是用户尚未确认的两项之一。清掉后 aria2 任务需从头重下 |
| **`mcr.microsoft.com/playwright:v1.49.1-jammy` 镜像** | **3322 MiB** | 用户尚未确认。已核：`docker ps -a --filter ancestor=…` 为空（0 引用）；本机验收脚本走 `~/.cache/ms-playwright`（chromium-1148 已在），不依赖该镜像。删了以后若要容器内跑 Playwright 需重拉 3.3 GB |
| `/opt/rust` | 620 MiB | 报告列为「需确认」。Go + Node 栈当前不用 Rust，但属工具链、删除成本高（重装慢），本次不动 |
| `/data/backup-20261029-175711`、`deploy-*`、`cloudreve-backup`、`backup-site302`、`backup-y139-*.db`、`ouFmtYpb-migrate-backup` | ≈12 MiB | 报告标「可清（体量小、优先级最低）」，但这些是**部署前唯一的库快照与容器定义**，是回滚路径的一部分。为 12 MiB（占 `/data` 0.06%）牺牲回滚能力不划算，**主动保留** |
| `/data/workspace/cr-vas` | 78 MiB | 是构建产物（Go 二进制），但它是 `vas-1030-r3` 那次构建的交付物，不确定是否还有任务要取用，属「归属不明」→ 列此并保留 |
| `/data/workspace/cloudreve-src` | 31 MiB | 源码检出（`git status` 干净），其它任务的工作区 |
| `/data/workspace/backup-admin-20261030-132628` | 2 MiB | admin 构建的配置备份，归属不明，保留 |
| `cloudreve-fork:vas-1030-r3` / `admin-r6` / `easypay-1030-r2` / `debian:bookworm-slim` | 396 / 361 / 418 / 116 MB | **回滚镜像与基础镜像，全部保留**。任务约束要求保留回滚镜像；报告建议删 `vas-1030-r3`（净 135 MiB），但它对应的构建产物还在工作区、且属同批构建，收益小于风险，本次不动 |
| `/var/lib/containerd`（镜像层） | 4067 MiB | 在 `/` 上，删它只能靠删镜像；可用项只剩 Playwright（需确认）。**未改 `daemon.json`/containerd 配置**，迁移 snapshotter 属有风险的结构改动，不在本次范围 |
| `/opt/site302` 的 deleted cwd | — | 目录已不存在，占用已不在 `du` 里，但进程 PID 653159 仍持有句柄。见 §0.3 |
| `/tmp` | 109 MiB | **是 tmpfs（内存盘）**，清了不释放磁盘，只减内存压力 |

## 5. `/data/home` 剩下的 5073 MiB 是什么

全部是**正在使用的缓存**，不是重复副本：

| 路径 | MiB | 说明 |
|---|---|---|
| `~/.cache/yarn` | 2517 | 唯一一份 yarn 缓存（另一份已删），下次 `yarn install` 靠它离线装 |
| `~/.cache/go-build` | 1581 | Go 构建缓存，13:50 仍在写入，删了下次全量重编 |
| `~/.cache/ms-playwright` | 863 | 验收用 chromium，**必须保留**（测试脚本依赖） |
| 其余（`node`/`mesa*`/`sessions`/`chromium` 等） | ≈12 | — |

这部分属于「可清但会拖慢下次构建」的范畴。报告里「推荐方案」把 Go 缓存也削到一份的最小值，
是在牺牲后续构建速度换空间；当前 `/data` 已到 55%、可用 9.0 GiB，**没有必要再削**。

## 6. 守门约定（本次验证有效）

后续构建任务沿用报告 §6.4 的做法，并在收尾时执行本次验证过的两条：

```bash
# 构建结束：删前端依赖与产物（可重建）
rm -rf /data/workspace/*/frontend/node_modules /data/workspace/*/frontend/build
# 只保留一份缓存：yarn 留 ~/.cache/yarn、Go 留 ~/.cache/go-build（不要再用 .yarncache/.gocache）
```

- **不要用 `docker image prune -af`**（会清掉在用与回滚镜像）；
- **不要写「`find` 命中就 `rm -rf`」的通配脚本**（#45 误删源码目录的教训）；
- 用 `sudo` 删 `probe2` 这类 root 属主的 Playwright 目录时要盯住路径，别用通配符扩大范围。

## 7. 一句话结论

本次只清「可确认属临时产物」的部分：`/data` 从 **82% 降到 55%**（释放约 **5.0 GiB**，可用
3.6 → 9.0 GiB），`/` 基本不变（journal 回收约 40 MiB）。**最大头的两块——5212 的剧集 5.3 GiB
与 Playwright 镜像 3.3 GiB——按约束保留，等用户确认**；12 MiB 的部署存档因是回滚依据主动保留。
清理后 15212、15213 均返回 200，测试站容器与镜像未变。
