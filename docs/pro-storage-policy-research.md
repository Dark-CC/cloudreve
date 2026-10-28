# 对比研究：Pro 用户组多选存储策略与用户端切换目录存储策略

> 研究对象：官方 Pro v4.18.0 复制实例（15213，https://15213-agqw5litn4acpbo7b5axu3ycku.instabox.tech）vs 本 fork（baixiong 分支，v4.23.5）。
> 方法：15213 上以管理员 + 普通用户实测（Playwright 抓网络请求 + DOM dump + 直接调 API）；fork 侧源码审读 + 在 Baixiong 上构建本地实例（`/tmp/cr-local`，V4.23.5-local）端到端复现。
> 结论只依据实测与代码，不依据推测。

---

## 0. 结论速览

1. **Pro 的用户组存储策略就是"一个多选字段"**：`edges.storage_policies` 直接是数组，没有单独的"默认策略 + 可切换策略"两个概念。管理端是 MUI 多选 Select（chips 形态，非复选框）。
2. **Pro 用户端"切换目录存储策略"确实存在**：入口是**进入目录后的工具栏按钮**（不是右键菜单），弹 MUI Menu 直接点选；绑定作用于**目录 + 后续新上传**，子目录继承最近父目录绑定，复制/移动不应用。
3. **fork 已经实现了功能等价的整套能力**（上游 PR #182 及后续修复，均在 v4.23.5 里）：组 `allowed_policies` 多选、目录 `sys:preferred_policy` marker、服务端 pickPolicy、`GET/PUT /file/policy`、`POST /file/relocate`、前端对话框与右键菜单入口。
4. **15212 上"可切换存储策略功能无效"的根因**：不是代码缺失或链路 bug——**`group_allowed_policies` 表是空的（组从未保存过可切换策略），导致用户端 `GET /file/policy` 只返回 1 项（默认策略），前端入口按 `count > 1` 条件全部隐藏**。配置正确后（本地实例实测）全链路正常：入口出现 → 绑定目录 → 上传会话自动用绑定策略 → 文件实体落在该策略。
5. **核心数据模型差异**：Pro = 单一多选字段（组直接多选，第一个/`policy_ordering` 排序即默认）；fork = 默认策略（单选，`groups.storage_policy_id`）+ 附加可切换池（M2M `group_allowed_policies`）。Pro 的模型更简洁，fork 的模型语义更清晰但多一个字段。
6. **建议保留 fork 现有功能而非移除**（详见 §5）：它就是用户要的"用户端切换目录存储策略"的同构实现，且比 Pro 版本多了"跟随上级目录"（解除绑定）选项；需要的是**按 Pro 形态补齐/调整**（可选），而非移除。

---

## 1. Pro 实测：管理端（用户组多选存储策略）

### 1.1 UI 形态

- 位置：管理面板 → 用户组 → 编辑组 → "基本信息"区。
- 控件：**MUI 多选 Select**，选中项以 **chips（标签）** 形式排列在输入框内；下拉选项无复选框，选中项以高亮显示。
- 文案（zh-CN）："可切换存储策略""可多选，用户可在选定范围内自由切换"。
- 无"默认策略"单独字段——**组的策略集合本身即多选**。

### 1.2 数据模型与 API

`GET /api/v4/admin/group/:id`：

```json
{
  "code": 0,
  "data": {
    "id": 1, "name": "Admin", "...": "...",
    "edges": { "storage_policies": [ {"id": 1, "name": "Default storage policy", "type": "local", ...}, {"id": 2, "name": "A", "type": "s3", ...} ] },
    "settings": { "policy_ordering": [1, 2], "...": "..." }
  }
}
```

- `edges.storage_policies`：**数组**（M2M，组的全部可用策略）。
- `settings.policy_ordering`：策略 id 排序（服务端按 id 排序持久化，**不保留管理员选择时的顺序**）。

保存：`PUT /api/v4/admin/group/:id`，body：

```json
{ "group": { "id": 1, "...完整组对象": "...", "edges": { "storage_policies": [ {"id": 2}, {"id": 1} ] } } }
```

