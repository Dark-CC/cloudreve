# Baixiong 上启动云盘 Pro（15212 端口）：现状核查与阻塞报告

核查日期：2026-11-05。目标：把用户放入 Baixiong `/root/CloudrevePro` 的 Cloudreve Pro 启动在 15212 端口，交付公网 IP+端口。

**结论：本次未能启动。** 用户放入的 arm64 Pro 二进制在读取 license 的环节必然段错误（SIGSEGV），这是二进制自身的问题，与 license 内容、与本机环境、与配置都无关。15212 当前未监听，未创建 systemd 服务，未做任何对外发布操作。

## 一、用户放入的文件（实查）

| 文件 | 实查 |
|---|---|
| `cloudreve_v4.18.0` | 86,573,240 字节，**ELF 64-bit ARM aarch64**、静态链接、已 strip；sha256 `8626417cb47012a88990160c08e0e51f09cf52ad735e416b7891e738c7dc3807` |
| `license_key` | 130 字节两行：第 1 行 41 字符（`CR_HEXOR_DATA`），第 2 行 88 字符 base64（license key），行尾为 CRLF，末行无换行 |
| `start.sh` | 交互式启动脚本（前台/后台/停止三选一），读 `license_key` 导出 `CR_HEXOR_DATA` 并用 `--license-key` 启动 |

架构这一关是过的：Baixiong 是 aarch64，二进制也是 aarch64，不存在 amd64 跑不了的问题。缺的是 `config.ini` 与 `data/`，已按最小改动补齐（见第三节）。

## 二、启动失败的原因（已定位）

程序启动到「加载 license」这一步即崩溃，日志原文头部：

```
unexpected fault address 0x1dee1c0
fatal error: fault
[signal SIGSEGV: segmentation violation code=0x1 addr=0x1dee1c0 pc=0x92324]
...
syscall.Getenv / os.Getenv
github.com/cloudreve/Cloudreve/v4/pkg/licensemanager.getCRMSite+0x1c
```

判定依据（三点互相印证）：

1. **崩溃地址落在外星。** 该 ELF 的 `.text` 段结束于 `0x1ded664`，`.rodata` 从 `0x1df0000` 开始；崩溃地址 `0x1dee1c0` 正落在这两者之间的空洞里，不属于任何可读段。程序把一个不可读地址当成了环境变量名的指针去哈希，因此必崩。这个地址是**编码在二进制内部的常量**，不是随机值、不随输入变化。
2. **与 license 内容无关。** 把 `CR_LICENSE_KEY` 设成随便一个值 `x`，同样在 `0x1dee1c0` 崩；不设这个变量时程序不崩，能正常打印 `Please specify license key by "--license-key" argument or "CR_LICENSE_KEY" env`。也就是说：**只要程序尝试读取 `CR_LICENSE_KEY`，就必崩**。
3. **与本机环境无关。** 用 `env -i` 清空环境只留 `PATH` 依然崩；用 `--license-key=` 命令行参数方式（内部同样会读该变量）也崩；不带 environment 时同机其它 Cloudreve 实例（15230）运行正常，机器与内核没有问题。

综合看：这份二进制在构建或分发过程中被改动/损坏过（该版本 Pro 的 license 校验代码是动态拼接环境变量名的，这段代码里的字符串常量地址失效）。**这不是换 license、改配置、改端口能绕过去的，必须换一份完好的二进制。**

同时需要说明的是脚本与配置本身是好的：试跑时程序已经成功连上 SQLite 并初始化出 `data/cloudreve.db`（空库，schema 已建），说明除 license 读取外的启动链路都正常。

## 三、已完成的准备工作（换好二进制即可直接起）

在 `/root/CloudrevePro` 下已就位，未启动：

| 项目 | 说明 |
|---|---|
| `data/conf.ini` | 新建，权限 600。内容：`Mode = master`、`Listen = :15212`、`Debug = false`、`HashIDSalt` 占位 |
| `license.env` | 新建，权限 600。由 `license_key` 两行生成 `CR_HEXOR_DATA=` / `CR_LICENSE_KEY=`，供 systemd `EnvironmentFile` 使用（**不进入 ps 进程列表**，与 15213 实例做法一致） |
| `data/cloudreve.db` | 试跑时自动初始化出的空库（约 648 KB），可复用；要全新开始删掉即可 |

⚠️ `HashIDSalt` 目前是占位值 `CHANGE_ME_HASHID_SALT`。**正式上线前应改为随机串**，且一旦有数据就不能再改（会影响资源 ID 解析）。因为本次未上线，保持占位。

临时文件（试跑日志 `run-test.log`、`/tmp` 下的 `t2~t5.log`、`e.log`、`license_key.bak`）已全部删除，未清理任何 docker 镜像。目录仍占 84 MB（主要是那 86 MB 的二进制本身）。

## 四、15212 对外可达性（未测，因服务未起）

本机 `15212` 当前无监听。Baixiong 本机无 ufw、nftables INPUT 为 accept、iptables 仅 Docker 链，因此本机侧无拦截；能否从公网访问取决于 **OCI 安全列表是否放行 15212/TCP**。作为参照，同机现有对外可达的 15230 是走 Cloudflare 隧道而非直连安全列表。待服务真正起来后才能实测，**本次不作任何「已可访问」的说法**。

## 五、需要用户决定的事

1. **换一份完好的 Pro 二进制（首选）。** 用官方下发的同版本（或更新版本）**linux_arm64** 包替换 `/root/CloudrevePro/cloudreve_v4.18.0`，其余文件不用动。替换后一条命令即可验证：见第六节。
2. 或者，用 amd64 版在 Baixiong 跑需要 qemu-user（本机未安装、binfmt 未注册），属额外改造，不建议。
3. 若手头没有 arm64 官方包，可考虑回到从 InstaBox 15213 复制实例所在的机器（amd64）上继续测试，那边同版本原件可正常启动（`docs/instabox-pro-15213-copy-deploy.md` 有记录）——但那属于另一台机器，需用户确认。

## 六、二进制换好后的一键上线步骤

```bash
cd /root/CloudrevePro
# 1) 验证二进制能读到 license（应停在监听日志而非崩溃）
set -a; . ./license.env; set +a
timeout 20 ./cloudreve_v4.18.0 -c data/conf.ini
# 2) 写入随机 HashIDSalt（首次启动前做）
sed -i "s/^HashIDSalt = .*/HashIDSalt = $(head -c 32 /dev/urandom | base64 | tr -d '/+=' | head -c 32)/" data/conf.ini
# 3) 装 systemd 服务（cloudreve-pro15212.service，enable 开机自启）
```

systemd 单元要点（此次未写盘，待二进制可用后创建）：

```ini
[Unit]
Description=Cloudreve Pro 15212 (Baixiong)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/root/CloudrevePro
EnvironmentFile=/root/CloudrevePro/license.env
ExecStart=/root/CloudrevePro/cloudreve_v4.18.0 -c /root/CloudrevePro/data/conf.ini
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

首次访问后，**第一个注册的用户自动成为管理员**（v4 初始化流程，`email_active` 默认关闭）。按团队约定，不预设也不修改管理员密码。

## 七、回滚

本次未创建服务、未改任何既有实例、未做端口发布，无需回滚。若要去掉本次新增的准备文件：`rm -rf /root/CloudrevePro/data /root/CloudrevePro/license.env`（用户自己放入的三个文件保持原样）。
