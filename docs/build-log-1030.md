# 15220 测试站构建与验收日志（2026-10-31）

机器：`centos-stream-server`（machineId `D4C3Y873LzIzbRPozJJGe`，aarch64，CentOS Stream 10，5 核 / 6.2G / 970G 空闲）

本文记录**编译失败的真实原因**、修复过程、前端打包耗时、实例部署、可达性实测与逐项验收结论。

---

## 1. 编译失败：原因、报错原文、判断

### 1.1 报错原文

```
application/statics/statics.go:27:12: pattern assets.zip: no matching files found
```

（本轮复现同样报错，见 §1.4）

### 1.2 涉及文件

| 文件 | 作用 |
| --- | --- |
| `application/statics/statics.go` 第 27 行 | `//go:embed assets.zip` 指令所在 |
| `application/statics/assets.zip` | 被嵌入的前端产物包（**在 `.gitignore` 内**，不入库） |
| `frontend/vite.config.ts` 第 87 行 | `outDir: "build"`（相对 vite root，即仓库根），产物实际落在 `assets/build/` |

### 1.3 判断

失败**不是代码问题**，是构建顺序问题：

1. `assets.zip` 由前端构建产生，且被 `.gitignore` 排除，全新克隆的仓库里根本没有这个文件。
2. Go 的 `//go:embed` 在**编译期**校验目标文件必须存在，缺文件直接编译失败，不会延迟到运行期。
3. 所以必须先跑前端打包 → 打成 `assets/build/...` 形状的 zip → 再 `go build`。

即：**`go build` 只是表象，根因是前端产物没先准备好。** 上一轮末尾那行被截断的报错就是这一条，不是 Go 源码有语法/类型错误。

### 1.4 复现与修复验证

```bash
# 复现：临时移走 zip
mv application/statics/assets.zip /tmp/assets.zip.bak
go build -buildvcs=false -o /tmp/probe-build .
# → application/statics/statics.go:27:12: pattern assets.zip: no matching files found
mv /tmp/assets.zip.bak application/statics/assets.zip
```

修复后完整构建通过，见 §2、§3。

**这一步已经固化进 `deploy.sh` 的上游流程**：先前端打包，再打 zip，再编译后端。

---

## 2. 前端打包

```bash
cd /data/cr-src/frontend
yarn install --network-timeout 1000000 --ignore-engines
NODE_OPTIONS=--max-old-space-size=4096 yarn run build
```

### 结果

| 项 | 值 |
| --- | --- |
| 结果 | `Done in 45.20s.`（退出码 0） |
| 产物位置 | `/data/cr-src/assets/build/`（**注意：不是 `frontend/build/`**） |
| 产物大小 | 46 MB |
| 顶层条目 | 9 个（含 `sw.js`、`workbox-e3490c72.js`、`index.html` 等） |
| 工具链 | Node v26.13.0、yarn 1.22.22 |

### 产物路径的坑（重要）

`vite.config.ts` 写 `outDir: "build"`，而 vite 的 root 是仓库根，所以产物落在 **`/data/cr-src/assets/build/`**，不是 `frontend/build/`。我一度按 `frontend/build` 去找，以为构建没出产物，实际产物一直在正确位置——这个目录形状刚好就是 `go:embed` 需要的。

### 打 zip

```bash
cd /data/cr-src
rm -f application/statics/assets.zip
(cd . && zip -qr - assets/build) > application/statics/assets.zip
```

结果：`assets.zip` 14,883,379 字节（约 15 MB），内部前缀确认为 `assets/build/`（用 `unzip -l` 验过）。

---

## 3. 后端编译

```bash
cd /data/cr-src
GOFLAGS=-mod=mod GOSUMDB=off go build -buildvcs=false -ldflags="-s -w" -o /data/cr-build/cloudreve .
```

