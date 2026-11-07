# pan.cdn.ad 生产站重启 + 设置缓存清理 —— 实测报告

> 执行日期：2026-11-07 13:26–13:30（+08:00）　|　目标机：`155.117.85.143`（`root`，密钥 `SSH_PROD_155`）
> 触发：用户原话「pan.cdn.ad 这个帮我重启一下前端，现在有点缓存问题」
> 性质：**生产环境操作**（626 用户 / 673,178 文件在线）
> 凭据：只走团队密钥，未写入任何文件、日志或本报告

---

## 0. 一句话结论

**已重启 Cloudreve 应用容器并清空 222 个设置缓存键，站外实测全部正常。** 用户感知的不可用窗口约 **23 秒**，期间访问会被 nginx 返回 502；数据库、Redis、旧 Pro 站均未受影响（三个容器的启动时间戳一字未变）。重启后 `show_desktop_app_promotion` 仍为 `0`，「客户端下载」入口保持关闭，未被重启打回。

**关于「缓存问题」的真实来源**：服务端已无残留。实测 7 个 CDN 节点与源站返回的前端资源指纹完全一致，静态资源本身带内容指纹（与会话无关）。若用户仍看到旧页面，问题在 **浏览器端或 CDN 边缘节点**，不在本站代码——建议见第 7 节。

---

## 1. 操作前基线（13:26:43）

```
container: cloudreve-fork
image:     cloudreve/cloudreve:latest  (sha256:f7a4641...dd9a)
created:   2026-11-06T17:21:53Z
startedAt: 2026-11-06T17:56:06Z        （重启前已稳定运行 12 小时）
restartCount: 0        restartPolicy: unless-stopped
ports:     127.0.0.1:5213 -> 5213/tcp  （仅绑本地，对外只走 nginx 443）
network:   cloudreve-fork_fork-net
mounts:    /opt/cloudreve-fork/cloudreve       -> /cloudreve/data
           /opt/cloudreve-fork/bin/cloudreve-4.23.8 -> /cloudreve/cloudreve
compose:   项目 cloudreve-fork，服务名 cloudreve-fork，配置文件 /opt/cloudreve-fork/docker-compose.yml
```

运行架构（本次实测确认）：

```
浏览器 → CDN 节点 (bs.cdn.ad, 162.247.5.68-76, 共 7 个)
       → 源站 155.117.85.143:443 (nginx 容器，配置 /opt/nginx/nginx.conf:226 proxy_pass 127.0.0.1:5213)
       → cloudreve-fork 容器 :5213
旧 Pro 4.18 仍在 :5212，只绑 127.0.0.1，不接流量（本次全程未触碰）
```

## 2. 数据基线（重启前，13:26:52）

| 表 | 行数 |
| --- | ---: |
| users | 626 |
| files | 673,178 |
| entities | 612,894 |
| shares | 350 |
| settings | 299 |
| public 下总表数 | 38 |

`db_version` 标记齐全：`4.16.1` / `4.17.0` / `4.18.0` / `4.18.0-pro` / **`4.23.8`**。

## 3. 执行动作

### 3.1 清设置缓存（13:27:1x）

- 范围：**仅 `setting_*` 前缀**，共 **222 个键**。
- 方式：`SCAN` 列出后逐键 `UNLINK`（非阻塞删除）。**未使用 `FLUSHALL` / `FLUSHDB`**。
- 效果核对：

| 指标 | 清理前 | 清理后 |
| --- | ---: | ---: |
| Redis 总键数 | 272 | 55 |
| `setting_*` | 222 | 4（扫描间隙应用新写入） |
| `entity_url_*`（文件直链缓存） | 43 | **44（完好保留）** |

键名样例（只报键名，不打印值）：`setting_captcha_type`、`setting_defaultTheme`、`setting_default_group`、`setting_show_desktop_app_promotion`、`setting_cron_*`、`setting_fs_event_push_*`、`setting_thumb_*` 等。

### 3.2 重启应用容器（13:27:57 → 13:28:10）

```bash
docker restart cloudreve-fork      # 单容器重启，不牵连 PG/Redis
```

`docker restart` 本身耗时 **12.1 秒**；从发出命令到内部 `127.0.0.1:5213` 首次返回 200 共 **22.7 秒**——这是用户实际感知的不可用窗口。

## 4. 验收结果

### 4.1 容器状态 ✅

```
cloudreve-fork             Up (重启后)
cloudreve-fork-postgresql  Up 12 hours   StartedAt=2026-11-06T17:18:19Z  ← 未变
cloudreve-fork-redis       Up 12 hours   StartedAt=2026-11-06T17:18:19Z  ← 未变
cloudreve-service (旧Pro)  Up 12 hours   StartedAt=2026-11-06T17:11:28Z  ← 未变
```

三个不该动的容器 `StartedAt` 与 `RestartCount=0` 均与操作前完全一致，**确实没被牵连**。端口绑定 `127.0.0.1:5213->5213/tcp` 不变。

