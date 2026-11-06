# Baixiong 的 tds 升级到 v0.1.71

- 机器：Baixiong（`machineId: i0oReGoVU5bGHKHeGEmKG`，aarch64 / Ubuntu，主机名 `instance-10110429`）
- 执行时间：2026-11-06 16:20 UTC
- 结论：**升级成功**，守护进程已在 v0.1.71 上重新运行

## 升级前状态

```
$ tds --version
tds 0.1.70

$ tds status
[tds] Daemon:   running (pid 2106583, v0.1.70, since 11/5/2026, 12:07:17 PM, started by tds start)

$ readlink -f /usr/bin/tds
/usr/lib/node_modules/@todos-dev/cli/dist/index.js
```

安装方式是 npm 全局包 `@todos-dev/cli`（`/usr` 前缀），守护进程由 `tds start` 拉起。

## 执行

按用户要求，`tds upgrade` 是本次 build 的最后一步：

```
$ tds upgrade
[tds] Running 'upgrade' in a detached helper (pid 2349825); it completes even if this shell dies with the daemon.
[2026-11-06T16:20:19.965Z] [tds] Upgrading tds v0.1.70 in /usr
```

升级期间观察到的中间态（均属正常，未做任何干预）：

- npm 先移除旧包，`/usr/bin/tds` 与 cli 包目录一度不存在；
- 随后拉取 `@todos-dev/cli@0.1.71` 与平台包 `@todos-dev/cli-linux-arm64@0.1.71`；
- 远程 shell 因守护进程重启多次掉线。

daemon.log 的完整链路：

```
[2026-11-06T16:20:19.965Z] [tds] Upgrading tds v0.1.70 in /usr
[2026-11-06T16:20:49.772Z] [tds] Upgraded v0.1.70 → v0.1.71.
[2026-11-06T16:20:49.773Z] [tds] restart helper: stopping daemon
[2026-11-06T16:20:50.375Z] [tds] Stopped.
[2026-11-06T16:20:50.769Z] [tds] Started in background (pid 2350053).
```

## 升级后验证

```
$ tds --version
tds 0.1.71

$ tds status
[tds] Daemon:   running (pid 2350053, v0.1.71, since 11/6/2026, 4:20:50 PM, started by tds start)
[tds] Workspaces: /root/.tds/workspaces
[tds] Machine: Baixiong (i0oReGoVU5bGHKHeGEmKG)
[tds] Team:     64JIh0QkAoA5JQi6Dv8HE
[tds] Server:   https://todos.dev

$ cat /root/.tds/daemon.json
{"pid":2350053,"startedAt":1793982050769,"runner":"cli","version":"0.1.71"}

$ ps -p 2350053 -o pid,etime,rss,cmd
2350053  00:58  85196  /usr/bin/node /usr/bin/tds start --supervisor
```

`daemon.json` 记录的版本与 pid 与 `tds status` 一致，均为 0.1.71 / 2350053，无旧版进程残留。

## 说明

- 升级期间的掉线是守护进程重启导致的预期现象，不是故障；未执行任何 `kill` 或手动 `tds start`。
- 本 build 未被中断（续跑时已无需再次升级），因此本次仅执行了只读的 `tds --version` / `tds status` 与日志查看。
