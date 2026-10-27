# 邀请码修复镜像构建与部署记录（InstaBox 测试实例）

部署日期：2026-10-27。将后台「邀请码」弹窗布局修复（提交 `2e1d5d79`，分支 `tds/conv-01a17976-238c-7b7f-bb07-a5feaa16a03b`，当时尚未合入 main）编入自建镜像，替换测试实例原先运行的 `ghcr.io/dvorinka/cloudreve:latest`。

## 镜像与构建管线

| 项目 | 值 |
|---|---|
| 镜像标签（机上） | `cloudreve-fork:4.23.4-invfix2e1d5d79` |
| 源码 | `2e1d5d79`（= main `7d6e607b` + 邀请码弹窗布局修复，仅前端/文档改动） |
| 二进制 sha256 | `db17f97296a255babe3117421136294f3e5c16fd4d4a9794688960585597c359` |
| 回滚镜像（机上保留） | `ghcr.io/dvorinka/cloudreve:latest`（image ID `1f794775c66e`，sha256 `1f794775…4300f18`） |

构建管线（仓库为私有 GitHub 仓库，InstaBox 机无法直接 clone，故在本平台构建机上构建、上传二进制）：

1. **平台构建机**（2C/12G，Docker 29.9.2）：`git archive 2e1d5d79` 出干净源码，多阶段 Dockerfile（`node:20-alpine` 前端 `yarn install && yarn build`（NODE_OPTIONS=--max-old-space-size=6144）→ `golang:1.26-alpine` 复刻 `.build/build-assets.sh`（前端产物打成 `application/statics/assets.zip`）+ `GOARCH=amd64 CGO_ENABLED=0 go build`，ldflags 注入 `BackendVersion=4.23.4`、`LastCommit=2e1d5d79`）。注意构建机是 arm64，必须显式交叉编译到 amd64（InstaBox 机为 x86_64）。
2. **上传**：InstaBox API `PUT /v1/machines/{id}/files?path=/tmp/cr-fix/cloudreve&mode=0755`（最大 5 GiB，实测 90MB 一次成功，sha256 前后校验一致）。
3. **机上成镜像**：以机上已有的官方镜像为底（免重新拉取全部运行时依赖）：`FROM ghcr.io/dvorinka/cloudreve:latest` + `COPY cloudreve ./cloudreve`，机上 `docker build`。
4. **换容器**：`docker stop/rm cloudreve-fork-test` 后按原参数重建（`-p 15212:5212 -v /data/cloudreve-fork-test:/cloudreve/data --restart unless-stopped`），数据目录未动。

## 部署后公网验证（2026-10-27，1440px 视口无头浏览器）

- `GET /api/v4/site/ping`（公网）→ `{"code":0,"data":"4.23.4"}`；登录页 200。
- 管理员登录（Admin 组，凭据由用户维护，不入库）成功。
- `/admin/user` 打开「邀请码」弹窗，几何实测对照修复标准全部达标：
  - 弹窗 900×334 居中（x=270）；四个输入框同行**底部对齐**（y 基线 413/452）；
  - 宽度 207 / 207 / 135 / 135px（邀请码 / 用户组 / 最大使用次数 / 过期时间），无挤压、无标签竖排换行；
  - 「新建」按钮 135×37 与输入基线对齐。
- 功能实测：留空邀请码点「新建」→ 自动生成新码并出现在表格（`4l52jvlbqogw9c0a`，maxUses=5），无任何页面 JS 错误。
- 截图：`1-users-page.png`（用户管理页）、`final-2-invitation-dialog.png`（弹窗全页）、`final-3-invitation-dialog-only.png`（弹窗本体）、`final-5-after-create.png`（新建后）。

## 回滚

```bash
sudo docker stop cloudreve-fork-test && sudo docker rm cloudreve-fork-test
sudo docker run -d --name cloudreve-fork-test --restart unless-stopped \
  -p 15212:5212 -v /data/cloudreve-fork-test:/cloudreve/data \
  ghcr.io/dvorinka/cloudreve:latest   # 旧镜像仍在机上，未删除
```

## 注意

- 数据目录 `/data/cloudreve-fork-test` 只有 `cloudreve.db` + `conf.ini`，**没有** `statics/` 目录，不会遮蔽镜像内置前端；若日后出现 `statics/`（eject 产物），需先移除才能让新镜像前端生效。
- 上传临时文件 `/tmp/cr-fix` 已清理；机上无修复版本的中间镜像标签已删除，避免误用。
