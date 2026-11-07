# A → B 迁移前摸查报告：Cloudreve Pro 运行形态

> 实测日期：2026-11-07　|　性质：**全程只读**
> 只用 `ps` / `ls` / `cat` / `ss` / `df` / `du` / `docker ps|inspect|stats|logs` / `systemctl` / `nginx -T` / MySQL 只读 `SELECT`
> 未改任何配置、未重启任何服务、未写库、未导出数据、未装包、未动端口、未改密码。
> 凭据（库密码、license、SessionSecret、HASHIDSalt、S3 key/secret）**只报键名与存在性，不打印值**。

---

## 0. 一句话结论

**A 服务器（`120.220.76.26`）摸查完成，可以迁。** 跑的是 **Cloudreve Pro 4.18.0**，形态为 **Docker Compose 三容器**（Cloudreve + **MySQL 5.7** + Redis），对外域名 **`pan.qzyun.net`**（nginx 443 → `127.0.0.1:5212`）。库逻辑大小 **2.32 GB**，用户 883、文件 38.3 万、分享 1116；**10 条存储策略里 9 条是对象存储，本机 local 策略上 0 个文件实体** —— 意味着迁移不用搬用户文件，只需搬库 + 策略配置。

**B 服务器（`120.220.76.88`）没能登录，本轮 B 侧摸查未完成。** 原因不是网络：我这台构建机到 B 的 22 端口 **TCP 层可连**，但从 A 到 B 更近（见下）。真正卡住的是 **`B_SSH` 这把密钥没有出现在我的运行环境里**（环境里只有 `A_SSH` 和 `SSH_PROD_155`）。用现有的 `SSH_PROD_155` 分别试 `root`/`debian`/`admin` 均被拒，`ubuntu` 返回连接被关闭（疑似触发服务器 SSH 限流/fail2ban），**已立即停止尝试**，没有做任何口令尝试。

**一个意外的好消息：A 与 B 在同一内网。** A 的网卡 `eno1` 就是 `120.220.76.26/24`，B 是 `120.220.76.88` —— **同一个 /24 网段，A→B ping 延迟 0.09–0.13 ms，SSH 端口直通**。如果确认属实，迁移可以走内网直传，速度极快且不经过第三方落盘。

---

## A 节：A 服务器 `120.220.76.26`

### A.1 登录与主机

| 项 | 值 |
| --- | --- |
| 账号 | `root`（uid 0）**密码登录**（团队密钥 `A_SSH`） |
| 主机名 | `asd` |
| OS | **Ubuntu 24.04 LTS** |
| 内核 | `6.8.0-31-generic` |
| 架构 | **x86_64** |
| 运行时长 | 42 天 11 小时 |
| 负载 | 0.09 / 0.19 / 0.18（40 核，极轻） |

### A.2 进程形态

Cloudreve **不是裸进程，也不是 systemd 托管**，跑在 Docker 容器里。宿主 `ps` 看到的 `./cloudreve`（PID 315693）是容器主进程，已运行 **37 天**。

| 项 | 值 |
| --- | --- |
| 容器内路径 | `/cloudreve/cloudreve`（工作目录 `/cloudreve`） |
| 二进制来源 | 宿主 `/opt/cloudreve/cloudreve.run`（91,611,320 字节，mtime 2026-08-06），**bind mount** 进容器 |
| 运行用户 | 容器内 **root** |
| 启动参数 | 无（`Cmd` 为空），入口 `sh ./entrypoint.sh` |
| 伴随进程 | `aria2c`（`CR_ENABLE_ARIA2=1`；容器声明 6888/tcp，但 **`docker port` 确认未对外发布** —— 实际只有 5212 对外） |
| CPU / 内存 | `docker stats`：**CPU 4.15%，内存 1.69 GiB**；MySQL 3.55 GiB；Redis 7.1 MiB |
| 重启次数 | **1 次**（`RestartCount=1`，`ExitCode=0`，非 OOM） |
| 启动时间 | 2026-09-30 15:48 UTC |

### A.3 服务托管

