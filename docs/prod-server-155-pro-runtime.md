# 生产服务器 155.117.85.143 —— Cloudreve Pro 运行形态梳理

> 撰写日期：2026-11-06　|　性质：**只读梳理**（不改配置、不重启、不写库、不装包）
> 状态：**尚未登入 —— 密钥未下发到执行环境**，本文第 1 节为阻塞说明，第 2 节为已确认事实，第 3 节为登入后逐项执行的只读清单与报告模板。

---

## 1. 当前阻塞：`SSH_PROD_155` 密钥没有出现在执行环境里

团队密钥卡 `SSH_PROD_155`（id `bU0khR8FI4bJR_cNItRgb`）**已被你保存**，但本轮我从两个执行位置分别核实，都取不到它的值：

| 检查位置 | 结果 |
| --- | --- |
| 构建机（本工作区所在机器） | 环境里没有任何 `SSH_*` 以外的相关变量；`SSH_PROD_155` 长度为 0 |
| Baixiong（`132.226.92.95`，上一轮探测用的跳板） | 同上，长度为 0；`~/.ssh/` 下只有 `model_server`（属于 `58.240.173.20:65023`，不是这台机器） |

所以**这一轮无法登录**，第 3 节那些检查项一条都还没跑。这不是机器连不上 —— 见下节，网络是通的。

### 需要你确认的两件事

1. **密钥卡有没有「授权给当前 agent」**。平台规定：团队密钥要出现在 shell 里，除了保存密钥卡本身，还需要在 agent 页面打开「团队密钥 / Team secrets」开关，或把密钥单独授权给这个项目。密钥在 2026-11-06 16:30 保存，我这一轮启动时环境里仍为空，形态上正是「卡已存在、授权未通」。
   → 请到 agent 设置页确认该开关已开（或把 `SSH_PROD_155` 授权给本项目），我下一轮就能读到。
2. **登录账号是什么**。你还没说过。上一轮探测确认这台机器只接受 publickey，`root` / `ubuntu` / `admin` 三个账号当时用的是一把无关密钥、都被拒。拿到密钥后我会先看密钥注释里是否带账号线索，再只用这**一把**密钥依次试 `root` / `ubuntu` / `debian` / `admin` / `cloudreve`（不做任何口令尝试、不做字典），登进去后用 `whoami` 坐实真实账号。

> 顺带提醒：密钥卡说明写的是「OpenSSH 全文，含 BEGIN/END 行」，即整段私钥。私钥只应从密钥卡注入，不要在对话里贴出来 — 对话内容会长期留存。

---

## 2. 已确认事实（无需登录，上一轮 #75 探测 + 本轮复核）

| 项 | 结论 | 来源 |
| --- | --- | --- |
| TCP 22 | ✅ 开放 | 本轮 `nc -z` 复核（2026-11-06 16:31 UTC） |
| TCP 443 | ✅ 开放 | 本轮 `nc -z` 复核 |
| TCP 80 | ✅ 开放 | #75 |
| SSH 版本 | `OpenSSH_9.6p1 Ubuntu-3ubuntu13.20`，**只接受 publickey** | #75 |
| 主机密钥 | `ssh-ed25519 SHA256:1lRa/RVEcCTdF1/+UzRe9KYTh0eKP8K6zPwO20H2YsM` | #75 |
| 反向解析 | `155-117-85-143.ip.lshiyusercontent.com`（绿时云 / 境外 VPS 网段） | #75 |
| 443 上的应用 | `/api/v4/site/config` 返回 `401`，根路径 `/` 是自定义渐变玻璃拟态 404 页 —— 形态与 Cloudreve 一脉相符，但**未坐实** | #75 |
| HTTPS 证书 | ZeroSSL ECC DV，`CN=misub.kdns.fr`，有效期 2026-06-11 ~ **2026-09-09（已过期）** | #75 |
| 80 端口 | 固定 12 字节静态页 `This is OK.`（默认探活页） | #75 |

上一轮的结论原话是：「**可达但无凭据**」—— 网络和端口都没问题，卡在公钥白名单。

---

## 3. 登入后的只读检查清单（本轮未执行）

拿到密钥后按下列顺序一次性跑完，全程只用 `cat` / `ls` / `ps` / `systemctl status|cat` / `ss` / `df` / `du` / `uname` 这类只读命令；数据库只做 `SELECT`/文件大小查看，**不导出、不写入**。密钥值只从环境变量读进临时文件（600 权限），用完 `shred` 删除，不进任何输出。