- **必须回传完整 group 对象**；只传 `edges` 的精简 payload 服务端返回 500（实测）。
- 排序不在 payload 里持久化（服务端自行排序）。

### 1.3 测试改动记录（15213）

- 组 2（User）原为策略 `[1]`，研究过程中改为 `[1, 2]` 并在 gating 实验中反复改回；当前为 `[1, 2]`。
- 新建：策略 2 "A"（s3，假 endpoint `https://bucket.region.example.com`）、测试用户 `tester@pro15213.local`（组 2）、目录"策略测试目录/子目录"。
- 以上均可清理。

---

## 2. Pro 实测：用户端"切换目录存储策略"

### 2.1 入口与弹层形态

- **入口：进入目录后，文件管理器工具栏的按钮**（aria-label"切换目录存储策略"），不在右键菜单。
- **单可用策略时按钮仍然显示**（实测：组只有 1 个策略时按钮存在，点开菜单只有一项）——不按策略数量隐藏。
- 点击后先拉取 `GET /api/v4/user/setting/policies`，弹出 **MUI Menu（popover，非 dialog）**：列出用户可用的策略，**直接点选即完成切换**（无确认按钮、无当前项勾选标记、**无"继承/解除绑定"选项**）。
- 切换成功后 snackbar 提示"已切换至 X"，并刷新当前目录列表。

### 2.2 API 明细（实测抓包）

| 端点 | 方法 | 请求 | 响应 |
|---|---|---|---|
| `/api/v4/user/setting/policies` | GET | — | `[{id(hashid), name, type, max_size}]` |
| `/api/v4/file/policy` | **PATCH** | `{"uri":"cloudreve://my/策略测试目录","policy_id":"lYs6"}` | `{code:0}` |
| `/api/v4/file` | GET | `?uri=<目录>&page=0` | 列表 `props.storage_policy = {id,name,type,max_size}`（当前目录生效策略，含继承） |
| `/api/v4/file/upload` | **PUT** | `{uri, size, policy_id, last_modified, mime_type}` | 会话（见下） |
| `/api/v4/file/upload/:sessionId/:index` | POST | 文件内容（local 策略回调） | `{code:0}` |
| `/api/v4/workflow/relocate` | POST | （relocate 工作流，未展开实测） | — |

关键行为细节：

- **上传会话由客户端显式携带 `policy_id`**：取自当前目录列表响应的 `props.storage_policy`（服务端算好含继承的生效策略，客户端照抄）。fork 则是客户端不传、服务端 `pickPolicy` 解析——两种实现效果等价，但**契约不同**。
- **生效粒度 = 目录级 marker，只影响后续新上传**：绑定后新上传走该策略；**已有文件不受影响**；复制/移动进该目录的文件不应用绑定（Pro 语言包明确说明）。
- **子目录继承最近父目录的绑定**（实测：父绑 A → 子列表 `props.storage_policy = A`）；子目录自己切换后覆盖父绑定（子切 Default → 上传走 Default）。
- 用户设置里有"进入不同存储策略的目录时提醒我"开关（`notify_storage_policy_change`）。
- relocate（"存储策略转移"，迁移已有文件）是**独立功能**：入口在右键菜单"更多"，门控 = 组权限位 `relocate(10)` **且** 目录 capability `relocate(22)`；目录 capability 里另有 `mount(21)`（绑定策略也受目录 ACL 控制）。
- 权限/能力校验错误码：`40016 failed to mount policy`（绑定失败）。

### 2.3 Pro 实测发现的坑（fork 实现时要避免）

**管理端保存用户组（PUT /admin/group/:id）后，该组用户已绑定的目录策略失效且无法重新绑定。**

实测复现路径：tester 绑定目录到策略 A（成功，列表显示 A）→ 管理端把组 2 的策略集合改为 `[1]` 再改回 `[1,2]`（两次 PUT 均成功）→ 此后：

- 目录列表 `props.storage_policy` 变 `null`（绑定失效）；
- 重新 `PATCH /file/policy` 绑定任何策略都报 `40016 failed to mount policy`；
- 未动过组配置的管理员账号（组 1）绑定自己目录一切正常。