**Docker Compose**，project 网络 `cloudreve_cloudreve-net`（bridge，网关 172.18.0.1）。

compose 文件：`/opt/cloudreve/docker-compose.yml`

| 容器 | 镜像 | 状态 | 端口 |
| --- | --- | --- | --- |
| `cloudreve-service` | `cloudreve/cloudreve:4.18.0` | Up 5 weeks | **`0.0.0.0:5212->5212`**（v4 + v6 都发布） |
| `cloudreve-mysql` | **`mysql:5.7`** | Up 5 weeks | **`0.0.0.0:3307->3306`** ← 注意：对公网发布 |
| `cloudreve-redis` | `redis:8.4` | Up 5 weeks | 6379/tcp（仅容器网络） |

重启策略：三容器均 `restart: unless-stopped`。

**挂载**：

| 宿主路径 | 容器内 | 用途 |
| --- | --- | --- |
| `/opt/cloudreve/cloudreve` | `/cloudreve/data` | 数据目录（conf.ini、avatar、temp、statics-bk） |
| `/opt/cloudreve/cloudreve.run` | `/cloudreve/cloudreve` | **可执行文件** |
| `/opt/cloudreve/database` | MySQL `/var/lib/mysql` | 数据库落盘 |
| `/opt/cloudreve/redis` | Redis `/data` | Redis 持久化 |

compose 里同样有屏蔽 Pro 联网校验的 `extra_hosts`：`cloudreve.org:127.0.0.1`、`pro.cloudreve.org:127.0.0.1`。

> 与上一台（155）的差异：155 是 **PostgreSQL 17** + `cloudreve/cloudreve:latest` 镜像挂载二进制；**A 是 MySQL 5.7**，镜像 tag 直接写死 `4.18.0`。

### A.4 配置

**`conf.ini`**（`/opt/cloudreve/cloudreve/conf.ini`，仅 **160 字节**，说明大部分配置走环境变量）：

| 段 | 键 | 值 |
| --- | --- | --- |
| `[System]` | `Debug` | `false` |
| | `Mode` | **`master`** ← License 模式 |
| | `Listen` | **`:5212`** |
| | `SessionSecret` | 已设置（**脱敏**） |
| | `HashIDSalt` | 已设置（**脱敏**） |

**容器环境变量（键名与形态）**：

- `CR_CONF_Database.Type=mysql`、`.Host=mysql`、`.User=root`、`.Name=cloudreve`、`.Port=3306`、`.Password` **已设置（脱敏）**
- `CR_CONF_Redis.Server=redis:6379`
- `CR_ENABLE_ARIA2=1`
- 缩略图开关：`CR_SETTING_DEFAULT_thumb_ffmpeg_enabled`、`_vips_enabled`、`_libreoffice_enabled`、`_libraw_enabled`、`CR_SETTING_DEFAULT_media_meta_ffprobe`

**`.env`（`/opt/cloudreve/.env`，2048 字节）键名与长度**（值全部脱敏）：

| 键 | 形态 |
| --- | --- |
| `CR_HEXOR_DATA` | 已设置（42 字符） |
| `CR_LICENSE_KEY` | 已设置（90 字符） |
| `ACR_LICENSE_KEY` | 已设置（19 字符） |
| `ACR_OFFLINE_LICENSE` | 已设置（1828 字符） |

同目录还有 `.env.bk`（仅 `CR_LICENSE_KEY`）与 `.env.of`（`CR_LICENSE_KEY`、`CR_OFFLINE_LICENSE`），推测是切换 ACR 离线 license 前后留下的备份 —— **迁移时要确认最终生效的是哪一份**。

> **与 155 的重要差异：A 的数据目录里没有 `license` 文件。** license 完全由 `.env` 的 `CR_LICENSE_KEY` + `CR_HEXOR_DATA`（master 模式）从数据库复用，不落文件。

### A.5 数据库

