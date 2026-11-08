# 香港站 155 启用 v4.23.9 最终版二进制（2026-11-08）

- 站点：香港站 = `155.117.85.143`，`https://pan.cdn.ad`，fork 以容器 `cloudreve-fork` 运行，容器内监听 `:5213`，宿主 nginx 443 反代
- 作业时间：2026-11-08 17:00:23 – 17:01:31（CST）
- 关联：#86 只读核查报告 `docs/ops/b-version-audit-20261108.md`（该报告指出香港站仍在跑中间版 `ada18e24…`，最终版 `.new` 已落盘未启用）
- 作业性质：**仅替换二进制 + 重启一个容器**。未改数据库、未改任何设置项、未清 Redis、未动 nginx 与其它业务、未改任何账号密码。

---

## 一、结论（一句话）

香港站已切到与国内站逐字节相同的 v4.23.9 最终版（`4ca50c55…`），运行态、回环、外网三项均为 `v4.23.9`，日志无 panic、无唯一索引冲突；不可用窗口实测约 **30 秒**；三个「不动」容器的 `StartedAt` 全部未变。

---

## 二、目标确认：要覆盖哪个宿主文件

由 `docker inspect cloudreve-fork` 的挂载表读得（非照抄文档）：

| 项目 | 值 |
| --- | --- |
| 容器内可执行文件 | `/cloudreve/cloudreve` |
| 宿主挂载源 | `/opt/cloudreve-fork/bin/cloudreve-4.23.8`（bind，`rw`，`rprivate`） |
| 容器入口 | `sh ./entrypoint.sh`（镜像 `cloudreve/cloudreve:latest`） |
| 容器主 PID（重启后） | 518022；其中 cloudreve 进程 PID 518123 |
| 数据挂载（未动） | `/opt/cloudreve-fork/cloudreve` → `/cloudreve/data` |

**要覆盖的就是宿主 `/opt/cloudreve-fork/bin/cloudreve-4.23.8`**（文件名带 4.23.8，内容却是 v4.23.9 中间版）。

容器内 `conf.ini` 说明（未改）：容器启动时由 env 覆盖 `Database.Name=cloudreve_fork`、`Host=postgresql`、`User=cloudreve`、`Redis.Server=redis:6379`。

---

## 三、切换前状态

| 项目 | 切换前 |
| --- | --- |
| 宿主在用文件 | `/opt/cloudreve-fork/bin/cloudreve-4.23.8`，90,095,778 B，mtime 11-07 16:26 |
| 在用文件 sha256 | `ada18e24d735c6219e6c410f5e822e19345ba86db702d41a2338e1fddba088ef`（v4.23.9 **中间版**） |
| 容器内运行文件 sha256 | `ada18e24…`（经 `/proc/<容器主PID>/root/cloudreve/cloudreve` 校验） |
| 待启用暂存文件 | `/opt/cloudreve-fork/bin/cloudreve-4.23.9.new`，90,103,970 B，mtime 11-07 17:21 |
| 暂存文件 sha256 | `4ca50c5535de025cfd9d8377c2b97b8ac404567ab31b98f83e1cfb16a0ff860e`（= 国内站运行文件） |
| 容器状态 | `running`，`StartedAt 2026-11-07T08:26:53Z`（CST 16:26:53），`RestartCount=0` |
| 回环 ping | `{"code":0,"data":"v4.23.9","msg":""}` |
| 磁盘 | `/dev/vda1` 193G，已用 76G，剩 118G |

### 三个「不动」容器的基线 StartedAt

| 容器 | StartedAt（UTC） | 状态 |
| --- | --- | --- |
| `cloudreve-fork-postgresql` | `2026-11-06T17:18:19.266907957Z` | running，Restarts=0 |
| `cloudreve-fork-redis` | `2026-11-06T17:18:19.348430015Z` | running，Restarts=0 |
| `cloudreve-service`（旧 Pro，:5212） | `2026-11-06T17:11:28.588694018Z` | running，Restarts=0 |

---

## 四、操作与时间线（CST）

