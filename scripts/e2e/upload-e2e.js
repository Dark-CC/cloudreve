#!/usr/bin/env node
/**
 * Cloudreve 上传链路端到端（真实浏览器）复验脚本
 *
 * 用途：在真实浏览器里走完整前端路径上传一个文件，验证
 *   1. 上传后文件列表里「上传中」占位数为 0；
 *   2. 文件真的出现在列表里（不是残留的占位）；
 *   3. 刷新页面后仍然是 0（说明后端 CompleteUpload 已清掉 upload_session_id）。
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
 * 注意：新版 Chrome 已移除 --headless=old，playwright 1.48 默认仍会传，
 * 因此这里显式 ignoreDefaultArgs 并改用 --headless=new。
 */
const fs = require('fs');
const path = require('path');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:15230';
const EMAIL = process.env.EMAIL || '';
const PASSWORD = process.env.PASSWORD || '';
const CHROME = process.env.CHROME || '';
const OUTDIR = process.env.OUTDIR || '/tmp';
const HOST = new URL(BASE).host;
const sleep = ms => new Promise(r => setTimeout(r, ms));

const result = [];
const logs = [];
function log(...args) {
  const line = args.map(String).join(' ');
  logs.push(line);
  console.log(line);
}
function record(key, value) {
  result.push([key, value]);
  console.log(`${key} = ${value}`);
}
function writeResults() {
  const file = path.join(OUTDIR, 'upload-e2e-result.txt');
  fs.writeFileSync(file, result.map(([k, v]) => `${k} = ${v}`).join('\n') + '\n' +
    '\n--- log ---\n' + logs.join('\n') + '\n');
  console.log('result file: ' + file);
}

const withTimeout = (p, ms, tag) => Promise.race([
  p,
  sleep(ms).then(() => { throw new Error('TIMEOUT:' + tag); }),
]);

(async () => {
  if (!EMAIL || !PASSWORD) {
    console.error('缺少 EMAIL/PASSWORD 环境变量');
    process.exit(2);
  }
  const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright-core');
  const launchOpts = {
    ignoreDefaultArgs: ['--headless=old'],
    args: ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage'],
  };
  if (CHROME) launchOpts.executablePath = CHROME;

  const browser = await chromium.launch(launchOpts);
  const ctx = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1440, height: 950 } });
  const page = await ctx.newPage();

  const sessionReqs = [];
  page.on('request', r => {
    const u = r.url();
    if (u.includes('/api/v4/file/upload')) sessionReqs.push(r.method() + ' ' + u.slice(u.indexOf('/api')).split('?')[0]);
  });
  const uploaderLogs = [];
  page.on('console', m => {
    const t = m.text();
    if (t.includes('Uploader')) { uploaderLogs.push(t.slice(0, 200)); if (uploaderLogs.length <= 40) log('[up]', t.slice(0, 200)); }
  });
  page.on('pageerror', e => log('[pageerror]', e.message.split('\n')[0].slice(0, 160)));
  const shot = n => page.screenshot({ path: path.join(OUTDIR, `upload-e2e-${n}.png`) }).catch(() => {});

  let exitCode = 0;
  try {
    await withTimeout(page.goto(BASE + '/', { waitUntil: 'domcontentloaded', timeout: 30000 }), 45000, 'goto');
    await sleep(3000);
    log('url', page.url());

    // 登录是两步式：先填邮箱 → 下一步 → 再填密码
    await withTimeout(page.fill('input[type=email]', EMAIL), 15000, 'fill-email');
    await page.press('input[type=email]', 'Enter');
    await sleep(2500);
    record('has-password-step', (await page.locator('input[type=password]').count()) > 0);
    await withTimeout(page.fill('input[type=password]', PASSWORD), 15000, 'fill-password');
    await page.press('input[type=password]', 'Enter');
    await sleep(9000);
    if (!page.url().includes('/home')) {
      await page.goto(BASE + '/home', { waitUntil: 'domcontentloaded' });
      await sleep(9000);
    }
    record('logged-in', page.url().includes('/home'));
    await shot('01-home');

    let body = await page.locator('body').innerText();
    record('uploading-before', (body.match(/上传中/g) || []).length);

    const name = 'e2e-' + Date.now() + '.bin';
    const size = 300000;
    sessionReqs.length = 0;
    uploaderLogs.length = 0;

    // 拖拽上传：与用户把文件拖进窗口等价，是前端 Uploader 真正监听的入口。
    // 内容用随机字节，避免命中服务端秒传（那样就不会真正走分片上传链路）。
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

    // 上传完成判定：chunk 全传完，或命中服务端秒传，二者都视为完成
    let queueDone = false;
    for (let i = 0; i < 90; i++) {
      await sleep(1000);
      if (uploaderLogs.some(t => t.includes('Upload task completed'))) { queueDone = true; log('upload-completed-at', i + 's', '(chunks)'); break; }
      if (uploaderLogs.some(t => t.includes('Rapid upload'))) { queueDone = true; log('upload-completed-at', i + 's', '(rapid)'); break; }
      if (uploaderLogs.some(t => /Failed|error/i.test(t))) { log('upload-error-seen', uploaderLogs.find(t => /Failed|error/i.test(t))); break; }
    }
    record('uploader-reported-complete', queueDone);
    record('upload-requests', sessionReqs.length);
    await sleep(5000);
    await shot('02-list');

    body = await page.locator('body').innerText();
    record('uploading-after', (body.match(/上传中/g) || []).length);
    record('file-in-list', body.includes(name));

    await page.reload({ waitUntil: 'domcontentloaded' });
    await sleep(10000);
    body = await page.locator('body').innerText();
    record('uploading-after-reload', (body.match(/上传中/g) || []).length);
    record('file-in-list-after-reload', body.includes(name));
    await shot('03-reload');
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
      (k === 'uploader-reported-complete' && v !== 'true') ||
      (k === 'uploading-after' && Number(v) !== 0) ||
      (k === 'uploading-after-reload' && Number(v) !== 0) ||
      (k === 'file-in-list' && v !== 'true') ||
      (k === 'file-in-list-after-reload' && v !== 'true'));
    if (bad.length) exitCode = 1;
    record('VERDICT', exitCode === 0 ? 'PASS' : 'FAIL');
  }
  writeResults();
  process.exit(exitCode);
})();