| 项 | 值 |
| --- | --- |
| 结果 | 退出码 0，**构建通过** |
| 增量编译耗时 | 1.79 s（Go build cache 命中） |
| 全量编译耗时（`go build -a`） | **26.32 s**，退出码 0 |
| 产物大小 | 81,789,191 字节（78 MB） |
| 文件类型 | `ELF 64-bit LSB executable, ARM aarch64, stripped` |
| 产物路径 | `/data/cr-build/cloudreve` |
| sha256 前缀 | `7a73f305`（重新打包前端后；旧前端产物时是 `8f1c8d22`） |

> sha 变化是因为嵌入的前端资产变了，二进制内容随之变化，属正常。

工具链：Go 1.27.3；`go.mod` 要求 `go 1.26.0` / `toolchain go1.26.5`，满足。

SQLite 用纯 Go 驱动 `modernc.org/sqlite`，**不需要 cgo、不需要 sqlite3 命令行**。

---

## 4. 单元测试

```bash
cd /data/cr-src && GOFLAGS=-mod=mod go test ./inventory/... ./service/user/... ./pkg/setting/...
```

| 包 | 结果 |
| --- | --- |
| `inventory` | `ok`（含 `group_folder_direct_link_test.go` 等 t43 单测） |
| `service/user` | `ok 1.300s`（t44 订单/支付服务） |
| `pkg/setting` | `ok 0.372s`（易支付设置项） |
| 退出码 | 0（`UT_DONE_0`），**零失败** |

---

## 5. 实例部署

| 项 | 值 |
| --- | --- |
| 监听 | `0.0.0.0:15220`（`ss -lntp` 显示 `*:15220`，进程 `cloudreve`） |
| 数据目录 | `/data/cr-test-15220/data`（**真实磁盘，非 /tmp**；`/data` 所在盘 925G，用 28G） |
| 数据库 | `/data/cr-test-15220/data/cloudreve.db`（SQLite，空库初始化） |
| 二进制 | `/data/cr-test-15220/cloudreve`（部署副本） |
| 服务 | `cloudreve-test-15220.service`，已 `enable`（重启自启） |
| 管理员 | `admin@cr15220.local`（首注用户自动进 Admin 组，uid `5OSw`） |

### 启动命令（systemd 单元）

见 `docs/centos-stream-test-15220/deploy.sh`，核心为：

```ini
Environment=CR_CONF_System__Mode=master
Environment=CR_CONF_System__Listen=0.0.0.0:15220
Environment=CR_CONF_Database__Type=sqlite3
Environment=CR_CONF_Database__DBFile=/data/cr-test-15220/data/cloudreve.db
ExecStart=/data/cr-test-15220/cloudreve
```

坑 1：systemd 的 `Environment=` **不接受带点的变量名**，配置分段必须用双下划线 `__`，否则报 `Invalid environment assignment, ignoring`。
坑 2：SELinux `Enforcing` 下 `/data` 是 `default_t`，需 `semanage fcontext -a -t bin_t` + `restorecon`，否则 `status=203/EXEC`。

---

## 6. 可达性实测

### 6.1 监听地址

```
ss -lntp | grep 15220
LISTEN 0 4096 *:15220 *:* users:(("cloudreve",pid=23292,fd=13))
```

`*:15220` 即 `0.0.0.0:15220`，**不是** 127.0.0.1。

### 6.2 本机全部内网 IP（`ip -4 addr`）

```
192.168.64.5/24    dev enp0s1
192.168.128.2/24   dev enp0s2
```

（仅这两个非 loopback IPv4；无公网 IP）

### 6.3 非 loopback 网卡实测

```
curl -I http://192.168.64.5:15220/    → HTTP 200
curl -I http://192.168.128.2:15220/   → HTTP 200
```

### 6.4 防火墙排查

