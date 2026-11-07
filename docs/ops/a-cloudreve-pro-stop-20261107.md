# A 服务器 Cloudreve Pro 停止报告

> 执行日期：2026-11-07 15:27 CST
> 服务器：A = `120.220.76.26`（Ubuntu 24.04，主机名 `asd`）
> 操作性质：**只停止 Cloudreve 三个容器，不删除任何东西**；不触碰机上其他任何服务与进程。
> 凭据：本报告只记录键名与存在性，不含任何密码、密钥、license 值。

---

## 0. 一句话结论

**A 上的 Cloudreve Pro 4.18.0 已停止，用户站点 `pan.qzyun.net` 不受影响、照常服务**——CDN 回源已确认指向 B（`120.220.76.88`），停止前后经真实域名访问均为 HTTP 200，返回内容与 B 逐字节一致。停止只做了 `docker compose stop`：三个容器仍在、镜像仍在、13 GB 数据库原封不动，**回滚一条命令即可起回**。A 上其余业务（nginx、另一套 mysql/redis、PM2 Node 业务、Python 服务）全部未动、运行正常。

---

## 1. 停止前状态快照

### 1.1 运行形态

| 项 | 值 |
| --- | --- |
| 版本 | Cloudreve **Pro 4.18.0**（镜像 tag 写死 `cloudreve/cloudreve:4.18.0`） |
| 管理方式 | **Docker Compose**，项目目录 `/opt/cloudreve`，文件 `docker-compose.yml` |
| systemd 单元 | **无**（`systemctl list-units \| grep -i cloudreve` 为空） |
| 容器网络 | `cloudreve_cloudreve-net`（bridge） |
| 重启策略 | 三者均 `restart: unless-stopped` |

### 1.2 三个容器

| 容器名 | 镜像 | ImageID（前 12 位） | 端口映射 |
| --- | --- | --- | --- |
| `cloudreve-service` | `cloudreve/cloudreve:4.18.0` | `864b434b318e` | `0.0.0.0:5212->5212`、6888/tcp+udp（未发布） |
| `cloudreve-mysql` | `mysql:5.7`（5.7.44） | `0b14b8e5c533` | `0.0.0.0:3307->3306` |
| `cloudreve-redis` | `redis:8.4` | `5feef86310b9` | 仅容器网络，无宿主映射 |

启动参数：

- `cloudreve-service`：`Entrypoint=[sh ./entrypoint.sh]`，`Cmd=[]`，`WorkingDir=/cloudreve`
- `cloudreve-mysql`：`--innodb_buffer_pool_size=1G --max_connections=500 --innodb_log_file_size=256M`
- `cloudreve-redis`：`redis-server`

### 1.3 数据卷（宿主机路径，全部保留）

| 宿主机路径 | 容器内 | 用途 | 体积 |
| --- | --- | --- | --- |
| `/opt/cloudreve/database` | `/var/lib/mysql` | MySQL 数据 | **13 GB** |
| `/opt/cloudreve/redis` | `/data` | Redis 持久化 | 400 KB |
| `/opt/cloudreve/cloudreve` | `/cloudreve/data` | conf.ini、avatar、temp | 47 MB |
| `/opt/cloudreve/cloudreve.run` | `/cloudreve/cloudreve` | 可执行文件（bind mount） | 92 MB |

### 1.4 停止前的配置要点（仅键名）

- `.env` 含 `CR_LICENSE_KEY`、`CR_HEXOR_DATA`、`ACR_LICENSE_KEY`、`ACR_OFFLINE_LICENSE`（均已设置，值不记录）。
- `conf.ini`：`Mode = master`、`Listen = :5212`，`SessionSecret` / `HashIDSalt` 均已设置。
- 容器环境变量：`CR_CONF_Database.*`（含密码）、`CR_CONF_Redis.Server`、`CR_ENABLE_ARIA2=1`、若干 `CR_SETTING_DEFAULT_*` 缩略图开关。

---

## 2. 停止前的流量确认（关键前置）

**结论：CDN 回源已指向 B，A 已无真实流量。** 证据四层：

### 2.1 A 侧访问日志归零

`/var/log/nginx/access.log` 中 pan 相关请求按小时统计，最后一条真实用户请求在 **14:30 前后**，此后仅剩我方探测：

