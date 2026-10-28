# 存储策略 Pro 形态对齐 — 实现与验证报告

> 对应研究任务 [#16](docs/pro-storage-policy-research.md)：将 fork 的目录级存储策略体验对齐 Pro 版形态。
> 版本标记 `4.23.6-pre`（LastCommit `e05e1549`），已部署 InstaBox 15212 测试实例。

## 范围与决策

研究报告给出的对齐项处置如下：

| 项 | 内容 | 处置 |
|---|---|---|
| A | 用户端目录工具栏按钮 + Menu 直接点选绑定 | **已实现** |
| B | 列表响应带目录生效策略 | 上游已实现，无需改动 |
| C | 单一策略时不隐藏 dir-policy 入口 | **已实现** |
| D | 组模型改为单一多选（组内可选多策略） | **不做**（研究结论：涉及迁移风险，报告不建议） |
| E | 进入不同策略目录提醒（设置开关 + toast） | **已实现** |

## 代码变更

### 后端（保持现有链路，仅做可见性/字段补充）

- `service/explorer/policy.go`：目录绑定 marker `sys:preferred_policy` 由 private 元数据改为 **public**（列表响应 `parent.metadata` 直接可见，用户端无需额外请求即可渲染菜单勾选态）。
- `inventory/types/types.go` + `service/user/setting.go`：新增用户设置 `NotifyStoragePolicyChange`（`notify_storage_policy_change`，默认 `false`），PATCH `/api/v4/user/setting` 可更新。
- `service/user/response.go`：`UserSettings` 响应新增 `notify_storage_policy_change`（不带 `omitempty`，false 时也回传，对齐其它布尔字段风格）。

### 前端

- `FileManager/TopBar/StoragePolicySwitchButton.tsx`（新增）：工具栏"切换目录存储策略"按钮。菜单含"跟随上级目录（继承）"+ 当前生效策略说明、组允许策略列表（默认策略带"用户组默认"标注）、当前绑定项勾选标记。点选即绑定/解绑，snackbar 反馈后刷新列表。仅 `my` 空间、目录、所有者可见。
- `FileManager/FileManager.tsx`：进入策略变化目录的 toast 提醒。`(path, policy)` 二元组**从同一次列表响应原子读取**（`list.parent.path` + `list.storage_policy`），避免"导航先改 path、列表策略后到"造成的两段式 effect 自我抑制。同目录刷新/同目录切策略不重弹；`my` 空间外不触发。
- `Pages/Setting/Setting.tsx` + `Pages/Setting/StorageSetting.tsx`：设置页新增"存储"标签页（`/settings?tab=storage`），挂载配额条、**首选策略选择器**、**存储策略变更提醒开关**。StorageSetting 自 fork monorepo 重构（`791bfb07`）起是孤儿组件，本次恢复挂载；同时移除其内部对未注册 section `vas` 的 `loadSiteConfig` 调用（该调用会触发 siteConfig reducer `state["vas"].loaded` 崩溃 pageerror）。
- `FileManager/useActionDisplayOpt.ts`：右键"更多操作 → 上传存储策略"入口在**仅一个策略**时不再隐藏（对齐 Pro；仍要求用户组配置了 allowed_policies）。
- 13 个 locale 全量补齐新增文案（`setting.storage`、`fileManager.enteredPolicyDir`、开关/说明等）。

## 本地端到端验证（22/22 PASS，0 页面错误）

实例：`/tmp/cr-policy-e2e`（arm64 二进制，fresh SQLite）。策略：Default（默认，`data/uploads`）+ LocalB（`uploads-b/<uid>/<path>`）。账号：admin（Admin 组）/ tester（User 组，zh-CN），两组 allowed_policies=[Default, LocalB]。

| # | 检查 | 结果 |
|---|---|---|
| A1-A4 | 工具栏按钮出现；菜单含"跟随上级目录（继承）/当前生效"；列出两个策略；默认策略带"用户组默认" | PASS |
| B1-B4 | 进入目录；绑定 snackbar"已切换至 LocalB"；重开菜单 LocalB 带勾选、跟随项无勾选；"当前生效：LocalB" | PASS |
| C1 | 列表响应 `parent.metadata` 含公开 `sys:preferred_policy`（marker 为 public） | PASS |
| D1-D3 | bind-test 上传会话 `policy_id`=LocalB；物理文件落 `uploads-b/<数字uid>/bind-test/`；根目录上传落默认 `data/uploads/<数字uid>/`（对照组不受绑定影响） | PASS |
| E1-E2 | 设置页出现"存储策略变更提醒"开关；开启后 `GET /user/setting` 返回 `notify_storage_policy_change: true` | PASS |
| F1-F3 | 进入 LocalB 目录弹"当前目录使用存储策略：LocalB"；同目录刷新不重弹；关闭开关后不再提醒 | PASS |
| G1-G2 | 解绑后 `parent.metadata` 清空；解绑 snackbar | PASS |
| H1 | 右键更多操作含"上传存储策略"（C 项不回归） | PASS |
| — | 全程无页面 JS 错误 | PASS |

服务端正确性另有 DB 层佐证：`entities.storage_policy_entities=2`（LocalB）、`source` 落 `uploads-b` 路径。

## 部署（InstaBox 15212）

- 镜像 `cloudreve-fork:4.23.6-pre`（`FROM ghcr.io/dvorinka/cloudreve:latest` + COPY 二进制），容器 `cloudreve-fork-test`，绑定/端口/环境变量与 4.23.5-fix3 完全一致。
- 二进制 sha256 三重核验一致：`7214bfbea6e79606fcd2928c4af75db1b32b9be08ae8210c1fa5d854d6b4b574`（本机构建、上传响应、机上 sha256sum）。
- 线上冒烟：banner `V4.23.6-pre Commit #e05e1549`；ping 4.23.6-pre；tester 菜单列出全部 3 个策略（Default/A/本地策略B）；存储标签页渲染（配额+开关）；开启提醒后进入"策略试用目录"（绑 B）出现 toast；0 页面错误。
- 回滚：`docker stop cloudreve-fork-test && docker rm cloudreve-fork-test` 后以 `cloudreve-fork:4.23.5-fix3` 原参数重跑即可（镜像仍在机上）。

## 已知事项

- **D 项搁置**：组模型单一多选按研究报告结论不做（迁移风险 > 收益；现有 allowed_policies m2m 已覆盖需求）。
- `LastCommit` 标记为 `e05e1549`（构建基线 checkpoint）；正式 v4.23.6 tag 时按发版流程重新构建，标记取 tag 所在提交。
- 测试脚本：`/root/shot/policy-e2e.mjs`（本地 22 项）、`/root/shot/policy-smoke-15212.mjs`、`/root/shot/policy-toast-15212.mjs`（线上冒烟）。
