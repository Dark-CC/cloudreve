# A → B 迁移前摸查报告：Cloudreve Pro 运行形态

> 实测日期：2026-11-07　|　性质：**全程只读**
> 只用 `ps` / `ls` / `cat` / `ss` / `df` / `du` / `docker ps|inspect|stats|logs` / `systemctl` / `nginx -T` / MySQL 只读 `SELECT`
> 未改任何配置、未重启任何服务、未写库、未导出数据、未装包、未动端口、未改密码。
> 凭据（库密码、license、SessionSecret、HASHIDSalt、S3 key/secret）**只报键名与存在性，不打印值**。

---

## 0. 一句话结论

**A 服务器（`120.220.76.26`）摸查完成，可以迁。** 跑的是 **Cloudreve Pro 4.18.0**，形态为 **Docker Compose 三容器**（Cloudreve + **MySQL 5.7** + Redis），对外域名 **`pan.qzyun.net`**（nginx 443 → `127.0.0.1:5212`）。库逻辑大小 **2.32 GB**，用户 883、文件 38.3 万、分享 1116；**10 条存储策略里 9 条是对象存储，本机 local 策略上 0 个文件实体** —— 意味着迁移不用搬用户文件，只需搬库 + 策略配置。

**B 服务器（`120.220.76.88`）摸查完成，是一台理想的迁移目标。** Ubuntu 24.04.3、**x86_64（与 A 同架构）**、40 核 / **62 GiB 内存**、磁盘 **剩余 203 GB**、负载几乎为零。**B 上完全没有 Cloudreve**（进程、容器、systemd 单元、文件目录四项全空），**5212/3307/6379 端口全空闲**，nginx 已装但没有 `pan` 子域站点 —— **零冲突、零历史包袱**。B 缺的只有 PostgreSQL/Redis（本次本就不需要）和一个独立的 MySQL 实例，Docker 已就绪（29.1.2）。

**A 与 B 在同一内网。** A `eno1` = `120.220.76.26/24`，B `eth0` = `120.220.76.88/24`，**同段双向互 ping 0.1 ms、SSH 端口直通**。迁移走内网直传，不经第三方落盘。

**迁移量已确认按"剪裁"口径**：用户决定 `tasks` + `audit_logs` 两张表全部不迁，库体积从 **2.32 GB 降到约 0.4 GB**。

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

## B 节：B 服务器 `120.220.76.88`

> ✅ **本轮已完成全部摸查**（用户授权 `B_SSH` 密钥后）。全程只读。

### B.1 登录

| 项 | 结果 |
| --- | --- |
| 密钥 | `B_SSH`，**ED25519 无口令**，注释 `server@qzyun.cn` |
| `root` | ✅ **登录成功**（`whoami` = root） |
| `ubuntu` / `debian` / `admin` | ❌ `Connection closed` / `reset`（服务器未放这些账号的公钥） |
| 账号 | 只用 `root` + 这一把密钥，**未做任何口令尝试** |

> 前一轮报告的"B 未完成"已作废 —— 当时 `B_SSH` 未注入运行环境。本轮密钥到位后一次登录成功。

### B.2 系统底座（摸查项 ③）

| 项 | 值 |
| --- | --- |
| 主机名 | **`3L-D15-19`** |
| OS | **Ubuntu 24.04.3 LTS** |
| 内核 | `6.8.0-90-generic` |
| 架构 | **x86_64** ✅（与 A 相同，**无跨架构问题**） |
| CPU | **40 核**，Intel Xeon E5-2680 v2 @ 2.80GHz |
| 内存 | 总 **62 GiB**，已用 5 GiB，available **56 GiB** |
| Swap | 16 GiB（0 使用） |
| 运行时长 | **329 天 18 小时**（长期稳定运行） |
| 负载 | 0.03 / 0.04 / 0.00（**几乎闲置**） |

### B.3 磁盘可用空间（摸查项 ④）—— **富余充足**

| 挂载点 | 设备 | 容量 | 已用 | **可用** | 使用率 |
| --- | --- | --- | --- | --- | --- |
| `/` | `/dev/sda4` (230.3G) | 231 G | 28 G | **203 G** | 13% |
| `/boot` | `/dev/sda2` | 172 M | 126 M | 32 M | 80% |