| 组件 | 状态 | 处理 |
| --- | --- | --- |
| firewalld | `active`，zone `public`，接口 `enp0s1 enp0s2` | 原本只放行 `cockpit dhcpv6-client ssh`；已执行 `firewall-cmd --permanent --add-port=15220/tcp && firewall-cmd --reload`，现 `--list-ports` 输出 `15220/tcp` |
| iptables | `Chain INPUT (policy ACCEPT)`，无拦截规则 | 无需处理 |
| nftables | 仅 firewalld 自己管理的表 | 无需处理 |

结论：**防火墙已放行，端口对外（内网）可达。**

---

## 7. 验收结果

脚本：`/data/pw/verify4.js`（Playwright + chromium，文本断言，不依赖人眼看图）
输出：`/data/pw/shots/result-v4.json`，截图与文本快照同在 `/data/pw/shots/`

### 7.1 已实测通过

| 验收项 | 证据 |
| --- | --- |
| 首页可访问 | HTTP 200，标题「登录 - Cloudreve」 |
| 管理员登录 | `POST /api/v4/session/token` → code=0，`group.name = Admin` |
| **后台订单列表 API（t44）** | `{"code":0,"data":{"orders":[],"total":0,...}}` |
| **补单路由存在（t44）** | `POST /admin/payment-orders/NOSUCHID/reconcile` → status=200 code=40001（业务错误，非 404 路由缺失） |
| **订单落库（t44 核心）** | 网关不可达时下单返回 50001，但 `GET /admin/payment-orders` 仍返回订单 `id=bmcE, status=pending, provider=easypay` |
| **订单有效期 30 分钟** | `expires_at - created_at = 1800s` |
| **后台订单页渲染 + 列头完整** | 页面文本含 `订单号 用户 商品 金额 支付渠道 状态 创建时间 支付时间 操作` |
| **后台订单页展示真实订单号** | 页面文本含 `bmcE` |
| 凭证续期横幅（t43） | `{"code":0,"data":[]}` |
| 易支付 4 设置项齐全 | `enabled/gateway/pid/type` 全部存在 |
| **VAS 单价 0.01 元端到端** | 建 `price=1` 的 SKU → 商城 → 下单 → 订单 `amount=1` |
| **VAS 小数单价端到端** | 建 `price=12345` 的 SKU → 商城 → 下单 → 订单 `amount=12345`（123.45 元） |
| VAS 容量单位 GB/TB | `SkuTable.tsx:307-308` 有 `GB` / `TB` 两个 `MenuItem` |
| **前台商城读到 SKU** | `GET /api/v4/user/shop/skus` 返回商品 |
| 商城页渲染 + 「我的订单」入口 | 页面含「购买商城 / 余额 / 容量包 / 流量包 / 我的订单」 |
| 后台设置页渲染 | len=1557 |
| VAS 管理界面可访问 | len=1136 |
| 群组列表 API + 目录直链开关 | 群组页含「直链」字样（`group.folderDirectLink`），后端 `FolderDirectLink` 字段与单测均在 |
| 文件页渲染 | 正常 |
| 无致命前端 JS 错误 | 0 条 TypeError/ReferenceError |

### 7.2 未能自动验证（明确标注）

| 项 | 原因 |
| --- | --- |
| 易支付**真实支付闭环**（二维码弹窗 → 扫码 → 回调到账） | 没有可用的真实网关 / PID / 商户密钥。已用假网关验证「下单→订单落库」链路与「二维码弹窗渲染」，但「扫码付款后回调改单」需要真实商户配置 |
| 移动云盘（139）授权框与 Endpoint 隐藏 | 需要真实 139 盘账号完成 OAuth 授权；Authorization 按约定 `<redacted>`，不注入环境 |
| 秒传缩略图真实生成 | 需要实际文件上传并与上游做秒传交互 |
| 下拉字号 14px | 纯 CSS 视觉项，文本断言无法判定，需人工看截图 |

### 7.3 脚本自身的一处误报（已定位，并用按 id 精确选择的复测推翻）

