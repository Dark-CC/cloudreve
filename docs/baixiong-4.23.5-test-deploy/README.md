# 测试实例升级部署报告：baixiong d447fd41 → v4.23.5

**日期**：2026-10-28　**执行**：构建代理　**性质**：测试实例替换部署（非发版）

用户指示原话："先把测试实例换成新版，我测试一下没问题我再和你说发版"——**本任务不打 tag、不发版**；用户测试确认后另行指示。

## 1. 构建产物

| 项 | 值 |
|---|---|
| 源码 | baixiong 分支 tip `d447fd41`（含 PR #49 邀请码弹窗修复、PR #51 链接替换+版本 4.23.5、PR #52 event/abuse 布局+时间框 MUI 化+vas 网格） |
| 版本 | `V4.23.5  Commit #d447fd41`（`constants.go` 硬编码 + ldflags 注入） |
| 构建方式 | `git archive d447fd41` 干净源码 → 多阶段 Docker（node:20-alpine 前端 yarn build → golang:1.26-alpine 打 `application/statics/assets.zip` + GOARCH 交叉编译），构建机为 arm64 |
| amd64 二进制 | 89,800,866 字节，sha256 `ba0b0f1a5a2b58c42d332c16d368817f3c570ffad63842ea8ac3bae3436f8cc0`（上传、机上 Dockerfile 构建、镜像内三次核验一致） |
| 前端指纹 | `AdminBundle-CsM_wGxK.js`（内容寻址哈希），`index-CPcHorTO.js` |

## 2. 本地几何验证（同构建 arm64 二进制 + 同一份前端产物，Playwright 1600×900）

15/15 全部 PASS（完整数据见 `verify-results.json`，截图见 `shots/`），数值与两份修复文档的验收标准逐一吻合：

| 修复项 | 断言 | 实测 |
|---|---|---|
| event/abuse 居中 | 三页表格均包在 `MuiContainer-maxWidthXl` 内、几何一致、符合 x≈266/w≈1292 | task/event/abuse 均为 x=266, w=1292（container x=241 w=1342）✅ |
| 邀请码弹窗布局 | 约 900px 宽居中、四字段单行底对齐无挤压、新建按钮底对齐 | 弹窗 900px 居中；code 207 / group 207 / maxUses 135.3 / expires 135.3（与 #49 文档 207/207/135/135 一致）；按钮底 433.6 = 字段底 433.6 ✅ |
| 时间框 MUI 化 | 非原生 `datetime-local`，点击弹 MUI 日历 | input `type=text` + 日历图标按钮，点击弹 `MuiPickersLayout-root`（calendar=true）✅ |
| vas 手动积分调整 | 三字段单行、块底对齐、宽度正常（修复前 52/69px） | email 383.3@x329 / delta 285.5@x720 / note 383.3@x1014（与 batch2 文档 383/286/383 完全一致），块底 1949.9×3，Apply 按钮底对齐 ✅ |
| 移动端 390px | event 页无横向溢出 | overflow=0px ✅ |
| 稳定性 | 无页面 JS 错误 | 0 错误 ✅ |

说明：delta（积分变动）输入框本体比另两者高 23px 是其下方带 helper text 的设计（SettingForm 块底仍对齐）；expires 的 `<input>` 本体 85px 窄于外框是 MUI end-adornment（日历图标）正常行为，可见外框（`MuiInputBase-root`）为满格 135.3px。

## 3. 线上验证（公网，无管理员凭据的替代方案）

线上后台密码用户已改，无法登录 UI 复测——按任务 spec 回退为"本地同构建几何验证 + 线上产物指纹/接口"验证，均已通过：

| 检查 | 结果 |
|---|---|
| `GET /api/v4/site/ping`（公网） | `{"code":0,"data":"4.23.5"}` ✅（旧为 4.23.4） |
| 容器启动 banner | `V4.23.5  Commit #d447fd41  Pro=false` ✅ |
| 线上 index 引用的 AdminBundle | `AdminBundle-CsM_wGxK.js` = 本地构建产物 sha256 `e7817939…` **字节一致** ✅ |
| 线上 AdminBundle 内容特征 | `datetime-local` ×1（旧包 ×2；余 1 为 UserForm 处，符合预期）、`maxWidth:"xl"` ×18（旧包 ×16；+2 为 event/abuse 新增容器）✅ |
| 数据目录 | `/data/cloudreve-fork-test`（cloudreve.db + conf.ini）未动，SQLite 正常连库 ✅ |

## 4. 部署操作记录（InstaBox 机 m_agqw5litn4acpbo7b5axu3ycku）

```
PUT  /v1/machines/{id}/files  /tmp/cr-4235/cloudreve   # 上传后 API 回报 sha256 与本地一致
机上: sha256sum 复核 → Dockerfile:
      FROM cloudreve-fork:4.23.4-invfix2e1d5d79
      COPY cloudreve /cloudreve/cloudreve
      RUN chmod +x /cloudreve/cloudreve
docker build -t cloudreve-fork:4.23.5-baixiong .
docker run --rm --entrypoint sha256sum <新镜像> /cloudreve/cloudreve   # 再次核验 ba0b0f1a…
docker stop cloudreve-fork-test && docker rm cloudreve-fork-test
docker run -d --name cloudreve-fork-test --restart unless-stopped \
  -p 15212:5212 -v /data/cloudreve-fork-test:/cloudreve/data \
  cloudreve-fork:4.23.5-baixiong
```

新镜像 = 旧镜像叠加新二进制（层复用，仅增 ~90MB）。

## 5. 回滚

旧镜像保留在机上（新镜像 FROM 它，不可能被误删）：

```
docker stop cloudreve-fork-test && docker rm cloudreve-fork-test
docker run -d --name cloudreve-fork-test --restart unless-stopped \
  -p 15212:5212 -v /data/cloudreve-fork-test:/cloudreve/data \
  cloudreve-fork:4.23.4-invfix2e1d5d79        # sha256:2a75ee185ecea0ca072ed6507779edabb80b3ab1e855c26bf4a575bf597fee14
```

## 6. 待办（用户测试确认后）

- 用户线上实测四项修复 + 常用功能（https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech）。
- 确认后按发版政策执行 v4.23.5 发版：GitHub Release + 多平台二进制（复用本次构建产物），release notes 需另写。**发版不在本任务内。**

## 附：截图清单（shots/）

`admin-task/event/abuse.png`（三页表格几何一致）、`invitation-dialog.png`（弹窗 900px 居中四字段单行）、`invitation-datetime-open.png`（MUI 日历弹层展开）、`vas-full.png`（vas 页手动积分调整区）、`mobile-admin-event.png`（390px 无横向溢出）。
