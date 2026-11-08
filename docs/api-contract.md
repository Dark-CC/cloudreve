# 后端接口契约（v4）· 前后端并行开发锚点

> 版本：**契约 v1（2026-11-08 冻结）**　|　适用：[`docs/self-hosted-drive-master-plan.md`](./self-hosted-drive-master-plan.md) 的 P0/P1/P2
> 来源：**全部字段与路径均取自活体 Pro `4.19.1-pro`（`http://127.0.0.1:15300`）的真实抓包**，并与本仓 fork 的 `routers/router.go` / `service/` 逐条核对。凡标「⚠ 待补」的条目，技术方案上不影响 P0/P1 开工，实现前补齐。
> 用法：前端照本文件写请求与类型、用 mock 数据先跑起来；后端照本文件实现；两侧都不得私自改字段名。

---

## 0. 通用约定

### 0.1 基础

| 项 | 约定 |
|---|---|
| 前缀 | `/api/v4`（所有业务接口；另有 `/dav`、`/f/`、`/s/`、`/static/` 在根路径） |
| 编码 | 请求与响应均 `application/json; charset=utf-8`（上传分片除外，见 §5.2） |
| 响应信封 | `{"code": 0, "data": <载荷>, "msg": ""}`；出错时 `{"code": <非0>, "msg": "<可展示文案>", "correlation_id": "<uuid>"}` |
| HTTP 状态码 | **业务错误一律 HTTP 200 + 信封里的 `code`**；仅路由不存在返回 HTTP 404、网关错误 502 |
| 时间格式 | RFC3339 带纳秒（如 `2026-11-08T14:30:39.546277086Z`）；相对时间戳用 Unix 秒（`expires`、`last_modified`） |
| 分页 | 列表类两种：`page` / `page_size`，或游标 `next_page_token`；响应统一 `pagination` 对象 |

### 0.2 认证

| 场景 | 方式 |
|---|---|
| 网页 / 客户端 | 请求头 `Authorization: Bearer <access_token>`（**无 Cookie**，token 存前端） |
| 未登录可访问 | `/api/v4/site/*`、`/api/v4/session/*`、`/api/v4/session/token`、`/api/v4/share/info/:id`、`/api/v4/share/listed` |
| 匿名分享浏览 | 分享空间 URI 自带凭据（§8.2），无需登录 |
| WebDAV | `Authorization: Basic <dav_accounts 的用户名:密码>`，路径 `/dav` |
| 签名直链 | `/api/v4/file/content/:entityId/:speed/:name?sign=<签名>`（签名由服务端下发，客户端不构造） |

Token 语义（实测 `POST /session/token` 返回）：

```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5...",
  "access_expires": "2026-11-08T15:33:40.809...Z",
  "refresh_expires": "2026-11-22T14:33:40.809...Z"
}
```

- 前端在 `access_expires` 前 60 秒调刷新接口；刷新失败即登出。
- token 载荷含 `sub`（用户 hashid）与 `token_type`（`access` / `refresh`）。

### 0.3 ID 与 URI

**所有对外 ID 都是 hashid 字符串**（如 `02sD`、`DPsG`、`drsg`），不是数字。生成规则（必须逐字复刻）：`go-hashids`，salt = 站点设置 `hash_id_salt`，编码整数数组 `[数据库id, 类型]`，类型常量顺序：

```
0 Share  1 User  2 File  3 Folder  4 Tag  5 Policy  6 SourceLink  7 Group
8 Entity  9 AuditLog  10 Node  11 Task  12 DavAccount  13 Payment  14 InvitationCode  15 Sku
```

**URI 方案**（客户端与服务端之间传递文件的唯一标识，不是 URL）：