| 时段 | A 上 pan 请求数 |
| --- | --- |
| 12:00 | 1,358 |
| 13:00 | 1,307 |
| 14:00–14:29 | 669 |
| **15:20 之后** | **0** |

停止前专门等待观察 90 秒：`15:21–15:27` 期间 A 上 **零请求**。

### 2.2 B 侧访问日志高速增长

`/var/log/nginx/pan.qzyun.net.access.log` 同时段按分钟统计：

| 时间 | 请求数 |
| --- | --- |
| 15:15 | 570 |
| 15:17 | 897 |
| 15:22 | 1,342 |
| 15:23 | 1,127 |
| 15:25 | 399（尚未满分钟） |

**同一时间窗内 A 为 0、B 为每分钟数百至上千** —— 流量确已完全在 B。

### 2.3 经 CDN 返回的内容与 B 逐字节一致

用可用 CDN 边缘节点 `120.220.39.217` 取首页，与 B 直连结果比对：

| 来源 | 页面大小 | 结果 |
| --- | --- | --- |
| 经 CDN | **4,235 字节** | 与 B 直连**完全一致** |
| B 直连 `120.220.76.88:443` | 4,235 字节 | 基线 |
| A 直连（停止前） | **3,830 字节** | 4.18 版本，明显更小 |

配合真实分享接口 `GET /api/v4/share/info/xY4co` 在 B 上返回 `code:0`（`新文件.txt`，owner `3223398545`），可确认 CDN 命中 B 的 4.23.8，而非 A 的 4.18。

### 2.4 关于 CDN 边缘的两个观察（与本次停止无关，但需存档）

| 边缘 IP | 访问结果 |
| --- | --- |
| `120.220.39.217`（A、B 本机 DNS 解析到） | **200** |
| `163.181.214.1`（构建机 DNS 解析到） | **522**（`error=connection_timeout`） |

**522 在本次停止之前就已存在**（开工第一次探测即为 522），与停止 A 无关，是 CDN 单节点回源异常，属 CDN 控制台侧的遗留问题（迁移报告 §10.6 已记录过同类现象）。本次停止前后该现象无变化。

---

## 3. 执行的停止操作

### 3.1 确认 MySQL 只服务于 Cloudreve（不是共用库）

```
SHOW DATABASES;
→ information_schema, cloudreve, cloudreve_verify, mysql, performance_schema, sys
```

**只有 `cloudreve` 一个业务库**（`cloudreve_verify` 是早前迁移时的验证临时库）。mysql 容器网络内只有 `cloudreve-service` / `cloudreve-redis` 两个成员，3307 端口无任何外部连接。

**机上另一套 `mysql:8.4`（`192.168.123.1:3306`）属于其他业务，与本项目无关，全程未触碰。**

因此停 MySQL 是安全的。

### 3.2 停止命令

```bash
cd /opt/cloudreve && docker compose stop --timeout 60
```

执行于 **15:27:12**，结束于 **15:28:15**，退出码 0。三个容器依次停停：

```
Container cloudreve-service Stopping
Container cloudreve-service Stopped
Container cloudreve-redis Stopping
Container cloudreve-mysql Stopping
Container cloudreve-redis Stopped
Container cloudreve-mysql Stopped
```

**未执行任何删除类命令**：没有 `rm`、`docker rm`、`docker rmi`，没有删数据目录，没有改任何配置文件。

### 3.3 容器退出码说明

| 容器 | ExitCode | OOMKilled | 含义 |
| --- | --- | --- | --- |
| `cloudreve-mysql` | **0** | false | **干净关闭**，InnoDB 正常刷盘 |
| `cloudreve-redis` | **0** | false | 干净关闭 |
| `cloudreve-service` | **137** | false | 128+9 = SIGKILL，是 60 秒超时后强杀 |

`cloudreve-service` 的 137 是 Pro 主进程对 SIGTERM 响应慢、超时被强杀的常见现象，**不是内存问题**（`OOMKilled=false`），且它本身不持有需要落盘的状态（数据全在 MySQL 与对象存储），因此不影响数据完整性。MySQL 的 ExitCode=0 才是关键——库是安全关闭的。

---

## 4. 停止后的验证

### 4.1 A 上 Pro 已停

