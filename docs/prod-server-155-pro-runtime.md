# 生产服务器 155.117.85.143 —— Cloudreve Pro 运行形态梳理

> 实测日期：2026-11-07 00:32–00:35 UTC　|　账号：`root`（密钥 `SSH_PROD_155`，ED25519 无口令）
> 性质：**全程只读**（`ps` / `ls` / `cat` / `ss` / `df` / `du` / `docker inspect` / `docker logs` / `psql SELECT`）
> 未改动任何配置、未重启任何服务、未写入数据库、未安装任何软件包、未修改任何账号密码。

---

## 0. 一句话结论

**登录成功。** `155.117.85.143` 上用 `root` 账号配 `SSH_PROD_155` 密钥可以正常登录，其余四个常见账号（`ubuntu`/`admin`/`debian`/`cloudreve`）被拒 —— 说明服务器端只放了 root 的授权，不是密钥不匹配。

这台机器跑的是 **Cloudreve Pro v4.18.0**，形态是 **Docker Compose 三容器**（Cloudreve + PostgreSQL 17 + Redis），**不是 systemd 托管**，**不是 SQLite**。库里有 630 个用户、72.8 万个文件，库总大小 4.8 GB，其中 `audit_logs` 独占 2.1 GB。

**能直接作为 fork 宿主，但建议用「新起一套并行」而不是原地替换**，理由见第 5 节。

---

## 1. 登录验证

| 账号 | 结果 |
| --- | --- |
| `root` | ✅ **登录成功**（`whoami` = root，uid 0） |
| `ubuntu` | ❌ `Permission denied (publickey)` |
| `admin` | ❌ 同上 |
| `debian` | ❌ 同上 |
| `cloudreve` | ❌ 同上 |

- 密钥形态：ED25519 256 位，**无口令**，`ssh-keygen -y` 可正常解析（排除「格式错」和「带口令」两种失败情形）。
- 只用这一把密钥认证，**未做任何口令尝试、未做字典**。
- 服务端 `/root/.ssh/authorized_keys` 有 3 条授权；另外装了 fail2ban（jail：`sshd`、`3x-ipl`）。
- 登录后立即把私钥从临时文件中清除（`shred -u`），未写入任何文件或日志。

---

## 2. 进程形态

Cloudreve **不是裸进程跑在宿主上**，而是跑在 Docker 容器里；宿主 `ps` 看到的 `cloudreve` 进程就是容器内的主进程（PID 3902115，已运行 3 天 12 小时）。

| 项 | 值 |
| --- | --- |
| 二进制 | 容器内 `./cloudreve`，工作目录 `/cloudreve` |
| 二进制来源 | 宿主 `/opt/cloudreve/cloudreve_v4.18.0`（单文件，91,611,320 字节，mtime 2026-08-06），以**文件挂载**方式映射到容器内 `/cloudreve/cloudreve` |
| 备份额 | 同目录还有 `cloudreve_v4.18.0.bk`（同大小，mtime 2026-07-23） |
| 运行用户 | 容器内 **root（uid 0）** —— 注意不是专门的低权限用户 |
| 启动参数 | 无参数（`Cmd` 为 null），入口脚本 `sh ./entrypoint.sh` |
| 伴随进程 | `aria2c --enable-rpc --save-session /cloudreve/data --listen-port 6888 --dht-listen-port 6888`（由 `CR_ENABLE_ARIA2` 启用，离线上传用） |
| CPU / 内存 | 实测 `docker stats`：CPU **4.31%**，内存 **1.13 GiB / 7.7 GiB**；`ps` 侧 `%MEM` 13.4% |
| 重启次数 | **55 次**（`RestartCount=55`），当前 `ExitCode=0`、`OOMKilled=false`、Running=true |

> 55 次重启值得注意：不是 OOM 崩溃（OOMKilled 为 false，退出码 0），更像是有过多次手动 `restart` 或宿主重启历史上累积。当前状态健康。

---

## 3. 服务托管

**Docker Compose，不是 systemd 单元。**

- compose 文件：`/opt/cloudreve/docker-compose.yml`，project 名 **`cloudreve`**，网络 `cloudreve_cloudreve-net`（bridge）。
- 容器名与角色：

| 容器 | 镜像 | 状态 | 端口 |
| --- | --- | --- | --- |
| `cloudreve-service` | `cloudreve/cloudreve:latest`（image id `f7a4641…`，构建于 2026-07-15） | Up 3 days | **`0.0.0.0:5212->5212/tcp`**、6888/udp |
| `cloudreve-postgresql` | `postgres:17` | Up 5 weeks | 5432/tcp（仅容器网络内） |
| `cloudreve-redis` | `redis:latest` | Up 5 weeks | 6379/tcp（仅容器网络内） |