| URI | 含义 |
|---|---|
| `cloudreve://my/<path>` | 当前用户的根目录空间（`cloudreve://my/` 为根） |
| `cloudreve://trash/<path>` | 回收站 |
| `cloudreve://shared_by_me/<path>` | 我分享出去的（符号链接） |
| `cloudreve://shared_with_me/<path>` | 别人分享给我的（符号链接，可选功能） |
| `cloudreve://<shareKey>@share/<path>` | 某个分享内的文件（`<shareKey>` 即 `/s/<shareKey>` 的 key） |
| `cloudreve://<shareKey>:<password>@share/<path>` | 加密分享，URI 内联密码（用于分享页免二次询问） |

### 0.4 boolset（权限位串）

`capability`、`permission`、`file_shares` 等字段是 **boolset**：位图 → 字节 → base64 字符串。

- 例：`"39/9AQ=="` 表示根目录对一个管理员的全集能力；分享访问拿到的 `"Lw=="` 是只读子集。
- 前端**只做**三件事：`capability.includes("read")` 式判断通过解包函数（`new Boolset(str).enabled("write")`）；不可自行拼装；不可假设固定字符串。
- 新系统必须实现同一套位序，否则老数据里的 capability 会被误读（表现为「菜单项灰掉」或「该有按钮的地方没有」）。

### 0.5 错误码（前端需按码分支）

| code | 含义 | 前端行为 |
|---|---|---|
| 0 | 成功 | — |
| 40001 | 参数错误 / 校验失败 | 表单红字提示 `msg` |
| 40077 | 目标实体不存在 | 刷新当前列表 |
| 40081 | 批量操作部分失败（`aggregated_error` 里逐项） | 汇总 toast |
| 40090 | 触发限速（HTTP 头带 `Retry-After`） | 倒计时禁用按钮 |
| 404 | 资源/路由不存在 | 404 页 |

限速点（新系统沿用）：`POST /session/token` 10 次/分/IP；`/session/token/refresh` 30 次/分；注册 5 次/分；分享公开列表 60 次/分。

### 0.6 目录浏览用的上下文提示

`GET /file` 的响应带 `context_hint`（uuid）。同一目录下的后续操作（`/file/url`、`/file/thumb`、上传）把它们放进请求头 **`X-Cr-Context-Hint`** 可省一次库查询。前端照传即可；不传也能工作（仅性能差异）。

---

## 1. 站点与配置

### 1.1 `GET /api/v4/site/ping`

免认证。返回版本字符串：`{"code":0,"data":"4.19.1-pro","msg":""}`

### 1.2 `GET /api/v4/site/config/:section`

免认证（`basic` 在有 token 时附带当前用户）。`section ∈ {basic, login, explorer, ...}`，前端按需拉取。

`basic` 实测节选（**主题与站点信息全从这里来，前端不得硬编码**）：

```json
{
  "code": 0,
  "data": {
    "instance_id": "506f1b09-...",
    "title": "Cloudreve",
    "themes": "{...MUI 调色板 JSON 字符串...}",
    "default_theme": "#1976d2",
    "user": { "id": "02sD", "email": "admin@cr15300.local", "nickname": "admin",
              "status": "active", "created_at": "...",
              "group": { "id": "Xvsa", "name": "Admin", "permission": "/X8F",
                         "direct_link_batch_size": 1000, "trash_retention": 604800 },
              "language": "zh-CN" },
    "logo": "/static/img/logo.svg",
    "logo_light": "/static/img/logo_light.svg",
    "shop_nav_enabled": true,
    "custom_html": {},
    "captcha_type": "normal",
    "captcha_ReCaptchaKey": "defaultKey"
  }
}
```

`login` 实测：`{"authn":true,"register_enabled":true,"tos_url":"...","privacy_policy_url":"...","shared_with_me_acl":false}`

`explorer` 实测：`{"icons":"[...文件类型图标映射...]","...视图与缩略图开关..."}`

---

## 2. 登录与会话