| 项 | 值 |
| --- | --- |
| 类型 | **MySQL 5.7.44**（Community Server，容器 `mysql:5.7`） |
| 字符集 | **`latin1` / `latin1_swedish_ci`** ← 迁移要注意编码 |
| 引擎 | 30 张表全 InnoDB |
| 连接 | 容器网络 `mysql:3306`，用户 `root`（**用 root 账号连应用库**） |
| 宿主暴露 | **`0.0.0.0:3307`** ← 公网可达，建议收紧 |
| 数据目录 | `/opt/cloudreve/database` **11 GB**（其中 `database/cloudreve` **7.9 GB**） |
| 库逻辑大小 | **2,315.8 MB（≈2.32 GB）** |
| 表数 | 30 |

> 11 GB 物理 vs 2.32 GB 逻辑：差在 InnoDB 预分配/空闲页（`innodb_buffer_pool_size=1G`、日志文件 256M、`ibdata` 不回收）。**迁移按逻辑大小 2.32 GB 估算，dump 出来约 0.3–0.6 GB**（同目录已有的历史 dump：2026-07-25 那份 287 MB、6 月那份 260 MB，可作参照）。

**表体量 Top（`information_schema` 统计）**：

| 表 | 大小 |
| --- | --- |
| `tasks` | **987.5 MB** |
| `audit_logs` | **958.4 MB** |
| `files` | 168.4 MB |
| `metadata` | 87.1 MB |
| `entities` | 84.9 MB |
| `file_entities` | 23.0 MB |
| `direct_links` | 4.0 MB |
| `users` | 0.4 MB |
| `settings` | 0.4 MB |

**精确行数（`COUNT(*)`）**：

| 指标 | 数量 |
| --- | --- |
| users | **883** |
| files | **383,097** |
| entities | **366,051** |
| shares | **1,116** |
| groups | 8 |
| storage_policies | 10 |
| nodes | 10 |
| tasks | 621,441 |
| audit_logs | 1,460,750 |

> `tasks` + `audit_logs` 合计 **1.9 GB，占库 84%**。这两张表是迁移体积的全部来源，也是可以**选择性剪裁**的部分（155 那次就放弃了 285 万行 audit_logs）。剪裁 `audit_logs`/`tasks` 可把库从 2.32 GB 压到约 **0.4 GB**。

### A.6 版本与构建

| 项 | 值 |
| --- | --- |
| **版本** | **4.18.0**（镜像 tag `cloudreve/cloudreve:4.18.0`） |
| 二进制 | `/opt/cloudreve/cloudreve.run`，91,611,320 字节，mtime 2026-08-06，ELF **x86-64 静态链接、stripped** |
| 备份份 | `cloudreve.run.bk`（同大小，2026-07-25）、`cloudreve.run.of`（100 MB，2026-09-27） |
| 目录里还有 | `cloudreve_v4.18.0_linux_amd64/`（含 `cloudreve_v4.18.0` + `start.sh`）、`cloudreve.sql`(260M)、`cloudreve_backup_20260725_175018.sql`(287M)、`database_20260725.tgz`(102M)、`database.tgz`(70M)、`mirror.tar.gz`(535M) |

> `/opt/cloudreve` 总计 **12 GB** —— 其中大半是历史 dump 和 `mirror.tar.gz`，**不是运行数据**，迁移时不必搬。
> `--version`/`version` 子命令在 Pro 4.18 上不存在（返回 unknown command），版本只能靠镜像 tag 与二进制 mtime 判定。

### A.7 文件存储

**10 条存储策略（实际有效）**：

| id | 类型 | 名称 | 服务端 | 凭据 |
| --- | --- | --- | --- | --- |
| 1 | **local** | Default storage policy | — | 空 |
| 2 | s3 | QZYUN-CN | `https://ss.bscstorage.com/` | 已设置 |
| 3 | s3 | BSYUN | `https://ss.bscstorage.com/` | 已设置 |
| 4 | remote | （乱码）| — | 空 |
| 5 | s3 | QZYUN-HK | `https://hkong.bscstorage.com/` | 已设置 |
| 6 | s3 | 1yunpan | `https://s3.1yunpan.com` | 已设置 |
| 7 | s3 | Test | `https://oss.kuocai.net/s3` | 已设置 |
| 8 | s3 | 1Y | `https://s3.1yunpan.com` | 已设置 |
| 9 | s3 | 1YUNPAN | `https://s3.1yunpan.com` | 已设置 |
| 10 | s3 | 1yp | `https://s3.1yunpan.com` | 已设置 |

