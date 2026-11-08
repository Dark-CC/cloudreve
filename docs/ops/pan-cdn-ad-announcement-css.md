# 站点公告支持受控 CSS：实施与香港站上线报告

- 日期：2026-11-08
- 站点：香港站 `pan.cdn.ad`（生产机 155.117.85.143，容器 `cloudreve-fork`）
- 分支：`tds/conv-01a1ba6f-75dd-7894-92dc-a56431c4787f`
- 涉及 todo：#88

## 1. 结论

公告现在会保留样式：管理员把整份 HTML 文档（样式全在 `<style>` 块里）粘贴进后台公告，用户端弹窗能按原设计显示——白底圆角卡片、顶部蓝色描边、阴影、黄色提示框、红色/绿色状态徽章、列表与页脚对齐。样式被限制在公告容器内，不会改写站点其它部分；脚本、事件属性、外链资源一律丢弃。香港站已上线并写入测试公告，等待用户登录确认视觉效果后决定保留或清空。

## 2. 问题

改造前，公告的 HTML 会经过白名单清洗（`pkg/htmlsafe`），而 `<style>` 元素和 `style` 属性都在白名单之外：整段 `<style>` 被丢掉，所以用户粘贴的公告只剩裸文字，排版全无。前端 `SafeHTMLContent` 组件还会再清洗一次，行为一致。

## 3. 方案

不是简单地「放行 `<style>`」，而是**在作用域隔离的前提下放行受控 CSS**：

1. 解析 `<style>` 内容，逐条规则过滤；
2. 每个选择器加上作用域前缀 `[data-cr-announcement]`；
3. 输出回单个 `<style>` 元素，由公告容器承载该属性。

写配置（后台保存）与读配置（接口下发）两侧都会走这套清洗，且清洗是幂等的：已加过前缀的值再清洗不会重复叠加。

## 4. 代码改动

| 文件 | 改动 |
| --- | --- |
| `pkg/htmlsafe/css.go`（新增） | `SanitizeCSS`：手写词法解析（规则表、声明块、选择器分割），属性 allow-list、值黑名单、视口单位黑名单，作用域包装；上限 64KB / 500 条规则 / 3 层嵌套 |
| `pkg/htmlsafe/htmlsafe.go` | 新增 `ScopeAttribute = "data-cr-announcement"`；`Sanitize` 预收集 `<style>` 文本，清洗后前置输出；`style` 属性经同一套过滤后放行（过滤后为空则丢弃） |
| `frontend/src/component/Common/SafeHTMLContent.tsx` | 与后端对齐的第二道过滤：用浏览器原生 `CSSStyleSheet.replaceSync()` 解析，`@media`/`@supports` 递归，其余 at-rule 丢弃，`style` 属性逐条过滤，容器打 `data-cr-announcement` |
| `pkg/htmlsafe/css_test.go`（新增） | 作用域、组合器、媒体查询、文档态选择器丢弃、幂等、危险载荷、正向保留、自定义属性、规则上限、空输入 |
| `pkg/htmlsafe/htmlsafe_test.go` | 追加 5 个用例：保留带作用域的 `<style>`、整份文档、`style` 属性过滤、用户真实样张、幂等 |
| `pkg/htmlsafe/doc_page_test.go`（新增） | 生成浏览器验证页 `docs/announcement-css/css-scope-verification.html`（仅 `HTMLSAFE_UPDATE_DOCS=1` 时运行） |

## 5. 安全边界

- **禁止**：`<script>`、所有 `on*` 事件属性、`javascript:` / `vbscript:` / `data:` 协议、`@import`、`expression()`、`behavior`、`-moz-binding`、`url(` 与 `image-set(`（外链资源）、`@font-face` / `@keyframes` / `@page` / `@charset` / `@layer` 等其余 at-rule。
- **丢弃**：视口单位（`vh`/`vw`/`vmin`/`vmax` 等），避免公告把弹窗撑满屏幕。
- **保留**：颜色、边框、圆角、阴影、内边距、字号、行高、`flex` 布局、`rgba()`、`calc()`、`var()`、`linear-gradient()`、`!important`、自定义属性（`--x: ...`）。
- **作用域**：`html` / `body` / `:root` 开头选择器改写为容器自身；依赖页面状态的选择器（`body.dark`、`html[data-theme]`、`:root.dark`）整条丢弃——它们无法在容器内安全表达。
- **`*`** 映射为 `[data-cr-announcement] *`（作用于子树，不含容器）。
- 设计取向：**宁可少一个效果，不冒一个 XSS 的风险**。

## 6. 上线记录

