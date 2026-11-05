# 测试站迁移：Baixiong 编译部署 + Cloudflare 临时隧道

**日期**：2026-11-05
**来源**：todo「把测试站搬到 Baixiong 上编译部署，用 Cloudflare 临时隧道打通外网」

## 结论

测试站已从 InstaBox 迁到 **Baixiong**，用 #67 修复后的代码（commit `29a3e469`）自行编译部署，
通过 Cloudflare 临时隧道对外提供服务，并已用**真实浏览器**完成上传链路的端到端复验：**PASS**。

原 InstaBox 测试站（15221 端口）已停止服务，那台机器上的数据目录 `/data/cr-openlist-15221` 原样保留、未删除。
15213 / 15220 等既有实例全程未触碰。

> 注意所述路径在 **InstaBox 那台机器**上；Baixiong 上没有这个目录，也不需要它。

## 访问方式

| 项目 | 值 |
| --- | --- |
| 公网地址（临时隧道） | `https://frederick-incidents-hardware-controlled.trycloudflare.com` |
| 本机地址 | `http://127.0.0.1:15230` |
| 管理员账号 | `admin@cr15230.local` |
| 管理员密码 | `Cloudreve@2026`（与 15220 测试站保持一致的固定口令） |
| 版本 | fork 4.23.8，`LastCommit=7e2fe95` |

> 临时隧道的域名是 Cloudflare 每次启动随机分配的，**重启 cloudflared 后域名会变**。
> 当前域名写在上面的表格里；域名变了按下面「重建隧道」取新域名即可，服务本身不用动。

## 部署形态

全部落在持久路径 `/data/cr-bx-15230/`，没有使用 `/tmp`：

| 路径 | 内容 |
| --- | --- |
| `bin/cloudreve-4.23.8-bx15230` | 部署二进制（109M） |
| `bin/cloudreve-4.23.8-bx15230.orig` | 回滚副本（与部署版同源） |
| `data/` | 运行时数据（SQLite 库、上传缓存） |
| `conf/config.ini` | 配置（`DBType=sqlite3`，监听 `*:15230`） |
| `opl/` | 临时 OpenList 实例的数据与文件落盘目录 |
| `logs/` | 服务与隧道日志、E2E 结果 |
| `conf/*.service` | 两个 systemd unit 的源文件副本（实际装在 `/etc/systemd/system/`） |
| `e2e-result-PASS.txt` | 端到端复验的最终结果（走公网隧道域名跑出） |

- Cloudreve 由 systemd 托管：`cr15230.service`（`systemctl {start,stop,restart,status} cr15230`，`journalctl -u cr15230`）。
- 隧道同样由 systemd 托管：`cloudflared-cr15230.service`，已 `enable`，挂了会自动重启。
- OpenList 作为对照存储后端跑在容器 `cr-opl-15230`（`127.0.0.1:15244`），
  storage id `1`，mount `/crbx` → `/data/cr-bx-15230/opl/files/crbx`，guest 权限 `12`。
- Cloudreve 侧对应存储策略 id `2`「OpenList test (bx15230)」，`mount_path=/crbx`，
  `dir_name_rule=uploads/{uid}/{path}`，Admin 组默认策略已切到它。

## 验证结论（#67 修复是否真的生效）

用 `scripts/e2e/upload-e2e.js` 在真实 chromium 里走完整前端路径：
登录 → 进入文件列表 → 把随机内容文件**拖进窗口**完成上传 → 检查列表 → 刷新再检查。

最终这轮验收是**直接跑在公网隧道域名**（`https://frederick-incidents-hardware-controlled.trycloudflare.com`）上的，
即浏览器—Cloudflare—隧道—Cloudreve 全链路，结果为 `PASS`。

判定项与实测值：

| 判定项 | 含义 | 实测 |
| --- | --- | --- |
| `logged-in` | 登录并进入文件列表 | `true` |
| `uploader-ready` | 前端上传组件挂载完成 | `true` |
| `uploading-before` | 上传前「上传中」占位数量 | `0` |
| `file-in-list` | 上传的文件出现在列表里 | `true` |
| `uploading-after` | 上传完成后「上传中」占位数量 | `0` |
| `file-in-list-after-reload` | 刷新页面后文件仍在列表 | `true` |
| `uploading-after-reload` | 刷新后「上传中」占位数量 | `0` |
| **`VERDICT`** | 总判定 | **`PASS`** |

