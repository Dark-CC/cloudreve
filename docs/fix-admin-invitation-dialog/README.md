# /admin/user 邀请码弹窗样式错位 — 修复与验收

任务来源:后台 `/admin/user` 页「邀请码」弹窗样式疑似错位(用户反馈)。已实地登录测试实例 <https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech> 复现并量化确认错位,在 `frontend/` 源码修复,并本地起 dev server 实测验证。

## 1. 错位现象(修复前,线上实例实测)

弹窗打开后,「新建邀请码」表单的 4 个输入框被压扁、标签竖排换行、各行高度错乱(1440px 视口,弹窗 900px、内容区 852px 实测):

| 元素 | 修复前实测 (x / y / w / h) | 问题 |
|---|---|---|
| 「邀请码」标签 | 294 / 390 / **69** / 20 | 标签被挤到一格宽 |
| 「邀请码」输入框 | 294 / 414 / **69** / 39 | 输入框只有 69px,placeholder「自动生成」显示不全 |
| 「用户组」标签 | 508 / 350 / **12** / **60** | 3 个字竖排成 3 行 |
| 「用户组」输入框 | 508 / 432 / **12** / 21 | 输入框只剩 12px,基本不可见 |
| 「最大使用次数」标签 | 562 / 350 / **34** / **60** | 标签换 6 行 |
| 「最大使用次数」输入框 | 562 / 414 / **34** / 39 | 只剩 34px |
| 「过期时间」标签 | 776 / 370 / **56** / 40 | 换 2 行 |
| 「过期时间」输入框 | 776 / 414 / **56** / 39 | 日期时间完全无法输入/显示 |

截图:`before-dialog.png`(线上实例,修复前)。

## 2. 根因

`frontend/src/component/Admin/User/InvitationCodeDialog.tsx` 中,「新建邀请码」表单行写成了:

```jsx
<Stack direction={{ xs: "column", sm: "row" }} ... >
  <SettingForm lgWidth={4}>...</SettingForm>
  <SettingForm lgWidth={3}>...</SettingForm>
  ...
</Stack>
```

`SettingForm` 默认渲染为一个 **`Grid2 container`(整行弹性容器)**,其内部字段宽度按 `lgWidth/12 × 容器宽` 计算。把它作为 **flex 子项**塞进不换行的 Stack 行(总宽度需求远超 100%)后,各容器在 flex 收缩规则下被随意压缩成 206/46/206/225px;内部字段再乘上比例,就得到 69/12/34/56px 的错乱结果,标签随之竖排换行、高度参差——并非主题颜色问题,而是**布局结构错误**(Grid 容器误作 flex 行内子项)。

注:该弹窗为 fork 自有提交 `ff211bbe feat(user): invitation-code registration (#24)` 携带的问题,非上游原有代码。

## 3. 修复

仅改前端布局(`frontend/src/component/Admin/User/InvitationCodeDialog.tsx`),不动任何后端逻辑与 API:

- 表单行改用项目既有模式:单个 `<Grid2 container spacing={1} alignItems="flex-end">` + 全部 `SettingForm` 改为 `noContainer` 条目(参考 `Pages/Setting/ProfileSetting.tsx` 的用法);
- 字段占比 `3/3/2/2`(邀请码 / 用户组 / 最大使用次数 / 过期时间),「新建」按钮放入右侧 `size={{xs:12, md:2}}` 单元右对齐;
- 移除原先包按钮的 `pb: 0.5` Box 补丁及 `Stack` 行;`xs`(<768px)下所有字段 12 栏独占一行,保持原设计的纵向堆叠意图。

## 4. 修复后实测(本地 dev server 代理线上 API,同人同数据)

同一视口、同实例账号登录重测:

- 4 个输入框单行排列,底部对齐:邀请码 207px、用户组 207px、最大使用次数 135px、过期时间 135px,「新建」按钮 135×37 对齐输入基线;
- 全部标签单行 20px 高,无换行;
- `datetime-local` 填入 `2026-12-31T23:59` 后 `clientWidth == scrollWidth`(135px),无内部裁切;
- 移动端 390px 视口下表单正常纵向堆叠;
- `eslint` 该文件 0 告警;`tsc --noEmit` 无该文件相关新增报错(仓库 main 分支在 `download.ts`/`file.ts` 等存在既有报错,与本次无关)。

截图:`after-dialog.png`(全弹窗)、`after-dialog-closeup.png`(表单行特写)、`after-dialog-mobile.png`(移动端)。