| 接口 | 方法 | 请求 | 响应 `data` |
|---|---|---|---|
| `/session/token` | POST | `{"email","password"}`；开启验证码时带 `captcha` 相关字段（见 §1.2 `captcha_type`） | `{"user":{...},"token":{"access_token","refresh_token","access_expires","refresh_expires"}}` |
| `/session/token/2fa` | POST | `{"session_id","otp"}`（密码登录返回需 2FA 时给出 `session_id`） | 同上 token 结构 |
| `/session/token/refresh` | POST | `{"refresh_token"}` | 新 token 结构 |
| `/session/token` | DELETE | `{"refresh_token"}` | `null`（登出） |
| `/session/prepare` | GET | `?email=` | `{"webauthn_enabled":bool}`（决定是否显示 Passkey 登录） |
| `/session/authn` | PUT/POST | WebAuthn 挑战/断言 | 同 token 结构（P1 之后） |
| `POST /user` | POST | 注册：`{"email","password","captcha"?}` | 注册成功（是否自动登录视站点设置） |

`user` 对象（登录响应内，前端全局缓存）：

```ts
interface User {
  id: string;           // hashid
  email: string;
  nickname: string;
  status: "active" | "banned" | ...;
  created_at: string;
  group: { id: string; name: string; permission: string /* boolset */;
           direct_link_batch_size: number; trash_retention: number };
  language: string;     // "zh-CN"
  avatar?: string;      // 头像 URL，无头像时前端显示首字母
}
```

---

## 3. 用户与设置（P0 最小集）

| 接口 | 方法 | 说明 |
|---|---|---|
| `/user/setting` | PATCH | 改昵称、改密码（`{"nickname"?,"old_password"?,"new_password"?}`） |
| `/user/avatar` | PUT/GET | 头像上传与获取 |
| `/user/storage` | GET | 容量用量（已用/总量），侧栏显示 |
| `/user/shares` | GET | 我分享的文件列表（配合 §8） |

⚠ 待补：这些接口的字段级细节在 P0 实现时按 fork `service/user/setting.go` 对齐；不影响 P0 骨架开工。

---

## 4. 文件列表与操作

### 4.1 `GET /api/v4/file` — 列目录（**核心接口**）

查询参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| `uri` | string | 必填，见 §0.3 |
| `page` | int | 从 0 开始 |
| `page_size` | int | 默认 10，上限见 `props.max_page_size`（实测 2000） |
| `order_by` | string | `name` / `size` / `updated_at` / `created_at` |
| `order_direction` | string | `asc` / `desc` |
| `next_page_token` | string | 游标分页时替代 `page` |

实测响应（真实抓包，字段名逐个对齐）：

```json
{"code":0,"data":{
  "files":[{
    "type":0,                     // 0 文件 / 1 目录
    "id":"32H1",                  // 文件 hashid
    "name":"probe-test.txt",
    "permission":null,            // 当前访问者对该文件的权限 boolset
    "created_at":"2026-11-08T14:30:39.546277086Z",
    "updated_at":"1970-01-21T09:36:40Z",
    "size":11,
    "metadata":{},                 // 缩略图/媒体信息等，键名如 exif:*, sys:*
    "path":"cloudreve://my/probe-test.txt",
    "capability":"39/9AQ==",      // 当前用户能做哪些操作（§0.4）
    "owned":true,
    "primary_entity":"bAsn"       // 主实体 hashid（下载/预览时用）
  }],
  "parent":{ "type":1,"id":"DPsG","name":"","size":0,
             "path":"cloudreve://my/","capability":"39/9AQ==","primary_entity":"XOFm" },
  "pagination":{"page":0,"page_size":10,"is_cursor":true,"next_token":"..."},
  "props":{"capability":"39/9AQ==","max_page_size":2000,
           "order_by_options":["name","size","updated_at","created_at"],
           "order_direction_options":["asc","desc"]},
  "context_hint":"3125afd5-43ac-4621-b1d5-f5a3168927a0",
  "mixed_type":false,
  "storage_policy":{"id":"14sm","name":"Default storage policy","type":"local","max_size":0},
  "view":{"page_size":10,"view":"grid","thumbnail":true}
},"msg":""}
```

