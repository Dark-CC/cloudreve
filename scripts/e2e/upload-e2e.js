#!/usr/bin/env node
/**
 * Cloudreve 上传链路端到端（真实浏览器）复验脚本
 *
 * 在真实浏览器里走完整前端路径上传一个随机内容文件，验证「上传完成后不再残留上传中占位」：
 *   1. 上传后文件列表里「上传中」占位数为 0；
 *   2. 文件名出现在列表里（说明真的传上去了，而不是停在占位）；
 *   3. 刷新页面后仍然是 0（说明后端 CompleteUpload 已清掉 upload_session_id）。
 *
 * 走拖拽上传：把 File 放进 DataTransfer 派发 dragenter/dragover/drop，
 * 这是前端 Cloudreve-Uploader 实际监听的入口，等价于用户把文件拖进窗口。
 *
 * 依赖：playwright-core + 本地 chromium。
 *   PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm i playwright-core@1.48.2
 *
 * 用法：
 *   BASE_URL=http://127.0.0.1:15230 EMAIL=admin@example.local PASSWORD='***' \
 *   CHROME=/root/.cache/ms-playwright/chromium-1194/chrome-linux/chrome \
 *   node scripts/e2e/upload-e2e.js
 *
 * 退出码：0 = 全部通过；1 = 有断言失败；2 = 脚本异常。
 *
 * 实现注意（低配机器上踩过的坑）：
 * - 新版 Chrome 已移除 --headless=old，playwright 1.48 默认仍传，故 ignoreDefaultArgs。
 * - 不要注册 page.on('console')：Cloudreve 前端日志量很大，逐条回调会把页面主线程拖死。
 * - 不要并发跑多个实例，2 核机器上会互相拖慢到近乎挂起。
 */
const fs = require('fs');
const path = require('path');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:15230';
const EMAIL = process.env.EMAIL || '';
const PASSWORD = process.env.PASSWORD || '';
const CHROME = process.env.CHROME || '';
const OUTDIR = process.env.OUTDIR || '/tmp';
const RESULT_FILE = path.join(OUTDIR, 'upload-e2e-result.txt');
const sleep = ms => new Promise(r => setTimeout(r, ms));

const result = [];
function log(...args) { console.log(args.map(String).join(' ')); }
function record(key, value) {
  result.push([key, value]);
  console.log(`${key} = ${value}`);
  try {
    fs.writeFileSync(RESULT_FILE, result.map(([k, v]) => `${k} = ${v}`).join('\n') + '\n');
  } catch (e) { /* 结果文件写不了不影响判定 */ }
}
const withTimeout = (p, ms, tag) => Promise.race([
  p,
  sleep(ms).then(() => { throw new Error('TIMEOUT:' + tag); }),
]);

/** 统计页面文本里「上传中」出现次数，等价于上传中占位数量 */
async function countUploading(page) {
  const body = await page.locator('body').innerText();
  return (body.match(/上传中/g) || []).length;
}

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
  const shot = n => page.screenshot({ path: path.join(OUTDIR, `upload-e2e-${n}.png`) }).catch(() => {});

  let exitCode = 0;
  try {
    log('open', BASE);
    await withTimeout(page.goto(BASE + '/', { waitUntil: 'domcontentloaded', timeout: 60000 }), 75000, 'goto');
    record('login-page', page.url().includes('/session'));

    // 登录是两步式：先填邮箱 → 回车 → 再填密码
    await withTimeout(page.fill('input[type=email]', EMAIL), 30000, 'fill-email');
    await page.press('input[type=email]', 'Enter');
    await withTimeout(page.waitForSelector('input[type=password]', { timeout: 30000 }), 40000, 'password-step');
    record('has-password-step', true);
    await withTimeout(page.fill('input[type=password]', PASSWORD), 30000, 'fill-password');
    await page.press('input[type=password]', 'Enter');
    log('password submitted');

    let onHome = false;
    try {
      await withTimeout(page.waitForURL('**/home**', { timeout: 60000 }), 75000, 'wait-url');
      onHome = true;
      log('on-home via redirect');
    } catch (e) {
      log('no auto redirect, goto /home');
    }
    if (!onHome) {
      await withTimeout(page.goto(BASE + '/home', { waitUntil: 'domcontentloaded', timeout: 60000 }), 75000, 'goto-home');
      onHome = page.url().includes('/home');
    }
    record('logged-in', onHome);
    if (!onHome) throw new Error('NOT_LOGGED_IN');

    // 等上传器的隐藏 input 出现，说明前端上传组件已挂载
    await withTimeout(page.waitForSelector('#upload-file-input-1', { state: 'attached', timeout: 60000 }), 70000, 'uploader-mount');
    record('uploader-ready', true);
    await sleep(3000);
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

    // 以「文件名出现在列表」作为上传完成的判定
    let listed = false;
    for (let i = 0; i < 120; i++) {
      await sleep(1000);
      if ((await page.locator('body').innerText()).includes(name)) { listed = true; log('listed-at', i + 's'); break; }
    }
    record('file-in-list', listed);
    await shot('02-list');

    // 等列表稳定后统计残留占位
    await sleep(8000);
    record('uploading-after', await countUploading(page));
    await shot('03-list-stable');

    await page.reload({ waitUntil: 'domcontentloaded' });
    let afterReloadListed = false;
    let uploadingAfterReload = -1;
    for (let i = 0; i < 90; i++) {
      await sleep(1000);
      const body = await page.locator('body').innerText();
      if (body.includes(name)) {
        afterReloadListed = true;
        uploadingAfterReload = (body.match(/上传中/g) || []).length;
        log('reload-listed-at', i + 's');
        break;
      }
    }
    record('file-in-list-after-reload', afterReloadListed);
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
    const bad = result.filter(([k, v]) => {
      const s = String(v);
      return (k === 'logged-in' && s !== 'true') ||
        (k === 'uploading-after' && Number(s) !== 0) ||
        (k === 'uploading-after-reload' && Number(s) !== 0) ||
        (k === 'file-in-list' && s !== 'true') ||
        (k === 'file-in-list-after-reload' && s !== 'true');
    });
    if (bad.length) { exitCode = 1; log('FAILED-ASSERTIONS', JSON.stringify(bad)); }
    record('VERDICT', exitCode === 0 ? 'PASS' : 'FAIL');
  }
  console.log('result file:', RESULT_FILE);
  process.exit(exitCode);
})();