首轮「订单金额为小数单价 12345」一项报 FAIL，实测值为 `amount=1`。原因：那一次测试库里有两个**同名 SKU**（id=1 单价 1 分，id=2 单价 12345 分），脚本按名字 `find()` 取到第一个（1 分那个），下单金额自然为 1。

**产品逻辑无误**。清理测试数据后按 sku hashid 精确下单复测，结果：

| SKU | 单价 | 落库订单 amount |
| --- | --- | --- |
| `cr-test-price` | 12345 分（123.45 元） | **12345**，`status=pending`，`ttl=1800s` |
| `cr-test-cent` | 1 分（0.01 元） | **1** |

即：**0.01 元最小单价与 123.45 元小数单价，从后台建商品 → 前台商城 → 下单 → 订单落库，金额全程精确无损。** 该项由 FAIL 更正为 **PASS**。

最终计数：**PASS 27 / FAIL 0 / NOTVERIFIED 4（共 31 项）**。

---

## 8. 测试数据清理

所有测试数据用 `cr-test-*` 前缀，验完即删：

- `skus` 表中 `name LIKE 'cr-test-%'` 的行
- `payment_orders` 中对应 SKU 的订单行（订单本身无删除接口，属设计：财务记录不可删，用 SQLite 直接清理）
- 易支付设置恢复 `payment_easypay_enabled=0`、`key` 清空

---

## 9. 回滚

```bash
systemctl stop cloudreve-test-15220
install -m755 /data/cr-test-15220/cloudreve.rollback-YYYYMMDD /data/cr-test-15220/cloudreve
systemctl start cloudreve-test-15220
```

代码层面：集成分支 `integrate/1030-all-in-one` 未触碰 `baixiong`，不合并该分支即回滚。

---

## 10. 关键结论

1. **编译失败不是代码问题**，是 `go:embed` 要求 `application/statics/assets.zip` 必须存在，而它被 gitignore，必须先跑前端打包。已修复并验证通过。
2. 前后端均在本机构建成功：前端 45.20s / 46 MB，后端全量 26.32s / 78 MB。
3. 实例监听 `0.0.0.0:15220`，数据在 `/data` 真实磁盘，防火墙已放行，两个内网 IP 实测 200。
4. 四批改动均已合入并实测：「订单记录」这条 t44 主线（后台列表 + 补单 + 落库）**确认可用**，t43 的 VAS 容量单位/小数单价、目录直链开关、凭证续期也均可用。
5. 真实支付闭环、139 授权、秒传缩略图、字号像素级外观四项需要真实凭据或人眼判断，已明确标注为未验证。

---

## 11. 防火墙：关闭（2026-10-31，用户明确要求）

用户要求「把新机器的防火墙全部关闭」。执行范围仅限本机 `centos-stream-server`，不涉及其他任何机器。

### 11.1 关闭前状态（原样存档，供以后恢复）

```
firewalld.service — active (running) since 2026-10-31 00:39:14 CST; enabled
firewall-cmd --state → running
```

生效 zone `public`（默认、active），绑定网卡 `enp0s1 enp0s2`：

```
public (default, active)
  target: default
  interfaces: enp0s1 enp0s2
  services: cockpit dhcpv6-client ssh
  ports: 15220/tcp          ← 上一轮为测试站放行的端口
  forward: yes
  masquerade: no
```

`iptables -L -n`：INPUT / FORWARD / OUTPUT 三条链 **policy 均为 ACCEPT，无任何规则**。
`nft list ruleset`：只有 firewalld 自己创建的 `table inet firewalld`（含 `mangle_*` / `nat_*` / `filter_*` 系列链），无第三方规则。

**完整备份已落盘 `/data/fw-backup-20261031/`：**

| 文件 | 内容 |
| --- | --- |
| `nft-ruleset.txt` | 关闭前完整 nft 规则集（9,740 字节） |
| `firewalld-zones.txt` | `firewall-cmd --list-all-zones` 输出（2,814 字节） |
| `firewalld-etc/` | `/etc/firewalld` 整个目录的副本 |
| `iptables-save.txt` | 空（验证确无 iptables 规则） |

