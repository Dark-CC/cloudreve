# 移动云盘（y139）直链改由本站地址 302 下发

2026-10-30 实施。本文取代 [direct-only-y139-report.md](./direct-only-y139-report.md) 中"直链直接下发云盘预签名地址"的做法和相关验收项。

## 问题

移动云盘策略的文件在界面上「获取直链」，弹窗里显示并复制出的是云盘的预签名地址：

```
https://ykj-eos-wx2-01.eos-wuxi-3.cmecloud.cn/f2aa0675...?X-Amz-Signature=...
```

用户拿到的地址不属于本站，带 15 分钟有效期的 `X-Amz-Expires=900`，过期后再分享就失效；也和其它存储策略下发的链接形态不一致。

根因在 `pkg/filemanager/manager/entity.go` 的 `GetDirectLink`：此前为 y139 加了一段特判，命中 `types.PolicyTypeY139` 时直接返回 `entitysource` 的 `Url()`，跳过本站的 `routes.MasterDirectLink(...)`。该特判来自 `45ea249f`（wip: direct-only y139）与 `92808f41`（右键下载直连）。

## 做法

y139 不再有任何绕过本站的入口，但**仍然不中转文件字节**：

- `GetDirectLink` 删除 y139 特判，所有策略统一返回 `routes.MasterDirectLink(siteUrl, linkHashID, link.Name)`，即 `/f/:id/:name`。
- `/f/:id/:name` 对 y139 是 **302** 到云盘预签名地址，浏览器直接向云盘取数据，本站只发一个几百字节的重定向响应。地址留在本站域名下，不受云盘 15 分钟签名限制（每次访问重新签发）。
- `GetEntityUrls` 与 `GetUrlForRedirectedDirectLink` 的 `directEntitySource` 判定去掉 `|| policy.Type == types.PolicyTypeY139`，恢复为 `o.NoInternalProxy && meterClaim == ""`；缓存 key 追加 `directEntitySource`，避免「要外链」和「要中转」两种结果互相污染缓存。
- `service/explorer/file.go`：`RedirectDirectLink` 调用 `GetUrlForRedirectedDirectLink` 时传 `fs.WithNoInternalProxy(true)`，让 302 目标是云盘地址而不是本站中转地址；`FileURLService` 删除 `direct` 字段。
- 前端 `downloadSingleFile` 去掉 `direct = true` 参数，下载统一走本站 URL；`FileURLService` 的 `direct?: boolean` 字段同步删除。

流量计量不受影响：302 到云盘属外部 URL，走签发时按文件大小扣减（`ConsumeDirectTraffic` / `ConsumeStreamTraffic`），`Metered` 只在内部代理 URL 上为真。

## 保留的行为

以下 y139 限制仍然有效，属于驱动能力边界而非本次改动对象：

- y139 不支持批量打包下载：前端 `assertNoDirectOnlyPolicy` 守卫、后端 `archive.go` 拒绝。
- 上传侧 y139 强制直传（不经本站中转）。
- `PolicyType.y139` 枚举与相关文案（`downloadOnlyBatchDownloadUnsupported`）。

## 实测（InstaBox 测试站，2026-10-30）

样例：`cloudreve://my/bxy.mp4`，10,244,355 字节，位于策略 4（y139）。

| 项 | 旧镜像 v4.23.7 | 新构建 |
| --- | --- | --- |
| `PUT /api/v4/file/source` 返回 | `https://ykj-eos-wx2-01.eos-wuxi-3.cmecloud.cn/...&X-Amz-Signature=...` | `https://<本站>/f/NmTe/bxy.mp4` |
| `GET /f/NmTe/bxy.mp4` | — | **302**，`Location` 为云盘预签名地址，响应体 696 字节 |
| 跟随跳转取字节 | — | 10,244,355 字节，sha256 前缀 `bb9b91b0ef5862b3`，2.6 s |
| 界面「获取文件直链」弹窗 | 云盘地址 | 本站地址，无 `cmecloud.cn` / `X-Amz-Signature`，无「直接下载」按钮 |

## 验证命令

```bash
# 外链（注意：是 file/source，不是 file/url）
curl -s -X PUT http://127.0.0.1:5212/api/v4/file/source \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"uris":["cloudreve://my/bxy.mp4"]}'

# 302 而不中转：只看 Location，不下载
curl -sD- -o /dev/null http://127.0.0.1:5212/f/NmTe/bxy.mp4 | head -5
```

单测：`go test ./pkg/filemanager/manager/ -run 'TestDirectLinkIsServedFromSite|TestDirectEntitySourceNeedsNoProxy|TestEntityUrlCacheKeySeparatesDirect'`，见 `pkg/filemanager/manager/directlink_test.go`。

## 发版注意

- 旧直链（云盘预签名地址）在 15 分钟后自然失效，已发出去的那些不会自动改写成新形式；本站 `/f/...` 链接长期有效。发版说明里需提示用户「重新获取直链」。
- 部署前须知：测试站库里策略 5/6（`y139-bad-token`）token 无效且不在 Admin 组允许列表（allowed = 1,2,3,4），要用 139 必须先修 token 并把策略加进组允许集合。