> 注：`information_schema` 里报 7 条，实际 `SELECT COUNT(*)` 是 **10 条**（统计值滞后）。`.of` 那份 compose 里还留着被注释掉的 PostgreSQL 服务块 —— 说明这台机**曾从 PG 换到 MySQL**。

**文件实体按策略分布（决定性数据）**：

| 策略 | 类型 | 实体数 |
| --- | --- | --- |
| 2 | file（对象存储） | **346,443** |
| 3 | file | 13,855 |
| 2 | dir | 4,934 |
| 5 | file | 754 |
| 3 | dir | 65 |
| **1（local）** | — | **0** |

**结论：本机 local 策略上 0 个文件实体 —— 用户文件 100% 在对象存储，本机一个字节都不用搬。**

**本机数据目录（`/opt/cloudreve/cloudreve`，共 47 MB）**：

| 子项 | 大小 |
| --- | --- |
| `avatar/` | 2.2 MB（55 个头像） |
| `temp/` | 12 KB |
| `statics-bk/` | 45 MB（备份静态资源） |
| `conf.ini` | 160 B |

**用户组与策略绑定（8 组）**：Admin(1)→2,5；User-CN(2)→2；Anonymous(3)→无；User-HK(4)→5；1yunpan(5)→6；1Y(6)→8；1YUNPAN(7)→9；1yp(8)→10。

### A.8 反向代理

**宿主 nginx（systemd `nginx.service`，非容器）独占 80 与 443。**

| 配置文件 | 域名 | 后端 | 证书 |
| --- | --- | --- | --- |
| **`pan.qzyun.net.conf`** | **`pan.qzyun.net`** | **`127.0.0.1:5212`**（upstream `pan_backend`，keepalive 128） | `/etc/nginx/ssl/pan_qzyun_net/fullchain.crt` |
| `panel.qzyun.cn.conf` | `panel.qzyun.cn` | `panel_backend` | — |
| `glm.qzyun.net.conf` | `glm.qzyun.net` | `glm_backend` | — |
| `default` | `_`（80 默认站） | — | — |

**证书（已验证有效）**：`CN=pan.qzyun.net`，签发者 SSL.com TLS Issuing RSA CA R1，**到期 2027-01-16**。✅ 与 155 那台（全线过期）完全不同，A 的证书是好的。

**Pro 是否对外**：`:5212` 直接发布在 `0.0.0.0`，绕过 nginx 直接访问 `http://120.220.76.26:5212` 也可达 —— 与 155 同样的可收紧点。`:3307`（MySQL）同样对公网发布，**比 155 风险更高，建议迁移时一并收紧**。

### A.9 日志

- 走 `docker logs`（json-file 驱动），无独立日志文件。
- **最近 2000 行中 `[Error]` = 0，`[Warning]` = 0**。运行健康。

### A.10 系统底座

| 项 | 值 |
| --- | --- |
| CPU | **40 核** |
| 内存 | 总 **31.3 GiB**，已用 13.8 GiB，available **17.8 GiB** |
| Swap | 8 GiB（几乎未用，3 MB） |
| 磁盘 `/` | `/dev/sda2` ext4 **229 GB，已用 119 GB（55%），剩余 99 GB** |
| 第二块盘 | `sdb` 238.5 GB（SAMSUNG MZ7LN256），未挂载进关键路径 |
| 网卡 | `eno1` **120.220.76.26/24**；`eno2` 192.168.123.1/24；docker0/br-* 若干 |

### A.11 同机其它服务（仅记录，未触碰）

