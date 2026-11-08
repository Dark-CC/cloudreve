# 只读核查：国内站 B 正在运行的二进制属于哪个版本（2026-11-08）

- 核查时间：2026-11-08 16:3x（CST）
- 核查对象：国内站 B = `120.220.76.88`（`pan.qzyun.net`，宿主直跑）、香港站 155 = `155.117.85.143`（`pan.cdn.ad`，Docker 容器）
- 核查性质：**严格只读**。未重启任何服务、未替换或安装任何文件、未访问/修改数据库、未建临时库、未停任何进程；只用了 `systemctl show/status`、`ls -l`、`stat`、`sha256sum`、`strings`、`ps`、`docker inspect`、`curl` 本机回环。未读取、未输出任何凭据（`run.sh` 中的密码变量名仅知其为 `PG_PASSWORD`，值未读取）。
- 关联文档：`docs/ops/v4.23.9-geetest-html-dual-deploy.md`（部署报告）、`docs/admin-usersession-save-fix.md`（设置补种修复）

---

## 结论（一句话）

**国内站 B 跑的是 v4.23.9 的最终版**（`4ca50c55…`，含迁移软删除修复），**不是中间版**；反倒是**香港站 155 仍在跑中间版**（`ada18e24…`，缺迁移软删除修复）——它的最终版二进制已经落盘 24 小时，但从未启用。两台都还缺 `seedMissingDefaultSettings`（v4.23.9 之后合并、尚未发版的 #183 补种修复）。

---

## 一、两台运行态事实

| 项目 | 国内站 B（`pan.qzyun.net`） | 香港站 155（`pan.cdn.ad`） |
| --- | --- | --- |
| 托管方式 | systemd `cloudreve-fork.service`，宿主直跑 | Docker 容器 `cloudreve-fork`（supervisord 拉起） |
| 正在运行的可执行文件 | `/opt/cloudreve-fork/cloudreve` | 容器内 `/cloudreve/cloudreve` ← 宿主 `/opt/cloudreve-fork/bin/cloudreve-4.23.8` |
| 读取途径 | `/proc/27109/exe`（未标 `(deleted)`，与路径同 inode） | `/proc/372597/exe`（同上） |
| PID | 27109 | 372597（容器主 PID 372522） |
| 启动时间 | 2026-11-07 17:19:48（systemd `ActiveEnterTimestamp` 17:19:49） | 2026-11-07 16:26:58（容器 `StartedAt` 16:26:53，`RestartCount=0`） |
| 文件大小 | 90,103,970 B | 90,095,778 B |
| 文件 mtime | 2026-11-07 17:19:38 | 2026-11-07 16:26:35 |
| **运行文件 sha256** | **`4ca50c5535de025cfd9d8377c2b97b8ac404567ab31b98f83e1cfb16a0ff860e`** | **`ada18e24d735c6219e6c410f5e822e19345ba86db702d41a2338e1fddba088ef`** |
| 本机回环 `/api/v4/site/ping` | `{"code":0,"data":"v4.23.9"}` | `{"code":0,"data":"v4.23.9"}` |
| 二进制内注入的版本串 | `@v4.23.9` | `@v4.23.9` |
| 注入的 `LastCommit`（见 §5 说明） | `3b5da976` | `8be13999` |

两台 `ping` 都是 `v4.23.9` —— **版本号无法区分「最终版」与「中间版」，必须靠内容特征（§4）判定**。

### 1.1 各机上的二进制文件清单

国内站 B：

| 文件 | 大小 | mtime | sha256 前缀 | 身份 |
| --- | --- | --- | --- | --- |
| `/opt/cloudreve-fork/cloudreve`（运行中） | 90,103,970 | 11-07 17:19 | `4ca50c55` | v4.23.9 最终版 |
| `cloudreve.bak-v4.23.8` | 89,931,938 | 10-31 05:48 | `0e8bdbbc` | 旧 v4.23.8 |
| `cloudreve.bak-geetest` | 89,931,938 | 10-31 05:48 | `0e8bdbbc` | 同上（两份内容相同） |

香港站 155（`/opt/cloudreve-fork/bin/`）：

| 文件 | 大小 | mtime | sha256 | 身份 |
| --- | --- | --- | --- | --- |
| `cloudreve-4.23.8`（**正在运行**） | 90,095,778 | 11-07 16:26 | `ada18e24…` | v4.23.9 **中间版**（文件名是 4.23.8，内容却是 v4.23.9） |
| `cloudreve-4.23.9.new`（**未启用**） | 90,103,970 | 11-07 17:21 | `4ca50c55…` | v4.23.9 最终版 |
| `cloudreve-4.23.8.bak-geetest` | 89,931,938 | 10-31 05:48 | `0e8bdbbc…` | 旧 v4.23.8 |

---

## 二、sha256 对比（答案：**不一致**）

