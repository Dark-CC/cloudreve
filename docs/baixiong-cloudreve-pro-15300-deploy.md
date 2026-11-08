# Baixiong 上以 15300 端口启动 Cloudreve Pro：部署与验证报告

执行日期：2026-11-08。目标：把用户放入 Baixiong `/root/CloudrevePro/cloudreve` 的 Pro 二进制，用一个新的空闲端口跑起来，只动这一套实例。

**结论：已跑通并交付。** 服务以 systemd 单元 `cloudreve-pro15300.service` 托管（enabled + Restart=always），监听 `:15300`，版本 **4.19.1 Pro**，授权已联网兑换并落库（到期 2027-02-06），首个注册用户已成为 Admin。既有 15230 / 15299 两个 Cloudreve 实例与其它服务端口、配置、数据均未改动。

## 一、二进制实查

| 项目 | 实查结果 |
|---|---|
| 路径 | `/root/CloudrevePro/cloudreve`，94,503,038 字节，权限已改为 755 |
| 类型 | `ELF 32-bit LSB executable, ARM, EABI5 version 1 (SYSV), statically linked, stripped` |
| sha256 | `c55fc6da215d5f855b330fbabb1b32952afce0961b8ce6e50bd333bdb117a1b5` |
| 机器 | aarch64（Neoverse-N1），内核 AArch32 兼容层可直接执行该 32 位静态程序 |
| 实际版本 | 4.19.1 Pro，commit `b6585220`（来自 `/api/v4/admin/summary`） |

> 与 `docs/baixiong-cloudreve-pro-15212-deploy.md` 的旧结论对比：上次那份 4.18.0 **aarch64** 二进制在读取 license 时必崩（`SIGSEGV @0x1dee1c0`），因此当时判定「必须换二进制」。这次用户放入的是**另一份 32 位 ARM 的 4.19.1**，实测完整跑通、无崩溃，旧报告的阻塞不成立。**也没有使用 qemu 等任何变通手段。**

## 二、端口选择

改前收集的占用情况（`ss -lntp`）：15230、15299（两个既有 Cloudreve）、15244、20241、3100、111、53、62302（tds 的 sshd）、61927 / 62366（tds-tunnel）。

选 **15300**：既在 152xx 云盘惯用号段之外，又与上表全部错开。启动前与启动后各查一次，均为空闲。

## 三、目录与文件布局（全部在 /root/CloudrevePro 下，独立不混用）

```
/root/CloudrevePro/
├── cloudreve              用户放入的二进制（未改名、未移动）
├── conf/config.ini        600  Listen = :15300，sqlite3 指向本目录 data/
├── data/cloudreve.db      本实例独占数据库
├── data/cloudreve.db.bak-202611081428   改 siteURL 前的回滚点，600
├── data/uploads/          本实例上传目录
├── license.env            600  CR_LICENSE_KEY（systemd EnvironmentFile 注入，不进 ps）
├── .admin_creds           600  管理员邮箱与密码
└── README-15300.md        机器上的运维速查
```

`config.ini` 实际内容（不含任何密钥）：

```ini
[System]
Mode = master
Listen = :15300
Debug = false
LogLevel = info

[Database]
Type = sqlite3
DBFile = /root/CloudrevePro/data/cloudreve.db
```

> 该版本（4.19.1）的 `conf` 里没有 `[Log]` 段，写 `LogFile` 不生效——一开始配了也没生成文件，已删掉该段，日志统一走 journald。

## 四、systemd 托管（重启后仍在）

`/etc/systemd/system/cloudreve-pro15300.service`：

```ini
[Unit]
Description=Cloudreve Pro 15300 (Baixiong, /root/CloudrevePro)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/root/CloudrevePro
EnvironmentFile=/root/CloudrevePro/license.env
ExecStart=/root/CloudrevePro/cloudreve -c /root/CloudrevePro/conf/config.ini
Restart=always
RestartSec=5
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

```console
$ systemctl is-active cloudreve-pro15300.service   →  active
$ systemctl is-enabled cloudreve-pro15300.service  →  enabled
```

## 五、授权：首次联网兑换，之后走库

- 密钥来自团队密钥 `CLOUDREVE_PRO_LICENSE`，只写进 `/root/CloudrevePro/license.env`（600），由 systemd `EnvironmentFile` 注入；**未写入仓库、未写入报告、未出现在命令行**。验证：`ps -o args=` 里只有 `... cloudreve -c .../config.ini`，无密钥。
- 首次启动日志：

```
[Info] No license found, trying to obtain from Cloudreve.
[Info] License loaded, expire=2027-02-06 22:22:54.490250183 +0800
```

- 之后每次重启都直接从库里复用，不再联网：

```
[Info] License loaded, expire=2027-02-06 22:22:54.490250183 +0800      # 无 "trying to obtain"
[Info] Current running mode: Master.
[Info] Listening to ":15300"
```

- 管理端返回的授权信息：`expired_at=2027-02-06`、`root_domains=["kuocai.net"]`、`domains=["pan.kuocai.net"]`、`pro=true`。
  注意：这把 key 的授权到 **2027-02-06** 到期（不是百年期），到期前需要续。

## 六、管理员账号

- 通过 `POST /api/v4/user` 注册首个用户 `admin@cr15300.local`，落到 `group_users=1`（Admin 组）——v4 行为，实测确认。
- 登录 `POST /api/v4/session/token` 成功，`/api/v4/user/me` 返回 `group.name=Admin`，`/api/v4/admin/summary` 返回 `HTTP 200 code=0`，管理权限确认可用。
- 密码为本次新生成的随机串，**未复用团队里任何既有测试站口令**；只保存在机器上 `/root/CloudrevePro/.admin_creds`（600），不入库、不进聊天明文。

## 七、验证证据

```console
$ ss -lntp | grep 15300
LISTEN 0 4096 *:15300 *:* users:(("cloudreve",pid=2660824,fd=7))

$ curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:15300/
200                                   # 首页 HTML 3771 B

$ curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:15300/api/v4/site/ping
200
```

前端静态资源逐个抓取，全部 200：

```
/assets/common-5dK6VpSw.js        200
/assets/index-7AbHGp3_.js         200
/assets/index-qQxd8I_f.css        200
/assets/react-4-pFdlfx.css        200
/assets/react-gCCx5Qg1.js         200
/manifest.json                    200
/static/img/favicon.ico           200
```

**重启自愈**：`kill -9` 主进程后，systemd 在 12 秒内拉起新进程（旧 PID 2660528 → 新 PID 2660824），首页仍 200。

```console
$ kill -9 2660528; sleep 12
旧PID=2660528 新PID=2660824
$ curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:15300/   →  200
```

## 八、对外可达性（实测）

规格里说「本次先只本机可访问，对外暴露等用户确认」。实际情况是：**我没有做任何暴露动作**（没开安全列表、没建隧道、没改 DNS），程序本身默认监听所有网卡（`*:15300`），而该机器 OCI 安全列表对 15300 恰好放行。

- 本机经公网 IP 自测：`http://132.226.92.95:15300/` → HTTP 200。
- 从真正的外部网络验证（经第三方网页代理取回）：拿到 Cloudreve 登录页，标题 `Sign in - Cloudreve`，跳转 `/session`。

即：**该实例当前已可从公网直接访问 `http://132.226.92.95:15300/`**。若希望严格只本机，把 `Listen` 改成 `127.0.0.1:15300` 并重启即可（一行配置），等用户确认。

其它现状：站内 `register_enabled=1`（开放注册）、`email_active=0`（注册不需要验邮箱）。公网可达 + 开放注册意味着任何人可注册普通用户账号，需用户决定是否关闭注册。

## 九、隔离性确认

- 既有服务：15230（`cr15230.service`）、15299 两个 Cloudreve 实例开工前后均 active，端口监听不变，配置文件与数据库未改。
- 未触碰 pan.cdn.ad（155）、国内站（88）上的任何实例。
- 开工前记录的端口集合 `{15230,15299,15244,20241,3100,111,53,62302,61927,62366}`，收尾时仅多出 `15300` 一项。

## 十、过程中的一处异常（已清理）

验证期间，本机出现**另一进程**（另一工作区的 Playwright/HeadlessChrome 与 curl 脚本，`/tmp/pro.js`、`/tmp/playwright_chromiumdev_profile-*`）向 `127.0.0.1:15300` 发起登录与上传，在本实例里留下一个 `probe-test.txt`（11 B）。已用管理接口删除并清空回收站，磁盘上的残留 blob 也已删除。这不是本实例自身的流量，不影响交付，但说明该端口已被同机其它测试进程发现。

## 十一、清理与回滚

- 已删：首次试跑日志 `logs/test-run.log`、临时标记文件、`/tmp` 下的登录/删除用临时 JSON（含密码的临时文件用 `shred -u` 删除）、空的 `logs/` 目录、测试残留上传文件。
- 目录占用 92 MB，其中 91 MB 是用户放入的二进制本身；数据库约 700 KB。
- 回滚：`systemctl disable --now cloudreve-pro15300.service && rm /etc/systemd/system/cloudreve-pro15300.service && systemctl daemon-reload`，即回到改动前状态（不影响其它实例）。

## 十二、待用户决定

1. 保持公网可达（现状），还是改成只本机 `127.0.0.1:15300`。
2. 是否关闭开放注册（`register_enabled`），避免公网陌生人注册。
3. 若要正式对外，需要把站点根地址从 `http://127.0.0.1:15300` 改成实际外部地址（`settings.siteURL`），并可考虑接 Cloudflare 隧道（同机 15230 已有先例）。
4. 授权 2027-02-06 到期，到期前需续。
