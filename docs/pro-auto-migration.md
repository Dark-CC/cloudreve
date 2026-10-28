# Pro 数据库自动迁移：组存储策略绑定

从 Cloudreve Pro（≤ v4.18）切换到本 fork 时，Pro 用独立的 `group_storage_policies`
联表表达"组可用哪些存储策略"，而 fork 用 `group_allowed_policies` 多对多关联加
`groups.storage_policy_id` 默认策略列。若不迁移，Pro 数据库接入 fork 后每个组的
可用策略为空，上传报 50001（"No active storage policy available for the group"），
此前需要按 `docs/pro-db-migration-to-fork.md`（#24）手工执行 SQL。

## 现状：自动完成

fork 启动时会自动检测并平移（`inventory/migration_pro.go` 的
`repairProGroupPolicyBindings`），在两处接线：

- `migrate()` 补丁链末尾（首次迁移 Pro 库时）；
- `InitializeDBClient` 的"schema 已是最新"分支（早期版本 fork 已带走的 Pro 库，
  补齐历史上缺失的平移）。

行为：

- **检测**：按方言探测 `group_storage_policies` 表是否存在（MySQL
  `information_schema`、PG `to_regclass`、SQLite `sqlite_master`），不存在则静默跳过，
  fork 原生库零影响。
- **平移**：`INSERT … SELECT` 拷贝有效绑定（跳过已平移的行、跳过组或策略已不存在的
  悬空引用）；随后为无默认策略的组取其允许集合中最小 ID 的策略为默认。
- **幂等**：重复启动、部分手工修过的库都不会重复写入或覆盖管理员已设置的默认策略。
- **非致命**：失败只记 Error 日志（附手工兜底提示），不阻断启动——两条语句各自幂等，
  中间态（已拷绑定、默认仍空）本身可用（`ListByGroup` 只依赖允许集合）。

## 验证

- 单测：`inventory/migration_pro_test.go`（基础平移、幂等、部分手工修复共存、
  无 Pro 表零影响、方言探测、完整迁移链）。
- 实测：真实 Pro v4.18 sqlite 副本（2 用户 / 3 组 / 2 策略 / 4 绑定）起 fork 实例，
  登录、策略列表、上传、物理文件落盘全部通过，重启幂等；PostgreSQL 全链路同样
  通过（Pro 形态降级库 → 补丁链 → 平移 → 上传落盘 → 重启幂等）。