```
B 运行文件     4ca50c5535de025cfd9d8377c2b97b8ac404567ab31b98f83e1cfb16a0ff860e
155 运行文件   ada18e24d735c6219e6c410f5e822e19345ba86db702d41a2338e1fddba088ef   ← 不同
155 暂存 .new  4ca50c5535de025cfd9d8377c2b97b8ac404567ab31b98f83e1cfb16a0ff860e   ← 与 B 完全相同
```

- B 的运行文件与 155 上**暂存未启用**的 `cloudreve-4.23.9.new` **逐字节相同**。
- 部署报告 §3.2 结尾「两台部署的是同一次编译产出的同一个文件，sha256 均为 `ada18e24…`」这句话与实际相反：`ada18e24…` 只在 155 运行中，B 从来跑的都是 `4ca50c55…`。该句「B 待校验确认后此结论才成立」的待校验项，现在校验完毕——**结论不成立**。
- 部署报告 §3.1 说 155「已上线 v4.23.9」也对了一半：版本号确实是 v4.23.9，但那是**不含迁移修复的中间版**；最终版只是被放进 `bin/` 目录，没有 `mv` 覆盖、也没有重启（容器 `RestartCount=0`、`StartedAt` 比 `.new` 的 mtime 早 55 分钟）。

---

## 三、三项 strings 核对（对「正在运行的文件」计数）

`strings` 对象一律是 `/proc/<PID>/exe`，即进程真正映射的那个 inode，不是目录里「看起来同名」的文件。

### 3.1 任务书要求的三项

| 特征串 | 应然值 | 含义 | B 运行 | 155 运行 | 155 暂存 `.new` |
| --- | --- | --- | --- | --- | --- |
| `seedMissingDefaultSettings` | ≥1 | #183 启动补齐缺失默认设置 | **0** ❌ | **0** ❌ | 0 ❌ |
| `gcaptcha4.geetest.com` | ≥1 | 极验 v4 官方校验端点（修复生效） | **1** ✅ | **1** ✅ | 1 ✅ |
| `api.geetest.com/v4/validate.php` | =0 | 旧端点（已失效，必须不再出现） | **0** ✅ | **0** ✅ | 0 ✅ |

### 3.2 追加核对项（用于区分「中间版 / 最终版」）

版本号相同的情况下，靠这些内容特征判定究竟哪一次编译：

| 特征串 | 含义 | B 运行 | 155 运行 | 155 暂存 `.new` |
| --- | --- | --- | --- | --- |
| `SELECT name FROM settings WHERE deleted_at IS NOT NULL` | v4.23.9 的**迁移软删除修复**（原生 SQL 读取含软删除的键名，避免启动 panic） | **1** ✅ | **0** ❌ | 1 ✅ |
| `announcementPreProcessor` | 公告 HTML 写入时清洗 | 1 ✅ | 1 ✅ | 1 ✅ |
| `sanitizeLocalized` | 公告读取时清洗（多语言变体） | 1 ✅ | 1 ✅ | 1 ✅ |
| `htmlsafe` | 公告白名单过滤包 | 12 ✅ | 12 ✅ | 12 ✅ |
| `looksLikeJSON` | 极验「拦截页不当成校验失败」守卫 | 1 ✅ | 1 ✅ | 1 ✅ |
| `api2.geetest.com` | 已 NXDOMAIN 的老域名 | 0 ✅ | 0 ✅ | 0 ✅ |

**判读**：两台的极验修复与公告 HTML 三处都齐；**唯一实质差别是迁移软删除修复，B 有、155 运行中没有**。155 的「最终版」确实已经落盘（`.new` 就带这一项），但没有启用。

---

## 四、构建来源定位（把两串"版本号」钉到具体提交）

二进制里 `go1.26.5` 紧邻处有 ldflags `-X ...constants.LastCommit` 注入的短提交号：

| 机器 | 注入串 | 仓库中对应提交 | 该提交树的内容 |
| --- | --- | --- | --- |
| B 运行 | `3b5da976` | `feat(announcement): support sanitized HTML and release v4.23.9` | 含公告 HTML，**不含**软删除修复、不含补种 |
| 155 运行 | `8be13999` | `chore: checkpoint`（仅 `pkg/geetest/client.go(+test)`） | 只有极验修复 |

**一个必须说清的不一致**：B 的注入值是 `3b5da976`，但该提交树里的 `inventory/migration.go` **没有**软删除修复（该修复是之后 `21bd0f55` 引入的），而 B 的二进制里该 SQL 字面量明明存在。说明最终版是**在工作树有未提交改动的情况下编译的**（注入的提交号只代表当时的 `HEAD`）。同理 155 的 `8be13999` 树里根本没有 `pkg/htmlsafe`，而它的二进制里公告相关字符串齐全。

因此：**注入的提交号只能当参考，最终判定以 §3 的内容特征为准**，本报告的结论不依赖注入串。

---

## 五、结论与判读