- 重启策略：三容器均 `restart: unless-stopped`。
- **注意**：`image: cloudreve/cloudreve:latest` 但实际运行的是挂载进去的 **v4.18.0 二进制** —— 镜像本身只是提供了 entrypoint、运行时依赖和 aria2c；**升级 Pro 版本实际是换这个挂载文件**。这一点对迁移很关键。

### 挂载

| 宿主路径 | 容器内 | 用途 |
| --- | --- | --- |
| `/opt/cloudreve/cloudreve` | `/cloudreve/data` | **数据目录**（conf.ini、license、avatar、temp） |
| `/opt/cloudreve/cloudreve_v4.18.0` | `/cloudreve/cloudreve` | 可执行文件 |
| `/opt/cloudreve/database` | PG 容器 `/var/lib/postgresql/data` | 数据库数据 |
| `/opt/cloudreve/redis` | Redis 容器 `/data` | Redis 持久化 |

compose 里还写了两条 `extra_hosts`（`cloudreve.org:127.0.0.1`、`pro.cloudreve.org:127.0.0.1`），是**屏蔽 Pro 联网校验**的常见做法。

---

## 4. 配置

### 4.1 配置分发方式

配置分三层，优先级从低到高：

1. `conf.ini`（`/opt/cloudreve/cloudreve/conf.ini`，159 字节，够小说明几乎是空的）
2. **compose 的 `environment`**：`CR_CONF_Database.Type=postgres`、`.Host=postgresql`、`.User=cloudreve`、`.Name=cloudreve`、`.Port=5432`、`CR_Redis.Server=redis:6379`
3. `.env` 文件（`/opt/cloudreve/.env`，权限 644）注入 license 与缩略图开关

### 4.2 `conf.ini` 键名与形态

| 段 | 键 | 值 |
| --- | --- | --- |
| `[System]` | `Debug` | 布尔（已脱敏） |
| | `Mode` | **`master`** ← License 模式 |
| | `Listen` | **`:5212`**（容器内监听全部接口，靠 Docker 端口映射对外） |
| | `SessionSecret` | 已设置（脱敏） |
| | `HashIDSalt` | 已设置（脱敏） |

### 4.3 `.env` 与容器环境变量（只列键名）

`.env`：`CR_HEXOR_DATA`、`CR_LICENSE_KEY`、`ACR_LICENSE_KEY`、`ACR_OFFLINE_LICENSE` —— **均为已设置状态（值脱敏，未读取、未打印）**。

容器额外注入：`CR_ENABLE_ARIA2`、`CR_SETTING_DEFAULT_thumb_ffmpeg_enabled`、`CR_SETTING_DEFAULT_thumb_vips_enabled`、`CR_SETTING_DEFAULT_thumb_libreoffice_enabled`、`CR_SETTING_DEFAULT_media_meta_ffprobe`、`CR_SETTING_DEFAULT_thumb_libraw_enabled`。

> 与我们 fork 实例一致：license 走 `CR_LICENSE_KEY` + `CR_HEXOR_DATA` 从 DB 复用，不重复联网。这台是 **master 模式**，每次启动必须有 license。

### 4.4 License 文件

`/opt/cloudreve/cloudreve/license`，1800 字节，**非 JSON 格式**（内容为加密串，未解析、未输出）。配合 master 模式，属于「已授权」。

### 4.5 站点 URL

nginx 里 Pro 的对外域名是 **`pan.cdn.ad`**（见 §7）。配置文件内未发现独立写死的站点 URL 键。

---

## 5. 数据库

| 项 | 值 |
| --- | --- |
| 类型 | **PostgreSQL 17.10**（`postgres:17` 容器，Debian 17.10-1.pgdg13+1） |
| 连接 | 容器网络内 `cloudreve-postgresql:5432`，库名/用户均 `cloudreve`，**`POSTGRES_HOST_AUTH_METHOD=trust`（免密）** |
| 数据目录 | `/opt/cloudreve/database`，**5.0 GB** |
| 库逻辑大小 | **4,828 MB（≈4.8 GB）** |
| 表数 | 27 张 |

### 关键表与规模（只读 `pg_total_relation_size`）

| 表 | 大小 |
| --- | --- |
| `audit_logs` | **2,100 MB**（最大，占库 43%） |
| `tasks` | 1,165 MB |
| `metadata` | 866 MB |
| `files` | 357 MB |
| `entities` | 243 MB |
| `file_entities` | 62 MB |
| `direct_links` | 24 MB |
| `fs_events` | 880 kB |

