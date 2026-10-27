# 后台三处样式修复：event/abuse 布局、邀请码时间选择框 MUI 化、vas 手动积分调整错位

任务来源:用户反馈三处后台样式问题,均已实地复现并量化确认,在 `frontend/` 源码修复,并本地 dev server(代理线上测试实例 API)实测验证。承接昨日修复 `docs/fix-admin-invitation-dialog/README.md`(提交 `2e1d5d79`,已编入测试实例镜像 `cloudreve-fork:4.23.4-invfix2e1d5d79`)。

改动文件(仅前端布局/组件,不动后端):

| 文件 | 修复 |
|---|---|
| `frontend/src/component/Admin/Event/EventList.tsx` | 补 `<Container maxWidth="xl">` 与 `/admin/task` 对齐 |
| `frontend/src/component/Admin/Abuse/AbuseReportList.tsx` | 同上 |
| `frontend/src/component/Admin/User/InvitationCodeDialog.tsx` | 过期时间 `datetime-local` 原生输入框 → MUI X `DateTimePicker` |
| `frontend/src/component/Admin/Settings/VAS/ManualCreditAdjust.tsx` | 邀请码同款修法:单 `Grid2 container` + `SettingForm noContainer` |

## 1. /admin/event 与 /admin/abuse 布局错位(未居中)

### 现象(修复前,线上实例,1600px 视口)

| 页面 | 页头 x 坐标 | 表格起点 | 问题 |
|---|---|---|---|
| /admin/task(参照) | 265 | 266 / 宽 1292 | 正确:内容包在 `Container maxWidth="xl"` 里居中 |
| /admin/event | **241** | 贴左满宽 | 未包 Container,内容直接顶到 PageContainer 边缘,与 task 页错位 24px,大屏下表格拉满整行不居中 |
| /admin/abuse | **241** | 贴左满宽 | 同上 |

截图:`before/event.png`、`before/abuse.png`、`before/task.png`(参照)。

### 根因

`TaskList.tsx` 的页面结构是 `PageContainer > Container maxWidth="xl" > (PageHeader + 筛选行 + 表格 + 分页)`,而 `EventList.tsx` / `AbuseReportList.tsx` 少了 `Container` 这一层,内容直接占满 `PageContainer` 全宽。

### 修复

两页补上与 task 页完全一致的 `<Container maxWidth="xl">` 包裹(并同步筛选行 `mb: 1 → mb: 2` 与 task 对齐)。不改表格结构与数据逻辑。

### 修复后实测(本地 dev server,同视口)

| 页面 | 页头 x | 表格 x / 宽 |
|---|---|---|
| /admin/task | 265 | 266 / 1292 |
| /admin/event | **265** | **266 / 1292** |
| /admin/abuse | **265** | **266 / 1292** |

三页完全一致。移动端 390px 视口下三页页头 x=16、表格 x=17,亦一致(`after/mobile-admin-event.png`、`after/mobile-admin-abuse.png`)。

## 2. /admin/user 邀请码弹窗时间选择框 MUI 化

### 现象(修复前,线上实例)

「过期时间」用的是 `<DenseFilledTextField type="datetime-local">`,即浏览器原生日期时间控件——外观与弹窗内其它 MUI 组件不一致(无 MUI 边框/日历图标/弹出层),在部分浏览器下样式突兀。截图:`before/invitation-dialog.png`。

### 修复

改用仓库既有依赖与既有模式(**零新增依赖**:`@mui/x-date-pickers@^6.20.2` + `dayjs@^1.11.10` 已在 `package.json`,用法与 `Task/TaskCleanupDialog.tsx` 完全一致):

```tsx
<LocalizationProvider dateAdapter={AdapterDayjs}>
  <DateTimePicker
    value={expiresAt}                       // state: Dayjs | null
    onChange={(v) => setExpiresAt(v)}
    slotProps={{ textField: { fullWidth: true, size: "small" } }}
  />
</LocalizationProvider>
```

`expires_at` 提交仍走原 API 字段(`expiresAt.toISOString()`),后端无改动。

### 修复后实测

- 表单行 4 个字段单行排列:207 / 207 / 135 / 135px,高度 39~40px,「新建」按钮 135×37 底对齐,与昨日修复后的布局一致;
- 点击日历图标弹出 MUI 官方选择面板(DOM 存在 `MuiPickersLayout-root` / `MuiMonthCalendar`),截图:`after/invitation-datetime-open.png`;
- 移动端 390px 视口弹窗正常(`after/mobile-invitation-dialog.png`)。

## 3. /admin/settings?tab=vas 手动积分调整错位

### 现象(修复前,线上实例,1600px 视口)

与昨日邀请码问题同一根因——`SettingForm`(内部自带 `Grid2 container`)被塞进不换行的 `Stack direction={{ lg: "row" }}` 当 flex 子项,被压缩后内部字段再按 `lgWidth/12 × 容器宽` 计算宽度,结果惨不忍睹:

| 字段 | 修复前实测宽度 |
|---|---|
| 用户邮箱 | 486px(独占整列,纵向堆叠) |
| 积分变动 | **52px**(被压扁) |
| 备注 | **69px**(被压扁) |

截图:`before/vas-full.png`(整页,底部「手动积分调整」区)。

### 修复(邀请码同款)

`ManualCreditAdjust.tsx` 改为单个 `<Grid2 container spacing={1} alignItems="flex-end">`,三个 `SettingForm` 全部 `noContainer`,字段占比 `4/3/4`(邮箱 / 积分变动 / 备注),「调整」按钮放右侧 `size={{ xs: 12, lg: 1 }}` 单元右对齐——与 `InvitationCodeDialog.tsx` 修复后的结构一致。

### 修复后实测(本地 dev server,同视口)

| 字段 | x | 宽 | 高 |
|---|---|---|---|
| 用户邮箱 | 329 | 383 | 39 |
| 积分变动 | 720 | 286 | 39 |
| 备注 | 1014 | 383 | 39 |

三字段单行排列、底部对齐、无压缩,「调整」按钮在右侧对齐。移动端纵向堆叠正常(`after/mobile-vas.png`)。

## 4. 验证方式与结论

- 修复前:登录线上实例 <https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech>(管理员账号)Playwright 无头浏览器截图 + DOM 几何量化;
- 修复后:本地 `vite` dev server(独立本地配置 `vite.local.config.ts` 代理线上 API,仓库 `vite.config.ts` 未动,验证后已删),同人同数据同视口重测;
- `tsc --noEmit` 对四个改动文件无新增报错;`eslint` 四文件 0 告警;
- 页面 console 仅有仓库既有的 `findDOMNode` deprecation 警告(react-draggable / Transition,与本次无关)。

注意:线上测试实例仍跑旧镜像 `cloudreve-fork:4.23.4-invfix2e1d5d79`,本报告的"修复后"均为本地 dev server 实测;合并部署后线上生效。

## 截图清单

- `before/`:`event.png`、`abuse.png`、`task.png`(参照)、`invitation-dialog.png`、`vas-full.png`
- `after/`:`admin-event.png`、`admin-abuse.png`、`admin-task.png`(参照)、`invitation-dialog.png`、`invitation-datetime-open.png`、`vas-full.png`、`mobile-admin-event.png`、`mobile-admin-abuse.png`、`mobile-invitation-dialog.png`、`mobile-vas.png`
