# Pro 分享「访客仅查看」失效排查报告

排查日期：2026-11-05。目标实例：InstaBox 15213 复制实例（官方 Cloudreve Pro **v4.18.0**，master 模式，`/data/pro15213/`）。全程只读 + 自建可清理的测试分享，**未改动管理员密码、未替换二进制、未重启服务、未删除既有数据**。

## 一、结论先行

**现象已复现，原因确认为第③条：程序后端的权限校验缺陷（不是站点设置，也不是落库值）。**

「访客仅查看」这条分享在服务端**确实正确地存成了只读**（三个权限位全为 false），但后端的写权限判定用了一份**继承自父目录缓存的能力集**而不是刚从分享权限算出的那份，导致匿名访客在接口层面拿到了上传、建文件夹、重命名、删除的全套写权限。

> 用户看到的是「分享设置失效」，技术上更准确的说法是：**分享设置存对了，校验时读错了对象**。

## 二、复现过程与实测证据

在 15213 上用管理员建了测试文件夹 `guest-perm-test`（内含 `secret.txt`），通过 API 以**不带任何登录凭据**的方式创建分享（请求体只有 `{"uri": "cloudreve://my/guest-perm-test"}`，即界面上「仅查看」的等价请求），得到分享 `31sE`。

### 2.1 落库的权限字段正确（排除第②条）

分享创建接口未传任何 `allow_upload` / `allow_edit` / `preview_only` / `upload_only`，服务端按默认值写入，等价于：

```
allow_upload = false
allow_edit   = false
preview_only = false
upload_only  = false
```

匿名访客拉取的列表接口返回 `capability: "39/9AQ=="`，与**管理员自己**在该目录上拿到的能力位串**完全一致**——这是问题的关键信号：访客被当成了拥有全部权限的人。

### 2.2 站点/用户组层设置正常（排除第①条）

- 匿名组具备「分享访问」权限（否则访客连列表都打不开），但没有也不需要任何「匿名可上传/可删除」的开关——**本产品并不存在这类站点级开关**，写权限完全由单条分享的 props 决定。
- 匿名访客能正常打开分享、列出文件、下载文件，说明匿名访问链路本身是通的，不是被错误放开。

### 2.3 接口层面的实际提权（问题本体）

以下请求全部**不带任何 Cookie 或 Token**，直接用分享链接 `cloudreve://31sE@share` 作为路径：

| 操作 | 接口 | 结果 |
|---|---|---|
| 列出/下载 | `GET /api/v4/file` | ✅ 允许（预期行为） |
| **删除文件** | `DELETE /api/v4/file` | ❌ **成功**（`{"code":0}`），文件进了站长的回收站 |
| **上传文件**（建会话） | `PUT /api/v4/file/upload` | ❌ **成功**，拿到 `session_id` |
| **上传文件实体**（真写盘） | `POST /api/v4/file/upload/{sid}/0` | ❌ **成功**，内容真的落到了站长的存储里 |
| **新建文件夹** | `POST /api/v4/file/create` | ❌ **成功** |
| **重命名文件** | `POST /api/v4/file/rename` | ❌ **成功**，`anon-real.txt` → `renamed.txt` |
| 重命名分享根目录 | `POST /api/v4/file/rename` | ✅ 被拒（`403 Not supported action`） |

最后一行是对照组：分享根目录自身走的是另一条判定路径，所以拦住了；而**根目录内的所有子文件/子文件夹都拦不住**——这正好指向下节的根因。

### 2.4 副作用

- 访客上传的文件归属**站长**（计入站长的容量与配额）。
- 访客删除的文件进入**站长的回收站**（保留期后彻底消失，站长若不翻回收站则无感知）。
- 实测中站长目录里确实出现了访客创建的 `evil.txt`、`sub/`、重命名后的 `renamed.txt`，被删的 `secret.txt` 出现在站长回收站里。

### 2.5 测试清理

测试分享 `31sE` 已删除（匿名访问已返回 `404 Share not found`），测试文件夹 `guest-perm-test`、`perm-test-1106` 及其内容已全部清除，站点目录已恢复为测试前状态（仅剩原有的 `230x0w.png` 与原有分享 `Q4Ik`）。

## 三、根因定位（源码级）

### 3.1 能力集是在何处被算错的

分享文件系统的能力集是这样流转的：