### 行数（只读 count）

| 指标 | 数量 |
| --- | --- |
| users | **630** |
| files | **727,927** |
| entities | **1,094,603** |
| shares | **347** |
| groups（用户组） | 5 |
| settings | 237 |

> 与团队记忆 `ND6W9WDOJRRfV9USehAYZ` 里那次 2.6 GB dump 迁移是**不同的一台**（那次是别处的生产库），但表结构与量级同源 —— 迁移脚本的两步（补 `db_version` 标记 + 全序列 `setval`）依然适用，且这次可直接连库操作，不必走 dump 解码。

---

## 6. 版本与构建信息

| 项 | 值 |
| --- | --- |
| **版本** | **4.18.0** |
| 二进制时间 | mtime 2026-08-06（备份份 2026-07-23） |
| 构建环境 | 日志里的源码路径 `/home/aaronliu/vsts/_work/1/s/...` → **Azure DevOps 流水线构建**（官方 Pro 构建机） |
| 镜像 | `cloudreve/cloudreve:latest`，构建于 2026-07-15 |
| License | master 模式，`/cloudreve/data/license` 存在（1800 B），`.env` 中 `CR_LICENSE_KEY`/`CR_HEXOR_DATA` 已设置 |
| 运行时长 | 当前容器 3 天 12 小时（2026-11-03 03:37 起） |

---

## 7. 文件存储

### 7.1 数据目录（`/opt/cloudreve/cloudreve`）

| 子项 | 大小 |
| --- | --- |
| `avatar/` | 1.5 MB（35 个头像） |
| `temp/` | 6.2 MB |
| `conf.ini` | 4 KB |
| `license` | 4 KB |

**本地数据目录很小** —— 说明用户文件基本不落在本机，而是走对象存储。

### 7.2 存储策略（14 条，只读查询）

| id | 类型 | 名称 | 服务端 |
| --- | --- | --- | --- |
| 1 | **local** | Default storage policy | — |
| 2 | s3 | Storage-HK | `hkong.bscstorage.com` |
| 3 | ks3 | 云盘 | `s3.1yunpan.com` |
| 4 | s3 | A | `oss.kuocai.net/s3` |
| 5 | s3 | 美国储存 | `s3.1yunpan.com` |
| 6 | s3 | KQS | `oss.kuocai.net/s3` |
| 7 | s3 | Q | `http://155.117.85.143:5244/` ← **本机 OpenList** |
| 8 | s3 | K | `oss.kuocai.net/s3` |
| 9 | s3 | A | `dl.kuocait.xyz/s3` |
| 10 | s3 | KOS | `dl.kuocait.xyz/s3` |
| 11 | s3 | 美国储存 | `dl.kuocait.xyz/s3` |
| 12 | s3 | 美国储存 | `s3.1yunpan.com` |
| 13 | s3 | Storage-US | `s3.1yunpan.com` |
| 14 | s3 | 空投 | `up.cdn.ad/api/s3` |

**结论：14 条策略里 13 条是对象存储（S3/KS3），只有 1 条 local 且是默认兜底。** 有 5 个用户组（`groups=5`），组与策略的绑定在 `group_storage_policies` 表。

- 迁移意义：**绝大多数用户文件不在本机**，迁移的核心是 **PostgreSQL（4.8 GB）+ 策略配置 + S3 凭据**，而不是搬 PB 级文件。这大幅降低了迁移成本。
- 策略 7 指向本机的 OpenList（`:5244`），说明有一路文件是通过本机 OpenList 中转的对象存储。

---

## 8. 反向代理

**nginx（Docker 容器 `nginx:alpine`）独占宿主 80 与 443。**

- 配置文件：`/opt/nginx/nginx.conf`（挂载进容器），静态页 `/opt/nginx/html`，证书 `/opt/nginx/certs/`。
- 监听：`0.0.0.0:80`、`0.0.0.0:443`（容器 `nginx` 用 host 网络，`ss` 直接看到 nginx 进程）。

| 域名 | 后端 | 证书文件 |
| --- | --- | --- |
| **`pan.cdn.ad`** | `127.0.0.1:5212` ← **Cloudreve Pro** | `/etc/nginx/certs/cdn.ad.pem` |
| `misub.kdns.fr` | `127.0.0.1:8500`（misub 订阅面板） | `misub.kdns.fr.fullchain.cer` |
| `ai.cdn.ad` | `ai_backend` | `cdn.ad.pem` |
| `send.cdn.ad` / `kt.cdn.ad` | `127.0.0.1:8010`（next-server） | `cdn.ad.pem` |
| `ws.cdn.ad` | 多处（`149.104.5.157:8888`、`172.17.0.1:8377`） | — |
| `cdn.ad` | — | `cdn.ad.pem` |