| 时刻 | 动作 | 结果 |
| --- | --- | --- |
| 17:00:23.591 | `cp -p cloudreve-4.23.8 cloudreve-4.23.8.bak-v4.23.9-pre` | 备份 90,095,778 B，sha256 `ada18e24…`（与在用文件一致） |
| 17:00:29.448 | 校验 `.new` sha256 = `4ca50c55…` 通过后，`mv -f cloudreve-4.23.9.new cloudreve-4.23.8`（原子改名，不做原地截断，避免半写风险） | 目标文件 sha256 = `4ca50c55…`，90,103,970 B |
| 17:00:38.380 | 发出 `docker restart cloudreve-fork` | — |
| 17:00:49.530 | 容器退出（`State.FinishedAt`） | 旧进程优雅停机耗时约 11 秒 |
| 17:01:08.942 | 容器重新启动（`State.StartedAt`） | — |
| 17:01:10.172 | `docker restart` 命令返回 | 命令整体耗时约 31.8 秒 |
| 17:01:15 | 容器内 cloudreve 进程拉起 | PID 518123 |
| 17:01:18 | 日志：`Database schema is up to date.` | 无迁移动作，无建表 |
| 17:01:19 | 日志：`Listening to ":5213"` | 端口重新监听 |
| 17:01:31 | 外网复核全部通过（首页/接口 200） | — |

启动横幅证实版本与提交号：

```
   Vv4.23.9  Commit #3b5da976  Pro=false
```

（与国内站一致；`Commit` 为编译时 `HEAD`，判定仍以内容特征为准。）

---

## 五、恢复验证（逐项）

| # | 检查项 | 期望 | 实测 | 结论 |
| --- | --- | --- | --- | --- |
| 1 | 本机回环 `http://127.0.0.1:5213/api/v4/site/ping` | `v4.23.9` | `{"code":0,"data":"v4.23.9","msg":""}` | ✅ |
| 2 | 外网 `https://pan.cdn.ad/api/v4/site/ping` | `v4.23.9`，HTTP 200 | HTTP 200，`{"code":0,"data":"v4.23.9","msg":""}` | ✅ |
| 3 | 容器日志 panic / `duplicate key value violates unique constraint` | 0 | 0（重启后全量日志 561 行逐条查过） | ✅ |
| 4 | 容器内**正在运行**文件 sha256 | `4ca50c55…` | `/proc/518123/exe` → `4ca50c5535de025cfd9d8377c2b97b8ac404567ab31b98f83e1cfb16a0ff860e` | ✅ |
| 5 | 三个「不动」容器 StartedAt | 与基线一致 | PG `2026-11-06T17:18:19.266907957Z`、Redis `…17:18:19.348430015Z`、cloudreve-service `…17:11:28.588694018Z`，三者 Restarts 仍为 0 | ✅ |
| 6 | 站点首页 | HTTP 200 | `https://pan.cdn.ad/` → 200（2.24s） | ✅ |
| 7 | 接口健康 | 200 | `/api/v4/site/config/basic` 200、`/api/v4/site/config/app` 200（`{"shop_nav":false,"score_enabled":false}`） | ✅ |
| 8 | 前端静态资源 | 200 | 首页引用的 `/assets/index-*.js`、`/assets/react-*.js`、`*.css` 均 200 | ✅ |

### 5.1 三大核心特征串（对 `/proc/518123/exe` 计数，确认真的是「最终版」）

| 特征串 | 含义 | 计数 |
| --- | --- | --- |
| `SELECT name FROM settings WHERE deleted_at IS NOT NULL` | v4.23.9 迁移软删除修复（消除 `settings_name_key` 唯一索引冲突） | **1** ✅（切换前为 0） |
| `gcaptcha4.geetest.com` | 极验 v4 官方校验端点 | 1 ✅ |
| `api.geetest.com/v4/validate.php` | 已失效旧端点，应为 0 | 0 ✅ |
| `seedMissingDefaultSettings` | #183 启动补种，属 v4.23.9 之后、尚未发版 | 0（预期，非本次缺失） |

### 5.2 一条需要说明的日志（非本次引入）

重启后日志有一条 `[Error] Static resource version mismatch [Current 4.0.0-next, Desired: v4.23.9]`。

- 该条**在切换前每次启动都会出现**（11-07 15:23、11-07 16:27 两次启动均有完全相同的记录），属历史遗留提示；
- 前端资源实际正常下发（首页与全部引用资源 200），说明走的是内嵌/已构建资源，功能不受影响；
- 本次未按其提示删除 statics 目录重建——那属于另一件事，不在本任务范围。

