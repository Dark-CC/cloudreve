# Cloudreve Pro v4.18.0 数据库平移到 fork 实测报告

> 2026-10-28 实测。fork 主线版本 v4.23.5（镜像 `ghcr.io/dark-cc/cloudreve:v4.23.5-arm64`）。
> 数据源：官方 Pro v4.18.0 复制实例（15213，sqlite，`schema_version=50`，2 用户 / 3 组 / 2 策略 / 6 文件 / 5 entities / 1 分享 / 64 条审计日志）。
> 全程使用库副本与容器副本，原实例零改动。

## 一句话结论

**可以平移，且基本是"挂上就能跑"**：fork 启动时识别 Pro 的 `db_version_4.18.0-pro` 标记，自动跑 ent 迁移 + 补丁，用户/密码/文件/分享/设置全部无损生效；但有 **2 处必须手工补的关联**（组↔存储策略、可选的站点 URL）和 **1 套已知的列级数据丢失**（见风险清单）。

## 迁移机制（代码层面）

- fork `inventory/migration.go`：缺 `db_version_4.23.5` 标记 → 执行 ent `Schema.Create` 自动迁移 + 默认数据补齐（已有行全部正确 skip）→ `applyPatches` 解析所有 `db_version_` 标记，**剥 `-pro` 后缀**后按序应用补丁。实测 4.20/4.22 四个补丁正确应用，4.13 的 `reset_secret_key` 因标记已存在正确跳过——**Pro 的 secret_key 保留，既有会话签名仍有效**。
- ent v0.13 默认走 Atlas diff。SQLite 下实测行为：**需要结构变更的表按目标 schema 整表重建**（新列落位在 schema 原位、多余列被丢弃、匹配列数据按名拷贝）；**无 diff 的表原样保留**（Pro 遗留列物理保留但 fork 不读）。
- Pro 的 license / `db_version_4.18.0-pro` 标记留在 settings 中，fork 无门控，无害。

## 实测结果（Pro 副本 + fork v4.23.5 容器）

| 项目 | 结果 |
|---|---|
| 启动 | ✅ 正常，`/api/v4/site/ping` 返回 4.23.5 |
| 管理员登录 | ✅ Pro 原密码直接可用（密码哈希兼容） |
| 列目录 | ✅ 文件、目录（含中文目录）全部可见 |
| 下载 Pro 时期文件 | ✅ 本地策略文件按原绝对路径读取，md5 逐位一致 |
| 分享 | ✅ 列表/匿名详情/短链 302 全通（单文件分享走 `shares.file_shares` 列，fork 兼容此路径） |
| 上传新文件 | ✅ 写入 Pro 策略的 dir_name_rule 目录，内容校验一致 |
| SMTP 等设置 | ✅ Pro 配置的设置项被识别沿用 |

## 平移配方（实测验证）

1. **停 Pro，拷库**：`cloudreve.db`（sqlite）+ **uploads 数据目录**。
2. **路径处理**：Pro 本地策略的 `dir_name_rule` 与 `entities.source` 存的是**绝对路径**（如 `/data/pro15213/data/uploads/...`）。两种选法：
   - 新部署把 uploads 挂载到**相同绝对路径**（本次实测做法，零改库）；
   - 或迁移后 SQL 改写 `dir_name_rule` 和 `entities.source` 前缀（相对路径则相对二进制目录解析）。
3. **起 fork**：自动迁移在启动时完成，无需人工干预。
4. **补组↔策略关联（必做）**：Pro 用 `group_storage_policies` 表，fork 改用 `group_allowed_policies` + `groups.storage_policy_id`，迁移后为空 → 所有用户上传报 50001 "No active storage policy available for the group"。修复 SQL（实测有效）：
   ```sql
   INSERT INTO group_allowed_policies (group_id, storage_policy_id)
       SELECT group_id, storage_policy_id FROM group_storage_policies;
   UPDATE groups SET storage_policy_id =
       (SELECT MIN(storage_policy_id) FROM group_allowed_policies
        WHERE group_allowed_policies.group_id = groups.id);
   ```
5. **改站点 URL**：settings 的 `siteURL` 沿用 Pro 旧域名（本例为生产域名 pan.cdn.ad），后台改一次即可；下载/分享直链都由它签名生成。
6. 重启 fork，验证上传/下载/登录。

## 风险清单（数据丢失明细）

**列级丢失（表重建时按目标 schema 丢弃，数据不可恢复）：**

| 表 | 丢弃的 Pro 列 | 影响 |
|---|---|---|
| users | `credit` | 用户积分余额丢（本例均为 0 无影响；fork 用 `credits` 新列从 0 起） |
| users | `group_expires`, `notify_date`, `previous_group`, `unmanaged_email` | 临时组到期/通知/前组记录丢 |
| shares | `price`, `permissions` | 付费分享旧定价与权限掩码丢（fork 用 `price_points` 重新定价） |
| share_purchases | `payment_id`, `user_id`, `ticket_id` 改名 | 购买记录关联需按新列重建（fork 用 buyer_id/ticket/points） |
| gift_codes | `product_props`, `qyt`, `used`, `used_by` 等大改 | 礼品码需重新生成 |
| abuse_reports | `file_uri`, `share_id`, `reported_user_id`, `reporter_id`, `category` | 举报历史关联丢（新结构 reporter_email/target_type/reason） |

**表级（成孤儿表，物理保留、fork 不读写，无害但占空间）：**
`audit_logs`（64 条审计历史在 fork 界面不可见，数据仍在库中）、`file_ancestor_permissions`、`group_storage_policies`、`open_ids`、`payments`、`policy_load_balances`、`storage_packs`（7 张）。

- **open_ids 孤儿 = SSO 绑定丢失**：用 QQ/微信/OIDC 登录过的用户迁移后需重新绑定（fork 用 `sso_bindings`）。本例为空无影响。
- **policy_load_balances 孤儿**：Pro 的策略负载均衡配置不迁移，需在 fork 后台重配。
- **storage_packs / payments 孤儿**：Pro 容量包与支付记录不迁移；fork 的对应体系是 `skus` + `payment_orders` + `credit_txns`，需重新配置商品。

**遗留列（无 diff 的表未动，物理保留但 fork 不读）：** `files.permissions`（fork 改用 `acl_entries`，Pro 的单文件 ACL 语义不迁移）、`direct_links.type`。

**其他注意：**
- `storage_policies` 迁移后新增 `status` 列默认 `active`，Pro 策略自动生效（正合适）。
- MySQL 版 Pro 库未实测（本测试为 sqlite）；ent/Atlas 在 MySQL 上同样会做列级 diff，建议先在副本上试跑同配方。
- 回滚：迁移前留好整库备份即可单向回退（迁移本身改写库结构，不可逆）。
