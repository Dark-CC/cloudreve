# InstaBox 官方 Pro 5212 复制实例部署报告（宿主 15213）

部署日期：2026-10-28。将 InstaBox 机上用户的官方 Cloudreve Pro（宿主 5212，从机模式）复制一份，以**独立单机（master）模式**跑在宿主端口 15213，并发布公网。与生产 5212 从机、aria2（:6800）、fork 测试实例（15212）完全并存，未改动任何生产文件与配置。

## 一、原 5212 实例实查结果（只读检查）

| 项目 | 实查值 |
|---|---|
| 形态 | **裸二进制**（非容器），systemd 服务 `cloudreve.service`（`Restart=on-failure`，root，2026-10-25 12:26 起） |
| 版本 | 官方 **v4.18.0**（Pro，Commit `#9904711`，aaronliu 构建路径） |
| 目录 | `/data/ouFmtYpb/cloudreve_v4.18.0_linux_amd64/`（binary 91MB + `config.ini` + `license_key` + `data/` + `start.sh`） |
| 配置 | `[System] Mode = slave, Listen = :5212` + `[Slave] Secret = …` → **确认是从机** |
| 所属 master | `https://pan.cdn.ad`（日志可见 slave ping 回调与 `/api/v4/slave/download/*` 任务下发，来源 IP 155.117.85.143） |
| 数据库 | `data/cloudreve.db`，users=0、nodes 仅默认 master 记录 → 纯从机，无本地用户 |
| license 机制 | `license_key` 文件两行（`CR_HEXOR_DATA` + `LICENSE_KEY`），`start.sh` 交互式用它以环境变量 + `--license-key` 启动；**当前 systemd 进程无任何 license 参数/环境变量仍正常运行**——原因见下 |
| 下载组件 | `aria2.service`（127.0.0.1:6800，生产组件，未触碰） |
| 公网 | https://5212-agqw5litn4acpbo7b5axu3ycku.instabox.tech（`/` 与 `/api/v4/site/ping` 均 404 是 **slave 模式不服务前端/站点路由的正常表现**，本机 127.0.0.1:5212 同样 404，转发链路本身正常） |

## 二、从机身份的处理决定（重要）

原实例是从机（node 连 `pan.cdn.ad`）。若原样双开，两个同 Secret 的从机连同一 master 会互相冲突并可能干扰生产。因此**复制版改为独立单机（master）模式**：全新空数据库、自带初始化、不连任何 master， Slave 配置整段不复用。若需要复制版保持从机身份（例如做第二个存储/下载节点），请另行指示后再调整。

## 三、license 结论（实查 + 实测）

- **slave 模式 license 检查宽松**：原实例 db 中 `settings.license` 为空（length=0）且无启动参数仍正常跑。
- **master 模式每次启动都必须提供 license**：无参数启动报 `Please specify license key by "--license-key" argument or "CR_LICENSE_KEY" env`。
- 复制版首次启动用 `--license-key` + `CR_HEXOR_DATA` 从官方获取并落库（`License loaded, expire=2126-10-29`，百年期）；之后**只需环境变量 `CR_LICENSE_KEY` + `CR_HEXOR_DATA` 即可直接复用 db 中已存的 license，不再向官方重复获取**（实测日志无 "trying to obtain"）。

## 四、部署形态

| 项目 | 值 |
|---|---|
| 目录 | `/data/pro15213/`（完全独立，未写入原目录任何内容） |
| 组成 | `cloudreve_v4.18.0`（自原目录复制，sha256 `1f2b0c14…701c` 一致）、`license_key`（600）、`license.env`（600，由 license_key 生成的 EnvironmentFile）、`config.ini`（新建：`Mode = master, Listen = :15213`）、`data/cloudreve.db`（全新初始化） |
| 服务 | `cloudreve-pro15213.service`（已 enable，机器重启/8h 强停恢复后自启；license 走 `EnvironmentFile`，**不泄露在 ps 进程列表**） |
| 公网 | https://15213-agqw5litn4acpbo7b5axu3ycku.instabox.tech（InstaBox 公共端口 `pv-utu5sccqektc2gsge62tfmgmt4`，name `cloudreve-pro-copy`） |
| 管理员 | v4 初始化流程：**首个注册用户自动进入 Admin 组**（`email_active` 默认关闭，无需邮箱验证）。凭据按“凭据不入库”政策不写入本仓库文档；保存在机上 `/data/pro15213/.admin_creds`（600），并已直接告知用户 |

## 五、验证结果（2026-10-28）

- 公网首页 `GET /` → **200**（title `Cloudreve`）
- 公网 `GET /api/v4/site/ping` → `{"code":0,"data":"4.18.0-pro"}`
- 公网 `POST /api/v4/session/token` 登录 → 成功，group=Admin
- `systemctl restart` 自愈测试 → 通过（license 从 db 复用加载）
- 生产完整性核查：`config.ini`/`license_key` sha256 与部署前基线**完全一致**；pid 30019 连续运行未重启；`cloudreve.service`、`aria2.service` active；5212/6800 监听不变；fork-test 容器（15212）Up
- 磁盘：/data 5.4G → 5.5G（复制体 ~92MB）

## 六、常用运维命令

对该机只能通过 InstaBox API `POST /v1/machines/m_agqw5litn4acpbo7b5axu3ycku/exec`（user 身份，需 sudo）执行：

```bash
# 状态 / 日志
systemctl status cloudreve-pro15213.service
sudo journalctl -u cloudreve-pro15213.service -f

# 重启 / 停止 / 启动
sudo systemctl restart cloudreve-pro15213.service
sudo systemctl stop cloudreve-pro15213.service
sudo systemctl start cloudreve-pro15213.service
```

## 七、回滚 / 清理（一条命令）

```bash
sudo systemctl disable --now cloudreve-pro15213.service && sudo rm -rf /data/pro15213 /etc/systemd/system/cloudreve-pro15213.service && sudo systemctl daemon-reload
```

如需同时撤销公网端口（可选）：

```bash
curl -s -H "Authorization: Bearer $INSTABOX_API_KEY" -X DELETE \
  https://api.instabox.sh/v1/machines/m_agqw5litn4acpbo7b5axu3ycku/ports/15213
```

清理不影响生产 5212 从机、aria2 与 fork 测试实例 15212。

## 八、注意事项

- InstaBox 免费机 8 小时强停 + 冷却后整机重启，systemd 已 enable 会自动拉起本实例与生产实例；机器挂起期间公网 URL 返回 503（与 15212 行为一致），Baixiong 上的 keepalive 定时器会自动 resume。
- master 模式每次启动需要 `license.env`（已就位）；该文件与 `license_key` 均为 600 权限，切勿外传仓库。
- 复制版是空库新站：无生产数据、无存储策略配置，站点名/URL 等可在 `/admin` 面板自行调整。