### 3.1 进程形态
```bash
ps -eo pid,ppid,user,uid,%cpu,%mem,etime,args --sort=-%mem | grep -i -E 'cloudreve|pro' | grep -v grep
```
记录：二进制名与绝对路径（`readlink -f /proc/<pid>/exe`）、完整启动参数、UID 与用户名、CPU/内存占用。**注意：`ps` 输出可能含 `--license-key` 之类参数，需在写入报告前打码。**

### 3.2 服务托管
```bash
systemctl list-units --type=service --state=running | grep -i cloudreve
systemctl status <unit> --no-pager
systemctl cat <unit>            # 看 ExecStart / EnvironmentFile / WorkingDirectory / Restart
systemctl show <unit> -p EnvironmentFiles,FragmentPath,ExecStart
```
若非 systemd：查 `docker ps -a`、`supervisorctl status`、`/etc/supervisor/conf.d/`，以及裸跑迹象（无父 UNIT 的 nohup 进程）。

### 3.3 配置（只列键名与值形态）
```bash
ls -la <工作目录>
```
找 `config.ini` / `conf.ini` / `*.yml`，用**打码方式**输出：`sed -E 's/(=|:\s*).{6,}/\1<redacted>/'`，只保留键名、端口号、`master|slave`、`sqlite|mysql|postgres`、路径前缀这类形态信息。**数据库密码、JWT secret、license key 一律替换为 `<redacted>`。**

### 3.4 数据库
- SQLite：`find / -name '*.db' -path '*cloudreve*'`、`ls -la` 看主库 / `-wal` / `-shm` 大小；可选 `sqlite3 file.db "PRAGMA page_count;"` + `page_size`（纯只读，注意加 `file:...?mode=ro&immutable=1`）。
- MySQL/PG：`mysql --version` / `psql --version`，只看版本与库大小，不列数据行。

### 3.5 版本与构建
```bash
<bin> --version      # 或 -v
ls -la <工作目录> | grep -i -E 'license|CR_'
```
记录版本号（是否 4.18.x）、二进制构建时间（`ls -l` 或 `--version` 输出）、license 文件是否存在及模式（master 需 license，slave 宽松）。

### 3.6 文件存储
```bash
du -sh <数据目录>/* 2>/dev/null | sort -h
```
记录 uploads / thumbnails / avatars / 临时目录各自大小；查本地存储策略与 S3 配置所在（策略在 DB 里，配置键名在 config 中）。

### 3.7 反向代理
```bash
ss -lntp
ls /etc/nginx/sites-enabled/ /etc/nginx/conf.d/
nginx -T 2>/dev/null | grep -E 'listen|proxy_pass|server_name|ssl_certificate'
```
确认 80/443 由谁持有、Pro 监听 `127.0.0.1:PORT` 还是 `0.0.0.0`；确认过期证书对应哪个文件（预期 `misub.kdns.fr` 的 ZeroSSL 证书）。

### 3.8 日志
```bash
journalctl -u <unit> --since '7 days ago' -p warning --no-pager | tail -50
ls -la <数据目录>/logs/ 2>/dev/null
```
只看最近告警/错误条数与摘要，不导出全文。

### 3.9 系统底座
```bash
uname -a; cat /etc/os-release | head -3; df -h; free -h; nproc
```
记录与迁移容量相关的：磁盘总量、已用、剩余，内存大小。

### 3.10 同机其它服务
```bash
ss -lntp; docker ps 2>/dev/null; systemctl list-units --type=service --state=running
```

### 3.11 是否适合直接升级为 fork 宿主（判断项）
登入后按这几条给结论：现有 Pro 版本（4.18 → fork 4.23.8 迁移脚本是否适用）、是否 SQLite（记忆 `ND6W9WDOJRRfV9USehAYZ` 的 PG 迁移经验需换成 SQLite 路径）、数据目录总大小 vs 剩余磁盘、是否有同机 MySQL/Redis 竞争资源、systemd 单元能否直接换二进制（回滚成本）、证书过期是否需要一并处理。

---

## 4. 待填章节

- §3.1 ~ §3.11 的实测结果
- §5 结论：Pro 运行形态总结 + 是否适合直接升级为 fork 宿主

> 本文件在拿到密钥后会被实测结果整体重写；在此之前它只承载阻塞说明与检查清单。