推断：Pro 的目录绑定记录引用了**组-策略关联行**（或等价的联动状态），管理端保存组时 M2M 关联被删除重建，绑定悬空。**fork 的 marker 方案（目录 metadata 直接引用 policy_id）没有这个问题**——这是 fork 实现优于 Pro 的一个点。

---

## 3. fork 现状：已实现的同构功能（均在 v4.23.5 / 15212 二进制内）

上游 PR #182（commit `62fefa81`）+ 后续修复（`a4733635` 隐藏入口、`92106384` relocate 修复）完整落地：

| 环节 | 代码位置 | 说明 |
|---|---|---|
| 数据模型 | `ent/schema/group.go`（`allowed_policies` M2M edge，表 `group_allowed_policies`） | 组附加"可切换策略池"，默认策略仍是 `groups.storage_policy_id` 单选 |
| 组读取 | `inventory/group.go`（`LoadGroupAllowedPolicies` eager loading） | GET 组详情带 `edges.allowed_policies` |
| 组保存 | `inventory/group.go` `Upsert`（`ClearAllowedPolicies` + `AddAllowedPolicyIDs`） | PUT /admin/group 完整对象回传即落库（本地实测 ✓） |
| 策略解析 | `inventory/policy.go`（`ListByGroups`）+ `pkg/filemanager/fs/dbfs/dbfs.go` `pickPolicy` | 优先级：**最近祖先目录 marker > 用户 preferred > 组默认 > （load_balance 加权）**；marker 失效即 break 到下一层 |
| 上传 | `service/explorer/upload.go`（`PreferredStoragePolicy`）+ `dbfs.go` `createFile` | 客户端可传 `policy_id`（hashid）；不传则服务端 `getPreferredPolicy(parent)` 按目录解析（本地实测 ✓） |
| 用户端 API | `service/explorer/policy.go`、`routers/router.go:850-864` | `GET /file/policy`（可用列表 `[{id,name,type,is_default}]`）、`PUT /file/policy`（`{uri, policy}`，**policy 留空 = 跟随上级目录/解除绑定**）、`POST /file/relocate` |
| 前端管理端 | `frontend/src/component/Admin/Group/EditGroup/PolicyMultiSelectionInput.tsx`、`BasicInfoSection.tsx` | "可切换存储策略"多选 chips（提交为 `edges.allowed_policies:[{id}]`） |
| 前端用户端 | `useActionDisplayOpt.ts`（`policyCount > 1` 显隐门控）、`Dialogs/StoragePolicy.tsx`、`MoreMenuItems.tsx` | 入口在**右键菜单 → 更多操作**："上传存储策略"（目录绑定）+ "迁移存储"（relocate）；对话框 = Dialog + 单选组 + **"跟随上级目录"选项**（Pro 没有） |
| relocate 权限 | `types.GroupPermissionRelocate`（组权限位） | 管理组默认开启 |

### 3.1 本地实例端到端验证记录（Baixiong /tmp/cr-local，V4.23.5-local）

1. 管理端 API 建策略 B（local，id=2）→ `PUT /admin/group/1` 带 `edges.allowed_policies:[{id:2}]` → **落库成功**，GET 回显 `allowed:['策略B']`。
2. `GET /file/policy` → `[{Default, is_default:true}, {策略B}]` 两项。
3. `PUT /file/policy {uri:"cloudreve://my/策略测试", policy:"KOUL"}`（绑定策略 B）→ `{code:0}`，回显绑定策略。
4. `PUT /file/upload`（**不传 policy_id**）→ 会话响应 `storage_policy = 策略B`（服务端按目录绑定解析 ✓）。
5. 回调上传完成 → DB `entities.storage_policy_entities = 2`（文件实体归属策略 B ✓）。
6. Playwright：右键目录 → 更多操作 → 菜单出现"**上传存储策略**"与"**迁移存储**"两项 ✓。

**结论：fork 功能链路（配置 → 入口 → 绑定 → 上传生效）全部正常。**

---

## 4. 15212 "可切换存储策略功能无效"断点定位

对 15212（只读，docker 只读挂载 sqlite 查询）：