前端约定：
- `type` 决定渲染形态；目录点击进入（`uri = file.path`），文件点击按扩展名决定「预览 / 下载」。
- `capability` 决定工具栏与右键菜单项是否可用（**不得按「是不是自己上传的」猜**）。
- `view` 保存用户在 Pro 上的视图偏好（网格/列表、缩略图开关），前端读它初始化。

### 4.2 其它文件操作

| 接口 | 方法 | 请求体 | 响应 |
|---|---|---|---|
| `/file/create` | POST | `{"uri","type":"file"\|"folder"\|"share","metadata"?,"err_on_conflict"?,"share_id"?,"share_password"?}` | `FileResponse`（同 4.1 的 files[] 元素） |
| `/file/rename` | POST | `{"uri","new_name","expect_id"?}` | `FileResponse` |
| `/file/move` | POST | `{"uris":[...],"dst","copy":bool,"expect_ids"?,"on_conflict":"skip"\|"overwrite"}` | `null` |
| `/file` | DELETE | `{"uris":[...],"unlink":bool,"skip_soft_delete":bool}` | `null`（默认进回收站；`skip_soft_delete` 才物理删） |
| `/file/restore` | POST | `{"uris":[...]}` | `null`（从回收站恢复） |
| `/file/trash` | DELETE | — | `null`（清空回收站） |
| `/file/patch` | PATCH | `{"uris":[...],"patches":[{"key","value"}]}`（改元数据/星标等） | `null` |
| `/file/info` | GET | `?uri=` | `FileResponse` + `extended_info`（存储策略、用量、分享、实体、直链） |
| `/file/thumb` | GET | `?uri=&size=` | 图片二进制 |
| `/file/events` | GET | SSE 流 | 服务端推送文件变更（多端同步） |

`FileResponse` 完整字段见 §4.1；`extended_info` 结构：

```ts
interface ExtendedInfo {
  storage_policy?: { id: string; name: string; type: string; max_size: number };
  storage_used: number;
  shares?: Share[];
  entities?: Entity[];        // 多版本/多实体
  direct_links?: { id: string; url: string; downloaded: number; created_at: string }[];
}
```

---

## 5. 上传

### 5.1 `PUT /api/v4/file/upload` — 建上传会话

请求（实测）：

```json
{
  "uri": "cloudreve://my/probe-test.txt",
  "size": 11,
  "last_modified": 1762600000,
  "mime_type": "text/plain",
  "policy_id": "",                       // 目录/用户偏好策略，空 = 默认
  "metadata": {},                        // 可选
  "hash": "<sha256 hex 64位>",            // 可选，用于秒传/去重
  "entity_type": "",                     // "" | "live_photo" | "version"
  "previous": ""                         // 覆盖上传时传上一实体 id
}
```

响应（实测）：

```json
{"code":0,"data":{
  "session_id":"a0251045-1014-4182-bcf3-bff252deec8f",
  "upload_id":"",                        // S3 分片直传时的 multipart uploadId
  "chunk_size":26214400,                 // 26MB；0 表示「部分快」（小文件一次 PUT）
  "expires":1794234639,                  // Unix 秒
  "upload_urls":["https://..."],         // 仅 S3 直传时下发
  "credential":"", "ak":"", "keyTime":"", "completeURL":"", // COS/OSS 等直传凭据（按需）
  "storage_policy":{"id":"14sm","name":"Default storage policy","type":"local","max_size":0},
  "uri":"cloudreve://my/probe-test.txt",
  "callback_secret":"fL48iX0zLaDDAbH6z8jvkxQxefCG90O9",
  "rapid_uploaded":false,                // true = 秒传命中，客户端不用传字节
  "no_parts_uploaded":false              // true = 服务端已有同内容，仍需回调完成
},"msg":""}
```

### 5.2 `POST /api/v4/file/upload/:sessionId/:index` — 传分片

- Header：`Content-Type: application/octet-stream`，`Content-Length` 必填且准确。
- Body：裸二进制（第 `index` 块，索引从 0 起）。
- 成功响应：`{"code":0,"msg":""}`（**没有 data**）。
- 最后一块传完即触发完成（服务端回调），客户端随后重新 `GET /file?uri=<父目录>` 刷新，或读 SSE。