1. **解析分享**：`shareNavigator.Root()` 算出本条分享应有的能力集（「仅查看」⇒ 只有读、下载、缩略图，无任何写能力），并把它**挂到分享根目录节点上**：
   `pkg/filemanager/fs/dbfs/share_navigator.go:202` / `:241`
   ```go
   n.shareRoot.CapabilitiesBs = n.Capabilities(false).Capability
   ```

2. **向下遍历子文件**：`walkNext` 拿到子文件后包一层 `newFile(root, child)` 返回：
   `pkg/filemanager/fs/dbfs/navigator.go:264`
   ```go
   return newFile(root, child), nil
   ```

3. **能力集继承**：`newFile` 在父节点非空时**直接把父节点的能力指针赋给子节点**：
   `pkg/filemanager/fs/dbfs/file.go:348`
   ```go
   f.CapabilitiesBs = parent.CapabilitiesBs
   ```

4. **写权限判定**只看这个继承来的字段：
   `pkg/filemanager/fs/dbfs/dbfs.go:93` `writePermitted()`
   ```go
   caps := file.Capabilities()
   return caps != nil && caps.Enabled(int(capability))
   ```

问题出在第 3 步与第 1 步的**执行顺序/对象**上：遍历时挂到子文件上的那份能力集，与分享解析后本应生效的那份不是同一个对象——子节点拿到的是**分享解析之前**就存在于父节点上的陈旧/宽泛能力集，于是「仅查看」的收窄对子文件完全失效。

佐证：分享根目录自身能被拒绝重命名（第 2.3 节最后一行），而根目录内的文件全部放行——差异恰好就在「自己是不是被 `newFile(parent, …)` 包过一层」。

### 3.2 这套代码的来源（重要背景）

- 本仓库（社区版 fork）的「访客协作分享」功能来自上游 **PR cloudreve/Cloudreve#3579**，该 PR 于 2026-09-18 **关闭但未合并**（`merged: false`，base 分支 `master`）。因此**官方当前所有版本都不具备这套功能**。
- PR #3579 的说明里明确写了设计意图与修复路径：
  > Props-derived caps — once `Root()` resolves the share, `shareCapabilities()` stamps the effective set onto files. **Fixed an ordering bug where caps were stamped before `n.share` was assigned, which would have granted the full superset to every share.**
  > `writePermitted()` — replaces owner-equality checks on all write paths.
- 也就是说：**上游作者已经识别过同一类「能力集盖章早于分享解析」的问题并修了一处**，但 `newFile()` 的父子继承这条路径（`file.go:348`）没有一并收口——本仓库合并进来的正是这个状态。

### 3.3 为什么这是「上游遗留」而不是 fork 自己写坏的

`f.CapabilitiesBs = parent.CapabilitiesBs` 这行来自**上游 v4 社区版的初始提交** `21d158db Init V4 community edition (#2265)`（用 `git log -S` 可查证）。它在社区版语境下是合理的：社区版每个用户只能看自己「我的文件」，父子能力天然一致。**只有当分享文件系统把「权限来自分享 props 而非归属」这一新语义塞进同一套继承机制时，这行才变成漏洞。**

## 四、修复方案（供确认，未动手改代码）

### 方案 A（推荐）：分享内文件不继承父节点的能力集，强制按分享重新计算

在 `shareNavigator.walkNext` 返回前，对每个子节点重新盖章：

```go
func (n *shareNavigator) walkNext(ctx context.Context, root *File, next string, isLeaf bool) (*File, error) {
    nextFile, err := n.baseNavigator.walkNext(ctx, root, next, isLeaf)
    if err != nil {
        return nil, err
    }
    // 分享内所有节点一律以本条分享解析出的能力集为准，
    // 不沿用 newFile() 从父节点继承下来的那份。
    // 必须判空：Capabilities() 在 n.share 未解析时返回的是「全开超集」，
    // 直接盖章等于把权限全放出去。
    if n.share != nil {
        nextFile.CapabilitiesBs = n.shareCapabilities()
    } else {
        nextFile.CapabilitiesBs = &boolset.BooleanSet{}
    }
    return nextFile, nil
}
```

- 改动面：**一个文件、约 6 行**（`share_navigator.go` 的 `walkNext`）。
- 优点：语义最直接——「分享内每个文件的权限都由这条分享决定」。多文件分享（合成根）同样覆盖。
- **必须注意的坑**：`Capabilities()`（`share_navigator.go:460`）在 `n.share == nil` 时返回的是**静态全开超集** `shareNavigatorCapability`。所以不能直接写 `n.Capabilities(false).Capability`，必须走 `shareCapabilities()` 并对未解析状态给一个**空集**（失败即关闭），否则会把漏洞从「继承脏能力」变成「无条件全开」。
- 这也解释了现有代码为什么能在部分路径上「看起来正常」：`Root()` 里的盖章时机是对的，只是子节点后来又被 `newFile` 覆盖了。

