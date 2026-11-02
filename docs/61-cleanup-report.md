# #61 停止后部署残留清理报告（Baixiong + 并行科技超算集群）

> 清理日期：2026-11-02 20:30 UTC（Baixiong）/ 2026-11-03 04:31–04:32 CST（集群，UTC+8）
> 性质：**只删 #61 的部署产物，不重新部署、不修复、不碰任何生产服务**
> 关联：[#61](todo:R1xOmHKwM5ieY1-xAltOY)（2026-11-03 02:14 被停止，改动已作废）
> 凭据口径：本报告只写路径与主机名，**不出现私钥、API Key 的任何字符**

---

## 0. 结论速览

| 侧 | 残留 | 处理 |
| --- | --- | --- |
| Baixiong | `model-gateway.service`（**enabled + active**，正在每 33 秒重试 ssh） | 已 stop / disable / 删除 unit / daemon-reload |
| Baixiong | `/opt/model-gateway/`（`tunnel.sh`、`apikey`） | 已删（12 KB） |
| Baixiong | `/root/.ssh/model_server`（私钥）、`/root/.ssh/known_hosts_model` | 已删（2.4 KB） |
| Baixiong | 18180 监听 | **清理前就没有**（隧道一直在等 `current-node.txt`），清理后仍无 |
| 集群 | `~/llm-service/`（19 GB：5 个 GGUF 分片 18.5 GB + llama.cpp 源码/构建 256 MB + 日志） | 已删 |
| 集群 | `~/llm-build-*.log`、`~/logs/` 等（在 `llm-service/` 内） | 随目录删除 |
| 集群 | **`llm-build` 作业 126633155 仍在 RUNNING，占 1 节点 / 2 张 DCU** | 已 `scancel`（按作业名，限本账号） |
| 集群 | `~/.probe2/`（探测期下载的 3 个 llama_cpp_python sdist，187 MB） | 已删 |
| 集群 | `~/slurm-126619396.out`（探测作业被取消的输出） | 已删 |
| 集群 | crontab 中的 `*/10` 看护条目 | **从未安装**（`crontab -l`：`no crontab for acslbzzr2m`），无需删除 |
| 集群 | `llm-serve` 作业 | **从未提交过**（`squeue`/`sacct` 均无此作业名） |

**共释放**：集群家目录 **≈ 19.2 GB**（19 GB + 187 MB），另释放 1 个计算节点上的 **2 张 DCU + 2 CPU + 7138 MB 内存**；Baixiong **≈ 14 KB**（服务本身只有脚本与 key 文件，磁盘占用可忽略，`df` 前后同为 30%）。

---

## 1. 为什么先实测而不是照清单删

#61 的对话记录末尾只有一行 `Cancelled`（无汇报），因此「做到哪一步」完全未知；其分支改动已被丢弃，Baixiong 与集群上的东西**只可能是当时直接落盘的产物**。本次全部以现场实测为准，清单中每项都先验证存在性再动手。

清理前实测到的关键事实：

- Baixiong `systemctl is-enabled model-gateway` → `enabled`，`is-active` → `active`；`journalctl -u model-gateway` 末几行是每 33 秒一次的
  `集群侧没有 current-node.txt（模型作业未运行），30 秒后重试` —— 即**隧道常驻进程一直在跑**，只是没有可转发的目标。
- 集群 `squeue -u $USER` 里**还有 1 个作业在跑**：
  ```
  JOBID         NAME        STATE     TIME    NODELIST(REASON)
  126633155     llm-build   RUNNING   33:28   e10r3n02
  TRES=cpu=2,mem=7138M,node=1,billing=2,gres/dcu=2
  ```
  这是 #61 的最后一次 HIP 构建作业，**部署已作废但它仍在占用 2 张 DCU**——本次最有价值的发现，比磁盘残留更实质。
- 集群 `~/llm-service/` 19 GB（`models/` 19 GB、`src/` 256 MB），`~/.probe2/` 187 MB，`~/slurm-126619396.out` 86 B。

### 与 Spec 的两处偏差（据实调整，理由如下）

1. **Spec 只点名删「作业名 `llm-serve`」的作业**。现场 `llm-serve` 从未存在，真正在跑的是 `llm-build`。若照字面执行，等于把 2 张 DCU 留在共享集群上继续计费/占资源，与「清理干净」直接冲突，故按**作业名 `llm-build`、限本账号**执行 `scancel`（未使用无差别 `scancel -u`）。
2. **Spec 未列出的两项 #61 残留**：`~/.probe2/`（三个 `llama_cpp_python` sdist，时间戳落在 #61 作业时段内）与 `~/slurm-126619396.out`（内容为 `JOB 126619396 ON e06r4n03 CANCELLED`，是 #61 探测作业的输出）。二者都是本账号、本任务产生的探测残留，一并删除；其余家目录内容（`.qwenpaw`、`uv-*.lock`、`job_example` 等）时间戳早于 #61 开工，**未触碰**。

---

## 2. 将删清单与存在性实测（清理前）

### Baixiong（`instance-10110429`，aarch64，root）

| 路径 | 实测 |
| --- | --- |
| `/etc/systemd/system/model-gateway.service` | 存在，270 B，`enabled` + `active` |
| `/etc/systemd/system/multi-user.target.wants/model-gateway.service` | 存在（enable 生成的软链） |
| `/opt/model-gateway/` | 存在，12 KB：`tunnel.sh`（1076 B，755）、`apikey`（49 B，600） |
| `/root/.ssh/model_server` | 存在，1679 B，600（RSA 私钥） |
| `/root/.ssh/known_hosts_model` | 存在，689 B，600（#61 为该集群固定 known_hosts） |
| 18180 监听 | **不存在**（`ss -lntp \| grep 18180` 退出码 1；`ss -tan` 亦无连接） |
| 其他 | `/root` 下无 #61 的其他文件；`/etc/hosts`、iptables 无 18180/集群地址相关条目 |

清理前的监听面（用于事后比对，确认没动别的东西）：`111`、`3100`（docker-proxy）、`62302`（sshd）、`61927`、`62366`（tds-tunnel）、`53`（systemd-resolve）。

### 集群（`login04`/`login09`，`acslbzzr2m`，CentOS 7，非 root）

| 路径 / 对象 | 实测 |
| --- | --- |
| `~/llm-service/` | 19 GB；含 `env.sh`、`fetch.sh`、`build.slurm`、`serve.slurm`、`keepalive.sh`、`patch-hip.sh`、`apikey`、`cmake-stubs/`、`src/`（256 MB）、`models/`（5 个 GGUF 分片，共 18.49 GB）、`logs/fetch.log`、13 个 `llm-build-*.log` |
| `~/.probe2/` | 187 MB：`0.2.90.tar.gz`、`0.3.2.tar.gz`、`0.3.4.tar.gz` |
| `~/slurm-126619396.out` | 86 B，探测作业被取消的输出 |
| `crontab -l` | `no crontab for acslbzzr2m` —— 看护条目从未安装 |
| `squeue -u $USER` | 仅 `126633155 llm-build RUNNING`，无 `llm-serve` |
| `ss -lntp \| grep 18180`（登录节点） | 无 |
| `~/.ssh/` | 仅 `authorized_keys`、`config`、`id_rsa*`、`known_hosts`、`thirdparty_*`，时间戳均早于 #61（未改动） |

---

## 3. 执行记录

### 3.1 Baixiong

```bash
systemctl stop model-gateway
systemctl disable model-gateway        # Removed "/etc/systemd/system/multi-user.target.wants/model-gateway.service".
rm -f /etc/systemd/system/model-gateway.service
systemctl daemon-reload
systemctl reset-failed model-gateway   # 返回 "Unit model-gateway.service not loaded."（unit 已删，属预期）
rm -rf /opt/model-gateway
rm -f /root/.ssh/model_server /root/.ssh/known_hosts_model
```

### 3.2 集群

```bash
scancel -u $USER --name=llm-serve      # rc=0（无此作业）
scancel -u $USER --name=llm-build      # rc=0 → 126633155 由 RUNNING 转 COMPLETING，8 秒后队列为空
rm -rf ~/llm-service
rm -rf ~/.probe2
rm -f  ~/slurm-126619396.out
```

执行期间临时落地过一把私钥（`umask 077`，600）用于登录，用完立即 `shred -u`，未进入仓库、日志或对话。

---

## 4. 清理后验证（逐条对照验收项）

| 验收项 | 实测输出 |
| --- | --- |
| Baixiong 18180 已消失 | `ss -lntp \| grep 18180` → 退出码 **1**；`ss -tan \| grep 18180` → 退出码 **1** |
| 隧道进程已停 | `pgrep -af tunnel.sh` → 退出码 **1** |
| unit 已彻底移除 | `systemctl status model-gateway` → `Unit model-gateway.service could not be found.`；`ls /etc/systemd/system \| grep -i model` → 空；`systemctl list-unit-files \| grep -i model` → 空 |
| 目录与私钥已删 | `/opt/model-gateway` → `No such file or directory`；`/root/.ssh` 只剩 `authorized_keys`(0 B)、`known_hosts`(426 B，10-25) |
| Baixiong 监听面无变化 | 清理前后 `ss -lntp` diff 只有 `111`/`62302` 的 systemd fd 号变化（daemon-reload 重建 socket），端口与进程集合一致，**无端口增删** |
| 防火墙/主机名无残留引用 | `iptables -S \| grep 18180` → 空；`grep -rn "58.240.173.20\|model-gateway\|18180" /etc/hosts /etc/systemd/system` → 空 |
| 集群 `squeue` 无 `llm-serve`（且无任何本账号作业） | `squeue -u $USER` → 仅表头；`squeue -h -u $USER \| wc -l` → **0** |
| `llm-build` 作业已终止 | `sacct -j 126633155` → `CANCELLED+`，`Elapsed=00:33:30`；`squeue -h -u $USER \| wc -l` → **0**（其余 12 条 `llm-build` 是 #61 当时 `FAILED` 的构建尝试，属记账历史，见 §5） |
| `crontab -l` 无看护条目 | `no crontab for acslbzzr2m` |
| 目录已删 | `ls -d ~/llm-service ~/.probe2 ~/slurm-126619396.out` → 三条均 `No such file or directory`；`ls ~ \| egrep "llm\|probe\|slurm"` → 空 |
| 集群 18180 无监听 | 登录节点 `ss -lntp \| grep 18180` → 退出码 **1** |
| 集群无残留进程 | `pgrep -af -u $USER "llama-server\|tunnel.sh\|keepalive"` → 仅匹配到本次执行的 shell 自身，无业务进程 |

---

## 5. 保留项与未清理原因

| 对象 | 为何保留 |
| --- | --- |
| Baixiong 的 `111`/`3100`/`62302`/`61927`/`62366`、Docker、nginx、API 中转站 | 生产服务，Spec 明确不碰；实测端口集合清理前后一致 |
| `/tmp/dl/`（集群登录节点，47 MB，属用户 `jdstkxx`） | 他人文件，Spec 明确不碰（实测确认属 `jdstkxx`，与 Spec 记载一致） |
| `/tmp/uv-setuptools-*.lock`（0 B，3 个，属本账号） | 时间戳 2026-11-02 20:49 CST，早于 #61 开工，非本次残留 |
| `~/.qwenpaw`、`~/.cache`、`~/.config`、`job_example` 等 | 早于 #61 的既有内容，不属于部署产物 |
| Slurm 记账历史（`sacct` 里的 13 条 `llm-build` 记录，含本次 `CANCELLED` 的 126633155） | 平台侧审计记录，非文件残留，账号无权也不应删除 |
| #61 分支 `tds/conv-01a19d27-…` 上的代码改动 | 已被平台丢弃；本次清理只针对两台机器上的落盘产物 |

---

## 6. 释放的空间与资源

| 侧 | 释放 |
| --- | --- |
| 集群家目录（`/public/home/acslbzzr2m`） | `llm-service` **19 GB** + `.probe2` **187 MB** + `slurm-126619396.out` 86 B ≈ **19.2 GB**（`df` 显示 `/public` 61 P 池 77% 使用，单用户变化不可见，故以 `du` 为准） |
| 集群计算资源 | 释放 1 个节点上的 **2 张 DCU**（`gres/dcu=2`）、2 CPU、7138 MB 内存 —— 该作业在 #61 被停止后仍在跑 33 分钟 |
| Baixiong | `/opt/model-gateway` 12 KB + 两个 key 文件 2.4 KB ≈ **14 KB**；`df /` 前后同为 `29G / 96G (30%)` |
| Baixiong 系统状态 | 少一个 `enabled` 的 unit，开机不再拉起隧道进程；不再每 33 秒对集群发起一次 ssh |

---

## 7. 需要知道的两点

1. **私钥只留在团队密钥里**。`/root/.ssh/model_server` 已删除，`SSH_MODEL_SERVER` 仍是唯一副本；将来若重启部署，可从该密钥重新落盘（`umask 077`，600），无需重新申请。
2. **`llm-serve` 与 cron 看护从未真正上线**：`serve.slurm`/`keepalive.sh` 只落在 `~/llm-service` 里，从未提交作业、从未安装 crontab。因此 #61 对外从未提供过 18180 服务，Baixiong 上的 18180 也从未真正监听成功——清理没有中断任何在用能力。

本次**未**做任何重新部署、未修改任何生产服务配置、未改动仓库代码（仅新增本报告）。