| 检查 | 结果 |
| --- | --- |
| `docker ps -a` 三个容器 | 全部 `Exited`（容器仍在，仅停止） |
| 端口 5212 | **已释放** |
| 端口 3307 | **已释放** |
| `curl http://120.220.76.26:5212/` | **连接被拒** ✓ |
| `curl` 到 A 的 443（Host: pan.qzyun.net） | **502**（nginx 在，后端已断，符合预期） |

### 4.2 A 上其他业务零影响

| 服务 | 形态 | 停止后状态 |
| --- | --- | --- |
| nginx | systemd | **active** ✓ |
| docker / containerd | systemd | **active** ✓ |
| `mysql`（mysql:8.4） | Docker，`192.168.123.1:3306` | **Up 6 weeks** ✓ |
| `redis`（redis:8.4） | Docker，`192.168.123.1:6379` | **Up 6 weeks** ✓ |
| PM2 v7.0.4 God Daemon | 裸进程，`:8001` | 进程在，HTTP 200 ✓ |
| Node 服务 `node .output/server/index.mjs` | 裸进程，`127.0.0.1:3080` | 进程在 ✓ |
| `panel.qzyun.cn` | nginx 站点 | **307**（与停止前一致）✓ |
| `glm.qzyun.net` | nginx 站点 | **200**（与停止前一致）✓ |

监听端口核对：`80` / `443` / `22` / `8001` / `3080` / `3306` / `6379` 全部仍在，仅 `5212` 与 `3307` 消失。

进程数 596 → 607、负载 0.66 → 0.28，均在正常波动范围内。

### 4.3 B 站点不受影响（用户侧仍可访问）

| 检查 | 结果 |
| --- | --- |
| B 直连 `120.220.76.88:443` | **200** ✓ |
| 经 CDN `https://pan.qzyun.net/`（从 A 机器发起） | **200** ✓ |
| 经 CDN（从 B 机器发起） | **200** ✓ |
| 经 CDN 首页字节数 | 4,235，与 B 直连一致 ✓ |
| 真实分享 `GET /api/v4/share/info/xY4co` | `code:0`，数据完整 ✓ |
| B 停止后 30 秒新增请求 | **470 条**（持续正常服务） |
| B 的 nginx 错误日志 | 40 条错误**全部集中在 14:37:22**（服务启动瞬间），此后无新增 ✓ |

### 4.4 nginx 对 `pan.qzyun.net` 的处理

**按推荐方案：A 的整个站点配置原样保留，未做任何改动。**

- `/etc/nginx/sites-available/pan.qzyun.net.conf` 未修改，仍 `proxy_pass http://pan_backend`（`127.0.0.1:5212`）。
- 因 CDN 已不再回源到 A，该配置事实上处于闲置状态，**保留不动是最省事的做法**：万一需要回滚，只需起回容器，nginx 侧零改动即时可用。
- 现状行为：若有请求意外打到 A，会得到 **502**（而不是 404 或维护页）。这在技术上等价于「后端不可用」，不会误导用户，也不影响其他站点。
- **不需要改成维护页或 404** —— 改了反而给回滚增加一步。此项按 spec 的推荐选项执行。

---

## 5. 一句话回滚命令

```bash
cd /opt/cloudreve && docker compose start
```

**已在本次任务中实测有效**（15:29 起回验证 → 15:31 再次停止）：

| 回滚实测项 | 结果 |
| --- | --- |
| `docker compose start` | 三容器 `Started`，退出码 0 |
| 25 秒后容器状态 | 全部 `Up` |
| `curl http://127.0.0.1:5212/` | **200** ✓ |
| MySQL 数据读取 `SELECT COUNT(*) FROM cloudreve.users` | **883**（与停止前一致）✓ |

⚠️ 回滚只解决「A 起回来」。**若要让用户真正回到 A，还需把 CDN 回源地址改回 `120.220.76.26`**——注意这会让 B 上的新数据（停止后的写入）与 A 分叉，属应急手段而非日常回滚路径。

---

## 6. 最终备份

### 6.1 备份文件

| 项 | 值 |
| --- | --- |
| 路径 | **`/opt/cloudreve-stop-20261107/cloudreve_final_20261107.sql.gz`** |
| 大小 | 231,198,529 B（220 MB） |
| sha256 | `6357c314742a7cac7a579a064dfbfc82bff44b1c6ecf084d5a3dc31c56861235` |
| 解压后 | 1,536,944,633 B（1.43 GiB） |
| 目录权限 | `700` |
| 存放位置 | **持久盘**（`/opt`，非 `/tmp`） |