### 5.3 客户端流程（照做即可）

```
1. PUT /file/upload           → 拿 session_id / chunk_size / (upload_urls?)
2. 若 rapid_uploaded=true     → 跳过传输，直接刷新列表
3. 若 upload_urls 非空        → 按 url 直传对象存储（每块 PUT），再 POST 完成回调
   否则                       → POST /file/upload/{sid}/{i} 逐块上传（可并发，默认 3~5 并发）
4. 任一块失败               → 重试 3 次；仍失败则 DELETE 会话并提示
5. DELETE /file/upload       → {"session_id":"..."}（取消上传）
```

### 5.4 `DELETE /api/v4/file/upload`

请求 `{"session_id":"..."}`。取消/清理未完成会话。

> 坑（已在生产踩过，必须实现对新系统的防护）：分片上传中断后若会话元数据没清干净，文件会永久显示「上传中」。新系统在完成回调与失败路径**都要**清 `sys:upload_session_id`，且完成逻辑要防 `owner == nil`。

---

## 6. 下载与预览

### 6.1 `POST /api/v4/file/url` — 取下载/预览地址（**推荐方式**）

请求：

```json
{
  "uris": ["cloudreve://my/probe-test.txt"],   // 批量，上限见组权限 direct_link_batch_size
  "download": true,                            // false = 预览（内联），true = 附件下载
  "redirect": false,                           // true 且只有 1 个 uri 时直接 302 到目标
  "entity": "",                                // 指定实体（多版本时）
  "archive": false,                            // true = 打包下载目录
  "skip_error": false,
  "no_cache": false,
  "purchase_ticket": ""                         // 付费分享重连用（本期不做）
}
```

响应（实测）：

```json
{"code":0,"data":{
  "urls":[{"url":"http://127.0.0.1:15300/api/v4/file/content/bAsn/0/probe-test.txt?download=true&sign=0u87...%3D%3A1794151847"}],
  "expires":"2026-11-08T15:30:47.392555741Z"
},"msg":""}
```

- `urls[i]` 与请求 `uris[i]` 顺序一一对应。
- `expires` 前必须使用；过期重新取。
- 该 URL 可能是**本站签名地址**（中转）或**对象存储/CDN 直链**（302 到 S3）——前端不必区分，直接 `window.location` / `<video src>`。
- 目录打包（`archive: true`）返回的是打包任务/直链（异步，视站点设置）。

### 6.2 `GET /api/v4/file/content/:entityId/:speed/:name?sign=`

站内流式下载/预览（`Range` 支持，`HEAD` 支持）。前端一般不直接拼，用它是因为 `file/url` 下发的就是这个。

### 6.3 直链（`/f/`，**对外分享/嵌入用，不是登录 API**）

| 路径 | 行为 |
|---|---|
| `GET /f/<sourceLinkId>/<name>` | 302/内联到对象存储 |
| `GET /f/d/<sourceLinkId>/<name>` | 强制下载（`Content-Disposition: attachment`） |
| `PUT /api/v4/file/source` | 创建直链，请求 `{"uris":[...]}`，响应 `{"data":[{"id","url"}]}` |
| `DELETE /api/v4/file/source/:id` | 删除直链 |

**生产上有 169,633 条直链在用，路由形态与 hashid 编码不许变。**

---

## 7. 分享

### 7.1 `PUT /api/v4/share` — 创建（或 POST `/share/:id` 编辑）

请求（Pro 4.19 实测结构）：

