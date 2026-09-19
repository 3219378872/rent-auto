// Review probe only: intercept every API request; no backend or platform writes.
// PW_MODULE=/absolute/path/to/playwright UX_FONTCONFIG=/optional/fonts.conf node probe.cjs
// Start the unmodified frontend on 127.0.0.1:4179 first.
const { chromium } = require(process.env.PW_MODULE || 'playwright')
const fs = require('node:fs')
const path = require('node:path')
const assert = require('node:assert/strict')
const output = process.env.UX_OUTPUT || __dirname
const origin = 'http://127.0.0.1:4179'
const ts = '2026-09-19T08:00:00Z'
const longName = '★ StatTrak™ M9 Bayonet | Doppler (Factory New) Phase 4'
const names = [longName, 'AK-47 | Redline (Field-Tested)', 'AWP | Asiimov (Field-Tested)', '★ Sport Gloves | Vice (Minimal Wear)']
const inventory = Array.from({ length: 64 }, (_, i) => ({
  id: i + 1, channel: i % 2 ? 'uu' : 'eco', asset_id: `fixture-${i}`, hash_name: names[i % names.length],
  market_hash_name: names[i % names.length], template_id: i % 4 + 1,
  mark_price: 1850 + i, cost_basis: i % 5 ? 1600 : 0, tradable: true,
  status: ['listed', 'in_stock', 'leased', 'missing'][i % 4],
}))
const listings = inventory.map((r, i) => ({
  ...r, goods_ref: `listing-${i}`, actual_state: i % 4 ? 'active' : 'leased', desired_state: 'active',
  rent_price: 3.5, long_rent_price: 3.2, deposit: 2590, max_days: 30, factor: 0.95,
  listed_at: ts, last_reprice_at: ts,
  last_decision: { action: i % 3 ? 'reprice' : 'skip', at: ts, new_rent: 3.7, skip: 'guardrail_conflict' },
}))
const orders = inventory.map((r, i) => ({
  ...r, order_ref: `ECO-20260919-${String(i).padStart(7, '0')}`, order_type: i % 8 === 3 ? 'buyout' : i % 2 ? 'short' : 'long',
  status: ['leasing', 'delivering', 'arbitrating', 'bought_out', 'returning', 'breach', 'done', 'unknown'][i % 8],
  rent_days: 7, rent_price: 3.5, order_amount: 24.5, deposits: 2590, started_at: '2026-09-12T08:00:00Z',
  due_at: ts, finished_at: null,
}))
const templates = names.map((hash_name, i) => ({
  hash_name, display_name: ['M9 刺刀 · 多普勒（崭新出厂）', 'AK-47 · 红线（久经沙场）', 'AWP · 二西莫夫（久经沙场）', '运动手套 · 迈阿密风云（略有磨损）'][i],
  category: ['knife', 'rifle', 'sniper', 'gloves'][i], blacklisted: false,
  value_anchor: 1850, uu_template_id: 100 + i, uu_mark_price: 1840, eco_ref_price: 1860,
}))
const strategies = [
  { id: 1, name: 'default', scope: 'global', channel_route: 'eco_only', params: { baseline: { topn: 20 } }, real_execution_enabled: false, priority: 0, updated_at: ts },
  { id: 2, name: `tpl:${names[1]}`, scope: 'template', channel_route: 'eco_only', params: { baseline: { k1: 0.9 } }, real_execution_enabled: true, priority: 5, updated_at: ts },
]
const audits = [{ ts, actor: 'system', action: 'reprice', channel: 'eco', target: 'fixture-1', detail: {
  listing_id: 2, dry_run: true, reason: '这是模拟的长审计详情，用来检查能否完整阅读以及区分模拟与真实执行。'.repeat(7), old_rent: 3.5, new_rent: 3.7,
} }]
const dashboard = {
  assets: { total: 158600, inventory: 110000, deposits: { uu: 10000, eco: 20000 }, wallets: { uu: 7200, eco: 11400 } },
  income: { total: 3150, today: 68.5, by_channel: [{ channel: 'uu', income: 1550, orders: 44 }, { channel: 'eco', income: 1800, orders: 59 }] },
  leased_out: 14, annualized_roi: 0.1845,
  categories: [{ category: 'knife', cost: 78000, income: 2240, yield: 0.0287 }],
  series_30d: Array.from({ length: 30 }, (_, i) => ({ date: new Date(Date.UTC(2026, 7, 21 + i)).toISOString().slice(0, 10), income: 25 + i * 1.4 + Math.sin(i) * 15 })),
}
const report = { sourceCommit: '5a9b303d3003eed0f3b9d43402b5947d5be328c5', evidence: 'Chromium with synthetic, intercepted API fixtures; not a live backend or real device', viewports: [], observations: {}, consoleErrors: [], unexpectedRequests: [] }
let mode = 'normal'
let requests = []
let dialogs = []
const delay = (ms) => new Promise(resolve => setTimeout(resolve, ms))