> **剩余 203 GB**，是本迁移最大的意外之喜。A 的库 2.32 GB（剪裁后 0.4 GB），**空间绰绰有余，连备份、旧库、并行双实例都放得下**。
> `/boot` 用了 80%，但不影响本次迁移（内核够旧就已经占着）。`sda3` 是 8G swap。

### B.4 是否装/跑 Cloudreve Pro（摸查项 ①）—— **完全没有**

用户要求"进程、容器、systemd 单元都查一遍"，逐项结果：

| 检查项 | 命令 | 结果 |
| --- | --- | --- |
| 进程 | `ps -eo pid,user,cmd \| grep -i cloudreve` | ❌ **无** |
| systemd 单元 | `systemctl list-units --type=service --all \| grep -i cloudreve` | ❌ **无** |
| Docker 容器 | `docker ps -a` | ❌ **无 cloudreve 容器** |
| 文件系统目录 | `/opt` `/srv` `/root` `/data` `/var/www` 下 `find -iname "*cloudreve*"` | ❌ **无任何目录/文件** |

**结论：B 是一台干净的机器，没有任何 Cloudreve 历史包袱。** 这直接回答了两个关键问题：

- **不存在"B 上那套 Pro 留着还是清掉"的抉择** —— 没有可清的东西。
- **不存在端口冲突风险**（Cloudreve 常用 5212 在 B 上未被占用）。
- **也不存在数据被覆盖的风险** —— 全新部署。

### B.5 已在跑的数据库/缓存/Docker（摸查项 ②）

| 组件 | 状态 | 详情 |
| --- | --- | --- |
| **Docker** | ✅ **已装并运行** | Docker **29.1.2** |
| **MySQL** | ⚠️ 有，但**不是给 Cloudreve 用的** | 容器 `mysql8`（`mysql:8.4`），`192.168.123.2:3306`，数据 `/opt/mysql/data`（885 MB），**Restart=no**（不自启），StartedAt 2025-12-30 |
| **PostgreSQL** | ❌ **未装** | — |
| **Redis** | ❌ **未装** | 无 systemd 服务、无容器、无进程 |

> ⚠️ **`mysql8` 容器与本次迁移无关，不要动它。** 它监听在 `192.168.123.2`（内网地址），不自启，属于另一个业务。**Cloudreve 迁移需要另起一个独立的 MySQL 实例**（见下方建议）。

### B.6 已占用端口（摸查项 ⑤）

| 端口 | 监听地址 | 占用者 |
| --- | --- | --- |
| **80** | `0.0.0.0` / `[::]` | **nginx** |
| **443** | `0.0.0.0` / `[::]` | **nginx** |
| 22 | `0.0.0.0` / `[::]` | sshd |
| 3000 | `0.0.0.0` | PM2（`user-app`，Node） |
| 1337 | `0.0.0.0` | Node（`strapi-app`） |
| 3306 | `192.168.123.2` | Docker `mysql8`（**仅内网**） |
| 53 | `127.0.0.53/54` | systemd-resolved |

**5212、3307、6379 全部空闲** —— Cloudreve 迁移可直接沿用 A 的端口方案，无需改动现有服务。

### B.7 是否装了 nginx 与现成站点（摸查项 ⑥）

**nginx 1.24.0（Ubuntu），已装并 active**，80/443 都在跑。配置在 `/etc/nginx/conf.d/qzyun.cn.conf`（另有 `no-ssl.default.conf`、`templates/`）。

| 域名 | 后端 | 说明 |
| --- | --- | --- |
| **`panel.qzyun.cn`** | `127.0.0.1:3000`（upstream `user_app`） | PM2 托管的 Node 站 |
| **`strapi.qzyun.cn`** | `127.0.0.1:1337`（upstream `strapi_app`） | Strapi CMS |
| **`www.qzyun.cn`** | — | 静态/站点根 |
| `_`（default_server） | — | 80/443 默认兜底 |

**关键：B 上没有 `pan` 子域的任何站点配置** —— 意味着给 Cloudreve 新建一个 `pan.qzyun.net` 的 server 块是**零冲突**的，只需新增一个配置文件。

> **域名体系一致**：A 的站是 `pan.qzyun.net`，B 已经在服务 `panel.qzyun.cn` / `strapi.qzyun.cn` / `www.qzyun.cn` —— 同一批人维护的 qzyun 域名族，DNS 与证书的运维路径是通的。