---

## 六、不可用窗口（实测）

| 口径 | 数值 |
| --- | --- |
| 旧进程最后一条成功访问日志 | 17:00:46（GET 302） |
| 17:00:47–17:00:52 期间旧进程无任何访问日志 | 0 条 |
| 容器退出（FinishedAt） | 17:00:49.530 |
| 新进程开始监听 `:5213` | 17:01:19 |
| **服务端不可用窗口** | **约 30 秒**（17:00:49.5 → 17:01:19），按最后成功请求计约 32 秒 |
| 外网经 CDN 首次复核 200 | 17:01:31（含 CDN 回源，端到端上限约 44 秒） |

**与经验值的偏差**：记忆中的经验值是「容器约 12 秒、端到端约 23 秒」。本次实际为：停机阶段 11 秒（旧进程优雅退出，等待在途任务收尾）+ 容器启动到端口监听 10 秒（17:01:08.9 起 → 17:01:19 监听，其中日志初始化在 17:01:16，DB 连接与 schema 检查约 2 秒），`docker restart` 命令本身耗时 31.8 秒。

也就是说：**故障窗口比经验值长约一倍**，原因在「旧进程停机 11 秒 + 新进程初始化 10 秒」，而非容器无法启动或反复重启（`RestartCount=0`，一次成功）。后续若对停机时间敏感，可考虑 `docker stop -t 5` 后再 `docker start`，或预热验证；本次未做额外优化，只按任务要求一次性完成切换。

---

## 七、回滚（一句话）

```bash
cd /opt/cloudreve-fork/bin && cp cloudreve-4.23.8.bak-v4.23.9-pre .rollback.tmp && mv -f .rollback.tmp cloudreve-4.23.8 && docker restart cloudreve-fork && sha256sum cloudreve-4.23.8
```

预期输出 sha256 = `ada18e24d735c6219e6c410f5e822e19345ba86db702d41a2338e1fddba088ef`（中间版）。

- 回滚后同样只重启 `cloudreve-fork` 一个容器；PG / Redis / 旧 Pro 不受影响，无需 `compose down/up`。
- 回滚不需要动数据库：两个版本库结构相同（新版本启动时日志明确 `Database schema is up to date.`，未执行任何迁移）。

### 切换后的文件清单（`/opt/cloudreve-fork/bin/`）

| 文件 | 大小 | mtime | sha256 | 身份 |
| --- | --- | --- | --- | --- |
| `cloudreve-4.23.8`（运行中） | 90,103,970 | 11-07 17:21 | `4ca50c55…` | v4.23.9 最终版（= 国内站） |
| `cloudreve-4.23.8.bak-v4.23.9-pre`（新备份） | 90,095,778 | 11-07 16:26 | `ada18e24…` | 本次替换下来的中间版 |
| `cloudreve-4.23.8.bak-geetest` | 89,931,938 | 10-31 05:48 | `0e8bdbbc…` | 更早的 v4.23.8 |
| ~~`cloudreve-4.23.9.new`~~ | — | — | — | 已被改名启用（不再存在） |

---

## 八、明确未做的事

- 未执行 `docker compose down/up`；`/opt/cloudreve-fork/docker-compose.yml`（mtime 11-07 01:21）与 `/opt/nginx/nginx.conf`（mtime 11-07 01:58）未被触碰，nginx 仍反代 `127.0.0.1:5213`。
- 未连接或修改数据库（除应用自身启动时的 schema 检查），未执行任何 SQL。
- 未改任何设置项，**未清 Redis**（本次不需要）。
- 未修改任何用户账号、密码或权限；未上传/下载任何用户文件。
- 未发版、未打 tag、未改仓库代码。

---

## 九、遗留（供后续排期，不在本任务范围）

1. 两台（国内站 B、香港站）都还缺 #183 的 `seedMissingDefaultSettings` 启动补种——它属于 v4.23.9 之后、尚未发版的内容；下一次构建需带上。
2. `Static resource version mismatch` 提示长期存在，可单独核查 statics 版本标记来源。
3. 停机窗口 30 秒左右，若需更短可评估 `stop -t 5` / 蓝绿端口方案。
