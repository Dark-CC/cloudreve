#!/usr/bin/env node
/**
 * Cloudreve 上传链路端到端（真实浏览器）复验脚本
 *
 * 在真实浏览器里走完整前端路径上传一个随机内容的文件，验证：
 *   1. 前端上传器完成上传（分片全传完，或服务端判定秒传）；
 *   2. 上传后文件列表里「上传中」占位数为 0；
 *   3. 文件真的出现在列表里；
 *   4. 刷新页面后仍然是 0（说明后端 CompleteUpload 已清掉 upload_session_id）。
 *
 * 走的是拖拽上传路径（把 File 放进 DataTransfer 派发 dragenter/dragover/drop），
 * 这是前端 Cloudreve-Uploader 实际监听的入口，等同于用户把文件拖进窗口。
 *
 * 依赖：playwright-core + 一个本地 chromium 可执行文件。
 *   PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm i playwright-core@1.48.2
 *
 * 用法：
 *   BASE_URL=https://xxx.trycloudflare.com \
 *   EMAIL=admin@example.local PASSWORD='***' \
 *   CHROME=/root/.cache/ms-playwright/chromium-1194/chrome-linux/chrome \
 *   node scripts/e2e/upload-e2e.js
 *
 * 退出码：0 = 全部通过；1 = 有断言失败；2 = 脚本异常。
 *
 * 实现注意：
 * - 新版 Chrome 已移除 --headless=old，playwright 1.48 默认仍会传，故 ignoreDefaultArgs。
 * - 不在 console 回调里做同步文件写，低配机器上会把页面渲染拖死。
 */
const fs = require('fs');
const path = require('path');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:15230';
const EMAIL = process.env.EMAIL || '';
const PASSWORD = process.env.PASSWORD || '';
const CHROME = process.env.CHROME || '';
const OUTDIR = process.env.OUTDIR || '/tmp';
const HOST = new URL(BASE).host;
const RESULT_FILE = path.join(OUTDIR, 'upload-e2e-result.txt');
const sleep = ms => new Promise(r => setTimeout(r, ms));

const result = [];
const uploaderLogs = [];
function log(...args) {
  const line = args.map(String).join(' ');
  console.log(line);
}
function record(key, value) {
  result.push([key, value]);
  console.log(`${key} = ${value}`);
  try {
    fs.writeFileSync(RESULT_FILE, result.map(([k, v]) => `${k} = ${v}`).join('\n') + '\n' +
      '\n--- uploader log ---\n' + uploaderLogs.map(s => s.slice(0, 200)).join('\n') + '\n');
  } catch (e) { /* 结果文件写不了不影响判定 */ }
}

const withTimeout = (p, ms, tag) => Promise.race([
  p,
  sleep(ms).then(() => { throw new Error('TIMEOUT:' + tag); }),
]);

/** 以文本里出现「上传中」的次数量化残留占位数量 */
const countUploading = async page => {
  const body = await page.locator('body').innerText();
  return (body.match(/上传中/g) || []).length;
};