### 4.2 日志无 fatal / panic ✅

重启后窗口内 `grep -icE "panic|fatal"` = **0**。启动后正常处理业务请求（PROPFIND 207、分享下载 302、`/api/v4/user/capacity` 200 等）。

### 4.3 站外（经 CDN）可访问 ✅

| 路径 | HTTP | 备注 |
| --- | ---: | --- |
| `/` | 200 | 经 CDN 三次探测 200（节点 162.247.5.75 / .70） |
| `/connect` | 200 | |
| `/signin` | 200 | |
| `/home` | 200 | |
| `/api/v4/site/config/app` | 200 | `{"code":0,"data":{"shop_nav":false,"score_enabled":false}}` |
| `/api/v4/site/ping` | 200 | 返回版本 `4.23.8` |

### 4.4 客户端下载入口仍关闭 ✅

- `settings.show_desktop_app_promotion` = **`0`**（DB 值与操作前一致）
- `/api/v4/site/config/app` 中 `desktop_app_promotion` 出现次数 = **0**（`omitempty`，关闭时不下发）
- 缓存重建后 Redis 中该键存的值经 gob 解析仍为字符串 `0`，与新写入的 DB 值一致，**没有被旧值打回**

### 4.5 数据完整、未重新建表 ✅

重启后行数与第 2 节基线**逐项相等**：users 626 / files 673,178 / entities 612,894 / shares 350 / settings 299，表数 38，`db_version_4.23.8` 标记仍在。

## 5. 回滚方式

无异常，未触发回滚。若今后需要：

- 回滚本次重启：容器已按原参数（compose 项目 `cloudreve-fork`、同镜像、同挂载）启动，无需额外动作。
- 回滚站点版本（fork → 旧 Pro）：把 `/opt/nginx/nginx.conf:226` 的 `proxy_pass http://127.0.0.1:5213` 改回 `5212` 并 `docker exec nginx nginx -s reload`，秒级生效，旧容器与旧库全程未动。

## 6. 缓存键重建情况（重启后，13:29）

应用按需重新填充了缓存：`setting_*` 现 125 个（用到才建，非全量）、`entity_url_*` 45 个、Redis 总键 176 个。属正常行为，说明缓存层工作正常。

## 7. 「缓存问题」的真实来源与建议

### 实测发现

1. **源站与全部 7 个 CDN 节点内容一致。** 逐一 `--resolve` 打每个节点，返回的 HTML 均引用同一份前端入口 `src="/assets/index-DsCJIZF-.js"`，与源站 155 直连结果相同——**CDN 上没有残留旧版页面**。
2. **前端资源本身带内容指纹**（`index-DsCJIZF-.js`、`common-5dK6VpSw.js`、`index-qQxd8I_f.css`）。内容变则文件名变，天然免疫缓存。
3. **静态资源缓存策略**：源站下发 `Cache-Control: public, max-age=31536000`（一年），CDN 原样透传。这不是问题——指纹文件名保证了安全；但意味着**如果哪天覆盖部署了同名资源，浏览器与 CDN 会持续一年不取新内容**，这是唯一需要留意的隐患。
4. **HTML 与 API**：`Cache-Control: public, no-cache`，即「可存但每次必须回源验证」，不会提供过期页面。

### 结论

**服务端不存在需要清掉的陈旧缓存，本次重启已把服务端缓存完全重建。** 若用户那边仍看到旧界面，原因在链路末端：

- **浏览器本地缓存 / Service Worker**：建议让用户强制刷新（Windows `Ctrl+F5`，macOS `Cmd+Shift+R`），或在开发者工具 Application → Storage 里 Clear site data。
- **CDN 边缘节点个体差异**：7 个节点中若某个节点持有旧对象，用户恰逢解析到它就会看到旧页；通常几分钟内自动失效。若长期复现，需到 CDN 控制台做该域名的缓存刷新（purge）。
- **本机 DNS 缓存**指向了已下线的旧节点。

### 后续建议（交用户定夺，本次未擅自改动）

- 静态资源一年长效缓存 + 指纹文件名是合理配置，**建议保持**；只需确保日后发版时前端构建产物文件名一定带新指纹（vite 默认行为，当前已满足）。
- 若 CDN 侧频繁出现单节点旧内容，可在 CDN 控制台将 `/` 与 `/api/*` 设为「不缓存」，静态资源维持一年。此项属 CDN 配置变更，需用户授权后另行执行。

---

## 8. 本次未做的事（符合约束）

- 未重启、停止、重建 PostgreSQL 与 Redis 容器
- 未触碰 `:5212` 上的旧 Pro 容器
- 未修改 nginx 配置
- 未修改任何用户账号、密码或业务设置项的值（只清缓存，未写值）
- 未使用 `FLUSHALL` / `FLUSHDB`
- 未改任何代码