- `storage_policies`：2 条——`1 Default storage policy(local)`、`2 A(s3)`（用户建过第二个策略）。
- `groups`：组 1/2 的 `storage_policy_id` 均为 1（默认策略，正常）。
- **`group_allowed_policies`：空表**（没有任何组保存过可切换策略）。
- `metadata`：只有 3 条 exif 记录，**没有任何 `sys:preferred_policy` 目录 marker**（用户端从未成功设置过目录策略）。
- 用户 1 个（`123@qq.com`，即管理员）。

前端显隐逻辑（`useActionDisplayOpt.ts`）：右键菜单"上传存储策略/迁移存储"两项要求 `allowedPolicyCount === undefined || > 1`，计数来自 `GET /file/policy`。组未配置可切换策略时该接口只返回默认策略 1 项 → **两个入口全部不渲染**。

**根因**：15212 上用户组建了策略 A，但**组的"可切换存储策略"多选框没有保存成功（或没有配置）**——`group_allowed_policies` 从未落库。于是用户端看不到任何切换入口，判定"功能无效"。**不是代码缺失，也不是运行时 bug**（本地实例同版本二进制全链路验证通过，§3.1）。

> 注：无法回放用户当时的操作；但"表为空 + 入口按计数隐藏"与"试用无效"完全互洽。若用户记得当时是在管理端组页选了策略并保存，则需要进一步确认当时前端提交是否成功（本地实测保存链路正常）。

### 4.1 立即可做的验证（无需改代码）

在 15212 管理端：用户组 → 编辑 Admin/User 组 → "可切换存储策略"选中策略 A → 保存 → 用户端右键任意目录 → 更多操作 → 应出现"上传存储策略"。若出现并可用，即确认上述根因。

---

## 5. 移除/保留建议

**建议：保留，不移除。** 依据：

1. **不是重叠，而是同一功能**：fork 现有"可切换存储策略（组）+ 上传存储策略（目录）"与用户要实现的"用户组多选 + 用户端切换目录存储策略"是**同构功能**——移除后还要原样实现回来。
2. **功能已验证可用**（§3.1），"无效"是配置/入口可见性问题（§4），不是半成品。
3. **fork 版本在两点上优于 Pro**：① 目录绑定有"跟随上级目录"（解除绑定）选项，Pro 一旦绑定只能换绑不能解绑；② 绑定 marker 直接引用 policy_id，不会出现 Pro 的"管理端保存组后绑定悬空"bug（§2.3）。

若要**完全对齐 Pro 形态**（属增强，非必须），差异与改动见 §6。

---

## 6. 与 Pro 的差异全景 + 对齐实现设计（如果要做）

### 6.1 差异表

| 维度 | Pro（v4.18.0 实测） | fork（v4.23.5 实测/源码） |
|---|---|---|
| 组策略数据模型 | 单一多选字段 `edges.storage_policies` | 默认单选 `storage_policy_id` + 可切换池 `allowed_policies`（M2M） |
| 组默认策略 | 集合内排序（`policy_ordering`）隐含 | 显式 `storage_policy_id` 字段 |
| 管理端 UI | 多选 Select + chips | 默认单选 + 可切换多选 chips（两个控件） |
| 用户端入口 | **目录内工具栏按钮** | **右键目录 → 更多操作**菜单项 |
| 用户端弹层 | MUI Menu 直接点选，无确认 | Dialog + 单选组 + 确认，含"跟随上级目录" |
| 单策略时入口 | 仍显示 | 隐藏（`count > 1` 门控） |
| 切换 API | `PATCH /file/policy {uri, policy_id}` | `PUT /file/policy {uri, policy}`（留空=继承） |
| 上传策略传递 | 客户端显式传 `policy_id`（取自列表 `props.storage_policy`） | 客户端不传，服务端 `pickPolicy`（也接受显式 `policy_id`） |
| 目录生效策略下发 | 列表响应 `props.storage_policy`（含继承） | 列表无此字段（前端从 `file.metadata['sys:preferred_policy']` 读当前值） |
| relocate | `POST /workflow/relocate`（工作流）+ 组权限位 + 目录 capability 双门控 | `POST /file/relocate` + 组权限位 |
| 组策略变更后已有绑定 | **失效且不可重绑（bug，§2.3）** | 正常（marker 引用 policy_id） |
| 进入异策略目录提醒 | 用户设置开关 | 无 |
| load_balance 策略类型 | 无 | 有（`pickPolicy` 加权兜底） |

