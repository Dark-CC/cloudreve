/**
 * 诊断脚本（无请求拦截版）：选文件后前端是否发起上传请求。
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
  log('step launch');
  const b = await chromium.launch({ executablePath: process.env.CHROME, ignoreDefaultArgs: ['--headless=old'], args: ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage'] });
  const ctx = await b.newContext({ locale: 'zh-CN' });
  const p = await ctx.newPage();
  const reqs = [];
  p.on('request', r => { const u = r.url(); if (u.includes(HOST) && u.includes('/api')) reqs.push(r.method() + ' ' + u.slice(u.indexOf('/api'))); });
  try {
    log('step goto');
    await p.goto(BASE + '/', { waitUntil: 'domcontentloaded', timeout: 30000 });
    await sleep(3000);
    log('step login1');
    await p.fill('input[type=email]', EMAIL);
    await p.press('input[type=email]', 'Enter');
    await sleep(2500);
    log('step login2');
    await p.fill('input[type=password]', PASSWORD);
    await p.press('input[type=password]', 'Enter');
    await sleep(9000);
    log('url', p.url());
    if (!p.url().includes('/home')) { await p.goto(BASE + '/home', { waitUntil: 'domcontentloaded' }); await sleep(9000); log('url2', p.url()); }

    const dom = await p.evaluate(() => {
      const out = [];
      document.querySelectorAll('input[type=file]').forEach(e => out.push({ id: e.id, cls: (e.className || '').toString().slice(0, 100), accept: e.accept, hidden: e.offsetParent === null, outer: e.outerHTML.slice(0, 200) }));
      return out;
    });
    log('file-inputs', JSON.stringify(dom));

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
      log('api-reqs', JSON.stringify(reqs));
      log('body-after', (await p.locator('body').innerText()).slice(-600).replace(/\n+/g, ' | '));
    }
    await p.screenshot({ path: path.join(OUTDIR, 'diag.png') });
  } catch (e) { log('ERR', e.message.split('\n')[0]); }
  finally { await b.close(); log('END'); }
})();