(async () => {
  if (!EMAIL || !PASSWORD) {
    console.error('缺少 EMAIL/PASSWORD 环境变量');
    process.exit(2);
  }
  const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright-core');
  const launchOpts = {
    ignoreDefaultArgs: ['--headless=old'],
    args: ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--disable-gpu'],
  };
  if (CHROME) launchOpts.executablePath = CHROME;

  const browser = await chromium.launch(launchOpts);
  const ctx = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();

  let uploadReqs = 0;
  page.on('request', r => {
    if (r.url().includes('/api/v4/file/upload')) uploadReqs++;
  });
  // 只收集，不做同步 IO
  page.on('console', m => {
    const t = m.text();
    if (t.includes('Uploader')) uploaderLogs.push(t);
  });
  const shot = n => page.screenshot({ path: path.join(OUTDIR, `upload-e2e-${n}.png`) }).catch(() => {});

  let exitCode = 0;
  try {
    log('open', BASE);
    await withTimeout(page.goto(BASE + '/', { waitUntil: 'domcontentloaded', timeout: 40000 }), 60000, 'goto');
    await sleep(4000);

    // 登录是两步式：先填邮箱 → 回车 → 再填密码
    await withTimeout(page.fill('input[type=email]', EMAIL), 20000, 'fill-email');
    await page.press('input[type=email]', 'Enter');
    await sleep(3000);
    await withTimeout(page.fill('input[type=password]', PASSWORD), 20000, 'fill-password');
    await page.press('input[type=password]', 'Enter');
    log('password submitted');

    // 等进入文件列表（登录成功会跳到 /home）
    let onHome = false;
    for (let i = 0; i < 40; i++) {
      await sleep(1000);
      if (page.url().includes('/home')) { onHome = true; log('on-home-at', i + 's'); break; }
    }
    record('logged-in', onHome);
    // 等前端 Uploader 初始化完成（它初始化后才接受拖拽）
    for (let i = 0; i < 40; i++) {
      if (uploaderLogs.some(t => t.includes('Set allowed file suffix'))) { log('uploader-ready-at', i + 's'); break; }
      await sleep(1000);
    }
    record('uploader-ready', uploaderLogs.some(t => t.includes('Set allowed file suffix')));
    await shot('01-home');

    record('uploading-before', await countUploading(page));

    const name = 'e2e-' + Date.now() + '.bin';
    const size = 300000;
    await page.evaluate(async ({ name, size }) => {
      const dt = new DataTransfer();
      const buf = new Uint8Array(size);
      for (let i = 0; i < size; i++) buf[i] = (Math.random() * 256) | 0;
      dt.items.add(new File([buf], name, { type: 'application/octet-stream' }));
      for (const el of [document.body, document.documentElement]) {
        for (const type of ['dragenter', 'dragover', 'drop']) {
          el.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt }));
        }
      }
    }, { name, size });
    log('dropped', name);

    // 完成判定：分片全传完，或服务端判定秒传
    let done = false;
    for (let i = 0; i < 120; i++) {
      await sleep(1000);
      if (uploaderLogs.some(t => t.includes('Upload task completed'))) { done = true; log('completed-at', i + 's (chunks)'); break; }
      if (uploaderLogs.some(t => t.includes('Rapid upload'))) { done = true; log('completed-at', i + 's (rapid)'); break; }
      const bad = uploaderLogs.find(t => /Failed|Error/i.test(t));
      if (bad) { log('upload-error', bad.slice(0, 160)); break; }
    }
    record('upload-completed', done);
    record('upload-requests', uploadReqs);
    await shot('02-queue');

    // 等列表刷新
    let listed = false;
    for (let i = 0; i < 40; i++) {
      await sleep(1000);
      if ((await page.locator('body').innerText()).includes(name)) { listed = true; log('listed-at', i + 's'); break; }
    }
    record('file-in-list', listed);
    record('uploading-after', await countUploading(page));
    await shot('03-list');

    await page.reload({ waitUntil: 'domcontentloaded' });
    let reloadedListed = false;
    let uploadingAfterReload = -1;
    for (let i = 0; i < 45; i++) {
      await sleep(1000);
      const body = await page.locator('body').innerText();
      if (body.includes(name)) { reloadedListed = true; uploadingAfterReload = (body.match(/上传中/g) || []).length; break; }
    }
    record('file-in-list-after-reload', reloadedListed);
    record('uploading-after-reload', uploadingAfterReload);
    await shot('04-reload');
  } catch (e) {
    log('ERROR', e.message.split('\n')[0]);
    record('error', e.message.split('\n')[0]);
    await shot('99-error');
    exitCode = 2;
  } finally {
    await browser.close();
  }

  if (exitCode === 0) {
    const bad = result.filter(([k, v]) =>
      (k === 'logged-in' && v !== 'true') ||
      (k === 'upload-completed' && v !== 'true') ||
      (k === 'uploading-after' && Number(v) !== 0) ||
      (k === 'uploading-after-reload' && Number(v) !== 0) ||
      (k === 'file-in-list' && v !== 'true') ||
      (k === 'file-in-list-after-reload' && v !== 'true'));
    if (bad.length) exitCode = 1;
    record('VERDICT', exitCode === 0 ? 'PASS' : 'FAIL');
  }
  console.log('result file:', RESULT_FILE);
  process.exit(exitCode);
})();