### 11.2 执行的操作

```bash
systemctl stop firewalld && systemctl disable firewalld
systemctl stop nftables iptables 2>/dev/null
systemctl disable nftables iptables 2>/dev/null
```

- `firewalld`：已 stop + disable，两条 enable 符号链接被移除（`multi-user.target.wants/firewalld.service`、`dbus-org.fedoraproject.FirewallD1.service`）。
- `nftables`：系统里有该单元但本就 `inactive/disabled`，保持不动。
- `iptables`：**该单元在此系统不存在**（`systemctl is-enabled iptables` → `not-found`），跳过。CentOS Stream 10 用 nftables 后端，`iptables` 命令是兼容层。

### 11.3 关闭后复验

| 检查项 | 结果 |
| --- | --- |
| `systemctl is-active firewalld` | `inactive` |
| `systemctl is-enabled firewalld` | `disabled` |
| `multi-user.target.wants/firewalld.service` | 符号链接不存在 |
| firewalld 进程（`pgrep`) | 0 个 |
| `nft list ruleset \| wc -l` | **0（规则集完全为空）** |
| `iptables -L -n` | 三链 policy ACCEPT，无规则 |
| 测试站 `http://192.168.64.5:15220/` | **200**（关防火墙后仍正常） |

### 11.4 这台机器是否在云上 / 是否还有云厂商安全组

**从机器内部无法判定云厂商安全组**——那层在本机之外（云控制台的 VPC/防火墙面板），机器内部看不到也改不了。以下是根据机器内可见事实得出的判断：

- 网卡：`enp0s1` = `192.168.64.5/24`，`enp0s2` = `192.168.128.2/24`，**均为 RFC1918 私有地址，没有公网 IP 直接绑在网卡上**。
- 默认路由：`default via 192.168.64.1 dev enp0s1`（DHCP 下发）→ 典型 NAT 环境。
- 出站公网 IP：`155.117.85.143`（`curl ifconfig.me`），与网卡地址不同，确认是 **NAT 出口**。
- 向 `155.117.85.143:22` 做 `ssh-keyscan`，**有响应**，但 banner 是 `SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.20`——而本机是 CentOS Stream 10 上的 OpenSSH_9.x 不同版本，且 host key 也不匹配（本机 ed25519 指纹 `SHA256:rcfmRzEOnEqLFdtVWSkRvkj3/KB9PViaea7b1N6VGsU`）。
  → **那个公网 IP 上跑着另一台 Ubuntu 机器，不是本机**。很多 NAT 出口下多台设备共用同一出口 IP，这是典型现象。没有证据表明本机的 22 端口被映射到公网。

**结论：** 这台机器在 NAT 后面（网段 `192.168.64.0/24`，网关 `192.168.64.1`），**不是直接公网可达**。云厂商侧是否还有一层安全组，**需要用户自行在云控制台确认**，我在机器内部查不出来。若网关 `192.168.64.1` 是用户的软路由/防火墙，则还需在它上面做端口映射才能公网访问。

### 11.5 如何恢复防火墙

```bash
systemctl enable --now firewalld
# 若要恢复成关闭前的精确状态：
cp -a /data/fw-backup-20261031/firewalld-etc/. /etc/firewalld/
firewall-cmd --reload
```

---

## 12. 第三方 SSH 连接配置（2026-10-31）

用户要用自己的 SSH 客户端（Xshell / FinalShell / PuTTY / 终端）连本机。

### 12.1 排查到的原始状态（重要）