另外核对了两处旁证：

- OpenList 落盘目录 `/data/cr-bx-15230/opl/files/crbx/uploads/2/` 文件数量随每次上传增长；
- Cloudreve 列表接口返回的条目 `type=0`、`size=300000`、`metadata={}`——**没有残留 `sys:upload_session_id`**，
  这正是 #67 修复要解决的问题（此前会留占位、一直显示「上传中」）。

结论：#67 的修复在 Baixiong 自建的 4.23.8 上复现通过，前端不再过滤 `upload_urls`、后端 `CompleteUpload`
不再因 owner/settings 为空而中断，上传完成后占位被正确清掉。

## 保活与重建

### 服务

```bash
systemctl status cr15230          # 看状态
systemctl restart cr15230         # 重启
journalctl -u cr15230 -n 100      # 看日志
```

### OpenList 容器

```bash
docker start cr-opl-15230         # 机器重启或容器停了就拉起来
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:15244/
```

### 重建隧道（域名会变）

隧道已是 systemd 服务，机器重启会自动拉起：

```bash
systemctl status cloudflared-cr15230     # 看状态
systemctl restart cloudflared-cr15230    # 重启（会分配新域名）

# 从日志里取当前域名
grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' /data/cr-bx-15230/logs/cf-tunnel.log | tail -1
```

拿到新域名后打开即可，Cloudreve 侧无需改配置。

### 重跑端到端复验

```bash
cd /data/cr-bx-15230/browsertest
# 首次需要：PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm i playwright-core@1.48.2
bash run-e2e.sh           # run-e2e.sh 里的 BASE_URL 可改成本地或隧道域名
cat /data/cr-bx-15230/logs/upload-e2e-result.txt   # 末行为 VERDICT = PASS 即通过
```

脚本本身在仓库里：`scripts/e2e/upload-e2e.js`（纯 playwright-core，不下载浏览器，用机器上已有的 chromium）。

## 清理

构建完已清理临时与中间产物（**未使用 `docker image prune -af`**，未删任何镜像）：

- `src/`（源码树 + `frontend/node_modules` + 前端 build 产物）：约 1.5G
- `browsertest/node_modules`（playwright-core）：约 8M
- 历次试跑的 e2e 日志与临时诊断脚本

`/data/cr-bx-15230` 从 **1.7G 降到 593M，释放约 1.1G**；根分区磁盘占用从 36G 降到 35G。
保留：部署二进制与回滚副本、`conf`、`bin/data`、`opl`、`logs`、E2E 脚本与结果。

## 踩过的坑（后续维护参考）

1. **不要并发跑多个 chromium**：2 核 ARM 上会互相拖慢到页面几乎不响应，看起来像"卡死"，其实是互相抢 CPU。
2. **不要注册 `page.on('console')`**：Cloudreve 前端日志量很大，逐条回调会把页面主线程拖死；脚本改为按页面文本断言。
3. **上传入口用拖拽**：页面上的 `#upload-file-input-1` 是隐藏 input，`setInputFiles` 选中它不会触发上传；
   真正的入口是 `dragenter/dragover/drop` 事件，脚本用 `DataTransfer` 派发，等价于用户把文件拖进窗口。
4. 登录是**两步式**（先邮箱回车，再密码），且登录后不一定自动跳转，脚本里有 `goto('/home')` 兜底。
5. 隧道域名走 Cloudflare 时前端会报一次 WebAuthn `SecurityError`（RP ID 不是可注册域后缀），
   属预期噪声，不影响登录与上传。

## 已知限制

- 临时隧道 `trycloudflare` **不保证长期稳定**，进程挂了链接即失效；要长期可用需换成具名隧道（需 Cloudflare 账号）。
- OpenList 是为本次验证临时起的对照实例，随主机重启不会自动恢复，需要手动 `docker start cr-opl-15230`。
- 当前 Cloudreve 与 OpenList 都只听本机/本地回环，对外仅通过隧道暴露。
