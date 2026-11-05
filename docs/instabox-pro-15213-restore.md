# InstaBox Pro 实例 15213 恢复报告（2026-11-06）

## 一、结论

**已恢复并实测通过，可直接测试。**

- 访址：<https://15213-agqw5litn4acpbo7b5axu3ycku.instabox.tech>
- 管理员：`admin@pro15213.local`（密码另附，见 `/data/pro15213/.admin_creds`，未写入本报告）
- 版本：`4.18.0-pro`（官方 v4.18.0 master 单机模式）

## 二、故障真因：不是实例挂了，是端口发布没了

排查顺序与结果：

1. InstaBox 机器本体在线。`GET /v1/machines/m_agqw5litn4acpbo7b5axu3ycku` → `state: running`、`desiredState: running`、`runtime.ready: true`，无 OOM、无暂停、无冷却（`cooldownUntil` 已是过去时间）。
2. Pro 服务一直在跑。`systemctl is-active cloudreve-pro15213.service` → `active`（自 2026-10-28 09:11 UTC 起连续运行 1 周 1 天），进程 `/data/pro15213/cloudreve_v4.18.0 -c config.ini`，监听 `*:15213`。
3. 应用健康。机内 `curl http://127.0.0.1:15213/api/v4/site/ping` → `{"code":0,"data":"4.18.0-pro"}`。
4. **平台侧端口发布确实消失了**。`GET /v1/machines/.../ports` 只返回 `5244`、`8080`、`15221` 三条，文档里记载的 15213 发布（`pv-utu5sccqektc2gsge62tfmgmt4` / `cloudreve-pro-copy`）不在列表里。公网 404「This URL is not published」由此而来——是 InstaBox 平台层没有这条发布，与 Cloudreve 自身无关。

即：机器、数据、服务、license 全都完好，只有一条对外发布的映射丢了。

## 三、修复动作（仅一步）

重建端口发布：

```
POST /v1/machines/m_agqw5litn4acpbo7b5axu3ycku/ports
{"port":15213,"name":"cloudreve-pro-copy"}
→ id: pv-i6yvkxq7vyq6q62qjc3zlmpila
→ url: https://15213-agqw5litn4acpbo7b5axu3ycku.instabox.tech
```

域名与文档记载完全一致（同一台机器、同一端口，域名由 `<port>-<machineId>` 规则生成）。没有改配置、没有重启服务、没有动数据库、没有改任何密码。

## 四、交付实测（全部在公网侧执行，非机内自测）

| 检查项 | 结果 |
| --- | --- |
| 首页 `GET /` | HTTP 200 |
| `GET /api/v4/site/ping` | `{"code":0,"data":"4.18.0-pro"}` |
| 管理员登录 `POST /api/v4/session/token` | HTTP 200，`code: 0`，拿到 `access_token` |
| 登录身份 | `admin@pro15213.local`，组 `Admin` |
| 带 token 列根目录 `GET /api/v4/file?uri=cloudreve://my` | `code:0`，正常返回文件列表（含既有测试文件 `230x0w.png`） |
| 站点配置 `GET /api/v4/site/config/basic` | `code:0`，instance_id 正常 |
| Pro 管理端 `GET /api/v4/admin/summary` | HTTP 200 |

四项交付判据（公网可打开 / ping 正常 / 管理员可登录 / Pro 管理端可用）全部实测通过。

## 五、未触碰项（与执行约束的核对）

只读核查发现，本机当前实际在跑的 Cloudreve 相关服务**只有 Pro 15213 一个**：

- `systemctl --state=running` 中与 cloudreve 相关的仅 `cloudreve-pro15213.service`；
- `docker ps -a` 仅两条：`baixiong-relay`（Up 5 days, healthy）与 `cr-opl-15221`（已 Exited 5 hours，非本次操作所致）；
- 端口监听中不存在 5212、15212、15220、6800（aria2）。

也就是说，Spec 与旧报告里提到的「同机生产 5212 从机、15212 fork 测试容器、15220 实例、aria2:6800」在当前这台机器上**均已不在**——它们此前的下线与我无关，本次操作也没有停或改任何现存服务。`/data` 下既有目录全部保留，未删除任何数据。

## 六、给测试者的提示

- 免费机限制仍然生效：空闲 30 分钟自动暂停、连续运行 480 分钟强制暂停，恢复需要 5–20 分钟冷却（用户已接受此限制，不再单独确认）。
- 如果**再次**出现公网 404「This URL is not published」，症状极可能还是端口发布丢失，而不是实例挂了——按本报告第三节一条 POST 即可恢复。建议后续把这一步做成例行检查项。
- 实测过程中为避免密码留在 shell 历史/日志，仅在本机内 `sudo cat` 读凭据，未写入任何文件或报告。