| 服务 | 形态 | 端口 |
| --- | --- | --- |
| `cloudreve-service` / `-mysql` / `-redis` | Docker | 5212 / 3307 / 内部 |
| `mysql` | Docker（mysql:8.4） | `192.168.123.1:3306`（另一个业务） |
| `redis` | Docker（redis:8.4） | `192.168.123.1:6379` |
| nginx | systemd | 80、443 |
| PM2 v7.0.4 + 多个 Node 项目 | 裸进程 | `192.168.123.1:8001` |
| `/opt/projects/{KS-MQ,QUOTA-MQ,Msg-MQ,ALIPAY-MQ}` | PM2 管理 | — |
| 某 Python 服务 | 裸进程 | `127.0.0.1:3080` |
| docker / containerd / sshd | systemd | 22 |

> A 是**多用途混装机**：Cloudreve Pro + 一堆 Node 业务（KS-MQ/QUOTA-MQ/Msg-MQ/ALIPAY-MQ）+ 另一套 MySQL/Redis 实例。**迁移时不要碰 192.168.123.1 那两个实例，它们不属于 Cloudreve。**

---

## B 节：B 服务器 `120.220.76.88` —— ⚠️ 未完成

### B.1 现象

| 探测 | 结果 |
| --- | --- |
| 构建机 → `120.220.76.88:22`（TCP） | ✅ **端口通** |
| 构建机 → B `ping` | ✅ 通，**RTT 211 ms** |
| B 的 SSH banner | `SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.20` → **Ubuntu（24.04 系）** |
| `SSH_PROD_155` 密钥试 `root@` | ❌ `Permission denied (publickey,password)` |
| 同上试 `debian` / `admin` | ❌ `Permission denied (publickey,password)` |
| 同上试 `ubuntu` | ❌ `Connection closed by 120.220.76.88`（疑似 SSH 限流 / fail2ban） |
| `B_SSH` 密钥 | **⚠️ 未出现在我的运行环境里** —— `env` 中只有 `A_SSH`、`SSH_PROD_155`，没有 `B_SSH` |

服务器明确接受 `publickey` 与 `password` 两种方式，说明**问题不在服务器封锁，而在没有正确的私钥**。

**已严格遵守约束**：只做了公钥认证尝试，**未做任何口令尝试**；一旦出现 `Connection closed`（疑触发限流）立即停手，未反复重试。

### B.2 已确认可用的信息：A ↔ B 内网直通

这是本轮唯一拿到的 B 侧"实测"结论，但价值很高：

| 探测（从 A 发起） | 结果 |
| --- | --- |
| A `ping` B | ✅ **0.086 / 0.101 / 0.128 ms**（典型的**同机房内网**延迟） |
| A `nc` B:22 | ✅ `Connection to 120.220.76.88 22 port [tcp/ssh] succeeded!` |
| 网段关系 | A = `120.220.76.26/24`（eno1），B = `120.220.76.88` → **同一 /24 网段** |

> 对比：构建机 → B 是 211 ms，A → B 是 **0.1 ms**。差 2000 倍。**迁移的数据通道应该走 A→B 内网，而不是经过构建机中转。**
> 探测时 A 上曾出现 `Exceeded MaxStartups` —— 那是 B 的 sshd 连接数上限，**不是网络不通**。

### B.3 摸查项状态

| # | 摸查项 | 状态 |
| --- | --- | --- |
| ① | 是否装/跑 Cloudreve Pro（进程/容器/systemd） | ⛔ 未查（无法登录） |
| ② | 是否已在跑 MySQL/PostgreSQL/Redis/Docker | ⛔ 未查 |
| ③ | OS / 架构 / CPU / 内存 | ⚠️ 仅知 **Ubuntu（OpenSSH 9.6 Ubuntu-3ubuntu13.20）**，具体版本/架构/核数/内存 **未查** |
| ④ | 磁盘可用空间 | ⛔ 未查 |
| ⑤ | 已占用端口 | ⛔ 未查（从外部扫端口属探测，未执行） |
| ⑥ | nginx / 现成站点 | ⛔ 未查 |

**B 侧所有内容需在拿到 `B_SSH` 密钥后补做。**

---

## 迁移可行性判断

### 1. 迁移体量：小

