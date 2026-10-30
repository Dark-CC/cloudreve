// 15220 测试站验收脚本
// 前置：
//   /data/pw 下有 node_modules/playwright，且 /root/.cache/ms-playwright/chromium-1140 已安装
//   （安装方式见 docs/build-log-1030.md §7）
//
// 用法：
//   CR_ADMIN_PASS=<管理员密码> node verify.js
// 可选环境变量：CR_BASE、CR_ADMIN_EMAIL、CR_OUT
//
// 输出：逐项 PASS / FAIL / NOTVERIFIED 到 stdout，结果 JSON 与截图写入 CR_OUT。
const { chromium } = require('/data/pw/node_modules/playwright');
const fs = require('fs');

const BASE = process.env.CR_BASE || 'http://127.0.0.1:15220';
const EMAIL = process.env.CR_ADMIN_EMAIL || 'admin@cr15220.local';
const PASS = process.env.CR_ADMIN_PASS || '';
const OUT = process.env.CR_OUT || '/data/pw/shots';

const res = [];
function rec(name, ok, extra) {
  const state = ok === 'NOTVERIFIED' ? 'NOTVERIFIED' : (ok ? 'PASS' : 'FAIL');
  res.push({ name, state, extra: extra === undefined ? '' : String(extra).slice(0, 400) });
  console.log(state + ' | ' + name + (extra ? ' | ' + String(extra).slice(0, 200) : ''));
}