```json
{
  "uri": "cloudreve://my/probe-test.txt",
  "uris": [],                                  // 多选分享时用 uris，二者取其一
  "is_private": false,
  "password": "",
  "downloads": 0,                              // 0 = 不限
  "expire": 0,                                 // 0 = 永久；否则 Unix 秒或天数（实现按 Pro 语义）
  "share_view": false,                         // true = 以「分享视图」展示
  "show_readme": false,
  "hide_readme": false,
  "permissions": {
    "everyone": "39/9AQ==",                    // boolset：匿名所有人的权限
    "same_group": "39/9AQ==",
    "other": "39/9AQ==",                       // 其它组
    "anonymous": "Lw==",                        // 未登录匿名
    "user_explicit": { "<userHashid>": "39/9AQ==" },
    "group_explicit": { "<groupHashid>": "39/9AQ==" }
  }
}
```

`permissions` 语义（**Pro 的分享权限矩阵，UI 就是按它渲染的**）：

- 四个内置主体：`everyone`（所有人）、`same_group`（同组）、`other`（其他组）、`anonymous`（匿名）。
- 可加白名单：`user_explicit` / `group_explicit`（key 为 hashid）。
- 值都是 boolset；**空对象会被拒绝**（实测报 `40001 Share permissions cannot be empty`）。

响应（实测）：

```json
{"code":0,"data":"http://127.0.0.1:15300/s/drsg","msg":""}
```

`data` 是**分享完整 URL 字符串**（不是对象），key 为 `drsg`。

### 7.2 `GET /api/v4/share/info/:id` — 分享信息（免认证）

实测：

```json
{"code":0,"data":{
  "id":"drsg","name":"probe-test.txt","visited":0,"unlocked":true,
  "source_type":0,
  "owner":{"id":"02sD","nickname":"admin","created_at":"..."},
  "created_at":"2026-11-08T14:33:18.034648938Z","expired":false,
  "permissions":"Lw==","url":"http://127.0.0.1:15300/s/drsg","size":11
},"msg":""}
```

- `unlocked:false` → 前端弹密码输入，调 `GET /share/info/:id?password=xxx` 再取一次。
- `expired:true` → 展示过期页。
- `permissions` 是当前访问者的 boolset（决定「能不能下载/上传」）。

### 7.3 分享内浏览与下载

```
列目录：GET /api/v4/file?uri=cloudreve://drsg@share/<path>
下载  ：POST /api/v4/file/url {"uris":["cloudreve://drsg@share/<path>"],"download":true}
上传（可上传分享）：PUT /api/v4/file/upload {"uri":"cloudreve://drsg@share/<目录>/<文件名>"}
```

实测分享内列表（匿名，注意 `permission` 有值、`path` 是 share URI）：

```json
{"code":0,"data":{"files":[{"type":0,"id":"32H1","name":"probe-test.txt","permission":"Lw==",
  "size":11,"path":"cloudreve://drsg@share/probe-test.txt","capability":"39/9AQ==",
  "primary_entity":"bAsn"}],
 "parent":{"type":1,"id":"DPsG","path":"cloudreve://drsg@share/","permission":"Lw=="},
 "pagination":{"page":0,"page_size":0}, ...}}
```

### 7.4 其它分享接口

| 接口 | 方法 | 说明 |
|---|---|---|
| `/share` | GET | 我的分享列表；`?page_size=&order_by=&order_direction=&next_page_token=` |
| `/share/:id` | POST | 编辑分享（请求体同创建） |
| `/share/:id` | DELETE | 删除单个 |
| `/share` | DELETE | 批量删除 `{"ids":[...]}` |
| `/share/listed` | GET | 公开分享目录（免认证，`?query=` 搜索） |
| `GET /s/:id` | 页面路由 | 短链跳转到分享页；`/s/:id/:password` 带密码直达 |

`Share` 对象（列表项 / `extended_info.shares` 元素）：

```ts
interface Share {
  id: string; name?: string; remain_downloads?: number;
  visited: number; downloaded?: number; expires?: string;
  unlocked: boolean; password_protected?: boolean; source_type?: 0|1;
  owner: User; created_at?: string; expired: boolean;
  url: string; show_readme?: boolean; size: number;
  // 仅所有者可见
  is_private?: boolean; password?: string; listed_publicly?: boolean;
  slug?: string; share_view?: boolean;
  allow_upload?: boolean; allow_edit?: boolean;
  preview_only?: boolean; upload_only?: boolean;
  price_points?: number; note?: string;
}
```