### B.8 同机其它服务（仅记录，未触碰）

| 服务 | 形态 | 端口 |
| --- | --- | --- |
| nginx | systemd | 80、443 |
| PM2（`user-app`） | 裸进程 | 3000 |
| `strapi-app` | 裸进程 | 1337 |
| `mysql8` | Docker | `192.168.123.2:3306` |
| docker / containerd / sshd | systemd | — |

`/opt/projects/` 下：`strapi-app`、`user-app`、`www.qzyun.cn`（共 1.9 GB）。

### B.9 一个重要的地址澄清

**`60.214.102.28` 不是另一台机器，它就是 B 服务器自己的第二个网卡 IP。**

B 的网卡 `eth0` 同时挂着两个地址：

| 地址 | 网段 |
| --- | --- |
| **`120.220.76.88/24`** | 与 A（`120.220.76.26`）同段 ← **迁移走这个** |
| `60.214.102.28/24` | 另一个公网段 |
| `192.168.123.2/24` | 内网（eno2） |
| `172.17.0.1` / `172.18.0.1` | Docker 网桥 |

> 这解释了上一轮的困惑：早期用 `60.214.102.28` 探测时端口全超时（该地址的入站策略不同/未开放），而 `120.220.76.88` 一直可达。**两者是同一台机器。**
> 上一轮我对 `60.214.102.28` 只做过端口探测，**从未登录、未尝试认证、未传数据**。

### B.10 A ↔ B 内网直通（已双向实测）

| 方向 | 结果 |
| --- | --- |
| **A → B** | `ping` **0.086 / 0.101 / 0.128 ms**；`nc` B:22 **succeeded** |
| **B → A** | `ping` **0.139 / 0.108 ms**，0% 丢包；`nc` A:22 **succeeded** |
| 对比：构建机 → B | **211 ms** |

**两机同处 `120.220.76.0/24`，互 ping 0.1 ms 级，双向 SSH 端口直通。** 迁移的数据通道必须走内网，**不经过构建机中转、不落第三方**。

---

## 迁移可行性判断

### 1. 迁移体量：小，且已决定剪裁到最小

**用户已确认：`tasks` 与 `audit_logs` 两张表全部不迁。**

| 项目 | 体积 | 说明 |
| --- | --- | --- |
| 数据库（全量） | 2.32 GB | 其中 `tasks` 987 MB + `audit_logs` 958 MB = **84%** |
| **数据库（剪裁后）** | **≈0.4 GB** ✅ | **实际要搬的量** |
| **用户文件** | **0 字节** | local 策略 0 实体，文件 100% 在对象存储 |
| 本机数据目录 | 47 MB | 头像 2.2 MB + `statics-bk` 45 MB（可丢，不必搬） |
| 二进制 | 92 MB | fork 版本另出（amd64） |

**结论：真正要过网的数据约 0.4 GB。** 对比 155 那台（4.8 GB 库），A 的迁移量只有它的 **1/12**。以 A→B 内网 0.1 ms 延迟的带宽，**传输本身是分钟级乃至秒级的事**。

> 剪裁带来的取舍（用户已知悉并接受）：丢失历史操作审计日志（146 万行）与历史任务记录（62 万行）。**用户、文件、实体、分享、策略、组等全部业务数据完整保留。**

### 2. 架构：完全一致，无跨架构问题

| 项 | A | B | 结论 |
| --- | --- | --- | --- |
| 架构 | **x86_64** | **x86_64** | ✅ **一致** |
| OS | Ubuntu 24.04 LTS | Ubuntu 24.04.3 LTS | ✅ 同代 |
| 内核 | 6.8.0-31 | 6.8.0-90 | ✅ 同系列 |
| CPU | 40 核 | 40 核（Xeon E5-2680 v2） | ✅ 一致 |

**直接使用 fork 的 `linux_amd64` 产物即可**，无需为 B 单独构建。

### 3. B 的空间与资源够不够：**绰绰有余**

