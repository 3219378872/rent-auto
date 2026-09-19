// Remediation acceptance: intercept every API request; no backend or platform writes.
// PW_MODULE=/absolute/path/to/playwright UX_FONTCONFIG=/optional/fonts.conf node probe.cjs
// Start the remediated frontend on 127.0.0.1:4179 first.
const { chromium } = require(process.env.PW_MODULE || 'playwright')
const fs = require('node:fs')
const path = require('node:path')
const assert = require('node:assert/strict')
const output = process.env.UX_OUTPUT || __dirname
const origin = 'http://127.0.0.1:4179'
const ts = '2026-09-19T08:00:00Z'
const longName = '★ StatTrak™ M9 Bayonet | Doppler (Factory New) Phase 4'
const names = [
  longName,
  'AK-47 | Redline (Field-Tested)',
  'AWP | Asiimov (Field-Tested)',
  '★ Sport Gloves | Vice (Minimal Wear)',
]
const inventory = Array.from({ length: 64 }, (_, i) => ({
  id: i + 1,
  channel: i % 2 ? 'uu' : 'eco',
  asset_id: `fixture-${i}`,
  hash_name: names[i % names.length],
  market_hash_name: names[i % names.length],
  template_id: (i % 4) + 1,
  mark_price: 1850 + i,
  cost_basis: i % 5 ? 1600 : 0,
  tradable: true,
  status: ['listed', 'in_stock', 'leased', 'missing'][i % 4],
}))
const listings = inventory.map((r, i) => ({
  ...r,
  goods_ref: `listing-${i}`,
  actual_state: i % 4 ? 'active' : 'leased',
  desired_state: 'active',
  rent_price: 3.5,
  long_rent_price: 3.2,
  deposit: 2590,
  max_days: 30,
  factor: 0.95,
  listed_at: ts,
  last_reprice_at: ts,
  last_decision: {
    action: i % 3 ? 'reprice' : 'skip',
    at: ts,
    new_rent: 3.7,
    skip: 'guardrail_conflict',
  },
}))
const orders = inventory.map((r, i) => ({
  ...r,
  order_ref: `ECO-20260919-${String(i).padStart(7, '0')}`,
  order_type: i % 8 === 3 ? 'buyout' : i % 2 ? 'short' : 'long',
  status: [
    'leasing',
    'delivering',
    'arbitrating',
    'bought_out',
    'returning',
    'breach',
    'done',
    'unknown',
  ][i % 8],
  rent_days: 7,
  rent_price: 3.5,
  order_amount: 24.5,
  deposits: 2590,
  started_at: '2026-09-12T08:00:00Z',
  due_at: ts,
  finished_at: null,
}))
const templates = names.map((hash_name, i) => ({
  hash_name,
  display_name: [
    'M9 刺刀 · 多普勒（崭新出厂）',
    'AK-47 · 红线（久经沙场）',
    'AWP · 二西莫夫（久经沙场）',
    '运动手套 · 迈阿密风云（略有磨损）',
  ][i],
  category: ['knife', 'rifle', 'sniper', 'gloves'][i],
  blacklisted: false,
  value_anchor: 1850,
  uu_template_id: 100 + i,
  uu_mark_price: 1840,
  eco_ref_price: 1860,
}))
const strategies = [
  {
    id: 1,
    name: 'default',
    scope: 'global',
    channel_route: 'eco_only',
    params: { baseline: { topn: 20 } },
    real_execution_enabled: false,
    priority: 0,
    updated_at: ts,
  },
  {
    id: 2,
    name: `tpl:${names[1]}`,
    scope: 'template',
    channel_route: 'eco_only',
    params: { baseline: { k1: 0.9 } },
    real_execution_enabled: true,
    priority: 5,
    updated_at: ts,
  },
]
const audits = [
  {
    ts,
    actor: 'system',
    action: 'reprice',
    channel: 'eco',
    target: 'fixture-1',
    detail: {
      listing_id: 2,
      dry_run: true,
      reason:
        '这是模拟的长审计详情，用来检查能否完整阅读以及区分模拟与真实执行。'.repeat(
          7,
        ),
      old_rent: 3.5,
      new_rent: 3.7,
    },
  },
]
const dashboard = {
  assets: {
    total: 158600,
    inventory: 110000,
    deposits: { uu: 10000, eco: 20000 },
    wallets: { uu: 7200, eco: 11400 },
  },
  income: {
    total: 3150,
    today: 68.5,
    by_channel: [
      { channel: 'uu', income: 1550, orders: 44 },
      { channel: 'eco', income: 1800, orders: 59 },
    ],
  },
  leased_out: 14,
  annualized_roi: 0.1845,
  categories: [{ category: 'knife', cost: 78000, income: 2240, yield: 0.0287 }],
  series_30d: Array.from({ length: 30 }, (_, i) => ({
    date: new Date(Date.UTC(2026, 7, 21 + i)).toISOString().slice(0, 10),
    income: 25 + i * 1.4 + Math.sin(i) * 15,
  })),
}