---

## 8. WebDAV（P1）

| 项 | 约定 |
|---|---|
| 入口 | `https://<站点>/dav` |
| 认证 | Basic，账号来自 `dav_accounts`（**独立密码**，不是登录密码） |
| 方法 | `PROPFIND` / `GET` / `PUT` / `MKCOL` / `DELETE` / `MOVE` / `COPY` / `LOCK` / `UNLOCK` |
| 路径 | `/dav/<挂载的目录路径>`；根为 `/dav` |
| 管理接口 | `GET/POST/DELETE /api/v4/devices/dav`（创建账号、查看、删除） |

生产有 249 个账号在用，`PROPFIND` 响应必须与 Pro 一致（否则 Finder/Windows 资源管理器挂载异常）。

---

## 9. 前端页面 ↔ 接口映射（并行开发对照表）

| 页面 | 必需接口 | 阶段 |
|---|---|---|
| 登录 | `POST /session/token`、`GET /session/prepare`、`GET /site/config/basic+login`、`GET /site/captcha` | P0 |
| 注册 | `GET /site/config/login`、`GET /site/captcha`、`POST /user` | P0 |
| 主框架/侧栏 | `GET /site/config/basic`、`GET /user/storage` | P0 |
| 文件列表 | `GET /file`、`GET /file/thumb`、`GET /file/events`(SSE) | P0 |
| 上传 | `PUT /file/upload`、`POST /file/upload/:sid/:i`、`DELETE /file/upload` | P1 |
| 下载/预览 | `POST /file/url`、`GET /file/content/...`、`GET /file/info` | P1 |
| 文件操作 | `POST /file/create|rename|move`、`DELETE /file`、`POST /file/restore`、`DELETE /file/trash` | P1 |
| 分享弹窗 | `POST /file/url`(校验)、`PUT /share`、`POST /share/:id`、`GET /site/config/explorer`(权限项文案) | P1 |
| 分享访问页 | `GET /share/info/:id`、`GET /file?uri=cloudreve://<key>@share/`、`POST /file/url` | P1 |
| 我的分享 | `GET /share`、`DELETE /share/:id` | P1 |
| 回收站 | `GET /file?uri=cloudreve://trash/`、`POST /file/restore`、`DELETE /file/trash` | P1 |
| 连接设备（WebDAV） | `GET/POST/DELETE /devices/dav`、`GET /user/setting` | P1 |
| 设置 | `PATCH /user/setting`、`PUT /user/avatar` | P1 |
| 管理后台 | `/api/v4/admin/*`（用户/组/策略/分享/文件/队列/事件） | P2 |

---

## 10. 契约纪律

1. **字段名、路径、方法以本文件为准**；任何一方要改，先改本文件并在 PR 里标注影响范围（前端/后端/客户端）。
2. 新增字段必须**向后兼容**（可选、有默认值）；删除或改名一律禁止（客户端与老数据依赖）。
3. 响应信封、错误码、boolset、hashid、URI 方案这五件事属于**不可协商项**——它们直接决定老分享链接、老直链、老客户端能否继续用。
4. P0/P1 期间接口如与 Pro 有出入，一律以 Pro 4.19.1（`127.0.0.1:15300`）实测行为为准，并在本文件「⚠ 待补」处补齐抓包记录。

---

## 11. 待补清单（不阻塞开工）

- [ ] `/user/*` 设置与容量的字段级定义（按 fork `service/user/setting.go` 补）
- [ ] SSE `/file/events` 的事件名与载荷（`listed` / `uploaded` / `deleted` …）
- [ ] S3 直传的完成回调地址与签名（`upload_urls` + `completeURL` 的真实形态）
- [ ] 打包下载（`archive:true`）的异步任务协议
- [ ] 管理后台 `admin/*` 全量（P2 前完成）
- [ ] 缩略图 `size` 参数取值与缓存头