async function main() {
  fs.mkdirSync(output, { recursive: true })
  const browser = await chromium.launch({ headless: true, env: { ...process.env, ...(process.env.UX_FONTCONFIG ? { FONTCONFIG_FILE: process.env.UX_FONTCONFIG } : {}) } })
  report.browser = browser.version()
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'zh-CN', timezoneId: 'Asia/Shanghai' })
  await context.route('**/*', async route => {
    const req = route.request(), url = new URL(req.url())
    if (url.origin !== origin) { report.unexpectedRequests.push(req.url()); return route.abort() }
    if (!url.pathname.startsWith('/api/')) return route.continue()
    const p = url.pathname.replace('/api/v1', '')
    requests.push({ path: p, query: url.search, method: req.method(), body: req.postData() })
    const json = (body, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
    if (mode === 'slow-inventory' && p === '/inventory') await delay(1600)
    if (mode === 'slow-strategies' && p === '/strategies' && req.method() === 'GET') await delay(1600)
    if (mode === 'slow-filter' && p === '/inventory' && url.searchParams.get('channel') === 'uu') await delay(1600)
    if (mode === 'inventory-error' && p === '/inventory') return json({ code: 'internal_error', message: 'internal error' }, 500)
    if (mode === 'health-error' && p === '/channels') return json({ code: 'unavailable', message: 'channels unavailable' }, 503)
    if (req.method() !== 'GET') {
      if (mode === 'slow-save') await delay(1200)
      if (p === '/auth/login') return json({ token: 'ux-fixture-session' })
      return json({ status: 'triggered' })
    }
    if (p === '/dashboard') return json(dashboard)
    if (p === '/channels') return json({ uu: 'error:token expired', eco: 'ok:…fixture', steam: 'not_configured' })
    if (p === '/strategies') return json(strategies)
    if (p === '/templates') return json(templates)
    if (p === '/jobs') return json([{ name: 'reprice', running: false, last_ok: false, last_error: 'fixture failure', last_run: ts, next_run: ts }])
    let rows = { '/inventory': inventory, '/listings': listings, '/orders': orders, '/audit': audits }[p]
    if (rows) {
      if (mode === 'empty') rows = []
      for (const key of ['channel', 'status', 'state']) {
        const v = url.searchParams.get(key)
        if (v) rows = rows.filter(r => r[key === 'state' ? 'actual_state' : key] === v)
      }
      const search = url.searchParams.get('search')
      if (search) rows = rows.filter(r => r.hash_name.toLowerCase().includes(search.toLowerCase()))
      const page = Number(url.searchParams.get('page') || 1), size = Number(url.searchParams.get('page_size') || 50)
      return json({ items: rows.slice((page - 1) * size, page * size), total: rows.length })
    }
    report.unexpectedRequests.push(req.url())
    return json({ message: 'Unmocked API blocked' }, 501)
  })
  const page = await context.newPage()
  page.on('pageerror', err => report.consoleErrors.push(err.message))
  page.on('dialog', async d => { dialogs.push(d.message()); await d.dismiss() })
  await page.goto(origin)
  await page.evaluate(() => localStorage.setItem('ra_token', 'ux-fixture-session'))
  const visit = async (route, wait = 250) => {
    await page.goto('about:blank')
    await page.goto(`${origin}/#${route}`)
    await page.waitForTimeout(wait)
  }
  const shot = async name => page.screenshot({ path: path.join(output, `${name}.png`), fullPage: false })
  const metrics = () => page.evaluate(() => {
    const rect = el => { const r = el.getBoundingClientRect(); return { x: Math.round(r.x), width: Math.round(r.width), height: Math.round(r.height) } }
    return { documentWidth: document.documentElement.scrollWidth, viewport: innerWidth, pageHeight: document.documentElement.scrollHeight,
      main: document.querySelector('main') ? { ...rect(document.querySelector('main')), scrollWidth: document.querySelector('main').scrollWidth } : null,
      aside: document.querySelector('aside') ? rect(document.querySelector('aside')) : null,
      tables: [...document.querySelectorAll('table')].map(t => ({ ...rect(t), rows: t.querySelectorAll('tbody tr').length })),
      controlsWithoutExplicitLabel: [...document.querySelectorAll('input,select,textarea')].filter(e => !e.labels?.length && !e.getAttribute('aria-label') && !e.getAttribute('aria-labelledby')).map(e => ({ tag: e.tagName, type: e.type, placeholder: e.getAttribute('placeholder') })),
    }
  })
  for (const width of [1440, 1280, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    for (const route of ['/login', '/', '/inventory', '/listings', '/orders', '/strategies', '/channels', '/audit']) {
      await visit(route)
      const name = route === '/' ? 'dashboard' : route.slice(1)
      await shot(`${width}-${name}`)
      report.viewports.push({ width, route, ...await metrics() })
    }
  }
  await page.setViewportSize({ width: 1440, height: 900 })
  mode = 'slow-inventory'
  await visit('/inventory', 250)
  report.observations.loadingLooksEmpty = { text: await page.locator('main').innerText(), rows: await page.locator('tbody tr').count(), busyIndicators: await page.locator('[aria-busy=true]').count() }
  await shot('loading-inventory')
  await page.waitForTimeout(1650)
  mode = 'slow-filter'
  await page.locator('select').first().selectOption('uu')
  await page.waitForTimeout(100)
  report.observations.staleFilter = { selected: await page.locator('select').first().inputValue(), firstRowChannel: await page.locator('tbody tr td').first().innerText(), busyIndicators: await page.locator('[aria-busy=true]').count() }
  await page.waitForTimeout(1700)
  mode = 'inventory-error'
  await visit('/inventory')
  report.observations.errorRecovery = { text: await page.locator('main').innerText(), buttons: await page.locator('main button').allTextContents() }
  mode = 'empty'
  await visit('/orders')
  report.observations.emptyOrders = await page.locator('main').innerText()
  await shot('empty-orders')
  mode = 'health-error'
  await visit('/')
  report.observations.healthFailure = { alerts: await page.locator('[role=alert],.error').count(), text: await page.locator('main').innerText() }
  await shot('dashboard-health-error')
  mode = 'normal'
  await visit('/listings')
  await page.locator('select').first().selectOption('eco')
  await page.waitForTimeout(200)
  requests = []; dialogs = []
  await page.getByRole('button', { name: '立即重定价', exact: true }).click()
  await page.waitForTimeout(250)
  report.observations.repriceScope = { requests: [...requests], dialogs: [...dialogs], message: await page.locator('.ok-msg').innerText(), availableButtons: await page.locator('main button').allTextContents() }
  mode = 'slow-strategies'
  await visit('/strategies', 200)
  report.observations.unloadedStrategy = { saveEnabled: await page.getByRole('button', { name: '保存全局策略', exact: true }).isEnabled(), renderedTopn: await page.getByLabel('topn 行情取样条数数值', { exact: true }).inputValue(), persistedTopn: strategies[0].params.baseline.topn }
  await page.waitForTimeout(1700)
  mode = 'normal'
  await visit('/strategies')
  report.observations.strategyHeight = await metrics()
  report.observations.strategySaveButtonY = await page.getByRole('button', { name: '保存全局策略' }).evaluate(el => Math.round(el.getBoundingClientRect().y))
  const topn = page.getByRole('spinbutton', { name: 'topn 行情取样条数数值', exact: true })
  await topn.fill('35')
  await page.getByRole('link', { name: '库存状态', exact: true }).click()
  await page.getByRole('link', { name: '策略配置', exact: true }).click()
  await page.waitForTimeout(200)
  report.observations.unsavedStrategy = { valueAfterReturn: await topn.inputValue(), dialogs }
  const k1 = page.getByRole('spinbutton', { name: 'k1 短租基线系数数值', exact: true })
  await k1.fill(''); await k1.pressSequentially('0.95', { delay: 80 })
  report.observations.numericTyping = { intended: '0.95', actual: await k1.inputValue() }
  mode = 'slow-save'; requests = []
  await page.getByRole('button', { name: '保存全局策略', exact: true }).dblclick()
  await page.waitForTimeout(100)
  report.observations.duplicateStrategySave = { requests: requests.filter(r => r.method === 'PUT'), disabled: await page.getByRole('button', { name: '保存全局策略', exact: true }).isDisabled() }
  await page.waitForTimeout(1500)
  mode = 'normal'
  await visit('/strategies')
  await page.getByRole('checkbox', { name: '全局真实执行', exact: true }).check()
  await page.getByRole('button', { name: '保存全局策略', exact: true }).click()
  await page.waitForTimeout(200)
  report.observations.saveFeedback = await page.locator('.ok-msg').evaluate(el => ({ y: Math.round(el.getBoundingClientRect().y), scrollY, visibleInViewport: el.getBoundingClientRect().bottom > 0, text: el.textContent }))
  await shot('strategy-save-feedback')
  await visit('/channels')
  await page.getByPlaceholder('PartnerId').fill('synthetic-partner')
  await page.locator('textarea').nth(1).fill('SYNTHETIC INVALID KEY FOR MOCK UI ONLY')
  mode = 'slow-save'; requests = []
  await page.getByRole('button', { name: '保存并验证', exact: true }).dblclick()
  await page.waitForTimeout(100)
  report.observations.duplicateCredentialSave = { requests: requests.filter(r => r.method === 'PUT'), disabled: await page.getByRole('button', { name: '保存并验证', exact: true }).isDisabled() }
  await page.waitForTimeout(1400)
  report.observations.credentialFieldAfterSuccess = await page.locator('textarea').nth(1).inputValue()
  mode = 'normal'
  await visit('/audit')
  report.observations.auditDetail = await page.locator('tbody tr td').last().evaluate(el => ({ clientWidth: el.clientWidth, scrollWidth: el.scrollWidth, title: el.title, expandControls: el.querySelectorAll('button,a,details').length }))
  await visit('/orders')
  report.observations.orderStatusOptions = await page.locator('select').nth(1).locator('option').allTextContents()
  report.observations.buyoutType = await page.locator('tbody tr').nth(3).locator('td').nth(3).innerText()
  await page.getByRole('link', { name: '库存状态', exact: true }).click()
  await page.waitForTimeout(200)
  await page.locator('select').first().selectOption('uu')
  await page.getByPlaceholder('搜索名称…').fill('AK-47')
  await page.waitForTimeout(450)
  await page.reload(); await page.waitForTimeout(250)
  report.observations.filterPersistence = { channelAfterReload: await page.locator('select').first().inputValue(), searchAfterReload: await page.getByPlaceholder('搜索名称…').inputValue() }
  await page.evaluate(() => localStorage.removeItem('ra_token'))
  await visit('/orders')
  await page.getByPlaceholder('密码', { exact: true }).fill('not-a-real-password')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.waitForTimeout(250)
  report.observations.loginReturn = page.url()
  const luminance = hex => {
    const rgb = hex.match(/\w\w/g).map(v => parseInt(v, 16) / 255).map(v => v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4)
    return rgb[0] * .2126 + rgb[1] * .7152 + rgb[2] * .0722
  }
  report.observations.primaryButtonContrast = Math.round((luminance('ffffff') + .05) / (luminance('4f8cff') + .05) * 100) / 100
  assert.equal(report.unexpectedRequests.length, 0, 'unmocked request; evidence isolation incomplete')
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify(report, null, 2) + '\n')
  console.log(JSON.stringify({ pages: report.viewports.length, observations: report.observations, unexpectedRequests: report.unexpectedRequests, consoleErrors: report.consoleErrors }, null, 2))
  await browser.close()
}
main().catch(err => { console.error(err); process.exit(1) })
