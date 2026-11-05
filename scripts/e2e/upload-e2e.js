#!/usr/bin/env node
/**
 * Cloudreve 上传链路端到端（真实浏览器）复验脚本
 *
 * 用途：在真实浏览器里走完整前端路径上传一个文件，验证
 *   1. 上传队列最终显示「已上传」（不是一直「上传中」）；
 *   2. 文件列表里「上传中」占位数为 0；
 *   3. 刷新页面后仍然是 0（说明后端 CompleteUpload 已清掉 upload_session_id）。
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

  // 阻断站点外的第三方资源（字体/CDN），避免 headless 环境长时间挂起
  await page.route('**/*', route => {
    const url = route.request().url();
    if (url.indexOf(HOST) >= 0 || url.startsWith('data:') || url.startsWith('blob:')) return route.continue();
    return route.abort();
  });
  page.on('request', r => {
    const u = r.url();
    if (u.indexOf('/api/v4/file/upload') >= 0) log('REQ', r.method(), u.slice(u.indexOf('/api')));
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
    await sleep(8000);
    if (!page.url().includes('/home')) {
      await page.goto(BASE + '/home', { waitUntil: 'domcontentloaded' });
      await sleep(8000);
    }
    record('logged-in', page.url().includes('/home'));
    await shot('01-home');

    let body = await page.locator('body').innerText();
    record('uploading-before', (body.match(/上传中/g) || []).length);

    const name = 'e2e-' + Date.now() + '.bin';
    const local = path.join(OUTDIR, name);
    fs.writeFileSync(local, Buffer.alloc(300000, 'e2e'));

    let input = await page.$('input[type=file]');
    if (!input) {
      const btn = page.locator('button:has-text("上传")');
      if (await btn.count()) {
        await btn.first().click();
        await sleep(2500);
        input = await page.$('input[type=file]');
      }
    }
    record('file-input-found', !!input);
    if (!input) throw new Error('NO_FILE_INPUT');

    await input.setInputFiles(local);
    log('selected', name);

    let queueDone = false;
    for (let i = 0; i < 120; i++) {
      await sleep(1000);
      const t = await page.locator('body').innerText();
      if (t.includes(name) && /已上传|上传完成|上传成功/.test(t)) { queueDone = true; log('queue-done-at', i); break; }
    }
    record('queue-shows-uploaded', queueDone);
    await shot('02-queue');

    await sleep(4000);
    body = await page.locator('body').innerText();
    record('uploading-after', (body.match(/上传中/g) || []).length);
    record('file-in-list', body.includes(name));
    await shot('03-list');

    await page.reload({ waitUntil: 'domcontentloaded' });
    await sleep(9000);
    body = await page.locator('body').innerText();
    record('uploading-after-reload', (body.match(/上传中/g) || []).length);
    record('file-in-list-after-reload', body.includes(name));
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
      (k === 'queue-shows-uploaded' && v !== 'true') ||
      (k === 'uploading-after' && Number(v) !== 0) ||
      (k === 'uploading-after-reload' && Number(v) !== 0) ||
      (k === 'file-in-list' && v !== 'true'));
    if (bad.length) exitCode = 1;
    record('VERDICT', exitCode === 0 ? 'PASS' : 'FAIL');
  }
  writeResults();
  process.exit(exitCode);
})();