导出命令（`--single-transaction`，不锁表、不影响读写）：

```bash
docker exec cloudreve-mysql sh -c 'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" \
  --single-transaction --quick --routines --triggers --events \
  --default-character-set=utf8mb4 --databases cloudreve' | gzip -1 > cloudreve_final_20261107.sql.gz
```

### 6.2 备份可读性验证

| 检查 | 结果 |
| --- | --- |
| `gzip -t` 完整性 | **OK** |
| 文件头 | `-- MySQL dump 10.13 Distrib 5.7.44, for Linux (x86_64)` ✓ |
| 文件尾 | `-- Dump completed on 2026-11-07 15:19:19` ✓ |
| `CREATE TABLE` 语句数 | **30**（与库内表数一致） |
| sha256 校验文件 | 已同目录保存 `.sha256` |

### 6.3 真实可恢复性验证（不只是「能解压」）

把备份导入同实例的临时库 `stop_verify_20261107`（原库 `cloudreve` 全程未触碰），耗时 4 分 23 秒，然后逐表核对：

| 表 | A 原库 | 从备份恢复 | 一致 |
| --- | --- | --- | --- |
| users | 883 | 883 | ✓ |
| files | 383,133 | 383,133 | ✓ |
| entities | 366,095 | 366,095 | ✓ |
| shares | 1,119 | 1,119 | ✓ |
| storage_policies | 10 | 10 | ✓ |
| groups | 8 | 8 | ✓ |
| settings | 245 | 245 | ✓ |
| file_entities | 365,525 | 365,525 | ✓ |

**8 项逐表完全相等。** 验证完成后临时库已 `DROP DATABASE` 清理干净，A 上只剩原库 `cloudreve`。

> 注：早期迁移备份 `/opt/cloudreve-migration-backup-20261107/` 仍在，本次新增的这份是**停止前的最终快照**，比它更新（files 多 32 条、shares 多 3 条，是迁移后到停止前 A 仍在服务期间的新增）。

### 6.4 停止快照文件

`/opt/cloudreve-stop-20261107/stop-snapshot-20261107.txt`（权限 600）记录了：三容器的镜像与 ImageID、重启策略、端口映射、数据卷路径、管理方式、停止后的容器状态、回滚命令。

同目录另有 `before-stop.txt`（停止前三容器状态）。

---

## 7. 确认未动的其他服务

**以下服务全部未停止、未重启、未修改配置**（spec 明确要求不得动）：

| 服务 | 形态 | 处理 |
| --- | --- | --- |
| 订阅面板 / `panel.qzyun.cn` | nginx 站点 | 未动，307 正常 |
| `glm.qzyun.net` | nginx 站点 | 未动，200 正常 |
| nginx 主服务 | systemd | 未动，未 reload、未 restart |
| `mysql`（mysql:8.4，`192.168.123.1:3306`） | Docker | 未动，Up 6 weeks |
| `redis`（redis:8.4，`192.168.123.1:6379`） | Docker | 未动，Up 6 weeks |
| PM2 Node 业务（KS-MQ/QUOTA-MQ/Msg-MQ/ALIPAY-MQ，`:8001`） | 裸进程 | 未动，200 正常 |
| Python 服务（`127.0.0.1:3080`） | 裸进程 | 未动，进程在 |
| sing-box / frps / advpn / kuaichuan / misub / subconverter | 按用户要求 | **全部未触碰** |

**MySQL 5.7 的判定**：查询确认它只有 `cloudreve` 一个业务库、无其他业务连接，**是 Pro 专用**，因此可以随 Pro 一起停止。机上另一套 mysql:8.4 才是共用库的那个，未动。

---

## 8. A 上遗留的待处理项

### 8.1 数据库密码已在 build 输出中暴露 — 建议更换

