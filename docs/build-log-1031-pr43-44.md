# 构建日志：#43 + #44 合并部署到 15220（2026-10-31）

## 构建目标

从 `baixiong` 主干（含 PR#154/#43 + PR#151/#44）全新编译，部署至 `http://192.168.31.202:15220`（`cloudreve-test-15220.service`）。

## 主干状态确认

| 项目 | 值 |
|---|---|
| 源码目录 | `/data/cr-src-build45`（全新克隆，避免与旧工作树冲突） |
| HEAD commit | `13d8414b` |
| PR#154（#43）| `455669dd` Merge pull request #154 |
| PR#151（#44）| `13d8414b` Merge pull request #151（HEAD） |
| 确认方式 | `git fetch origin baixiong && git log --oneline -5 origin/baixiong` |

## 备份记录

| 项目 | 路径 |
|---|---|
| 旧二进制备份 | `/data/cr-test-15220/backup-20261031-033857/cloudreve.bak` |
| 旧数据库备份 | `/data/cr-test-15220/backup-20261031-033857/cloudreve.db.bak` |

## 构建步骤

### 1. 前端 yarn build

```
cd /data/cr-src-build45/frontend
NODE_OPTIONS=--max-old-space-size=4096 yarn build > /data/fe-build45.log 2>&1
```

结果：`✓ built in 42.53s`，输出目录 `frontend/build/`

### 2. 打包 assets.zip

zip 内部路径必须为 `assets/build/`（statics.go 用 `fs.Sub(statics, "assets/build")`）：

```bash
mkdir -p /tmp/zip-stage/assets
cp -r /data/cr-src-build45/frontend/build /tmp/zip-stage/assets/
cd /tmp/zip-stage
zip -r /data/cr-src-build45/application/statics/assets.zip assets/
```

验证：`unzip -l` 确认包含 `assets/build/version.json`，大小 15MB。

### 3. Go build

```bash
cd /data/cr-src-build45
go build -o cloudreve -ldflags "-s -w" . > /data/be-build45.log 2>&1
```

结果：无错误，二进制 79MB，ARM aarch64，stripped。

## 部署步骤

```bash
systemctl stop cloudreve-test-15220.service
cp /data/cr-src-build45/cloudreve /data/cr-test-15220/cloudreve
systemctl start cloudreve-test-15220.service
```

## 管理员密码重置

原因：#43 任务在 03:09 部署时再次改写了数据库密码哈希，导致 `Cloudreve@2026` 无法登录。

处理方式：用 Python 生成 `salt:sha256(password+salt)` 格式的新哈希，直接更新 `users` 表。

```python
import hashlib, secrets, string
salt = secrets.token_hex(16)  # 32 chars
h = hashlib.sha256(('Cloudreve@2026' + salt).encode()).hexdigest()
# UPDATE users SET password='<salt>:<hash>' WHERE email='admin@cr15220.local'
```

登录验证：`POST /api/v4/session/token` → `{"code":0,"data":{...}}`，确认 code=0。

## 验证结果

| 端点 | 状态码 | 备注 |
|---|---|---|
| `GET /` | 200 | 首页正常 |
| `GET /build/version.json` | 200 | 前端资源正常 |
| `GET /assets/index-BNJ8ogYr.js` | 200 | 前端主 JS 正常 |
| `GET /admin` | 200 | 后台入口正常 |
| `GET /admin/payment-orders` | 200 | #44 订单管理页面可访问 |
| `GET /admin/policy` | 200 | #43 策略页面可访问 |
| `GET /admin/group/1` | 200 | #43 群组页面可访问 |
| `POST /api/v4/session/token` | code:0 | 登录成功 |
| `GET /api/v4/admin/payment-orders` | code:0 | 订单列表 API 正常 |
| `POST /api/v4/admin/policy` | code:0 | 策略列表 API 正常（返回 2 条） |

## 服务状态

```
Active: active (running) since Sat 2026-10-31 03:44:15 CST
Main PID: 44148 (cloudreve)
Memory: 196M
```

## 磁盘清理

| 清理项 | 释放空间 |
|---|---|
| `frontend/node_modules` | ~1.3GB |
| `frontend/build`（已打包进 assets.zip） | ~47MB |
| `/tmp/zip-stage` | ~47MB |
| **合计释放** | **~1.4GB** |

清理后 `/data/cr-src-build45/` 占用约 205MB（源码 + Go 模块缓存 + 二进制）。

## 构建坑记录（与 `build-log-1030.md` 一致）

1. **assets.zip 路径前缀**：zip 内必须以 `assets/build/` 开头（不是 `build/`），否则 `fs.Sub(statics, "assets/build")` 会取不到文件。打包时须先 `mkdir assets && cp -r build assets/` 再 zip 整个 `assets/`。
2. **前端 OOM**：`NODE_OPTIONS=--max-old-space-size=4096` 必须设置。
3. **构建目录隔离**：使用 `/data/cr-src-build45/` 全新克隆，避免被其他任务的残留工作树干扰。
4. **密码哈希格式**：Cloudreve v4 密码格式为 `salt:sha256(password+salt)`（salt 32 字节，见 `inventory/user.go:digestPassword`）。

## 待验收项（需浏览器实测）

以下需在浏览器中登录后台手动核查：

1. `/admin/payment-orders`：标题与侧栏均为「订单管理」、状态显示中文 Chip、日期选择器为 MUI 样式、无裸 i18n key。
2. `/admin/policy` → 添加移动云盘策略向导：无 Endpoint 输入框、有 Authorization 与 App Key 输入框。
3. `/admin/policy/:id`（已有移动云盘策略编辑页）：Authorization 和 App Key 编辑框可见。
4. `/admin/group/1` → 上传下载区块：「允许目录直链」开关可点击切换并保存。