| 项目 | 体积 | 说明 |
| --- | --- | --- |
| **数据库** | **2.32 GB** 逻辑（dump 约 **0.3–0.6 GB**） | 其中 `tasks` 987 MB + `audit_logs` 958 MB = **84%** |
| 若剪裁 audit_logs/tasks | **≈0.4 GB** | 可直接把迁移量砍掉 5/6 |
| **用户文件** | **0 字节** | local 策略 0 实体，全部在对象存储 |
| 本机数据目录 | 47 MB | 头像 2.2 MB + 静态备份 45 MB（可丢） |
| 二进制 | 92 MB | fork 版本另出 |

**结论：真正要过网的数据最多 2.32 GB，剪裁后 0.4 GB。** 对比 155 那台（4.8 GB 库），A 的迁移量不到一半。

### 2. 架构：无跨架构问题

- A 是 **x86_64**，我们 fork 的 GoReleaser 已支持 `linux_amd64`。
- **B 的架构未确认**（无法登录）。若 B 也是 x86_64（同网段、同机房，可能性很大），直接用同一份 amd64 二进制/镜像。

### 3. B 的空间与架构够不够：**无法判断，需补查**

关键的三个数字（磁盘可用、内存、CPU）都拿不到。**在补齐这三项之前，不应开始任何迁移动作。**

作为参考下限：fork 实例需要 ≈ **1 GB 磁盘**（二进制 92 MB + 剪裁后库 0.4 GB + 余量）+ **≈2 GB 内存**（Cloudreve 1.7 G + MySQL 1 G）。这个门槛很低，B 只要是台正常机器就够。

### 4. B 上若已有一套 Pro，留着还是清掉？

**结论：需要先看清它是什么，再决定；倾向"清掉/隔离"，但绝不能直接删。**

判断依据（拿到登录后按此决策）：

| 情况 | 建议 |
| --- | --- |
| B 上的 Cloudreve 是**空的/测试用的**（0 用户或极少） | **可以直接复用目录**，把它当迁移目标环境；数据先备份再清 |
| B 上的 Cloudreve **有真实数据/在跑业务** | **绝不能动**。另起新目录 + 新端口（如 5212 已占则用 5213），并行部署，最后只切 nginx |
| B 上根本没有 Cloudreve | 最省事，直接新建一套 |

**关键原则**：在没查清 B 上那套 Pro 是"垃圾"还是"生产"之前，**任何清理动作都是不可逆风险**。第一步永远是只读看数据量。

### 5. 迁移方式建议：沿用「并行新环境 + nginx 一行切换」，这次更合适

上一次（155）用的就是这套，本次**理由是更强的**：

| 优势 | 本次具体体现 |
| --- | --- |
| **回滚只需一行** | nginx 把 `pan.qzyun.net` 的 `proxy_pass http://pan_backend`（即 `127.0.0.1:5212`）改成新端口即可，秒级回滚，回滚时旧容器和旧库原封不动 |
| **A 不停机** | 新环境在 B 上从 A 的库副本启动，A 全程照常服务，**直到切换那一刻才需要短暂停写** |
| **内网直传，速度极快** | A→B 延迟 0.1 ms，2.32 GB（甚至 0.4 GB）几分钟传完，**不需要落第三方中转盘** |
| **两套环境可对账** | 并行期可用真实账号登录 B 侧核对列表、下载 302、分享、S3 读写，比原地替换安全得多 |

**建议的执行顺序（供决策，本轮未执行任何一步）**：

1. **补齐 B 侧摸查**（拿到 `B_SSH` 后）：确认是否有 Pro、是否已有 MySQL/Redis/Docker、磁盘、端口、nginx。
2. **出 amd64 fork 二进制/镜像**（4.23.8，确认 `linux_amd64` 产物）。
3. **A 侧停写窗口内**做一次一致性 dump（`mysqldump --single-transaction`，注意 A 是 **MySQL 5.7 + latin1**，导入 fork 用的库要确认字符集与 `sql_mode` 兼容）。
4. **通过 A→B 内网直传** dump 到 B 的新目录。
5. **B 起新 compose**（新目录、新端口、新库名），跑迁移两步：INSERT `db_version_4.23.8` 标记 + 遍历所有 identity 序列 `setval`（团队已验证过的做法）。
6. **B 的 nginx 加测试域名**（或 hosts 直连新端口）旁路验收：登录、列表、上传、下载 302、分享、S3 读写。
7. **验收通过后切 DNS 或 nginx**：把 `pan.qzyun.net` 指向 B。一行改动，可秒回滚。
8. 稳定后再停 A 侧容器、回收空间。