| 项目 | 原始状态 | 说明 |
| --- | --- | --- |
| sshd 服务 | **`inactive` / `disabled`** | **SSH 服务根本没开**，这是连不上的根因 |
| 监听端口 | 无 | 服务没跑自然无监听 |
| `PasswordAuthentication` | **`no`** | 由 `/etc/ssh/sshd_config.d/50-cloud-init.conf` 设置 |
| `PermitRootLogin` | 未设置（默认 `prohibit-password`） | 主配置与所有 drop-in 里均未出现 |
| root 密码 | **`L never`** | 密码被锁、从未设置过，无法密码登录 |
| `Port` | 未设置 → 默认 **22** | |

主配置 `/etc/ssh/sshd_config` 几乎全是注释，实际生效值全部来自 `sshd_config.d/` 三个 drop-in：
`40-redhat-crypto-policies.conf`（引入系统 crypto policy）、`50-cloud-init.conf`（`PasswordAuthentication no`）、`50-redhat.conf`（PAM/Syslog/X11 等）。

备份：`/data/ssh-backup-20261031/{sshd_config, sshd_config.d/}`。

### 12.2 改了什么

**踩过的坑（值得记住）：** 我先新建了 `99-allow-password-login.conf` 想靠「数字大优先级高」覆盖前面的设置——**没用**。OpenSSH 对同一参数取**首次出现**的值，`50-cloud-init.conf` 排在前，`99-` 里的 `yes` 被忽略，`sshd -T` 仍显示 `no`。正确做法是**直接改掉设置源头的那个文件**。

```bash
# 1) 直接改源头，而不是靠后面覆盖
sed -i 's/^PasswordAuthentication no/PasswordAuthentication yes/' \
  /etc/ssh/sshd_config.d/50-cloud-init.conf

# 2) 允许 root 登录（99- 这个文件里 PermitRootLogin 之前没冲突，故生效）
#    /etc/ssh/sshd_config.d/99-allow-password-login.conf:
#      PasswordAuthentication yes
#      PermitRootLogin yes
#      PubkeyAuthentication yes

# 3) 校验 + 重载
sshd -t && systemctl reload sshd

# 4) root 设密码
printf 'root:%s\n' "$PW" | chpasswd

# 5) 开机自启
systemctl enable --now sshd
```

### 12.3 改后生效值（`sshd -T` 实测）

```
port 22
listenaddress 0.0.0.0:22
listenaddress [::]:22
permitrootlogin yes
passwordauthentication yes
pubkeyauthentication yes
```

`passwd -S root` → `root P 2026-10-30 0 99999 7 -1`（**P = 已设密码**）。
`ss -lntp` → `LISTEN 0 128 0.0.0.0:22` + `[::]:22`，进程 `sshd` (pid 31015)。
服务 `active` / `enabled`（开机自启）。

### 12.4 真实登录实测（不是只看配置）

装 `sshpass` 做真实密码认证：

```
sshpass -p *** ssh root@192.168.64.5   → LOGIN_OK / centos-stream-server / root
sshpass -p *** ssh root@192.168.128.2  → LOGIN_OK_128_2
```

**两个内网 IP 的 root 密码登录均实测成功。**

### 12.5 连接参数（密码见对话回复，不写入本文件）

| 项 | 值 |
| --- | --- |
| 主机 | `192.168.64.5`（enp0s1，主用）或 `192.168.128.2`（enp0s2，备用） |
| 端口 | `22` |
| 用户名 | `root` |
| 密码 | 见对话回复 |
| 命令行 | `ssh root@192.168.64.5` |

**前提：用户必须在与 `192.168.64.0/24` 或 `192.168.128.0/24` 互通的内网中。** 公网直连不可用（见 §11.4：NAT 后、22 端口未做映射）。若需公网访问，需在网关 `192.168.64.1` 或云控制台做端口转发。

### 12.6 如何撤销这些 SSH 改动

```bash
rm -f /etc/ssh/sshd_config.d/99-allow-password-login.conf
cp -a /data/ssh-backup-20261031/sshd_config.d/. /etc/ssh/sshd_config.d/
sshd -t && systemctl reload sshd
```

（root 密码撤销：`passwd -l root`）