1. **国内站 B = v4.23.9 最终版（`4ca50c55…`）**：极验端点修复 ✅、公告 HTML 白名单 ✅、迁移软删除修复 ✅。
2. **唯一缺项是 `seedMissingDefaultSettings`（#183 的启动补种）**，它在 v4.23.9 发布之后才合并（`a244db32`，内容提交 `8c9ae3d2` / `f3c3f2b3`），**不属于 v4.23.9 这个版本**，两台都没有。按任务书「`seedMissingDefaultSettings` 应 ≥1」这条严格标准，两台都算「缺一项」，但缺的是**未发版的下一个版本**的能力，不是 v4.23.9 的漏装。
3. 按 v4.23.9 自身范围（极验 + 公告 HTML + 迁移修复）判定：**B 已到位，155 未到位**。
4. `docs/admin-usersession-save-fix.md` §5 的自我记录与本核查一致：B 的 `4ca50c55…`（11-07 17:19）不含补种修复，**需要下一次构建带上**。B 库当时也仍缺 `invitation_code_required`、`email_subaddress_chars` 两个键（该文档结论，本次未复核——见 §7）。
5. 香港站目前是**功能可用但更脆**的状态：一旦它的库出现「有软删除设置项」的情形，`db_version_*` 换代时仍会撞 `settings_name_key` 唯一索引。它的库软删除项为 0（部署报告已核实），所以现在没炸，属于运气而非设计。

---

## 六、若确认要替换：最小动作（**仅描述，本次未执行**）

### 6.1 香港站 155（把已落盘的最终版启用）

```bash
# 1) 原子替换容器所挂载的那个文件（compose 把固定文件名挂进容器，文件名不能改）
mv /opt/cloudreve-fork/bin/cloudreve-4.23.9.new /opt/cloudreve-fork/bin/cloudreve-4.23.8

# 2) 只重启这一个容器
docker restart cloudreve-fork
```

- 影响面：容器重启约 12 秒，含 CDN 回源的端到端窗口约 23 秒（部署报告实测）。
- 不动的东西：`cloudreve-fork-postgresql`、`cloudreve-fork-redis`、旧的 `cloudreve-service`（Pro，:5212）全程不动；判据是三者的 `StartedAt` 不变。
- 回滚：`mv cloudreve-4.23.8.bak-geetest`（`0e8bdbbc…`，v4.23.8）覆盖回去 + `docker restart cloudreve-fork`，约 12 秒恢复。
- 注意：`cloudreve-4.23.9.new` 是 11-07 17:21 上传的，替换前建议先 `sha256sum` 复核仍是 `4ca50c55…`（本次已核）。

### 6.2 国内站 B

- **以 v4.23.9 为目标：无需任何动作**，它已是最终版。
- **若要拿到 #183 的补种修复**（让 B 的库缺的 2 个键自动补齐、并在今后版本换代时自愈）：必须**重新构建一个新版本**（按发版政策 v4.23.10，需附 `docs/release-notes/v4.23.10.md`），再按 `mv` 备份 + `systemctl restart cloudreve-fork` 的方式替换。这是一次新部署，不属于本次核查范围，也不应为了它去重启现网。

---

## 七、本次未验证的事项（有意为之）

1. **B 的库当前是否仍缺 2 个设置键**：需要 `SELECT` 查询 `settings` 表，超出本次「只读且限定命令白名单」的边界，未做。已知留档：`/opt/cloudreve-fork/backup/settings-before-seed-20261107-172251.{csv,sql}`（11-07 17:22 的 settings 全表备份，含软删除行），要复核可据此比对。
2. **公告 HTML 的浏览器端渲染**是否正常：本次只看二进制内容，未登录后台、未改任何设置，因此未做端到端验证（部署报告已在 155 做过一次）。
3. **极验真人滑块全流程**：与本次无关，从未在本次触碰。

---

## 八、顺带发现的风险（只记录，未处理）

1. **国内站 B 的服务是 `UnitFileState=disabled` + `Restart=no`**（`systemctl show` 实测）。含义：**B 重启后不会自动拉起云盘**，进程若崩溃也不会自愈。本地迁移期这样设置也许是有意的，但对外服务长期如此等于没有开机自启兜底，建议确认后改为 `enable`（本次不动）。
2. **`/opt/cloudreve-fork/bin/cloudreve-4.23.9.new` 在香港站上挂了 24 小时仍未启用**，且文件名带 `.new` 容易在下次操作时被误当成"待上传文件"清理掉。启用或删除前请先按 §6.1 处理。
3. **部署报告 `docs/ops/v4.23.9-geetest-html-dual-deploy.md` 的结尾结论已过期**（「两台 sha256 均为 `ada18e24…`」）。已在该处加一行指向本报告的更正说明，正文历史记录不改写。
4. 国内站 `cloudreve.bak-v4.23.8` 与 `cloudreve.bak-geetest` 是同一份文件的两份拷贝（同为 `0e8bdbbc…`、同为 10-31 05:48），加上 `cloudreve_full.sql.gz`（229 MB）长期占着约 320 MB 磁盘；等确认最终版稳定后可清理其中一份（本次不动）。