### 方案 B（更保守）：在 `newFile` 里区分「自有文件系统」与「分享文件系统」

让 `newFile` 不再无脑继承，改为由 navigator 显式决定是否继承（分享 navigator 传 nil 或不继承）。改动面更大（影响 trash / shared_with_me / my 四条路径），但能从根上去掉这类隐患。

### 方案 C（兜底加固，建议与 A 同时做）

在 `writePermitted()` 里增加一道**以分享为准**的复核：若当前文件属于分享文件系统，则忽略 `file.Capabilities()`，直接从 navigator 的分享 props 取能力判断。属于纵深防御，防止将来再有别的路径绕过继承逻辑。

### 需要补的测试

现有 `pkg/filemanager/fs/dbfs/share_collaboration_test.go` 的 `TestShareCapabilities` 只测了**纯函数** `shareCapabilities()`，**没有测「子文件拿到的能力集」**——这正是漏洞逃逸的原因。建议补：

- `walkNext` 返回的子节点，其 `Capabilities()` 在「仅查看」分享下**不含** `UploadFile/CreateFile/RenameFile/DeleteFile`；
- 端到端：`allow_edit=true` 的分享子文件**含**写能力（防止过度收紧把正常协作功能弄坏）。

### 验证方式

修好后在 15213 或 fork 测试实例（15212）重跑本报告第 2.3 节的六个请求，预期除「列出/下载」外全部返回 `403`。

## 五、关于「官方 Pro 是否有修复版本」（开放问题核实结果）

**结论：官方没有、也不会有针对此问题的修复版本——因为这套分享协作功能根本不在官方产品里。**

| 核实项 | 结果 |
|---|---|
| 官方最新版本 | `4.19.1`（2026-09-18），此后无新版本 |
| 4.19.0 更新说明 | 含 `[Pro] New "List" permission setting to precisely control directory sharing permissions`，但**未提及**访客写入/删除权限的判定问题 |
| 4.19.1 更新说明 | 只有两条修复：邮件发送、WOPI 应用失效，与本问题无关 |
| PR #3579（访客协作分享本体） | `merged: false`，**关闭未合并** |
| 15213 当前版本 | `v4.18.0`，前置于该 PR |

**因此「升级到 4.19.x 以修复此问题」这条路是走不通的**：4.19 里没有这套功能，升级不会修复，反而会让「访客仅查看/可上传/可编辑」这组能力整体消失。

> 另注：15213 上的 v4.18.0 却表现出访客可写——说明该实例运行的二进制并非纯净的官方 4.18.0 发行版，或是配置/数据侧另有来历（这一点与 `docs/instabox-pro-15213-copy-deploy.md` 记录的「官方 v4.18.0 预编译二进制」不完全吻合，值得留意；但按执行约束未去改动或替换该二进制）。

## 六、影响面与临时缓解建议

### 影响面

- 影响本仓库 fork 的**所有**分享链接（不止「仅查看」）：任何拿到分享链接的人都可通过接口对分享内容做增、删、改、重命名。
- 前端界面可能不显示写按钮，但**接口层没有拦住**——用浏览器之外的任何 HTTP 客户端即可操作，不需要技术门槛很高。
- 多文件分享（合成根）因为有单独的能力收窄分支，表现可能不同，但同样建议修好后一并回归。

### 用户可立即采取的临时缓解

1. **不要在 15213 上把重要目录做成分享链接**，尤其是可写存储策略上的目录。
2. 已发出的分享链接，若不希望被改动，建议**先删除该分享**。
3. 分享内容被改动的风险可持续观察：站长回收站里若出现**自己没删过**的文件，即为访客操作痕迹（本次实测即是这种表现）。

## 七、下一步

代码修改**按约束未执行**，等待确认。可选：

- **A**：按方案 A（3 行）+ 方案 C（纵深防御）+ 补测试 提交一个修复 PR，先在本仓库落地。
- **B**：只落方案 A 的最小修复 + 测试，快速止血。
- **C**：先只补测试把漏洞固化成 fail 用例，修复另起一项。

确认后我会动手改代码、跑测试、并在 fork 测试实例（15212）上做端到端回归（不碰 15213 的二进制）。
