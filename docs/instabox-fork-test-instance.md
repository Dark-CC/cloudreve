# InstaBox fork 独立测试实例部署说明

部署日期：2026-10-27。用于 Dvorinka/cloudreve fork 的二开测试，与 InstaBox 机上的生产环境（裸二进制 Cloudreve v4.18.0 slave :5212 + Aria2 :6800）完全隔离，未改动任何生产配置。

## 实例概要

| 项目 | 值 |
|---|---|
| 机器 | InstaBox `m_agqw5litn4acpbo7b5axu3ycku`（instabox-baixiong） |
| 容器名 | `cloudreve-fork-test`（`--restart unless-stopped`） |
| 镜像 | `cloudreve-fork:4.23.4-invfix2e1d5d79`（自建，含邀请码弹窗布局修复 `2e1d5d79`；二进制 sha256 `db17f972…97c359`；2026-10-27 前为 `ghcr.io/dvorinka/cloudreve:latest` digest `sha256:1f794775…`，该旧镜像仍在机上可回滚，构建管线与验收见 `docs/instabox-invfix-deploy/README.md`） |
| 端口 | 容器内 5212 → 宿主机 `15212`（生产 5212 未受影响） |
| 数据目录 | `/data/cloudreve-fork-test`（bind mount → 容器 `/cloudreve/data`，含 `cloudreve.db` 与 `conf.ini`） |
| Docker data-root | `/data/docker`（新建 `/etc/docker/daemon.json`，此前 Docker 无任何容器，属安全改动） |
| 公网访问 | https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech（InstaBox 发布端口，不走机器公网 IP——InstaBox 机的 IP 不直接对外开这些端口） |
| 初始管理员 | 首个注册用户自动成为管理员（v4 行为，见官方 docs/zh/overview/deploy/configure.md） |

> ⚠️ 管理员凭据更新说明（2026-10-26）：本文档最初记录的首注账号 `admin@fork-test.local` 已被用户弃用并删除，现由用户自建的管理员账号取代；具体凭据以用户处保存为准，不再写入仓库。若凭据丢失需要重建管理员：`docker exec -it` 进入容器删除数据（或清理 `/data/cloudreve-fork-test/cloudreve.db`）重新初始化后，再次首注即可。

## 管理员账号获取方式

Cloudreve v4 不再打印初始管理员密码——**第一个注册的用户自动进入 Admin 组（group 1）**。本实例已完成首注，当前管理员账号由用户本人维护（见上方更新说明，勿在文档/聊天中记录明文）。

登录 https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech/login 后可进 `/admin` 管理面板。

## 常用运维命令

对该机只能通过 InstaBox API `/v1/machines/{id}/exec`（cmd 字符串，user 身份，/root 需 sudo）执行：

```bash
# 状态 / 日志
sudo docker ps --filter name=cloudreve-fork-test
sudo docker logs --tail 50 cloudreve-fork-test

# 重启 / 停止 / 启动
sudo docker restart cloudreve-fork-test
sudo docker stop cloudreve-fork-test
sudo docker start cloudreve-fork-test

# 更新到 fork 最新镜像（自建镜像管线，见 docs/instabox-invfix-deploy/README.md）
# （官方 ghcr 镜像不含 fork 的二开改动，仅作回滚用）
sudo docker stop cloudreve-fork-test && sudo docker rm cloudreve-fork-test
sudo docker run -d --name cloudreve-fork-test --restart unless-stopped \
  -p 15212:5212 -v /data/cloudreve-fork-test:/cloudreve/data \
  ghcr.io/dvorinka/cloudreve:latest

# 只读查库（宿主机已装 sqlite3）
sudo sqlite3 "file:/data/cloudreve-fork-test/cloudreve.db?mode=ro" "select id,email,nick,group_users from users;"
```

注意：容器内自带 Aria2 enabled 的默认配置（镜像 ENV `ENABLE_ARIA2`），远程下载任务走容器自身 aria2（镜像内 6800），与宿主机生产 Aria2（127.0.0.1:6800，root 运行、串行、force-save=false）互不相干。

## 验证记录（2026-10-27）

- 宿主机 `curl http://127.0.0.1:15212/` → HTTP 200
- 公网 `https://15212-…instabox.tech/`、`/login` → HTTP 200
- `GET /api/v4/site/ping` → `{"code":0,"data":"4.23.4"}`
- `POST /api/v4/user`（注册）→ code 0，用户入 group 1（Admin）
- `POST /api/v4/session/token`（登录）→ code 0，公网 URL 同样验证通过

## 磁盘

部署前 `/data` 余量 10G（49%）。镜像与容器数据均在 `/data`（Docker data-root + bind mount）。免费机 8h 强停 + 闲置 30 分钟暂停由 Baixiong 上的 instabox-keepalive 定时器保活（见 ops/instabox-keepalive/），恢复后容器随 `--restart unless-stopped` 自启。