### 8.1 Pro 是否对外

**Pro 的 `:5212` 既有 nginx 反代，也直接映射到了 `0.0.0.0:5212`** —— 也就是说绕过 nginx 直接访问 `http://155.117.85.143:5212` 也是可达的。这是一个可收紧的点（尤其证书到期后走 http 明文）。

### 8.2 证书现状（**已核实，全部过期**）

| 文件 | 域名 | 到期 |
| --- | --- | --- |
| `cdn.ad.pem` | `*.cdn.ad` | **2026-10-21（已过期）** ← pan.cdn.ad 用它 |
| `misub.kdns.fr.fullchain.cer` | `misub.kdns.fr` | **2026-09-09（已过期）** ← 上一轮探测看到的就是这张 |
| `panel.qzyun.net` / `strapi.qzyun.net` / `acme.qzyun.net` | — | 2026-08-04（均过期） |

> 上一轮报告说「证书 2026-09-09 过期，CN=misub.kdns.fr」—— **事实正确但结论需要修正**：那张证书属于 misub 面板，Cloudreve Pro 走的是 `pan.cdn.ad`，它用的是 `cdn.ad.pem`，**到期日是 2026-10-21，同样已过期**。也就是说**整个 443 目前对所有站点都是过期证书状态**，不止一个域名。目录里还有 acme（`/opt/nginx/acme/account.conf`），说明本可以用 ACME 续，但显然没有自动化在跑。

---

## 9. 日志

- **日志走 `docker logs`（stdout）**，无独立日志文件，无 logrotate 控制（靠 Docker 默认 json-file 驱动）。
- 最近 2000 行中：**`[Error]` / `[Warning]` 计数 = 0**。日志全是正常的 `[Incoming]` 请求行（201/204/207/302/404）与 `Audit log: flushed N logs to database`。
- 流量是真实的：每秒数条请求，说明**这是有活跃用户在用的生产站**。
- `audit_logs` 表 2.1 GB 也是这种活跃度的副产物（每次审计写入都进库）。

> 日志里含源码路径 `/home/aaronliu/vsts/_work/1/s/...` —— 可以确认是官方 Pro 构建产物，不是二开。

---

## 10. 系统底座

| 项 | 值 |
| --- | --- |
| OS | **Ubuntu 24.04.4 LTS**（Noble） |
| 内核 | `6.8.0-142-generic` |
| 架构 | x86_64（**注意：Baixiong 是 arm64，跨架构**） |
| 主机名 | `elaborate-extreme` |
| 运行时长 | **40 天 23 小时**（宿主自 2026-09 下旬起未重启） |
| CPU | **4 核** |
| 内存 | 总量 **7.7 GiB**，已用 2.4 GiB，available 5.3 GiB，**无 swap** |
| 磁盘 `/` | `ext4` 193 GB，**已用 68 GB（36%）**，**剩余 125 GB** |
| 负载 | 0.67 / 0.41 / 0.30（4 核，负载轻） |
| 虚拟化 | qemu-guest-agent 在跑 → KVM 虚拟机 |

**迁移容量判断：剩 125 GB。** 现有数据 = PostgreSQL 5.0 GB + `/opt/cloudreve` 其他内容（其中 `cloudreve.tar.zst` 327 MB、`dev` 11 GB、`kuaichuan` 3.1 GB 与本业务无关）。**放一套 fork 实例（二进制 ~92 MB + 库副本 ~5 GB + Node 前端构建）绰绰有余，磁盘不是瓶颈。内存 7.7G 跑双实例也够（Pro 当前占 1.13G，PG 221M，Redis 7.5M，合计约 1.4G）。**

---

## 11. 同机其它服务

| 服务 | 形态 | 端口/说明 |
| --- | --- | --- |
| **cloudreve-service** | Docker | 5212（Cloudreve Pro） |
| **cloudreve-postgresql** | Docker | 5432（仅内网） |
| **cloudreve-redis** | Docker | 6379（仅内网） |
| **openlist** | Docker | **5244–5245 公网**（`openlistteam/openlist`，被 Cloudreve 策略 7 引用） |
| **nginx** | Docker（host net） | **80、443 公网** |
| misub | Docker | `127.0.0.1:8500` |
| subconverter | Docker | 25500（内网） |
| frps | Docker | 宿主 `:7000`（内网穿透服务端） |
| next-server (v1) | 宿主裸进程（root，PID 3724089） | **`:8010` 公网** |
| sing-box | 宿主裸进程 | `:8080` 公网 + 若干 127.0.0.1 端口 |
| advpn / adguardvpn-cli | 宿主裸进程 | 127.0.0.1 多个端口 |
| kuaichuan / kuaichuan-staging | **systemd 单元** | 另有独立业务（`/opt/kuaichuan` 3.1G） |
| fail2ban | systemd | jail：sshd、3x-ipl |
| docker / containerd | systemd | — |

