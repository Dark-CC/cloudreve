# v4.23.6-full2：极验组件样式修复 + VAS 支付选项裁剪 — 部署与验收报告

- 版本标识：`4.23.6-full2`（BackendVersion ldflags 注入，LastCommit=3c25bb21）
- 二进制：linux/amd64 静态（构建机 arm64 交叉编译 GOARCH=amd64 CGO_ENABLED=0）
  sha256 `c4fdf5df91b9d8a87ad6b08d30da656fc89e0f804f43185655f75f59148b2518`
- 镜像：`cloudreve-fork:4.23.6-full2`（基于 4.23.6-full 镜像 + 替换二进制）
- 部署实例：InstaBox 15212 测试实例（https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech）
  换容器必须 `docker stop/rm` 后重新 `docker run`，`docker restart` 不会换镜像层。

## 改动内容

### 1. 注册页极验组件（frontend/src/component/Common/Captcha/GeeTestCaptcha.tsx）
- 移除冗余 SecondaryButton「Click to verify」验证框与 verified state，只保留极验
  SDK 原生按钮（自带弹窗触发与成功态展示）。
- 新增 GlobalStyles（scope `.cloudreve-geetest-mount`）：`.geetest_holder`
  width 100% / height 56px / 圆角 `theme.shape.borderRadius`，与同表单输入框
  （MUI FormControl 348px 宽、56px 高、12px 圆角）完全对齐。

### 2. VAS 支付提供商选项（frontend/src/component/Admin/Settings/VAS/VAS.tsx）
- 添加支付提供商菜单中暂时隐藏 微信支付 / 支付宝 / 自定义 三个选项，仅保留 Stripe。
- 只动前端呈现：后端 payment 设置键与 i18n 键（vas.wechatPay 等）全部保留，
  后端支持就绪后恢复 MenuItem 即可。

## 验收结果（Playwright 实测，2026-10-28）

### 极验对齐（geetest-signup-after.png）
- `.geetest_holder`：x=466 y=545.5 宽 348 高 56 圆角 12px —— 与输入框外框完全对齐
- 注册表单按钮只剩 `["Sign up"]`，无多余验证框
- 登录页渲染正常（LOGIN_PAGE_OK: true）

### VAS 菜单（vas-page.png / vas-menu.png）
- 点「添加支付提供商」后菜单项：`{"items":["Stripe"],"hasWechat":false,"hasAlipay":false}`
- 仅 Stripe，微信/支付宝/自定义均已隐藏

## 构建要点（复用 #26 管线）

1. node:20-alpine：`yarn install` → `yarn version --new-version 4.23.6-full2
   --no-git-tag-version`（version.json 必须与 BackendVersion 一致，否则后端报
   version mismatch）→ `NODE_OPTIONS=--max-old-space-size=6144 yarn build`
2. 前端产物 zip 前缀必须是 `assets/build/`（statics.go 用 `fs.Sub(statics,
   "assets/build")`），构建 context 排除残留 `assets/` 目录避免旧产物污染
3. golang:1.26-alpine：`GOARCH=amd64 CGO_ENABLED=0 go build`，ldflags 注入
   BackendVersion / LastCommit
4. ping 验证：`{"code":0,"data":"4.23.6-full2"}`

## 回滚

InstaBox 机上执行：

```bash
sudo docker stop cloudreve-fork-test && sudo docker rm cloudreve-fork-test
sudo docker run -d --name cloudreve-fork-test --restart unless-stopped \
  -p 15212:5212 -v /data/cloudreve-fork-test:/cloudreve/data cloudreve-fork:4.23.6-full
```

机上保留镜像：`4.23.6-full2`（当前）、`4.23.6-full`（回滚）。

## 截图

- `geetest-signup-after.png` — 注册页：极验组件与输入框对齐
- `vas-page.png` — 后台积分与增值服务 → 支付提供商页
- `vas-menu.png` — 添加支付提供商菜单（仅 Stripe）