| 资源 | B 现状 | 本迁移需要 | 余量 |
| --- | --- | --- | --- |
| **磁盘** | **可用 203 GB** | ≈2 GB（二进制 92 MB + 剪裁库 0.4 GB + MySQL 数据 + 备份 + 余量） | **≈100 倍富余** |
| **内存** | 总 62 GiB，available **56 GiB** | ≈2.5 GB（Cloudreve 1.7 G + MySQL 1 G + Redis） | **≈22 倍富余** |
| **CPU** | 40 核，负载 0.03 | 峰值 2–4 核 | 几乎全程闲置 |
| **端口** | 5212 / 3307 / 6379 **全空闲** | 正好用这三个 | **零冲突** |
| **Docker** | 已装 29.1.2 | 需要 | ✅ 就绪 |

**判断：B 的资源远超需求，磁盘和内存都不是瓶颈。** 甚至可以同时保留"新旧两套库 + 历史全量 dump"而毫无压力 —— 意味着**剪裁虽然能做，但即使临时决定不剪裁也放得下**（只是传输慢些）。

### 4. B 上那套 Pro：**不存在，无需抉择**

摸查结果（进程 / 容器 / systemd 单元 / 文件目录 **四项全空**）—— **B 上从来没有 Cloudreve**。

因此本轮不存在"留着还是清掉"的问题：

- ❌ 不需要清理任何 Cloudreve 数据
- ❌ 不存在覆盖生产数据的风险
- ✅ 直接新建一套即可，是**最干净的迁移目标**

> 唯一需要留意的邻近服务：Docker 里有个 `mysql8` 容器（`192.168.123.2:3306`，Restart=no，885 MB）**与本迁移无关，不要动它**。Cloudreve 要另起独立的 MySQL 实例（新容器/新端口/新库名）。

### 5. 迁移方式建议：**沿用「并行新环境 + nginx 一行切换」，本次条件比上次更好**

上次（155）用的就是这套。本次**理由更强**，因为 B 是一台干净的、资源富余的、同架构同内网的空机器：

| 优势 | 本次具体体现 |
| --- | --- |
| **回滚只需一行** | B 的 nginx 把 `pan.qzyun.net` 的 `proxy_pass` 从旧地址改成新端口即可，秒级回滚。**注意本次是跨机切换：回滚 = DNS/nginx 指回 A** |
| **A 全程不停机** | 新环境在 B 上用 A 的库副本启动，A 照常服务，**只在最终 dump 那一刻需要短暂停写**（剪裁后 0.4 GB，停写窗口可控制在分钟级） |
| **内网直传，不经第三方** | A↔B 同 /24、0.1 ms、SSH 直通 —— 0.4 GB **几乎瞬间传完**，不必中转构建机 |
| **B 零冲突** | 端口全空、无 Cloudreve、nginx 无 pan 站点 → 可自由选端口与目录 |
| **两套环境可对账** | 并行期用真实账号登录 B 侧核对列表、下载 302、分享、S3 读写 |

**建议的执行顺序（供决策，本轮未执行任何一步）**：

1. ~~补齐 B 侧摸查~~ ✅ **本轮已完成**
2. **出 amd64 fork 二进制/镜像**（4.23.8，确认 `linux_amd64` 产物）。
3. **A 侧停写窗口内**做一次一致性 dump（`mysqldump --single-transaction`），**排除 `audit_logs`、`tasks`**。
4. **通过 A→B 内网直传**（`scp` 或 `nc`）dump 到 B 的新目录。
5. **B 起新 compose**：新目录（如 `/opt/cloudreve-fork`）、新端口、**新 MySQL 实例**、新库名 `cloudreve`。
6. 跑迁移脚本：`db_version_4.23.8` 标记 + 补 `AUTO_INCREMENT` 值（**注意下面是 MySQL 的坑**）。
7. **B 的 nginx 加一个测试 server 块**（或 hosts 直连新端口）旁路验收：登录、列表、上传、下载 302、分享、S3 读写。
8. **验收通过后切 DNS**：把 `pan.qzyun.net` 的 A 记录从 `120.220.76.26` 指向 `120.220.76.88`（或先在 B 的 nginx 加该域名的 server 块）。**切 DNS 有 TTL 影响，比 nginx 改一行更需预留时间**。
9. 稳定后再停 A 侧容器、回收空间。

**A→B 迁移特有的三个坑（比 155 那次多）**：