Object.assign(dashboard, {
  roi_available: true,
  observation_started_at: '2026-08-20T00:00:00Z',
  observation_days: 30,
  costed_items: 50,
  inventory_items: 64,
  cost_total: 110000,
  sold_cost: 200,
})
dashboard.series_30d.forEach((p, i) => (p.orders = i % 7))
inventory.forEach((r, i) =>
  Object.assign(r, {
    category: templates[i % 4].category,
    cost_source: 'manual',
    cost_modified_at: ts,
    last_synced_at: ts,
  }),
)
listings.forEach((r, i) =>
  Object.assign(r.last_decision, {
    id: i + 1,
    dry_run: i % 2 === 0,
    success: i % 4 !== 1,
  }),
)
const priceActions = listings.map((r, i) => ({
  id: i + 1,
  ts,
  channel: r.channel,
  hash_name: r.hash_name,
  asset_id: r.asset_id,
  listing_id: r.id,
  action: i % 3 ? 'reprice' : 'skip',
  dry_run: i % 2 === 0,
  success: i % 4 !== 1,
  error: i % 4 === 1 ? '执行失败，请检查渠道状态或服务端日志' : '',
  old_rent: 3.5,
  new_rent: 3.7,
  old_long: null,
  new_long: 3.3,
  old_deposit: 2000,
  new_deposit: 2500,
  old_days: 30,
  new_days: 30,
  decision: {
    skip: i % 3 ? '' : 'guardrail_conflict',
    factor: 0.95,
    token: '[已隐藏敏感或内部信息]',
  },
}))
audits[0].detail.reason =
  '公开审计详情完整性验证。'.repeat(450) + 'END-OF-PUBLIC-DETAIL'