- **位置**：`/opt/cloudreve/docker-compose.yml` 第 56 行，`MYSQL_ROOT_PASSWORD` **明文写死**在 compose 文件里。
- **历史**：A→B 迁移期间，规范摸查时脱敏正则未覆盖 `CR_CONF_Database.Password`，导致该值出现在工具输出中（迁移报告 A 章末附注已记录）。
- **影响**：该 MySQL 的 3307 端口**对公网发布**（`0.0.0.0:3307->3306`），root 账号允许 `%` 登录 —— 密码泄露即可直连。
- **本次状态**：容器已停，3307 已释放，**风险面暂时归零**。但只要哪天回滚把容器起回来，这个暴露就立刻恢复。
- **建议**：若要长期保留 A 的 Pro，下次起回前先改密码（compose 里同步改）；若确定不再回滚，可直接废弃该密码。本报告不记录密码值。

### 8.2 机上某配置脚本明文写着 S3 凭据

- **历史**：此前已记录的遗留问题——A 上有机上的配置脚本以明文形式存放对象存储的 access key / secret key。
- **本次状态**：脚本未读取、未修改、未删除（spec 要求不删任何东西）。
- **建议**：A 已停止服务 Cloudreve，这些 S3 凭据的实际使用方只有 B。若确认 A 不再回滚，应**轮换 S3 密钥**（在对象存储控制台重新生成，只更新 B 侧策略），并删除或加密 A 上的明文脚本。
- 补充参考：迁移报告 §11.3 记录 A/B 两侧共 10 条存储策略，其中策略 2/3/5 承载 22 万余真实文件，凭据已完整迁入 B。

### 8.3 其他可顺带清理的项（本次均未执行）

| 项 | 位置 | 说明 |
| --- | --- | --- |
| 历史 dump 与镜像包 | `/opt/cloudreve/` | `mirror.tar.gz` 535 MB、`cloudreve.sql` 260 MB、`cloudreve_backup_20260725_175018.sql` 287 MB、两个 `database*.tgz` 约 176 MB —— 合计约 1.26 GB 的历史备份，非运行数据 |
| 旧二进制副本 | `/opt/cloudreve/` | `cloudreve.run.bk`、`cloudreve.run.of`，各 92–100 MB |
| 旧迁移验证库 | MySQL 内 `cloudreve_verify` | 早前迁移的验证残留库 |
| `zz_probe_redis` 容器 | **B 服务器** | `127.0.0.1:6399`，10 分钟前创建，非本项目创建，**未触碰**，仅供留意 |

> 以上为「发现即记录」，按 spec「不删除任何东西」的约束**本次一项都没做**。A 磁盘现状 `/` 229G 用 125G（58%），空间不紧张。

---

## 9. 附件：本次操作的完整命令清单

### 停止

```bash
cd /opt/cloudreve && docker compose stop --timeout 60
```

### 备份

```bash
mkdir -p /opt/cloudreve-stop-20261107 && chmod 700 /opt/cloudreve-stop-20261107
docker exec cloudreve-mysql sh -c 'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" \
  --single-transaction --quick --routines --triggers --events \
  --default-character-set=utf8mb4 --databases cloudreve' \
  | gzip -1 > /opt/cloudreve-stop-20261107/cloudreve_final_20261107.sql.gz
```

### 状态核对

```bash
docker ps -a --filter "name=cloudreve-"
ss -tln | grep -E ':(5212|3307)\b'        # 应无输出
docker compose start                       # 回滚
```

### 回滚

```bash
cd /opt/cloudreve && docker compose start
```

---

## 10. 时间线

| 时间（CST） | 动作 |
| --- | --- |
| 15:17 | 开工，确认 A 可登录、容器状态 |
| 15:18 | 发现 A 日志仍有流量，开始核查 CDN 回源 |
| 15:18:56 | 启动最终 mysqldump |
| 15:19:19 | dump 完成（220 MB，退出码 0） |
| 15:20:00–15:24:23 | 备份导入临时库验证，8 项计数全等 |
| 15:20 之后 | **A 上 pan 请求归零**；B 每分钟数百请求 |
| 15:24–15:27 | 观察 90 秒确认 A 彻底静止；记录其他服务基线 |
| 15:27:12 | **执行 `docker compose stop`** |
| 15:28:15 | 三容器全部停止，退出码 0 |
| 15:28–15:29 | 验证：A 的 5212 不可达、其他业务正常、B 正常、经 CDN 200 |
| 15:29–15:31 | **回滚实测**（start → 5212 返回 200 → MySQL 883 用户可读） |
| 15:31 | 再次 stop，恢复交付的停机状态 |
| 15:31+ | 最终验收：B 30 秒收 470 条真实请求 |
