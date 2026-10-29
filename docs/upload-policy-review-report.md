# 复查报告：右键下载未跳转云盘域名的真实原因

日期：2026-10-29
实例：`https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech`（容器 `cloudreve-fork-test`）

## 一、结论先说

**你提出的质疑是对的，此前的诊断方向错了。**

文件根本没被上传到移动云盘，而是全部落在了本机（local 存储策略）。因此「右键下载没有 302 跳转到云盘域名」不是下载链路或 302 逻辑的缺陷——**本机存储没有上游 URL 可跳**，返回本站中转地址是正确行为。

真正的缺陷在**上传侧**：前端明确告知了目标存储策略，后端把它丢掉了。

## 二、证据链

### 1. 文件实际落在本机

查询实例根目录全部文件及其策略归属，`230x0w (1) (1).png`、`q.txt`、`cr-y139-e2e`、`存储策略演示` 等**无一例外**归属 `Default storage policy`（`w0Uk`，类型 `local`）。没有任何文件落在 139 策略上。

### 2. 前端确实传了策略

`frontend/src/component/Uploader/core/uploader/base.ts:155` 创建上传会话时带 `policy_id: this.task.policy.id`，其中 `task.policy` 来自当前目录的 `list.storage_policy`（`Uploader.tsx:43`）。也就是说前端行为正确。

### 3. 后端把它丢了

`pkg/filemanager/fs/dbfs/upload.go` 中，`PreferredStoragePolicy`（即解码后的 `policy_id`）**只在两种情况下被采纳**：

```go
if req.ImportFrom != nil || (isThumb && req.Props.PreferredStoragePolicy != 0) {
    policy, err = f.storagePolicyClient.GetPolicyByID(ctx, req.Props.PreferredStoragePolicy)
} else {
    policy, err = f.getPreferredPolicyForSize(ctx, ancestor, req.Props.Size)
}
```

普通上传走 `else` 分支——由「目录/用户偏好 → 组默认策略」决定，**完全不看 `policy_id`**。默认策略是 local，于是文件写在本机。

### 4. 端到端复现（旧镜像）

在干净根目录（无目录偏好标记、组默认=策略 1）下调用 `PUT /api/v4/file/upload`：

| 点名 `policy_id` | 旧镜像返回的会话策略 |
|---|---|
| `w0Uk`（策略 1） | `w0Uk` Default storage policy ✓ |
| `MaTZ`（策略 3，本地策略B） | `w0Uk` Default storage policy ✗ **被忽略** |

## 三、修复

`pkg/filemanager/fs/dbfs/upload.go` 普通上传分支改为：客户端点名的策略**在属主所属组允许使用的前提下**被采纳，否则回退到原有选择逻辑（不报错，不中断上传）。

新增 `pkg/filemanager/fs/dbfs/dbfs.go:getRequestedUploadPolicy` 承担这一步，规则：

- 只在 `ListByGroups`（该用户实际可用的活跃策略集合）中查找该 id；
- 不在集合内 → 返回 `nil` 回退（防越权：伪造 `policy_id` 无法触及无权策略）；
- 策略为 `suspended` → 返回 `nil` 回退；
- 命中负载均衡型策略 → 先 `ResolveLoadBalance` 解析到实际子策略。

### 验证结果（新二进制，同一份真实数据库副本）

| 场景 | 期望 | 实测 |
|---|---|---|
| 点名允许的策略 `MaTZ` | 采纳 `MaTZ` | ✅ `MaTZ` 本地策略B |
| 点名允许的策略 `w0Uk` | 采纳 `w0Uk` | ✅ `w0Uk` |
| 点名**不允许**的策略 4 `NbI8` | 回退默认 | ✅ 回退 `w0Uk` |
| 未知 id / 已停用策略 | 回退默认 | ✅ 单元测试覆盖 |

单元测试 `TestGetRequestedUploadPolicy`（`pkg/filemanager/fs/dbfs/pick_policy_test.go`）锁住上述四条语义。
`go build ./...`、`go vet`、`go test ./pkg/filemanager/...` 全部通过。

## 四、关于 #33 的直连改动

#33（右键下载改直连 302）的改动**保留**，但它不是本问题的修复：

- 对 local 策略文件，`ShouldInternalProxy()` 返回中转是**正确**行为，改动前后一致；
- 对真正有上游的云盘策略（如 s3 策略 `A`），实测 `direct=true` 确实返回上游直链，该能力成立；
- `downloadSingleFile(file, preferredEntity?, direct = true)` 默认开启直连，让所有用户可见的单文件下载入口行为一致；调用方需本站 URL 时显式传 `false`。

保留理由：上传修好之后，文件真正落到云盘策略时，右键下载才能享受到直连——两处配合才完整。

## 五、15212 实例上两个需要你知晓的事实

1. **该实例没有任何可用的 139 策略。**
   策略 4 名为 `y139-test`，但实际类型是 `s3`、bucket `root`、region `ap-northeast-1`、token 为空——是配错的 s3 策略，不是 139 驱动。策略 5/6 类型是 `y139` 但 token 无效且不在 Admin 组允许集合内。

   因此**在这个实例上无法做 139 的端到端 302 验证**，需要一份有效的移动云盘凭据才能补上。

2. **我此前测试污染过实例数据，已清理。**
   今天 22:33 我曾在用户 `my` 根目录（`file_id=1`）写入一条 `sys:preferred_policy = NbI8` 元数据，导致此后所有根目录上传都试图走那个坏 s3 策略。已删除（`metadata.id=222`），并清理了全部 `cr-test-*`、`cr-verify-*` 测试文件与回收站。10-28 的三条既有目录偏好标记原样保留。

## 六、未完成 / 需要你决定

- **139 真实凭据**：要验证「云盘文件下载确实 302 到云盘域名」，需要一份有效的移动云盘 API 凭据（token）。凭据不入库、不入日志、不入报告，仅用于当次验证。未擅自索取。
- **验收路径**：修复部署后，请确认文件确实落在云盘策略上（详情页存储策略一栏），再测右键下载是否 302。若仍是 local 策略，说明上传时的目录策略选择或组允许集合仍有问题，需要继续查。
