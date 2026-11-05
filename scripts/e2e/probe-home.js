const fs = require('fs');
const BASE = process.env.BASE_URL;
const sleep = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  const { chromium } = require(process.env.PLAYWRIGHT_MODULE);
  const b = await chromium.launch({ executablePath: process.env.CHROME, ignoreDefaultArgs: ['--headless=old'], args: ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--disable-gpu'] });
  const p = await (await b.newContext({ locale: 'zh-CN' })).newPage();
  const errs = [];
  p.on('pageerror', e => errs.push(e.message.split('\n')[0].slice(0, 120)));
  p.on('requestfailed', r => errs.push('FAILED ' + r.url().slice(-70) + ' ' + (r.failure() || {}).errorText));
  const t0 = Date.now();
  await p.goto(BASE + '/', { waitUntil: 'domcontentloaded', timeout: 60000 });
  const t1 = Date.now();
  await sleep(12000);
  const info = await p.evaluate(() => ({
    title: document.title,
    url: location.href,
    inputs: Array.from(document.querySelectorAll('input')).map(e => e.type + ':' + (e.name || e.id || '')),
    bodyLen: document.body.innerText.length,
    bodyHead: document.body.innerText.slice(0, 200).replace(/\n+/g, ' | '),
  }));
  console.log('goto-domcontentloaded-ms', t1 - t0);
  console.log(JSON.stringify(info, null, 1));
  console.log('errors', JSON.stringify(errs.slice(0, 8), null, 1));
  await p.screenshot({ path: '/data/cr-bx-15230/logs/probe-home.png' });
  await b.close();
  console.log('END');
})();