| # | 坑 | 说明与对策 |
| --- | --- | --- |
| 1 | **MySQL 5.7 → fork** | 155 的经验是 **PostgreSQL**，`setval` 那套**不能照搬**。MySQL 无 sequence，自增靠 `AUTO_INCREMENT` —— 要改用 **`db_version` 标记 + 校验各表 `AUTO_INCREMENT`**。此条必须单独验证 |
| 2 | **字符集 `latin1`** | A 的库是 `latin1_swedish_ci`，fork 建表默认 `utf8mb4`。直接 dump/restore **可能在中文用户名、文件名、分享标题上乱码**。导入后必须抽查中文数据完整性，必要时做 charset 转换 |
| 3 | **跨机器切换 = DNS** | 155 那次是同一台机内改 nginx 端口，秒级回滚；**本次 A、B 是两台机器**，切换靠 DNS。回滚同样靠 DNS，**但受 TTL 约束**。建议：先把 `pan.qzyun.net` 的 TTL 调低（如 60s），观察一天后再切；或让 B 直接承接该域名（B 的 nginx 已有 qzyun 域名族，加站点零冲突） |

### 6. 需要用户确认的事

1. ✅ **`B_SSH` 密钥已授权并成功登录**（`root`）。
2. ✅ **两张表剪裁**：已按用户决定写入本报告（`tasks` + `audit_logs` 不迁）。
3. ⏳ **切换方式**：是把 `pan.qzyun.net` 的 DNS 指向 B，还是在 B 上新建站点后改 A 的 nginx 指向 B？**跨机器必然涉及 DNS，建议先降 TTL。**
4. ⏳ **停机窗口**：剪裁后 dump 只需分钟级停写，确认可接受的时间段。
5. ⏳ **A 的数据库密码与 license 相关变量建议轮换**（见合规性附注：曾在工具输出中意外暴露）。

---

## 附：本次执行的合规性

- ✅ **两台服务器全程只读**：只用 `ps` / `ls` / `cat` / `ss` / `df` / `du` / `find` / `docker ps|inspect|stats|logs` / `systemctl` / `nginx -v` / `lscpu` / `free` / `ip` / `pgrep` / MySQL 只读 `SELECT` / `ping` / `nc -z`。
- ✅ 数据库只做 `COUNT(*)`、`information_schema` 查询、`SHOW COLUMNS`；**未导出、未写入、未 ALTER、未建删任何对象**。
  - 注：为在 A 的 MySQL 容器内执行只读查询，曾向容器写入临时 `.sql` 文件（`/tmp/qN.sql`），查询完即删除 —— **不属于数据库写入**。
- ✅ 配置只输出键名与形态；`SessionSecret`、`HashIDSalt`、`CR_LICENSE_KEY`、`CR_HEXOR_DATA`、`ACR_*`、数据库密码**全部脱敏或未读取**。
- ✅ **未修改任何账号密码、未装任何包、未重启任何服务、未停止或删除任何进程/容器**。
- ✅ **B 侧未触碰任何现有服务**：`mysql8` 容器、PM2、`strapi-app`、`user-app`、nginx 配置**只读不写**，未写入任何文件。
- ✅ **未做任何口令尝试**（两台都是）：`A_SSH` 为密码登录属既定授权；B 侧只用 `B_SSH` 密钥 + `root`，`ubuntu`/`debian`/`admin` 各试一次确认被拒后即停止。
- ✅ 密钥只以 600 权限临时文件存在，用后删除（`shred -u` / `rm`）；**未打印、未进 git、未写入报告**。

### 两处需要说明的历史事项

1. **`60.214.102.28` 不是另一台机器，它就是 B 服务器自己的第二个网卡 IP**（`eth0` 同时挂 `120.220.76.88/24` 与 `60.214.102.28/24`）。
   - 早期按该地址探测时端口全部超时（该地址入站未开放），`120.220.76.88` 一直可达 —— 两者是同一台机器，因此不存在"碰错机器"的问题。
   - 当时**仅做网络层端口探测，从未登录、未尝试认证、未传任何数据**。

2. ⚠️ **A 侧一次意外的凭据外泄**：检查容器环境变量时，脱敏正则未覆盖 `CR_CONF_Database.Password` 与 `CR_HEXOR_DATA` / `ACR_OFFLINE_LICENSE` 的完整值，导致这些值出现在**我自己的工具输出**中。
   - **未写入本报告、未进 git、未提交到分支**（已用 `grep` 校验报告中零匹配）。
   - **建议迁移完成后轮换 A 的数据库密码与 license 相关变量。**
