# GeeTest v4 验证码端到端验证报告

**日期**：2026-10-28 · **实例**：15212 测试实例（cloudreve-fork-test，v4.23.6-geetest 构建）· **验证人**：构建代理

本报告记录极验 GeeTest v4（行为验证 4.0）支持在 15212 公网测试实例上的浏览器端到端验证过程与结论。

## 验证环境

| 项 | 值 |
| --- | --- |
| 实例 | https://15212-agqw5litn4acpbo7b5axu3ycku.instabox.tech |
| 镜像 | `cloudreve-fork:4.23.6-geetest`（含本次全部前后端改动） |
| 验证码配置 | `captcha_type=geetest`，`captcha_id=54088bb07d2df3c46b79f80300b0abbe`（极验官方线上演示 ID），`captcha_key=` 占位无效值 |
| 登录验证码 | 开启（`login_captcha=true`） |
| 浏览器 | Playwright + Chromium（headless），路由 `/session` |

**关于凭据**：官方演示 ID 的 key 不公开，服务器持有的是无效占位值。因此「验证通过放行」路径无法在真实凭据下演示；本验证覆盖到服务端出站校验被极验拒绝后的**失败关闭（fail-closed）**行为，这正是无效/泄露凭据下应有的安全表现。「验证通过」路径由 `pkg/geetest/client_test.go` 的 mock 服务端单测覆盖（含签名规范、端点回退、gen_time 守卫等 6 个用例）。

## 端到端流程（全部实测通过）

1. **登录页渲染验证码组件**：`/session` 输入邮箱进入密码阶段后，出现 "Click to verify" 按钮，组件由后端下发的 `captcha_type=geetest` + `captcha_id` 驱动加载 `static.geetest.com/v4/gt4.js`。
   证据：[01-login-widget.png](evidence/01-login-widget.png)
2. **弹出行为验证挑战**：点击按钮弹出极验挑战（本轮为 svg_icon 九宫格图标选择，三帧轮播，题面 "Select 1 images with …"）。
   证据：[02-icon-challenge.png](evidence/02-icon-challenge.png)
3. **完成挑战**：通过浏览器自动化按帧识别并点击目标图标，极验回调成功，弹窗提示 **Verification Success**，验证按钮进入已验证状态。
   证据：[03-verification-success.png](evidence/03-verification-success.png)
4. **四参数随登录请求提交**：填入密码点击 Sign in 后，前端把组件回调的四个参数随 `POST /api/v4/session/token` 一并提交（`password` 已脱敏）：

   ```json
   {
     "email": "123@qq.com",
     "password": "********",
     "captcha_output": "oGQdJjPxCpqjBQZt7EFbpMlynhsvBBN7…（一次性令牌）",
     "lot_number": "29790143151d411e81ea1ace46dd365a",
     "pass_token": "aaed7abd5be1c004d8784c6f9a47757c…",
     "gen_time": "1793219100"
   }
   ```

5. **服务端出站二次校验（容器日志实录）**：

   ```log
   [Outgoing] POST "https://api2.geetest.com/v4/validate.php?captcha_id=54088…"
     → dial tcp: lookup api2.geetest.com: no such host（该域名境内外均不可解析）
   [Outgoing] POST "https://api.geetest.com/v4/validate.php?captcha_id=54088…"
     → HTTP 567（极验对无效 sign_token 的返回；因服务器持占位 key，属预期）
   [Warn] middleware/captcha.go:267 GeeTest verification failed: Remote returns unexpected status code: 567
   [Incoming] 200 | POST "/api/v4/session/token" → {"code":40026,"msg":"Captcha validation failed"}
   ```

   服务端依次尝试 validate 端点列表、拿到极验明确拒绝后中止登录，**未签发会话**——fail-closed 生效。
   证据：[05-after-submit.png](evidence/05-after-submit.png)

## 单元测试（机上全绿）

`go vet` + `go test ./pkg/geetest/ ./pkg/setting/ ./pkg/tcaptcha/ ./middleware/ ./inventory/ -count=1` 全部通过，覆盖：签名确定性（`sign_token = hex(HMAC-SHA256(captcha_key, lot_number))`）、mock 服务端验证成功/失败/参数校验、双端点回退、全部端点不可达时的失败关闭、gen_time 过期守卫。

## 结论

- 前端组件渲染、i18n、挑战完成、四参数提交：**已实测**。
- 服务端中间件到极验官方端点的出站校验链路（含端点回退）：**已实测**（以无效 key 走到极验真实拒绝）。
- 有效凭据下的「验证通过放行」：**由单测覆盖，未在真实凭据下实测**（无公开可用凭据）。接入真实业务时填入官方控制台的 `captcha_id`/`captcha_key` 即可，无需改代码。

## 附：测试实例状态与恢复

15212 当前保留 `captcha_type=geetest` + 官方演示 ID + 占位 key 且登录验证码开启——因 key 无效，**任何账号当前都无法通过网页登录**（这正是 fail-closed 的演示）。验收完成后如需恢复登录：

```bash
# 宿主机上执行（停容器→改库→起容器）
docker stop cloudreve-fork-test
sudo sqlite3 /data/cloudreve-fork-test/cloudreve.db \
  "UPDATE settings SET value='0' WHERE name='login_captcha';"
docker start cloudreve-fork-test
```

（或把 `captcha_geetest_captcha_id/key` 换成真实凭据以启用完整功能。）
