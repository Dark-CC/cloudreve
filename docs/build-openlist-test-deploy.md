# OpenList 驱动（只读 + 302 下载 + 写入）独立实例实测报告

- 构建来源：主干 `baixiong`，tip `6d293c06`
- 版本：`4.23.8`（社区版，无需 license）
- 测试实例：InstaBox 独立实例，独立端口与独立数据目录，未触碰 15213 / 15220 等既有实例
- 测试日期：2026-11-03

## 1. 产物与部署

| 项 | 值 |
| --- | --- |
| 二进制 | `cloudreve-4.23.8-openlist`（linux/amd64，静态 ELF） |
| 大小 | 89,919,650 字节 |
| SHA256 | `e9ec26998d45ea7e3499809d58abb80b299257844a3f4aedbe74d7bf12ce8aa7` |
| 公网入口 | `https://15221-agqw5litn4acpbo7b5axu3ycku.instabox.tech` |
| 服务 | `cloudreve-openlist-15221.service`（systemd，`StartLimitIntervalSec=0`） |
| 程序目录 | `/data/cr-openlist-15221/` |
| 数据目录 | `/data/cr-openlist-15221/data/`（SQLite） |
| 反代 | 无。后端直接监听 `:15221`，无 nginx |

同一份源码另行交叉编译了 linux/arm64 版本备查，SHA256 `94cf997364c87452a013237b593a999f775689436f127ef172b0e8db7f08a045`。

## 2. 实测结果

### 2.1 登录与列目录

- `/api/v4/user/me` 返回管理员信息，登录链路正常。
- `GET /api/v4/file?uri=cloudreve://my` 返回本地默认策略（`local`）及目录能力位，列表可正常读取。
- 站点健康：`/api/v4/site/ping` → `{"code":0,"data":"4.23.8-openlist"}`。

### 2.2 新建 OpenList 存储策略

- `PUT /api/v4/admin/policy`（不带 id 为创建）成功创建 `type=openlist` 策略，`server=http://127.0.0.1:5244`，`settings.mount_path=/team`。
- 管理员用户组授予 `allowed_policies` 后，`/api/v4/file/policy` 可见该策略，且在此策略下的目录里，列表响应的 `storage_policy.type` 为 `openlist`。

需要写入的策略必须显式配置名称规则，否则写入会失败（见 2.5）。

### 2.3 上传落盘

- `PUT /api/v4/file/upload` 初始化后返回的 `storage_policy` 即为 OpenList 策略，说明策略选择正确。
- `POST /api/v4/file/upload/:sessionId/:index` 提交分片后返回 `{"code":0}`。
- Cloudreve 侧列表可见该文件且大小正确（11 字节）；OpenList 侧物理文件确实出现于对应目录。
- 结论：Web 上传 → OpenList 物理写入全链路打通。

### 2.4 索引导入

- `POST /api/v4/workflow/import` 可把 OpenList 上已存在的文件导入 Cloudreve 索引。
- 实测把 OpenList 上已有的 `hello.txt` 导入到 `cloudreve://my/opl/imp/`，Cloudreve 列表显示 `hello.txt` 大小 17 字节，与物理文件一致。

### 2.5 下载 302

- `POST /api/v4/file/url` 返回 OpenList 的签名原始地址（`/p/team/...`）。
- `PUT /api/v4/file/source` 返回本实例直链；对该直链发起请求实测返回：

```
HTTP/1.1 302 Found
Location: http://127.0.0.1:5244/p/team/opl/final/up.txt?sign=...
```

- 即下载不经过 Cloudreve 中转，直接 302 到 OpenList，符合本次改动的预期。

## 3. 实测中发现的三个问题

### 3.1 新建 OpenList 策略的路径规则为空，导致写入全部失败（严重）

现象：上传分片返回 `40052 Failed to upload file`，开启 debug 后底层错误是 `Invalid object name: empty save path`。所有写入（网页上传、缩略图、导入）都会失败，且报错信息对用户没有指向性。

原因：`service/admin/policy.go` 的创建逻辑只为 `local` 类型填充默认名称规则：

```go
if service.Policy.Type == types.PolicyTypeLocal {
    service.Policy.DirNameRule = util.DataPath("uploads/{uid}/{path}")
}
```

OpenList 及其它非 local 策略的 `DirNameRule` / `FileNameRule` 保持空串，`path.Join("", "")` 得到空保存路径，被 `validateUserPath` 判为非法对象名。

绕过方式（本次验证所用）：创建策略时显式给出

```
dir_name_rule  = "{path}"
file_name_rule = "{originname}"
```

建议：在创建非 local 策略时同样填入默认的 `"{path}"` / `"{originname}"`，或在保存路径为空时用上传 URI 推导，避免用户建完策略后所有写入静默失败。

补充：名称规则里的变量必须用 `{originname}`（合法列表见 `pkg/filemanager/fs/magicvar.go`）。写成 `{name}` 不会报错，会被当成字面量，物理文件名直接变成 `{name}`。

### 3.2 导入接口的 `dst` 不能带 `cloudreve://` 前缀（体验问题）

`service/explorer/workflows.go` 里 `dst` 已经是 `cloudreve://my/` 根 URI，再执行 `dst.Join(service.Dst)`。若调用方传入完整 URI（`cloudreve://my/opl/imp`），会被拼成 `cloudreve://ajt2@my/cloudreve%3A%2F%2Fmy%2Fopl%2Fimp`，任务随后以 `Invalid object name: name contains illegal characters` 失败。传相对路径（`opl/imp`）即正常。

建议：入参处先判断是否已是完整 URI，或直接拒绝带 scheme 的值并给出明确提示。

### 3.3 本地 SQLite 并发写锁

任务队列在状态流转时偶发 `database is locked (5) (SQLITE_BUSY)`，任务直接落到 `error`。本次是在外部用 sqlite3 并发查询数据库时触发，属测试操作干扰；但默认 SQLite 部署下任务队列与 Web 请求争锁的余量不大，建议关注（可通过 WAL + busy_timeout 缓解）。

## 4. 结论

- 本次两轮改动的核心目标均已实测达成：**OpenList 只读列出、上传写入落盘、索引导入、下载 302 直链**全部可用。
- 二进制可正常运行于独立实例，与既有实例完全隔离，未修改任何既有实例的配置或凭据。
- 主要风险点是 3.1：默认规则为空会让新建的 OpenList 策略在写入侧全盘失效，建议作为发版前修复项。

## 5. 测试后环境

- 测试实例仍在运行，可通过公网地址访问，管理员账号见交付说明（未写入本文档）。
- 所有测试用凭据未进入仓库与日志。