**没看到 MySQL、Redis 除容器外的独立实例。** 宿主上的独立 Redis 不存在，Cloudreve 用的是容器内那个。

> 这台机器是**多用途混装**：Cloudreve Pro 生产站 + OpenList + 订阅转换 + 代理穿透（sing-box/frps/advpn）+ 另一套业务（kuaichuan）。不是「专机专用」。

---

## 12. 结论：能否直接升级为 fork 宿主？

**技术上可以，但建议新起一套并行部署，跑通验收后再切域名 —— 不建议原地替换。**

### 支持「直接升级」的因素

1. **数据库完全兼容**：PG 17，表结构与我方 fork 4.23.8 一致（`users`/`files`/`entities`/`shares`/`storage_policies`/`group_storage_policies` 全在）。团队已验证过同源生产库 → 4.23.8 的全量迁移（记忆 `ND6W9WDOJRRfV9USehAYZ`），且这次可以直接连库操作，比那次 dump 迁移更稳。
2. **升级入口干净**：Pro 版本由挂载的单文件二进制决定（`/opt/cloudreve/cloudreve_v4.18.0`），换文件 + 重启容器即可切换，反向操作即可回滚。
3. **资源充足**：剩 125 GB 磁盘、4 核、5.3 GB 可用内存，双实例并行完全跑得动。
4. **文件不在本机**：13/14 条策略是对象存储，不用搬文件，迁移就是把库 + 策略配置搬过去。

### 反对「原地替换」的因素

1. **这是有真实活跃用户的生产站**（630 用户、72.7 万文件、每秒数条请求）。原地换二进制出错就是停机事故，且库一旦被 4.23.8 的迁移脚本改过，回滚到 4.18 需要回滚整个库备份。
2. **已有 55 次重启历史**，说明这个容器不太稳；在它身上做原地升级，出问题难判断是升级导致还是固有毛病。
3. **多用途混装**：同机还有 OpenList、代理、另一套业务。升级出错波及其他服务。
4. **证书全线过期**，说明这台机的运维（续签自动化）目前是断的 —— 在这种状态下做原地升级风险叠加。
5. **跨架构**：这台是 x86_64，团队构建机/Baixiong 是 arm64，**ARM 构建的 fork 二进制在这里跑不了，必须出 amd64 版本镜像/二进制**（仓库已有多平台 GoReleaser 配置，需确认产出包含 linux/amd64）。

### 建议路径（供决策，非本次执行）

1. **出 amd64 版 fork**（4.23.8，仓库 GoReleaser 已支持多平台，确认有 `linux_amd64`）。
2. **另起一套 compose**（新目录如 `/opt/cloudreve-fork`，新容器名、新端口如 5213、新库 `cloudreve_fork`），**从现网库做一次一致性备份恢复到新库**，跑迁移脚本两步（`db_version` 标记 + 全序列 `setval`）。
3. **nginx 加一个测试域名**（或新 server 块）指向 5213，用现网真实流量旁路验证：登录、列表、上传、下载 302、分享、对象存储读写。
4. 验收通过后，把 `pan.cdn.ad` 的 `proxy_pass` 从 5212 改到 5213（一行改动，可秒回滚），观察一天。
5. 稳定后再停旧容器，回收空间。
6. **顺带修证书**：`cdn.ad.pem` 已过期一个多月，建议在切域名前把 ACME 续签恢复。

---

## 13. 附：本次执行的合规性

- ✅ 全程只读：只用 `ps`/`ls`/`cat`/`ss`/`df`/`du`/`find`/`docker inspect`/`docker logs`/`docker stats`/`psql SELECT`/`which`/`uname`。
- ✅ 数据库只做 `count(*)`、`pg_size_pretty`、`information_schema` 查询；**未导出、未写入、未 ALTER**。
- ✅ 配置只输出键名与形态；`SessionSecret`、`HashIDSalt`、`CR_LICENSE_KEY`、`CR_HEXOR_DATA`、`ACR_*`、license 文件内容**全部脱敏或未读取**。
- ✅ 私钥只从环境变量读入临时文件（600 权限），用后 `shred -u` 删除；未打印、未写入报告、未进 git。
- ✅ 未修改任何账号密码、未装任何包、未重启任何服务。