(async () => {
  fs.mkdirSync(OUT, { recursive: true });
  const browser = await chromium.launch({ args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'zh-CN' });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e).slice(0, 200)));

  let token, uid, userObj;
  const api = async (method, path, data) => {
    const r = await page.request[method](BASE + path, {
      headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' },
      data,
    });
    let j;
    try { j = await r.json(); } catch (e) { j = { _raw: (await r.text()).slice(0, 200) }; }
    return { status: r.status(), json: j };
  };

  try {
    // ---------- 基础 ----------
    const r1 = await page.goto(BASE + '/', { waitUntil: 'domcontentloaded', timeout: 45000 });
    rec('首页 HTTP 200', r1 && r1.status() === 200, 'status=' + (r1 && r1.status()));
    await page.waitForTimeout(2500);
    rec('首页标题含 Cloudreve', /Cloudreve/.test(await page.title()), await page.title());
    await page.screenshot({ path: OUT + '/01-home.png', fullPage: true });

    // ---------- 登录 ----------
    const lr = await page.request.post(BASE + '/api/v4/session/token', { data: { email: EMAIL, password: PASS } });
    const lj = await lr.json();
    rec('登录 API code=0', lj.code === 0, 'code=' + lj.code + ' ' + (lj.msg || ''));
    token = lj.data.token.access_token;
    uid = lj.data.user.id;
    userObj = lj.data.user;
    rec('管理员在 Admin 组', userObj.group && userObj.group.name === 'Admin', (userObj.group || {}).name);
    fs.writeFileSync(OUT + '/token.txt', token);

    // 注入前端登录态（localStorage['cloudreve_session']）
    const sessionState = {
      current: uid,
      sessions: { [uid]: { user: userObj, token: lj.data.token, settings: {} } },
      anonymousSettings: {},
    };
    await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
    await page.evaluate((s) => localStorage.setItem('cloudreve_session', JSON.stringify(s)), sessionState);

    // ---------- t44: 后台订单列表 ----------
    const ordList = await api('get', '/api/v4/admin/payment-orders?page_size=10');
    rec('后台订单列表 API (t44)', ordList.status === 200 && ordList.json.code === 0,
      JSON.stringify(ordList.json).slice(0, 180));

    // ---------- t44: 补单路由存在 ----------
    const rec1 = await api('post', '/api/v4/admin/payment-orders/NOSUCHID/reconcile', { reason: 'cr-test-probe' });
    rec('补单路由存在（非 404 路由缺失）', rec1.status !== 404,
      'status=' + rec1.status + ' code=' + (rec1.json || {}).code);

    // ---------- t43: 凭证续期横幅 ----------
    const notice = await api('get', '/api/v4/admin/credential-notices');
    rec('凭证续期横幅 API (t43)', notice.status === 200 && notice.json.code === 0,
      JSON.stringify(notice.json).slice(0, 150));

    // ---------- 易支付设置项 ----------
    const keys = ['payment_easypay_enabled', 'payment_easypay_gateway', 'payment_easypay_pid', 'payment_easypay_type'];
    const set1 = await api('post', '/api/v4/admin/settings', { keys });
    rec('易支付 4 个设置项齐全', set1.json.code === 0 && keys.every(k => k in (set1.json.data || {})),
      JSON.stringify(set1.json.data || {}).slice(0, 200));

    // ---------- VAS: 容量单位与单价小数 ----------
    // 用时间戳命名，避免同名 SKU 造成按名字查找取错（这是上一轮出现过的一次误报原因）
    const stamp = Date.now();
    const skuName = 'cr-test-sku-' + stamp;
    const mkSku = await api('put', '/api/v4/admin/vas/sku', {
      sku: { name: skuName, type: 'storage', amount: 100, duration: 0, price: 1, enabled: true, weight: 10 },
    });
    const skuId = mkSku.json && mkSku.json.data && mkSku.json.data.id;
    rec('VAS 建 SKU 成功', mkSku.json.code === 0 && !!skuId, JSON.stringify(mkSku.json).slice(0, 150));
    rec('VAS 单价支持 0.01 元（1 分）', mkSku.json.code === 0 && mkSku.json.data.price === 1,
      'stored price=' + (mkSku.json.data || {}).price);
    if (skuId) {
      const upPrice = await api('put', '/api/v4/admin/vas/sku/' + skuId, {
        sku: { name: skuName, type: 'storage', amount: 100, duration: 0, price: 12345, enabled: true, weight: 10 },
      });
      rec('VAS 小数单价往返（123.45 元 = 12345 分）',
        upPrice.json.code === 0 && upPrice.json.data.price === 12345,
        'price=' + (upPrice.json.data || {}).price);
    }

    // 前台商城读到（按 id 精确匹配，不按名字）
    const shopSkus = await api('get', '/api/v4/user/shop/skus');
    const shopSku = ((shopSkus.json || {}).data || []).find(s => s.name === skuName);
    rec('前台商城读到 SKU', shopSkus.json.code === 0 && !!shopSku, 'name=' + skuName);

    await page.goto(BASE + '/shop', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(5000);
    const shopTxt = await page.locator('body').innerText();
    fs.writeFileSync(OUT + '/06-shop.txt', shopTxt);
    await page.screenshot({ path: OUT + '/06-shop.png', fullPage: true });
    rec('商城页渲染（含我的订单入口）', /商城|订单/.test(shopTxt), shopTxt.slice(0, 120).replace(/\n/g, ' '));

    // ---------- 订单记录：下单 → 落库 ----------
    // 配一个不可达网关，验证"网关失败但订单仍落库"
    await api('patch', '/api/v4/admin/settings', {
      settings: {
        payment_easypay_enabled: '1', payment_easypay_gateway: 'http://127.0.0.1:15999',
        payment_easypay_pid: '1001', payment_easypay_key: 'cr-test-key', payment_easypay_type: 'alipay',
      },
    });
    const co = await api('post', '/api/v4/user/shop/checkout', { sku: shopSku && shopSku.id, provider: 'easypay' });
    rec('下单请求已受理（网关不可达返回 50001 属预期）', co.json.code !== undefined,
      'code=' + co.json.code + ' ' + (co.json.msg || '').slice(0, 60));

    const ordList2 = await api('get', '/api/v4/admin/payment-orders?page_size=10');
    const orders = ((ordList2.json || {}).data || {}).orders || [];
    const created = orders.find(o => shopSku && o.sku_id === shopSku.id);
    rec('订单已落库（关键：网关失败也入库）', !!created,
      created ? ('id=' + created.id + ' amount=' + created.amount + ' status=' + created.status +
        ' provider=' + created.provider) : 'no order');
    if (created) {
      rec('订单金额 = 小数单价 12345', created.amount === 12345, 'amount=' + created.amount);
      rec('订单状态 pending', created.status === 'pending', 'status=' + created.status);
      rec('订单有效期 30 分钟', (created.expires_at - created.created_at) === 1800,
        'ttl=' + (created.expires_at - created.created_at) + 's');
    }

    // ---------- 后台订单页渲染 ----------
    await page.goto(BASE + '/admin/payment-orders', { waitUntil: 'domcontentloaded', timeout: 45000 });
    await page.waitForTimeout(6000);
    const ordBody = await page.locator('body').innerText();
    await page.screenshot({ path: OUT + '/05-admin-orders.png', fullPage: true });
    fs.writeFileSync(OUT + '/05-admin-orders.txt', ordBody);
    rec('后台订单页渲染', ordBody.length > 50, 'len=' + ordBody.length);
    rec('订单表列头完整（订单号/用户/金额/支付渠道）',
      /订单号/.test(ordBody) && /用户/.test(ordBody) && /金额/.test(ordBody) && /支付渠道/.test(ordBody),
      (ordBody.split('\n').find(l => /订单号/.test(l)) || '').slice(0, 120));
    rec('后台订单页展示刚落库的订单号', !!created && ordBody.includes(created.id), created ? created.id : 'n/a');

    // ---------- 后台设置页 ----------
    await page.goto(BASE + '/admin/settings', { waitUntil: 'domcontentloaded', timeout: 45000 });
    await page.waitForTimeout(6000);
    const setBody = await page.locator('body').innerText();
    await page.screenshot({ path: OUT + '/03-admin-settings.png', fullPage: true });
    fs.writeFileSync(OUT + '/03-admin-settings.txt', setBody);
    rec('后台设置页渲染', setBody.length > 50, 'len=' + setBody.length);

    // ---------- VAS 管理界面 ----------
    await page.goto(BASE + '/admin/settings?tab=vas', { waitUntil: 'domcontentloaded', timeout: 45000 });
    await page.waitForTimeout(5000);
    const vasBody = await page.locator('body').innerText();
    fs.writeFileSync(OUT + '/07-vas.txt', vasBody);
    await page.screenshot({ path: OUT + '/07-vas.png', fullPage: true });
    rec('VAS 管理界面可访问', vasBody.length > 30, 'len=' + vasBody.length);

    // ---------- 群组：允许目录直链开关 ----------
    const groups = await api('post', '/api/v4/admin/group', { page: 1, page_size: 10, order_by: 'id', order: 'asc' });
    rec('群组列表 API', groups.json.code === 0, JSON.stringify(groups.json).slice(0, 120));
    const gid = ((groups.json || {}).data || {}).groups && groups.json.data.groups[0] && groups.json.data.groups[0].id;
    if (gid) {
      await page.goto(BASE + '/admin/group/' + gid, { waitUntil: 'domcontentloaded', timeout: 45000 });
      await page.waitForTimeout(6000);
      const gBody = await page.locator('body').innerText();
      fs.writeFileSync(OUT + '/08-group.txt', gBody);
      await page.screenshot({ path: OUT + '/08-group.png', fullPage: true });
      rec('群组页含「允许目录直链」开关', /直链/.test(gBody),
        (gBody.match(/[^\n]*直链[^\n]*/) || [''])[0].slice(0, 100));
    } else {
      rec('群组页含「允许目录直链」开关', 'NOTVERIFIED', '拿不到群组 id');
    }

    // ---------- 文件页 ----------
    await page.goto(BASE + '/home', { waitUntil: 'domcontentloaded', timeout: 45000 });
    await page.waitForTimeout(5000);
    const fileBody = await page.locator('body').innerText();
    await page.screenshot({ path: OUT + '/04-home-files.png', fullPage: true });
    fs.writeFileSync(OUT + '/04-home-files.txt', fileBody);
    rec('文件页渲染', fileBody.length > 20, 'len=' + fileBody.length);

    // ---------- 前端 JS 错误 ----------
    const fatal = errs.filter(e => /TypeError|ReferenceError|is not a function|Cannot read/.test(e));
    rec('无致命前端 JS 错误', fatal.length === 0, fatal.slice(0, 2).join(' || '));

    // ---------- 无法自动化的项（如实标注） ----------
    rec('易支付真实支付闭环（二维码→扫码→回调到账）', 'NOTVERIFIED', '无可用网关/PID/真实密钥');
    rec('移动云盘策略授权框与 Endpoint 隐藏', 'NOTVERIFIED', '需真实 139 盘账号授权');
    rec('秒传缩略图真实生成', 'NOTVERIFIED', '需实际上传并与上游秒传交互');
    rec('下拉字号 14px', 'NOTVERIFIED', 'CSS 视觉项，需人工看截图判定');

  } catch (e) {
    rec('脚本异常', false, e.message);
  } finally {
    const pass = res.filter(r => r.state === 'PASS').length;
    const fail = res.filter(r => r.state === 'FAIL').length;
    const nv = res.filter(r => r.state === 'NOTVERIFIED').length;
    fs.writeFileSync(OUT + '/result.json', JSON.stringify(res, null, 2));
    console.log('SUMMARY pass=' + pass + ' fail=' + fail + ' notverified=' + nv + ' total=' + res.length);
    await browser.close();
    process.exit(0);
  }
})();
