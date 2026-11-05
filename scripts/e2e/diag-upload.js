/**
 * 诊断脚本：为什么在真实浏览器里选文件后没有发起上传请求。
 * 输出：所有网络请求（含被拦的第三方）、上传相关 DOM、控制台消息。
 */
const fs = require('fs');
const path = require('path');
const BASE = process.env.BASE_URL;
const EMAIL = process.env.EMAIL;
const PASSWORD = process.env.PASSWORD;
const HOST = new URL(BASE).host;
const OUTDIR = process.env.OUTDIR || '/tmp';
const sleep = ms => new Promise(r => setTimeout(r, ms));
const lines = [];
const log = (...a) => { const s = a.map(String).join(' '); lines.push(s); console.log(s); fs.writeFileSync(path.join(OUTDIR, 'diag.txt'), lines.join('\n') + '\n'); };

(async () => {
  const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright-core');
  const b = await chromium.launch({ executablePath: process.env.CHROME, ignoreDefaultArgs: ['--headless=old'], args: ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage'] });
  const ctx = await b.newContext({ locale: 'zh-CN' });
  const p = await ctx.newPage();
  const reqs = [];
  p.on('request', r => reqs.push(r.method() + ' ' + r.url()));
  p.on('console', m => log('[console.' + m.type() + ']', m.text().slice(0, 300)));
  p.on('pageerror', e => log('[pageerror]', e.message.split('\n')[0].slice(0, 200)));
  try {
    await p.goto(BASE + '/', { waitUntil: 'domcontentloaded', timeout: 30000 });
    await sleep(3000);
    await p.fill('input[type=email]', EMAIL);
    await p.press('input[type=email]', 'Enter');
    await sleep(2500);
    await p.fill('input[type=password]', PASSWORD);
    await p.press('input[type=password]', 'Enter');
    await sleep(9000);
    if (!p.url().includes('/home')) { await p.goto(BASE + '/home', { waitUntil: 'domcontentloaded' }); await sleep(9000); }
    log('url', p.url());

    // 上传相关 DOM
    const dom = await p.evaluate(() => {
      const out = [];
      document.querySelectorAll('input[type=file], [class*=upload], [id*=upload], [class*=Upload]').forEach(e => {
        out.push({ tag: e.tagName, id: e.id, cls: (e.className || '').toString().slice(0, 120), type: e.type, accept: e.accept, hidden: e.offsetParent === null });
      });
      return out.slice(0, 30);
    });
    log('upload-dom', JSON.stringify(dom, null, 1));

    const name = 'diag-' + Date.now() + '.bin';
    const local = path.join(OUTDIR, name);
    fs.writeFileSync(local, Buffer.alloc(300000, 'x'));

    reqs.length = 0;
    const input = await p.$('input[type=file]');
    log('input-found', !!input);
    if (input) {
      await input.setInputFiles(local);
      log('setInputFiles-done');
      await sleep(15000);
      log('reqs-after-select', JSON.stringify(reqs.filter(u => u.includes(HOST) && u.includes('/api')), null, 1));
      log('body-after', (await p.locator('body').innerText()).slice(-800).replace(/\n+/g, ' | '));
    }
    await p.screenshot({ path: path.join(OUTDIR, 'diag.png') });
  } catch (e) { log('ERR', e.message.split('\n')[0]); }
  finally { await b.close(); log('END'); }
})();