const report = {
  evidence:
    'Chromium with intercepted synthetic APIs; no live platform writes or physical-device claims',
  sourceHead: require('node:child_process')
    .execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' })
    .trim(),
  checks: {},
  viewports: [],
  pageErrors: [],
  unexpected: [],
}
let mode = 'normal',
  requests = [],
  acceptDialog = false
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
async function main() {
  fs.mkdirSync(output, { recursive: true })
  const browser = await chromium.launch({
    headless: true,
    env: {
      ...process.env,
      ...(process.env.UX_FONTCONFIG
        ? { FONTCONFIG_FILE: process.env.UX_FONTCONFIG }
        : {}),
    },
  })
  report.browser = browser.version()
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    permissions: ['clipboard-read', 'clipboard-write'],
    acceptDownloads: true,
  })
  await context.route('**/*', async (route) => {
    const req = route.request(),
      url = new URL(req.url()),
      p = url.pathname.replace('/api/v1', '')
    if (url.origin !== origin) {
      report.unexpected.push(req.url())
      return route.abort()
    }
    if (!url.pathname.startsWith('/api/')) return route.continue()
    requests.push({
      path: p,
      query: url.search,
      method: req.method(),
      body: req.postData(),
    })
    const json = (body, status = 200) =>
      route.fulfill({
        status,
        contentType: 'application/json',
        body: JSON.stringify(body),
      })
    if (mode === 'slow-all' && req.method() === 'GET') await delay(2000)
    if (
      mode === 'slow-filter' &&
      p === '/inventory' &&
      url.searchParams.get('channel') === 'uu'
    )
      await delay(1200)
    if (
      (mode === 'health-error' && p === '/channels') ||
      (mode === 'mode-error' && p === '/execution/status')
    )
      return json({ message: '状态暂不可用' }, 503)
    if (
      mode === 'read-error' &&
      req.method() === 'GET' &&
      !['/execution/status', '/jobs', '/inventory/categories'].includes(p)
    )
      return json({ message: '网络连接失败' }, 503)
    if (req.method() !== 'GET') {
      if (mode === 'slow-save') await delay(800)
      if (mode === 'save-error')
        return json({ message: '保存失败，请重试' }, 500)
      const body = req.postData() ? JSON.parse(req.postData()) : {}
      if (p === '/auth/login') return json({ token: 'ux-fixture-session' })
      if (p === '/strategies/global')
        Object.assign(strategies[0], {
          params: body.params,
          channel_route: body.channel_route,
          real_execution_enabled: body.real_execution_enabled,
        })
      if (p === '/strategies/template') {
        const idx = strategies.findIndex(
          (r) => r.name === `tpl:${body.hash_name}`,
        )
        const r = {
          id: idx < 0 ? 99 : strategies[idx].id,
          name: `tpl:${body.hash_name}`,
          scope: 'template',
          updated_at: ts,
          ...body,
        }
        if (idx < 0) strategies.push(r)
        else strategies[idx] = r
      }
      if (p.endsWith('/cost')) {
        const asset = decodeURIComponent(p.split('/')[3])
        inventory
          .filter((r) => r.asset_id === asset)
          .forEach((r) => (r.cost_basis = body.cost))
      }
      return json({ status: 'ok', fingerprint: 'abcd1234abcd' })
    }
    if (p === '/execution/status')
      return json({
        dry_run: true,
        reasons: ['environment_dry_run'],
        environment_dry_run: true,
        global_real_enabled: strategies[0].real_execution_enabled,
        effective_real_enabled: true,
        hash_name: url.searchParams.get('hash_name') || '',
        channel_route: 'eco_only',
        checked_at: ts,
      })
    if (p === '/dashboard') return json(dashboard)
    if (p === '/channels')
      return json({
        uu: 'error:凭证已过期',
        eco: 'ok',
        steam: 'not_configured',
      })
    if (p === '/strategies') return json(strategies)
    if (p === '/inventory/categories')
      return json(['knife', 'rifle', 'sniper', 'gloves'])
    if (p === '/jobs')
      return json([
        {
          name: 'reprice',
          running: false,
          last_ok: false,
          last_error: '任务失败，请检查渠道状态',
          last_run: ts,
          next_run: ts,
        },
      ])
    let rows = {
      '/inventory': inventory,
      '/listings': listings,
      '/orders': orders,
      '/audit': audits,
      '/price-actions': priceActions,
      '/templates/catalog': templates,
    }[p]
    if (rows) {
      if (mode === 'empty') rows = []
      for (const key of [
        'channel',
        'status',
        'state',
        'hash_name',
        'asset_id',
        'order_type',
        'id',
        'listing_id',
        'category',
      ]) {
        const value = url.searchParams.get(key)
        if (value)
          rows = rows.filter(
            (r) => String(r[key === 'state' ? 'actual_state' : key]) === value,
          )
      }
      if (url.searchParams.get('search'))
        rows = rows.filter((r) =>
          [r.hash_name, r.asset_id, r.order_ref, r.display_name].some((v) =>
            v
              ?.toLowerCase()
              .includes(url.searchParams.get('search').toLowerCase()),
          ),
        )
      if (url.searchParams.has('blacklisted'))
        rows = rows.filter(
          (r) =>
            r.blacklisted === (url.searchParams.get('blacklisted') === 'true'),
        )
      if (url.searchParams.get('mode'))
        rows = rows.filter(
          (r) => r.dry_run === (url.searchParams.get('mode') === 'dry_run'),
        )
      if (url.searchParams.get('result') === 'failed')
        rows = rows.filter((r) => !r.success)
      const n = Number(url.searchParams.get('page') || 1),
        size = Number(url.searchParams.get('page_size') || 50)
      return json({
        items: rows.slice((n - 1) * size, n * size),
        total: rows.length,
      })
    }
    report.unexpected.push(req.url())
    return json({ message: 'unmocked' }, 501)
  })
  const page = await context.newPage()
  page.on('pageerror', (err) => report.pageErrors.push(err.message))
  page.on('dialog', (d) => (acceptDialog ? d.accept() : d.dismiss()))
  page.setDefaultTimeout(10000)
  await page.goto(origin)
  await page.evaluate(() =>
    localStorage.setItem('ra_token', 'ux-fixture-session'),
  )
  const visit = async (route, wait = 150) => {
    await page.goto('about:blank')
    await page.goto(`${origin}/#${route}`)
    await page.waitForTimeout(wait)
  }
  const shot = (name) =>
    page.screenshot({ path: path.join(output, `${name}.png`) })
  const check = (name, value) => {
    report.checks[name] = value
    assert.ok(value, name)
  }
  try {
    for (const width of [1440, 1280, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      for (const r of [
        '/login',
        '/',
        '/inventory',
        '/listings',
        '/orders',
        '/strategies',
        '/channels',
        '/audit',
      ]) {
        await visit(r)
        await shot(`${width}-${r === '/' ? 'dashboard' : r.slice(1)}`)
        const metrics = await page.evaluate(() => ({
          width: innerWidth,
          documentWidth: document.documentElement.scrollWidth,
          unlabelled: [
            ...document.querySelectorAll('input,select,textarea'),
          ].filter(
            (e) =>
              !e.labels?.length &&
              !e.getAttribute('aria-label') &&
              !e.getAttribute('aria-labelledby'),
          ).length,
          tableScrolls: [...document.querySelectorAll('.table-scroll')].map(
            (e) => ({ client: e.clientWidth, scroll: e.scrollWidth }),
          ),
        }))
        report.viewports.push({ route: r, ...metrics })
        check(
          `no-document-overflow-${width}-${r}`,
          metrics.documentWidth <= width + 1,
        )
        check(`labels-${width}-${r}`, metrics.unlabelled === 0)
      }
    }
    await page.setViewportSize({ width: 1440, height: 900 })
    mode = 'slow-all'
    for (const r of [
      '/inventory',
      '/listings',
      '/orders',
      '/audit',
      '/strategies',
      '/channels',
      '/',
    ]) {
      await visit(r, 150)
      const text = await page.locator('main').innerText()
      check(`loading-${r}`, /加载/.test(text) && !/共 0/.test(text))
      if (r === '/strategies')
        check(
          'cannot-save-unloaded',
          (await page
            .getByRole('button', { name: '保存全局策略', exact: true })
            .count()) === 0,
        )
      await page.waitForTimeout(2000)
    }
    mode = 'normal'
    await visit('/inventory')
    mode = 'slow-filter'
    await page.getByLabel('渠道', { exact: true }).selectOption('uu')
    await page.waitForTimeout(100)
    check(
      'no-unmarked-old-filter-rows',
      (await page.locator('tbody tr').count()) === 0,
    )
    await page.getByLabel('渠道', { exact: true }).selectOption('eco')
    await page.waitForTimeout(1400)
    check(
      'stale-request-cannot-overwrite',
      await page
        .locator('tbody tr td')
        .first()
        .innerText()
        .then((v) => v.startsWith('ECO')),
    )
    mode = 'read-error'
    await visit('/inventory')
    check(
      'read-error-retry',
      await page.getByRole('button', { name: '重试', exact: true }).isVisible(),
    )
    mode = 'normal'
    await page.getByRole('button', { name: '重试', exact: true }).click()
    await page.locator('tbody tr').first().waitFor()
    mode = 'empty'
    await visit('/orders')
    check(
      'explicit-empty',
      await page
        .getByText('暂无数据，完成渠道配置并等待同步后可在此查看。')
        .isVisible(),
    )
    mode = 'health-error'
    await visit('/')
    check(
      'health-failure-is-unknown',
      await page.getByText('渠道健康未知，无法确认账号可用').isVisible(),
    )
    await shot('dashboard-health-failure')
    mode = 'mode-error'
    await visit('/listings')
    check(
      'unknown-execution-mode',
      await page.getByText('执行模式未知', { exact: true }).isVisible(),
    )
    mode = 'normal'
    await visit('/listings')
    await page.getByLabel('渠道', { exact: true }).selectOption('eco')
    await page.getByRole('button', { name: '运行全局重定价' }).click()
    const modal = page.getByRole('dialog')
    check(
      'global-scope-visible',
      await modal
        .innerText()
        .then(
          (t) => t.includes('所有符合策略的 UU / ECO') && t.includes('ECO'),
        ),
    )
    await shot('global-reprice-confirmation')
    requests = []
    await modal.getByRole('button', { name: '确认全局运行' }).click()
    await page.getByText(/任务已受理，请查看/).waitFor()
    check(
      'global-request-has-no-filter',
      requests.filter((r) => r.method === 'POST').length === 1 &&
        requests.find((r) => r.method === 'POST').query === '',
    )
    await visit('/strategies')
    const field = (name) => page.getByRole('spinbutton', { name, exact: true })
    const k1 = field('k1 短租基线系数数值')
    for (const [name, value] of [
      ['k1 短租基线系数数值', '0.95'],
      ['k2 长租基线系数数值', '1.02'],
      ['min_rent 租金下限数值', '0.5'],
    ]) {
      const input = field(name)
      await input.fill('')
      await input.pressSequentially(value, { delay: 60 })
      await input.press('Tab')
      check(`typed-${value}`, (await input.inputValue()) === value)
    }
    await k1.fill('')
    await k1.press('Tab')
    requests = []
    await page
      .getByRole('button', { name: '保存全局策略', exact: true })
      .click()
    check(
      'invalid-value-no-request',
      requests.filter((r) => r.method === 'PUT').length === 0,
    )
    await k1.fill('0.95')
    await page.getByRole('link', { name: '库存状态', exact: true }).click()
    check(
      'cancel-navigation-keeps-draft',
      page.url().includes('/strategies') && (await k1.inputValue()) === '0.95',
    )
    const save = page.getByRole('button', { name: '保存全局策略', exact: true })
    mode = 'slow-save'
    requests = []
    await save.dblclick()
    await page.getByText('策略已保存并回读确认').waitFor()
    const writes = requests.filter(
      (r) => r.method === 'PUT' && r.path === '/strategies/global',
    )
    check('one-strategy-request', writes.length === 1)
    check(
      'saved-keyboard-values',
      JSON.parse(writes[0].body).params.baseline.k1 === 0.95 &&
        JSON.parse(writes[0].body).params.baseline.k2 === 1.02 &&
        JSON.parse(writes[0].body).params.guardrails.min_rent === 0.5,
    )
    check(
      'feedback-in-viewport',
      await page.getByText('策略已保存并回读确认').evaluate((e) => {
        const r = e.getBoundingClientRect()
        return r.top >= 0 && r.bottom <= innerHeight
      }),
    )
    await shot('strategy-save-confirmed')
    mode = 'save-error'
    await k1.fill('1.01')
    await save.click()
    await page.getByText('保存失败，请重试').waitFor()
    check('failure-preserves-draft', (await k1.inputValue()) === '1.01')
    mode = 'normal'
    await page.getByRole('button', { name: '放弃更改', exact: true }).click()
    await page.getByRole('button', { name: '模板覆盖', exact: true }).click()
    await page.getByRole('button', { name: '编辑', exact: true }).click()
    check(
      'template-provenance',
      (await page.getByText(/继承全局/).count()) >= 18,
    )
    await page.getByRole('button', { name: '恢复继承 k1 短租基线系数' }).click()
    requests = []
    await page.getByRole('button', { name: '保存模板策略' }).click()
    await page.getByText('模板策略已保存', { exact: true }).waitFor()
    const tpl = JSON.parse(
      requests.find(
        (r) => r.method === 'POST' && r.path === '/strategies/template',
      ).body,
    )
    check(
      'restore-inheritance-preserves-settings',
      Object.keys(tpl.params).length === 0 &&
        tpl.priority === 5 &&
        tpl.real_execution_enabled === true &&
        tpl.channel_route === 'eco_only',
    )
    await visit('/audit')
    const trigger = page.getByRole('button', { name: '查看详情' }).first()
    await trigger.focus()
    await trigger.press('Enter')
    const detail = page.getByRole('dialog')
    check(
      'full-long-detail',
      await detail
        .locator('pre')
        .innerText()
        .then((t) => t.includes('END-OF-PUBLIC-DETAIL') && t.length > 4000),
    )
    await detail.getByRole('button', { name: '复制详情' }).click()
    check(
      'copy-full-detail',
      (await page.evaluate(() => navigator.clipboard.readText())).includes(
        'END-OF-PUBLIC-DETAIL',
      ),
    )
    await page.keyboard.press('Escape')
    check(
      'detail-focus-restored',
      await trigger.evaluate((e) => e === document.activeElement),
    )
    await page.getByLabel('起始时间', { exact: true }).fill('2026-09-20T10:00')
    await page.getByLabel('结束时间', { exact: true }).fill('2026-09-19T10:00')
    check(
      'invalid-range-visible',
      await page.getByText('结束时间必须晚于起始时间').isVisible(),
    )
    await visit('/audit?tab=pricing')
    await page.getByRole('button', { name: '查看详情' }).first().click()
    check(
      'historical-unknown-not-rebuilt',
      await page
        .getByRole('dialog')
        .getByText('策略版本：未记录 · 行情快照：未记录')
        .isVisible(),
    )
    await shot('pricing-detail')
    await visit('/orders')
    check(
      'buyout-labelled',
      (await page.getByText('买断', { exact: true }).count()) > 0,
    )
    const downloadPromise = page.waitForEvent('download')
    await page.getByRole('button', { name: '导出 CSV' }).click()
    const download = await downloadPromise
    const csv = fs.readFileSync(await download.path(), 'utf8')
    check('csv-all-64-orders', csv.trim().split('\n').length === 65)
    await visit('/inventory?channel=eco&search=AK-47')
    await page.reload()
    await page.waitForTimeout(200)
    check(
      'filter-url-survives-reload',
      (await page.getByLabel('渠道', { exact: true }).inputValue()) === 'eco' &&
        (await page
          .getByLabel('名称 / 资产 ID', { exact: true })
          .inputValue()) === 'AK-47',
    )
    await visit('/inventory?page=2')
    check(
      'page-url-restored',
      await page.getByText('第 2 页', { exact: true }).isVisible(),
    )
    await page.getByRole('button', { name: '上一页' }).click()
    await page.goBack()
    await page.getByText('第 2 页', { exact: true }).waitFor()
    check('browser-history-paging', page.url().includes('page=2'))
    await visit('/channels')
    await page.getByText('ECOSteam · ECO', { exact: true }).click()
    await page.getByLabel('PartnerId', { exact: true }).fill('fixture-partner')
    await page.getByLabel('ECO 私钥', { exact: true }).fill('SYNTHETIC-KEY')
    requests = []
    mode = 'slow-save'
    await page
      .getByRole('button', { name: '保存并验证', exact: true })
      .dblclick()
    await page.getByText('ECO 凭证已加密保存并验证').waitFor()
    check(
      'one-credential-write',
      requests.filter((r) => r.method === 'PUT').length === 1,
    )
    check(
      'clear-secret-and-show-fingerprint',
      (await page.getByLabel('ECO 私钥', { exact: true }).inputValue()) ===
        '' &&
        (await page.getByText('已保存私钥指纹：abcd1234abcd').isVisible()),
    )
    mode = 'normal'
    await page.setViewportSize({ width: 390, height: 844 })
    await shot('390-eco-config')
    check(
      'narrow-expanded-form-contained',
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    )
    await page.getByRole('button', { name: '导航', exact: true }).click()
    check(
      'narrow-nav-can-expand',
      await page
        .getByRole('link', { name: '租赁订单', exact: true })
        .isVisible(),
    )
    await page.evaluate(() => localStorage.removeItem('ra_token'))
    await visit('/orders?status=delivering')
    await page.getByLabel('密码', { exact: true }).fill('fixture-password')
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await page.waitForURL('**/#/orders?status=delivering')
    check(
      'login-returns-to-filtered-route',
      page.url().endsWith('/#/orders?status=delivering'),
    )
    const lum = (hex) =>
      hex
        .match(/\w\w/g)
        .map((v) => parseInt(v, 16) / 255)
        .map((v) => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
        .reduce((s, v, i) => s + v * [0.2126, 0.7152, 0.0722][i], 0)
    report.primaryContrast = (lum('ffffff') + 0.05) / (lum('3265c7') + 0.05)
    check('primary-contrast-aa', report.primaryContrast >= 4.5)
    check('no-page-errors', report.pageErrors.length === 0)
    check('network-isolated', report.unexpected.length === 0)
  } finally {
    fs.writeFileSync(
      path.join(output, 'browser-results.json'),
      JSON.stringify(report, null, 2) + '\n',
    )
    await browser.close()
  }
  console.log(
    `Passed ${Object.keys(report.checks).length} browser checks; ${report.viewports.length} viewport/page captures`,
  )
}
main().catch((err) => {
  console.error(err)
  process.exitCode = 1
})