| 时间（CST） | 动作 |
| --- | --- |
| 17:55:41 | 首次替换二进制并重启容器，停机 15 秒；ping 返回 `v4.23.9` |
| 17:58 | 把用户样张清洗后写入 `settings.announcement`，清 Redis `setting_announcement*` 缓存 |
| 18:04:18 | 第二次重启（修正静态资源版本戳，见下），停机 19 秒 |
| 18:06 | 校验 `version.json` = `4.23.9`，日志无「Static resource version mismatch」 |

- 回滚副本：`/opt/cloudreve-fork/bin/cloudreve-4.23.8.bak-anncss-20261108`（90103970 字节，sha256 `4ca50c5535de025cfd9d8377c2b97b8ac404567ab31b98f83e1cfb16a0ff860e`）
- 当前二进制：sha256 `eca8ce5d85824c60deb868b82abd2f2d7b15b3a8ee2d0dfb79200f7ed2a0db90`，90894498 字节
- 只重启了 `cloudreve-fork`；PG / Redis / 旧 Pro 容器 `StartedAt` 未变，全程未动。

**中途修正**：第一次构建前端时没有按发版脚本的约定注入版本号，`version.json` 的 `name`/`version` 为空，后端启动时打了两条静态资源版本不匹配的错误日志（功能不受影响，但属于噪声，且与发版流水线产物不一致）。改为用 `npm_package_name=cloudreve-frontend npm_package_version=4.23.9` 直接调用 `vite build`，并按发版约定用 `BackendVersion=4.23.9`（不带 `v` 前缀）重新编译二进制，重启后日志干净。

## 7. 验证证据

### 7.1 单元测试

`go test ./pkg/htmlsafe/` 全绿，覆盖作用域改写、危险载荷表（`expression()`、`javascript:`、`@import`、外链 `url()`、`behavior` 等）、幂等性、规则上限、用户样张整体清洗结果。

### 7.2 真实浏览器渲染（本机 headless Chromium）

用 `docs/announcement-css/css-scope-verification.html` 在无头浏览器里加载**实际落库的那份 HTML**，然后对 DOM 逐条断言：

- **36 条断言全部通过，失败 0**：容器背景/居中、卡片圆角与阴影与顶部蓝边、提示框底色、红/绿徽章配色、列表 `flex`、页脚右对齐、容器外同类元素无样式、`body` 背景未被改写、`<script>`/`on*`/`alert` 数量为 0、所有生效选择器都带作用域前缀、`min-height:100vh` 已丢弃。
- 截图：`docs/announcement-css/verify-local-render.png`

### 7.3 线上状态

- `https://pan.cdn.ad/version.json` = `{"name":"cloudreve-frontend","version":"4.23.9",...}`，与后端 `constants.BackendVersion` 一致。
- 线上首页引用的 `assets/index-RMkh4lLZ.js` 内含作用域代码（检索 `data-cr-announcement` 命中）。
- 公告接口 `GET /api/v4/user/setting/announcement` 未登录返回 401，符合预期（只对登录用户下发）。
- 数据库 `settings.announcement` 长度 2749 字符（3217 字节），内容为清洗后的样张，所有选择器已带 `[data-cr-announcement]` 前缀。
- 用户此前「不再提示」记录的旧公告文本与新公告不同，所以所有用户（含此前已关闭提醒的）都会看到这次弹窗。

## 8. 回滚

```bash
cd /opt/cloudreve-fork/bin
mv cloudreve-4.23.8.bak-anncss-20261108 cloudreve-4.23.8   # 或用 cp 覆盖
docker restart cloudreve-fork
```

清空公告（回到用户验证前的状态）：

```sql
UPDATE settings SET value = '', updated_at = now() WHERE name = 'announcement';
```

改完删缓存：`docker exec cloudreve-fork-redis redis-cli DEL setting_announcement setting_announcement_i18n`（值有 Redis 缓存且无 TTL，只改库不删缓存不生效）。

## 9. 已知限制

1. 不在白名单内的 CSS 属性会被丢弃（如需背景图、动画、自定义字体，需要另行放宽并重新评估风险）。
2. 视口单位一律丢弃，`min-height:100vh` 这类写法不会生效；在弹窗里本也不合适。
3. 依赖页面状态的选择器（深色模式相关）会被整条丢弃，公告在深色主题下仍用自身的浅色样式。
4. 公告容器自身会带上样张里的 `background-color` 与 `padding`（这是样张对 `body` 的设计意图，被有意映射到容器）；如果不想让公告卡片有灰底，去掉样张里的 `body` 规则即可。
5. 前端第二道过滤依赖浏览器原生 CSS 解析器，行为与后端手写解析器在极端怪异语法上可能略有差异；后端是权威，最坏情况是前端少渲染一条规则。

## 10. 待办

- 用户登录 `pan.cdn.ad` 看弹窗效果并截图确认。
- 确认后：清空公告恢复原状态，或保留（若这就是要发布的正式公告）。
- 国内站（120.220.76.88）等香港站结论明确后再上。
