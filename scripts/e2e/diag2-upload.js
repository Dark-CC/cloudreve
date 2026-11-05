/**
 * 诊断：定位 Cloudreve 上传器的真实输入渠道，并测试界面点击路径。
 */
const fs = require('fs');
const path = require('path');
const BASE = process.env.BASE_URL;
const EMAIL = process.env.EMAIL;
const PASSWORD = process.env.PASSWORD;
const HOST = new URL(BASE).host;
const OUTDIR = process.env.OUTDIR || '/tmp';
const LOGFILE = path.join(OUTDIR, 'diag2.txt');
const sleep = ms => new Promise(r => setTimeout(r, ms));
const lines = [];
const log = (...a) => { const s = a.map(String).join(' '); lines.push(s); try { fs.writeFileSync(LOGFILE, lines.join('\n') + '\n'); } catch (e) {} console.log(s); };

(async () => {
  const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright-core');
  log('== start ' + new Date().toISOString());
  const b = await chromium.launch({ executablePath: process.env.CHROME, ignoreDefaultArgs: ['--headless=old'], args: ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage'] });
  const ctx = await b.newContext({ locale: 'zh-CN', viewport: { width: 1440, height: 950 } });
  const p = await ctx.newPage();
  const apiReqs = [];
  p.on('request', r => { const u = r.url(); if (u.includes(HOST) && u.includes('/api')) apiReqs.push(r.method() + ' ' + u.slice(u.indexOf('/api'))); });
  p.on('console', m => { const t = m.text(); if (t.includes('Uploader')) log('[up] ' + t.slice(0, 200)); });
  try {
    await p.goto(BASE + '/', { waitUntil: 'domcontentloaded', timeout: 30000 });
    await sleep(3000);
    await p.fill('input[type=email]', EMAIL);
    await p.press('input[type=email]', 'Enter');
    await sleep(2500);
    await p.fill('input[type=password]', PASSWORD);
    await p.press('input[type=password]', 'Enter');
    await sleep(9000);
    log('url ' + p.url());
    if (!p.url().includes('/home')) { await p.goto(BASE + '/home', { waitUntil: 'domcontentloaded' }); await sleep(9000); }

    // 1. 所有 file input
    const inputs = await p.evaluate(() => Array.from(document.querySelectorAll('input[type=file]')).map(e => ({
      id: e.id, name: e.name, cls: (e.className || '').toString().slice(0, 80), accept: e.accept, multiple: e.multiple,
      inDom: document.contains(e), hidden: e.offsetParent === null, outer: e.outerHTML.slice(0, 250)
    })));
    log('file-inputs ' + JSON.stringify(inputs));

    // 2. 页面上可见按钮
    const btns = await p.evaluate(() => Array.from(document.querySelectorAll('button,[role=button]')).map(e => (e.innerText || '').trim()).filter(Boolean).slice(0, 40));
    log('buttons ' + JSON.stringify(btns));

    // 3. 点「上传」按钮（若有），再列 input
    const upBtn = p.locator('button:has-text("上传")');
    log('upload-buttons ' + await upBtn.count());
    if (await upBtn.count()) {
      await upBtn.first().click();
      await sleep(3000);
      const after = await p.evaluate(() => Array.from(document.querySelectorAll('input[type=file]')).map(e => ({
        id: e.id, hidden: e.offsetParent === null, outer: e.outerHTML.slice(0, 250)
      })));
      log('after-click-inputs ' + JSON.stringify(after));
      await p.screenshot({ path: path.join(OUTDIR, 'diag2-after-click.png') });
    }

    // 4. 直接向 DOM 派发 DragEvent（模拟拖拽上传）
    const name = 'drag-' + Date.now() + '.bin';
    const size = 300000;
    apiReqs.length = 0;
    const dropped = await p.evaluate(async ({ name, size }) => {
      const dt = new DataTransfer();
      const buf = new Uint8Array(size); buf.fill(120);
      dt.items.add(new File([buf], name, { type: 'application/octet-stream' }));
      // 找拖拽容器：有 'drop' 相关的元素，退化为 body
      const targets = [document.body, document.documentElement];
      let fired = 0;
      for (const el of targets) {
        for (const type of ['dragenter', 'dragover', 'drop']) {
          el.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt }));
          fired++;
        }
      }
      return fired;
    }, { name, size });
    log('drop-events-fired ' + dropped + ' file=' + name);
    await sleep(12000);
    log('api-after-drop ' + JSON.stringify(apiReqs.filter(x => x.includes('upload'))));
    log('body-after-drop ' + (await p.locator('body').innerText()).slice(-500).replace(/\n+/g, ' | '));
    await p.screenshot({ path: path.join(OUTDIR, 'diag2-after-drop.png') });
  } catch (e) { log('ERR ' + e.message.split('\n')[0]); }
  finally { await b.close(); log('== end'); }
})();