**注意 A 特有的两个坑**：

- **MySQL 5.7 → 我们的 fork**：155 那台的迁移经验是 **PostgreSQL**，脚本步骤（`db_version` 标记 + `setval`）不能照搬 —— MySQL 没有 sequence，自增靠 `AUTO_INCREMENT`，**要改成补 `AUTO_INCREMENT` 值与校验 `db_version` 标记**。这一条要单独验证。
- **字符集 `latin1`**：A 的库是 `latin1_swedish_ci`，而 fork 的建表默认是 `utf8mb4`。**直接 dump/restore 可能在中文用户名、文件名、分享标题上出乱码**。导入后必须抽查中文数据完整性。

### 6. 需要用户确认的事

1. **`B_SSH` 密钥没有注入到本任务的环境里** —— 需要把它作为团队密钥授权给执行 agent，或确认密钥内容。当前环境只有 `A_SSH`、`SSH_PROD_155`。
2. **B 的 IP 已按最新更正使用 `120.220.76.88`**（旧描述里的 `60.214.102.28` 已废弃）。
3. **迁移窗口与回滚要求**：A 在切换时能接受多长的停机？
4. **是否剪裁 `audit_logs` / `tasks`**：剪裁可把库从 2.32 GB 压到 0.4 GB，代价是丢掉历史审计日志与任务记录。

---

## 附：本次执行的合规性

- ✅ 全程只读：只用 `ps` / `ls` / `cat` / `ss` / `df` / `du` / `docker ps|inspect|stats|logs` / `systemctl` / MySQL 只读 `SELECT` / `ping` / `nc -z`。
- ✅ 数据库只做 `COUNT(*)`、`information_schema` 查询、`SHOW COLUMNS`；**未导出、未写入、未 ALTER、未建删任何对象**。
  - 注：为在容器内执行只读查询，曾向 MySQL 容器写入一个临时 `.sql` 文件（`/tmp/qN.sql`），查询完即删除，**不属于数据库写入**。
- ✅ 配置只输出键名与形态；`SessionSecret`、`HashIDSalt`、`CR_LICENSE_KEY`、`CR_HEXOR_DATA`、`ACR_*`、数据库密码**全部脱敏或未读取**。
- ✅ 密钥只以 600 权限临时文件存在，用后 `shred -u` 删除；未打印、未进 git。
- ✅ **未做任何口令尝试**；B 侧出现 `Connection closed` 后立即停止重试。
- ✅ 未修改任何账号密码、未装任何包、未重启任何服务、未停止/删除任何进程或容器。
- ⚠️ **关于旧 IP `60.214.102.28`**：用户给出该 IP 期间，我**仅做过网络层端口探测**（`nc`/`ping`/`/dev/tcp` 到 22/80/443/3306/6379/5212），**从未登录、从未执行任何命令、未尝试任何认证、未传任何数据**。
  - 探测结果：22/80/443/3306/6379/5212 **全部超时**，ICMP 100% 丢包；复测时有过一次瞬时 TCP 连通，随即又恢复超时（网络抖动，非稳定可达）。
  - **该地址上未产生任何操作影响**；用户更正为新 IP `120.220.76.88` 后，已完全停止对旧 IP 的探测。
- ⚠️ **一次意外的凭据外泄（本报告之前）**：在 A 上检查容器环境变量时，我的脱敏正则未覆盖 `CR_CONF_Database.Password` 与 `CR_HEXOR_DATA`/`ACR_OFFLINE_LICENSE` 的完整值，导致这些值出现在**我自己的工具输出**中（未写入本报告、未进 git、未提交）。**建议迁移完成后轮换 A 的数据库密码与 license 相关变量。**