### 6.2 对齐 Pro 的改动清单（按收益排序，均为可选增强）

**A. 用户端入口移到目录内工具栏（推荐，用户感知最直接）**
- 前端：`FileManager` 工具栏新增按钮（aria-label"切换目录存储策略"），复用现有 `GET /file/policy` 数据；弹层从 Dialog 改为 Menu（或保留 Dialog，仅挪入口）。
- 后端：无改动。

**B. 目录列表下发当前生效策略（`props.storage_policy`）**
- 后端：列表服务（`service/explorer/file.go` `List`）在 props 中追加当前目录生效策略（`pickPolicy` 结果，含继承），使前端与 Pro 一样"服务端算好、客户端照抄"。
- 前端：上传器创建会话时显式传 `policy_id`（与 Pro 契约一致）；或维持现状（服务端解析），此项可只做展示用。
- 收益：用户在任何目录都能看到"当前存储策略"；上传契约与 Pro 对齐，便于对照上游。

**C. 单策略时不隐藏入口（对齐 Pro 行为）**
- 前端：`useActionDisplayOpt.ts` 放宽 `policyCount > 1` 为 `>= 1`（relocate 项保持权限门控不变）。
- 注意：这可能让大量单策略部署突然多出菜单项，属产品决策，建议默认保持现状或加开关。

**D. 组模型合并为单一多选（不建议）**
- 需要数据迁移（`groups.storage_policy_id` → 集合第一个元素）、管理端 UI 重构、所有读组默认策略的代码（上传兜底、用户创建等）改走集合；破坏性与既有语义损失大于收益。**fork 双字段模型保留即可**，它只是 UI 呈现与 Pro 不同，能力是超集。

**E. "进入不同策略目录时提醒"**
- 前端：目录列表响应含生效策略后（B 项），切目录时对比前后策略并 toast；加用户设置开关（`notify_storage_policy_change`）。
- 后端：用户设置字段 + 迁移。

**数据迁移**：以上 A–E 均不需要破坏性迁移；marker（`sys:preferred_policy`）、`group_allowed_policies` 表结构保持不变。

**风险点**：
- B 项改动列表 API 响应结构（新增字段，向后兼容，无破坏）。
- A 项若同时保留右键入口，注意两处入口状态同步（同一 `policyOptionCache`）。
- 上游合并：fork 的 `allowed_policies`/marker/pickPolicy 均为自有代码，未来合并上游时需三向处理的面较小（集中在 `ent/schema`、`dbfs.go`、`policy.go`、前端 Group/FileManager 组件）。

---

## 7. 附：测试数据与环境

- 15213（Pro）：策略 "A"(s3, id=2)、用户 `tester@pro15213.local / Tester-2026!`（组 2 [1,2]）、目录"策略测试目录/子目录"（子目录绑 Default）。**注意**：组 2 的 M2M 在实验中被删重建过，tester 的目录绑定已悬空（§2.3 的坑本身），如需继续实验请删除测试目录重建。管理员 token 临时文件 `/tmp/pro-token`、`/tmp/pro-user-token`（本机构建机）。
- 本地 fork 实例：Baixiong `/tmp/cr-local`（端口 5212，admin@local.test / Local-Test-2026，策略 2 "策略B"，目录"策略测试"绑定策略 B，含 test-upload.txt）。二进制 `/tmp/cr-local-bin`（V4.23.5-local, arm64）。
- 15212：**只读**检查，未做任何修改；`/data/cloudreve-fork-test` 的 DB 读取方式为 docker 只读挂载 + db/wal 副本。
- Playwright 脚本：`/root/shot/pro-policy-research/`（pro-user-switch2.mjs、pro-user-upload.mjs、pro-user-final.mjs、pro-gating2.mjs、local-ui-check.mjs 等），截图与网络抓包在 `shots/`。
