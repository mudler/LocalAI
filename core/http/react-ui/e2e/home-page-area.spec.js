import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'

// The page area of Home, against the real test server: the pageArea of
// home.json runs a page below the composer, under a strip the shell draws;
// the page updates live and cannot cover the shell; a page that fails shows
// the failure tile in the area only (Revert, Open the file, Remove from
// Home); an administrator can switch the area off; the first pinned page is
// the start page. PAGES_SHOTS names a folder for screenshots.

test.describe.configure({ mode: 'serial', timeout: 90_000 })
holdHomeFiles(test)

const stamp = Date.now().toString(36)
const TODAY = `atoday-${stamp}`
const BAD = `abad-${stamp}`
const SLOW = `aslow-${stamp}`
const COVER = `acover-${stamp}`
const GONE = `agone-${stamp}`
const SA = `asa-${stamp}`
const SB = `asb-${stamp}`
const TILE = `atile-${stamp}`
const TILE2 = `atile2-${stamp}`
const TILE3 = `atile3-${stamp}`
const SHORT = `ashort-${stamp}`
const TALL = `atall-${stamp}`
const GROW = `agrow-${stamp}`
const SPAM = `aspam-${stamp}`
const shots = process.env.PAGES_SHOTS || ''

const put = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
const del = (request, path) => request.delete(`/api/chats/files?path=${encodeURIComponent(path)}`)
const settle = (page) => page.evaluate(() => Promise.all(document.getAnimations().filter(a => a.effect?.getTiming().iterations !== Infinity).map(a => a.finished.catch(() => {}))))
const shot = async (page, name) => {
  if (!shots) return
  await settle(page)
  await page.waitForTimeout(300)
  await page.screenshot({ path: `${shots}/${name}.png` })
}

const STYLE = `<style>
  :root { --bg: #f6efe2; --text: #2b2418; --card: #fdf8ee; --line: #e5d9bd; }
  :root[data-theme="dark"] { --bg: #1d1a14; --text: #efe6d2; --card: #272319; --line: #3b3526; }
  body { margin: 0; padding: 24px; background: var(--bg); color: var(--text); font: 15px/1.5 Georgia, serif; }
  .card { background: var(--card); border: 1px solid var(--line); border-radius: 14px; padding: 16px 20px; max-width: 520px; }
  h1 { font-size: 22px; margin: 0 0 8px; } ul { list-style: none; padding: 0; margin: 0; } li { padding: 6px 0; border-top: 1px solid var(--line); }
</style>`
const todayHtml = (word) => `<!doctype html><html><head>${STYLE}</head><body>
<div class="card"><h1>Today, Tuesday</h1><p id="word">${word}</p><p id="mode"></p><ul id="list"></ul></div>
<script>
document.getElementById('mode').textContent = localai.page.mode + '|' + localai.page.size + '|' + document.documentElement.getAttribute('data-mode')
const draw = () => fetch('items.json').then(r => r.json()).then(d => { document.getElementById('list').innerHTML = d.items.map(i => '<li>' + i + '</li>').join('') })
draw()
localai.on('files.changed', draw)
</script></body></html>`
const BAD_HTML = `<!doctype html><html><head>${STYLE}</head><body><p>nothing here</p>
<script>
throw new Error('items is not defined')
</script></body></html>`
// Blocks the thread for longer than the 5 seconds of the first paint.
const SLOW_HTML = `<!doctype html><html><body><p>slow</p><script>const t = Date.now(); while (Date.now() - t < 7500) {}</script></body></html>`
const COVER_HTML = `<!doctype html><html><body><div style="position:fixed;top:-2000px;left:-2000px;right:-2000px;bottom:-2000px;background:#c00;z-index:2147483647">covering</div></body></html>`
const TILE_HTML = `<!doctype html><html><body><p id="m"></p><script>document.getElementById('m').textContent = localai.page.mode + ' ' + localai.page.size</script></body></html>`

// Pages that report their height (localai.page.resize). The token is drawn
// once per load of the frame: the same token later means no reload.
const sized = (body, script) => `<!doctype html><html><head>${STYLE}</head><body>${body}<p id="token"></p>
<script>
document.getElementById('token').textContent = Math.random().toString(36).slice(2)
${script}
</script></body></html>`
const SHORT_HTML = sized('<div class="card"><h1>Short page</h1><div style="height:220px">Two lines of news.</div></div>', 'localai.page.resize()')
const TALL_HTML = sized('<div class="card"><h1>Tall page</h1><div style="height:2000px">A long list.</div></div>', 'localai.page.resize()')
const GROW_HTML = sized('<div class="card"><h1>Growing page</h1><div id="box" style="height:200px"></div></div>',
  "new ResizeObserver(() => localai.page.resize()).observe(document.body)\nsetTimeout(() => { document.getElementById('box').style.height = '300px' }, 1500)")
// A burst of a thousand heights, once the host watches, then 420.
const SPAM_HTML = sized('<div class="card"><h1>Noisy page</h1></div>',
  "setTimeout(() => { for (let i = 0; i < 1000; i++) localai.page.resize(160 + (i * 37) % 900); localai.page.resize(420) }, 1500)")

const area = (page) => page.getByTestId('page-area')
const areaFrame = (page) => area(page).frameLocator('[data-testid="page-frame"]')
const homeJson = (request) => request.get('/api/chats/wellknown/home').then(r => r.json())
const setHome = async (request, body) => { expect((await put(request, '/LocalAI/home.json', body)).ok()).toBeTruthy() }
const chatsWidget = { id: 'chats', type: 'chats', title: 'Last chats' }

async function home(page, theme = 'light') {
  await page.route('**/api/chats/triggers*', (r) => r.fulfill({ json: { triggers: [] } }))
  await page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private mode */ } }, theme)
  await page.goto('/app')
}

test.beforeAll(async ({ request }) => {
  const files = {
    [`/pages/${TODAY}/index.html`]: todayHtml('one'),
    [`/pages/${TODAY}/page.json`]: { title: 'Today', icon: 'calendar' },
    [`/pages/${TODAY}/items.json`]: { items: ['Call the plumber', 'Draft the invoice'] },
    [`/pages/${SLOW}/index.html`]: SLOW_HTML,
    [`/pages/${SLOW}/page.json`]: { title: 'Slow page' },
    [`/pages/${COVER}/index.html`]: COVER_HTML,
    [`/pages/${COVER}/page.json`]: { title: 'Cover page' },
    [`/pages/${SA}/index.html`]: '<!doctype html><h1>Task board</h1>',
    [`/pages/${SA}/page.json`]: { title: 'Task board' },
    [`/pages/${SB}/index.html`]: '<!doctype html><h1>Reading list</h1>',
    [`/pages/${SB}/page.json`]: { title: 'Reading list' },
    [`/pages/${TILE}/index.html`]: TILE_HTML,
    [`/pages/${TILE}/page.json`]: { title: 'Tile', widget: { size: 'small', compact: { width: 120 } } },
    [`/pages/${TILE2}/index.html`]: TILE_HTML,
    [`/pages/${TILE2}/page.json`]: { title: 'Tile two', widget: { size: 'small', compact: { width: 120 } } },
    [`/pages/${TILE3}/index.html`]: TILE_HTML,
    [`/pages/${TILE3}/page.json`]: { title: 'Tile three', widget: { size: 'small', compact: { width: 120 } } },
    [`/pages/${SHORT}/index.html`]: SHORT_HTML,
    [`/pages/${SHORT}/page.json`]: { title: 'Short page' },
    [`/pages/${TALL}/index.html`]: TALL_HTML,
    [`/pages/${TALL}/page.json`]: { title: 'Tall page' },
    [`/pages/${GROW}/index.html`]: GROW_HTML,
    [`/pages/${GROW}/page.json`]: { title: 'Growing page' },
    [`/pages/${SPAM}/index.html`]: SPAM_HTML,
    [`/pages/${SPAM}/page.json`]: { title: 'Noisy page' },
  }
  for (const [p, body] of Object.entries(files)) expect((await put(request, p, body)).ok(), p).toBeTruthy()
})
test.afterAll(async ({ request }) => {
  await request.post('/api/settings', { data: { home_page_area_enabled: true } })
  for (const p of ['/LocalAI/home.json', '/LocalAI/ui.json', ...[TODAY, BAD, SLOW, COVER, GONE, SA, SB, TILE, TILE2, TILE3, SHORT, TALL, GROW, SPAM].map((n) => `/pages/${n}`)]) await del(request, p)
})
test.afterAll(() => unlockHomeFiles())

test('the page area runs the page in mode area, between the composer and the widgets, under the strip', async ({ page, request }) => {
  await setHome(request, { pageArea: TODAY, widgets: [chatsWidget] })
  await home(page)
  await expect(area(page)).toBeVisible()
  await expect(area(page)).toHaveAttribute('data-frame', 'live')
  const f = areaFrame(page)
  await expect(f.locator('#mode')).toHaveText('area||area')
  await expect(f.locator('li')).toHaveCount(2)
  await expect(page.getByTestId('page-area-title')).toHaveText('Today')
  await expect(page.getByTestId('page-area-origin')).toContainText(/Page by/)
  await expect(page.getByTestId('page-area-runs-as-you')).toHaveText('Runs as you')
  await expect(page.getByTestId('page-area-reload')).toBeVisible()
  await expect(page.getByTestId('page-area-edit')).toBeVisible()
  const composer = await page.getByTestId('home-composer').boundingBox()
  const bar = await page.getByTestId('page-area-bar').boundingBox()
  const body = await area(page).locator('.wk-area__body').boundingBox()
  const grid = await page.getByTestId('home-widget-grid').boundingBox()
  expect(bar.y).toBeGreaterThanOrEqual(composer.y + composer.height)
  expect(body.y).toBeGreaterThanOrEqual(bar.y + bar.height - 1)
  expect(grid.y).toBeGreaterThanOrEqual(body.y + body.height)
  const bb = await area(page).boundingBox()
  const main = await page.getByTestId('home-widgets').boundingBox()
  expect(bb.width).toBeGreaterThan(main.width * 0.95)
  await shot(page, 'area-light')
})

test('the page area follows its files live, without a reload of the frame for a data file', async ({ page, request }) => {
  await setHome(request, { pageArea: TODAY, widgets: [chatsWidget] })
  await home(page)
  const f = areaFrame(page)
  await expect(f.locator('li')).toHaveCount(2)
  await f.locator('#word').evaluate((el) => { el.dataset.kept = 'yes' })
  expect((await put(request, `/pages/${TODAY}/items.json`, { items: ['Call the plumber', 'Draft the invoice', 'Order hinges'] })).ok()).toBeTruthy()
  await expect(f.locator('li')).toHaveCount(3)
  await expect(f.locator('#word')).toHaveAttribute('data-kept', 'yes')
  expect((await put(request, `/pages/${TODAY}/index.html`, todayHtml('two'))).ok()).toBeTruthy()
  await expect(f.locator('#word')).toHaveText('two')
  expect((await put(request, `/pages/${TODAY}/index.html`, todayHtml('one'))).ok()).toBeTruthy()
  await expect(f.locator('#word')).toHaveText('one')
  // The pageArea itself follows the change feed.
  await setHome(request, { widgets: [chatsWidget] })
  await expect(area(page)).toHaveCount(0)
  await setHome(request, { pageArea: TODAY, widgets: [chatsWidget] })
  await expect(area(page)).toBeVisible()
})

test('the composer stays usable and a page cannot cover it', async ({ page, request }) => {
  await setHome(request, { pageArea: COVER, widgets: [chatsWidget] })
  await home(page)
  await expect(area(page)).toHaveAttribute('data-frame', 'live')
  await expect(areaFrame(page).getByText('covering')).toBeVisible()
  const box = page.getByTestId('home-input')
  await box.fill('hello from the shell')
  await expect(box).toHaveValue('hello from the shell')
  const hit = await page.evaluate(() => {
    const el = document.querySelector('[data-testid="home-input"]')
    const r = el.getBoundingClientRect()
    const top = document.elementFromPoint(r.x + 12, r.y + 8)
    const bar = document.querySelector('[data-testid="page-area-bar"]').getBoundingClientRect()
    const barTop = document.elementFromPoint(bar.x + 8, bar.y + bar.height / 2)
    return { composer: !!top && !!top.closest('[data-testid="home-composer"]'), strip: !!barTop && !!barTop.closest('[data-testid="page-area-bar"]') }
  })
  expect(hit).toEqual({ composer: true, strip: true })
  // The frame is clipped to the area: it does not grow past its body.
  const body = await area(page).locator('.wk-area__body').boundingBox()
  const fr = await area(page).locator('iframe').boundingBox()
  expect(fr.height).toBeLessThanOrEqual(body.height + 1)
  expect(fr.y).toBeGreaterThanOrEqual(body.y - 1)
})

test('a page that throws shows the failure tile in the area only; Revert restores the last good version', async ({ page, request }) => {
  await put(request, `/pages/${BAD}/index.html`, todayHtml('good'))
  await put(request, `/pages/${BAD}/page.json`, { title: 'Bad page' })
  await put(request, `/pages/${BAD}/items.json`, { items: [] })
  await setHome(request, { pageArea: BAD, widgets: [chatsWidget] })
  await home(page)
  await expect(areaFrame(page).locator('#word')).toHaveText('good')
  // The assistant breaks the page; the area starts it again and fails.
  expect((await put(request, `/pages/${BAD}/index.html`, BAD_HTML)).ok()).toBeTruthy()
  const failed = page.getByTestId('page-area-failed')
  await expect(failed).toBeVisible({ timeout: 20_000 })
  await expect(failed).toHaveAttribute('data-reason', 'script')
  await expect(failed).toContainText('This page did not load.')
  await expect(page.getByTestId('page-area-why')).toContainText('raised an error')
  await expect(page.getByTestId('page-area-revert')).toBeVisible()
  await expect(page.getByTestId('page-area-open-file')).toHaveAttribute('href', new RegExp(`/app/files\\?path=%2Fpages%2F${BAD}`))
  await expect(page.getByTestId('page-area-remove')).toBeVisible()
  // The composer and the widgets keep working.
  await page.getByTestId('home-input').fill('still here')
  await expect(page.getByTestId('home-input')).toHaveValue('still here')
  await expect(page.getByTestId('home-widget-chats')).toBeVisible()
  await shot(page, 'area-failed-light')
  await page.getByTestId('page-area-revert').click()
  await expect(failed).toHaveCount(0)
  await expect(areaFrame(page).locator('#word')).toHaveText('good', { timeout: 20_000 })
  const back = await (await request.get(`/api/chats/pages/${BAD}`)).json()
  expect(back.html).toContain('good')
})

test('Remove from Home writes pageArea null and keeps the other keys', async ({ page, request }) => {
  await put(request, `/pages/${BAD}/index.html`, BAD_HTML)
  await setHome(request, { pageArea: BAD, headline: 'Hello {user.name}', widgets: [chatsWidget] })
  await home(page)
  await expect(page.getByTestId('page-area-failed')).toBeVisible({ timeout: 20_000 })
  await page.getByTestId('page-area-remove').click()
  await expect(area(page)).toHaveCount(0)
  await expect(page.getByTestId('page-area-failed')).toHaveCount(0)
  const doc = await homeJson(request)
  expect(doc.data.pageArea ?? null).toBeNull() // the server merge drops a null key
  expect(doc.data.headline).toBe('Hello {user.name}')
  expect(doc.data.widgets).toHaveLength(1)
  // A change set, so the assistant can see it and the user can undo it.
  const h = await (await request.get('/api/chats/files/history?path=/LocalAI/home.json')).json()
  expect(h.entries.length).toBeGreaterThan(1)
})

test('no first paint within 5 seconds shows the failure tile, and Reload is manual', async ({ page, request }) => {
  test.setTimeout(120_000)
  await setHome(request, { pageArea: SLOW, widgets: [chatsWidget] })
  await home(page)
  const failed = page.getByTestId('page-area-failed')
  await expect(failed).toBeVisible({ timeout: 40_000 })
  await expect(failed).toHaveAttribute('data-reason', 'noPaint')
  await expect(page.getByTestId('page-area-why')).toContainText('5 seconds')
  // No retry loop: the tile stays.
  await page.waitForTimeout(3000)
  await expect(failed).toBeVisible()
  await expect(page.locator('[data-testid="page-area"] iframe')).toHaveCount(0)
  await expect(page.getByTestId('home-widget-chats')).toBeVisible()
})

test('a missing page and an invalid pageArea show the failure tile', async ({ page, request }) => {
  await setHome(request, { pageArea: GONE, widgets: [chatsWidget] })
  await home(page)
  const failed = page.getByTestId('page-area-failed')
  await expect(failed).toHaveAttribute('data-reason', 'missing')
  await expect(page.getByTestId('page-area-why')).toContainText(GONE)
  await page.getByTestId('page-area-revert').click()
  await expect(page.getByTestId('page-area-note')).toBeVisible()
  await expect(page.getByTestId('home-widget-chats')).toBeVisible()

  // A value that is not a page, as a file the server did not write.
  await setHome(request, { widgets: [chatsWidget] })
  const p2 = await page.context().newPage()
  await p2.route('**/api/chats/triggers*', (r) => r.fulfill({ json: { triggers: [] } }))
  await p2.route('**/api/chats/wellknown/home', async (route) => {
    const res = await route.fetch()
    const json = await res.json()
    await route.fulfill({ response: res, json: { ...json, valid: true, data: { ...json.data, pageArea: 7 } } })
  })
  await p2.goto('/app')
  await expect(p2.getByTestId('page-area-failed')).toHaveAttribute('data-reason', 'invalid')
  await expect(p2.getByTestId('page-area-why')).toContainText('not a valid page name')
  await expect(p2.getByTestId('home-input')).toBeVisible()
  await p2.close()
})

test('an administrator switches the page area off for everyone: pageArea is ignored and a note says why', async ({ page, request }) => {
  await setHome(request, { pageArea: TODAY, widgets: [chatsWidget] })
  expect((await request.post('/api/settings', { data: { home_page_area_enabled: false } })).ok()).toBeTruthy()
  try {
    expect((await (await request.get('/api/chats/pages')).json()).page_area_enabled).toBe(false)
    await home(page)
    const off = page.getByTestId('page-area-off')
    await expect(off).toBeVisible()
    await expect(off).toContainText('turned off')
    await expect(off).toContainText('administrator')
    await expect(page.locator('[data-testid="page-area"]')).toHaveCount(0)
    await expect(page.locator('iframe[data-testid="page-frame"]')).toHaveCount(0)
    await expect(page.getByTestId('home-widget-chats')).toBeVisible()
    await shot(page, 'area-off-light')
    // The picker says so too. The server serves no pageArea while the area
    // is off and names it as withheld; the file keeps its page.
    await page.getByTestId('home-edit-layout').click()
    await expect(page.getByTestId('home-layout-area-hint')).toContainText('administrator')
    const held = await homeJson(request)
    expect(held.data.pageArea).toBeUndefined()
    expect(held.withheld).toEqual(['pageArea'])
  } finally {
    await request.post('/api/settings', { data: { home_page_area_enabled: true } })
  }
  expect((await homeJson(request)).data.pageArea).toBe(TODAY)
  await home(page)
  await expect(area(page)).toBeVisible()
})

test('the layout editor picks the page area and writes home.json, keeping the other keys', async ({ page, request }) => {
  await setHome(request, { headline: 'Hi', widgets: [chatsWidget] })
  await home(page)
  await expect(area(page)).toHaveCount(0)
  await page.getByTestId('home-edit-layout').click()
  const pick = page.getByTestId('home-layout-area-pick')
  await expect(pick).toHaveValue('')
  await expect(pick.locator('option').first()).toHaveText('None')
  // Every page of the home, with or without a widget block.
  await expect(pick.locator(`option[value="${TODAY}"]`)).toHaveCount(1)
  await expect(pick.locator(`option[value="${SA}"]`)).toHaveCount(1)
  await pick.selectOption(TODAY)
  await shot(page, 'area-editor-light')
  await page.getByTestId('home-layout-save').click()
  await expect(area(page)).toBeVisible()
  let doc = await homeJson(request)
  expect(doc.data.pageArea).toBe(TODAY)
  expect(doc.data.headline).toBe('Hi')
  await page.getByTestId('home-edit-layout').click()
  await expect(page.getByTestId('home-layout-area-pick')).toHaveValue(TODAY)
  await page.getByTestId('home-layout-area-pick').selectOption('')
  await page.getByTestId('home-layout-save').click()
  await expect(area(page)).toHaveCount(0)
  doc = await homeJson(request)
  expect(doc.data.pageArea ?? null).toBeNull() // the server merge drops a null key
  expect(doc.data.headline).toBe('Hi')
})

test('the page area counts toward the live frames of the app', async ({ page, request }) => {
  await setHome(request, {
    pageArea: TODAY,
    widgets: [chatsWidget, { id: 't1', type: 'page', page: TILE }, { id: 't2', type: 'page', page: TILE }, { id: 't3', type: 'page', page: TILE }],
  })
  expect((await put(request, '/LocalAI/ui.json', { slots: { sidebarFooter: [TILE, TILE2, TILE3].map((p) => ({ type: 'page', page: p })) } })).ok()).toBeTruthy()
  try {
    await page.setViewportSize({ width: 1280, height: 2600 })
    await home(page)
    await expect(area(page)).toHaveAttribute('data-frame', 'live')
    await expect(page.locator('[data-testid^="home-widget-t"]')).toHaveCount(3)
    await expect(page.locator('[data-testid^="home-widget-t"][data-frame="live"]')).toHaveCount(3)
    // The area, three tiles and three slot tiles: seven of the eight frames.
    await expect(page.locator('iframe[data-testid="page-frame"]')).toHaveCount(7)
  } finally {
    await del(request, '/LocalAI/ui.json')
  }
})

test('on a phone the page area fits the width, with no sideways scroll', async ({ page, request }) => {
  await setHome(request, { pageArea: TODAY, widgets: [chatsWidget] })
  await page.setViewportSize({ width: 390, height: 844 })
  await home(page)
  await expect(area(page)).toHaveAttribute('data-frame', 'live')
  await expect(areaFrame(page).locator('li').first()).toBeVisible()
  const fit = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth <= window.innerWidth, area: document.querySelector('[data-testid="page-area"]').getBoundingClientRect().right <= window.innerWidth + 1 }))
  expect(fit).toEqual({ doc: true, area: true })
  await shot(page, 'area-phone-light')
  await put(request, `/pages/${BAD}/index.html`, BAD_HTML)
  await setHome(request, { pageArea: BAD, widgets: [chatsWidget] })
  await expect(page.getByTestId('page-area-failed')).toBeVisible({ timeout: 20_000 })
  const fit2 = await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
  expect(fit2).toBe(true)
  await shot(page, 'area-failed-phone-light')
})

const bodyHeight = (page) => area(page).locator('.wk-area__body').evaluate((el) => Math.round(el.getBoundingClientRect().height))

test.describe('the height of the page area', () => {
  test('a page that never reports keeps the default height and scrolls inside', async ({ page, request }) => {
    await setHome(request, { pageArea: TODAY, widgets: [chatsWidget] })
    await page.setViewportSize({ width: 1280, height: 900 })
    await home(page)
    await expect(area(page)).toHaveAttribute('data-state', 'run')
    await page.waitForTimeout(1000)
    expect(await bodyHeight(page)).toBe(240)
    await expect(area(page).locator('.wk-area__body')).not.toHaveAttribute('data-height', /./)
  })

  test('a short page gets the height of its content, above the widgets', async ({ page, request }) => {
    await setHome(request, { pageArea: SHORT, widgets: [chatsWidget] })
    for (const [w, h] of [[1280, 900], [390, 844]]) {
      await page.setViewportSize({ width: w, height: h })
      await home(page)
      await expect(area(page).locator('.wk-area__body')).toHaveAttribute('data-height', /\d+/)
      const want = await areaFrame(page).locator('html').evaluate((el) => Math.ceil(el.getBoundingClientRect().height))
      expect(want).toBeGreaterThan(240)
      expect(want).toBeLessThan(h * 0.6)
      expect(Math.abs((await bodyHeight(page)) - want), `at ${w}`).toBeLessThanOrEqual(2)
      // The page shows all of it: no scroll inside the frame.
      expect(await areaFrame(page).locator('html').evaluate((el) => el.scrollHeight <= window.innerHeight + 1)).toBe(true)
      const body = await area(page).locator('.wk-area__body').boundingBox()
      const grid = await page.getByTestId('home-widget-grid').boundingBox()
      expect(grid.y).toBeGreaterThanOrEqual(body.y + body.height)
      await shot(page, `area-short-${w}-light`)
    }
    await home(page, 'dark')
    await expect(area(page).locator('.wk-area__body')).toHaveAttribute('data-height', /\d+/)
    await shot(page, 'area-short-390-dark')
  })

  test('a tall page is capped at about 60% of the window and scrolls inside', async ({ page, request }) => {
    await setHome(request, { pageArea: TALL, widgets: [chatsWidget] })
    for (const [w, h] of [[1280, 900], [390, 844]]) {
      await page.setViewportSize({ width: w, height: h })
      await home(page)
      await expect(area(page).locator('.wk-area__body')).toHaveAttribute('data-height', /\d+/)
      expect(Math.abs((await bodyHeight(page)) - Math.round(h * 0.6)), `at ${w}`).toBeLessThanOrEqual(1)
      await shot(page, `area-tall-${w}-light`)
    }
    // The cap follows the window.
    await page.setViewportSize({ width: 1280, height: 600 })
    await expect.poll(() => bodyHeight(page)).toBe(360)
    await page.setViewportSize({ width: 1280, height: 900 })
    await home(page, 'dark')
    await expect(area(page).locator('.wk-area__body')).toHaveAttribute('data-height', /\d+/)
    await shot(page, 'area-tall-1280-dark')
  })

  test('the area follows a page that grows, without a reload of the frame', async ({ page, request }) => {
    await setHome(request, { pageArea: GROW, widgets: [chatsWidget] })
    await page.setViewportSize({ width: 1280, height: 900 })
    await home(page)
    const token = areaFrame(page).locator('#token')
    await expect(token).not.toBeEmpty()
    const first = await token.textContent()
    await expect.poll(() => bodyHeight(page)).toBeGreaterThan(240)
    const before = await bodyHeight(page)
    await expect.poll(() => bodyHeight(page), { timeout: 10_000 }).toBeGreaterThanOrEqual(before + 90)
    expect(await token.textContent()).toBe(first)
    await expect(area(page)).toHaveAttribute('data-state', 'run')
  })

  test('a burst of heights applies at most a few, the last one wins, and the page keeps running', async ({ page, request }) => {
    await setHome(request, { pageArea: SPAM, widgets: [chatsWidget] })
    await page.setViewportSize({ width: 1280, height: 900 })
    await home(page)
    await expect(area(page)).toHaveAttribute('data-state', 'run')
    // Count the heights the host applies.
    await area(page).locator('.wk-area__body').evaluate((el) => {
      window.__areaSizes = []
      new MutationObserver(() => window.__areaSizes.push(el.getAttribute('data-height'))).observe(el, { attributes: true, attributeFilter: ['data-height'] })
    })
    await expect(area(page).locator('.wk-area__body')).toHaveAttribute('data-height', '420', { timeout: 10_000 })
    expect(await bodyHeight(page)).toBe(420)
    const applied = await page.evaluate(() => window.__areaSizes.length)
    expect(applied).toBeGreaterThanOrEqual(1)
    expect(applied).toBeLessThanOrEqual(3)
    await page.waitForTimeout(1500)
    await expect(area(page)).toHaveAttribute('data-state', 'run')
    await expect(area(page)).toHaveAttribute('data-frame', 'live')
  })

  test('a failed page shows the failure tile at its own height', async ({ page, request }) => {
    await put(request, `/pages/${BAD}/index.html`, BAD_HTML)
    await put(request, `/pages/${BAD}/page.json`, { title: 'Bad page' })
    await setHome(request, { pageArea: BAD, widgets: [chatsWidget] })
    for (const theme of ['light', 'dark']) {
      for (const [w, h] of [[1280, 900], [390, 844]]) {
        await page.setViewportSize({ width: w, height: h })
        await home(page, theme)
        await expect(page.getByTestId('page-area-failed')).toBeVisible({ timeout: 20_000 })
        await shot(page, `area-failed-${w}-${theme}`)
      }
    }
  })
})

test('dark theme', async ({ page, request }) => {
  await setHome(request, { pageArea: TODAY, widgets: [chatsWidget] })
  await home(page, 'dark')
  await expect(area(page)).toHaveAttribute('data-frame', 'live')
  await expect(areaFrame(page).locator('li').first()).toBeVisible()
  await shot(page, 'area-dark')
})

test.describe('the start page', () => {
  test('the first pinned page has a badge in the sidebar and a switch on its screen', async ({ page, request }) => {
    expect((await put(request, '/LocalAI/ui.json', { pinnedPages: [SA, SB], theme: 'light' })).ok()).toBeTruthy()
    await page.goto('/app/pages')
    const links = page.getByTestId('pages-pinned-link')
    await expect(links).toHaveCount(2)
    await expect(links.nth(0)).toHaveAttribute('data-page', SA)
    await expect(links.nth(0).getByTestId('pages-start-badge')).toHaveText('Start page')
    await expect(links.nth(1).getByTestId('pages-start-badge')).toHaveCount(0)
    // Its own screen, with the shell bar and the switch.
    await links.nth(0).click()
    await expect(page).toHaveURL(new RegExp(`/app/pages/${SA}$`))
    await expect(page.getByTestId('page-start-badge')).toHaveText('Start page')
    const sw = page.getByTestId('page-start-switch')
    await expect(sw).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('page-start')).toContainText('Make this my first pinned page')
    await expect(page.frameLocator('[data-testid="page-frame"]').getByText('Task board')).toBeVisible()
    await shot(page, 'start-page-light')
    // The second page is not the start page; the switch makes it the first.
    await links.nth(1).click()
    await expect(page).toHaveURL(new RegExp(`/app/pages/${SB}$`))
    await expect(page.getByTestId('page-start-badge')).toHaveCount(0)
    await expect(page.getByTestId('page-start-switch')).toHaveAttribute('aria-checked', 'false')
    await page.getByTestId('page-start-switch').click()
    await expect(page.getByTestId('page-start-switch')).toHaveAttribute('aria-checked', 'true')
    const ui = (await (await request.get('/api/chats/wellknown/ui')).json()).data
    expect(ui.pinnedPages).toEqual([SB, SA])
    expect(ui.theme).toBe('light')
    await expect(page.getByTestId('pages-pinned-link').nth(0)).toHaveAttribute('data-page', SB)
    await expect(page.getByTestId('pages-pinned-link').nth(0).getByTestId('pages-start-badge')).toBeVisible()
    await expect(page.getByTestId('pages-start-badge')).toHaveCount(1)
    // Off hands the start to the next pin.
    await page.getByTestId('page-start-switch').click()
    await expect(page.getByTestId('page-start-switch')).toHaveAttribute('aria-checked', 'false')
    expect((await (await request.get('/api/chats/wellknown/ui')).json()).data.pinnedPages).toEqual([SA, SB])
    // The Home route does not change.
    await page.goto('/app')
    await expect(page.getByTestId('home-input')).toBeVisible()
  })

  test('a page that is not pinned is pinned first by the switch', async ({ page, request }) => {
    expect((await put(request, '/LocalAI/ui.json', { pinnedPages: [SA] })).ok()).toBeTruthy()
    await page.goto(`/app/pages/${SB}`)
    await expect(page.getByTestId('page-start-switch')).toHaveAttribute('aria-checked', 'false')
    await page.getByTestId('page-start-switch').click()
    await expect(page.getByTestId('page-start-switch')).toHaveAttribute('aria-checked', 'true')
    expect((await (await request.get('/api/chats/wellknown/ui')).json()).data.pinnedPages).toEqual([SB, SA])
  })

  test('on a phone the bar wraps and the page has no sideways scroll', async ({ page, request }) => {
    expect((await put(request, '/LocalAI/ui.json', { pinnedPages: [SA, SB] })).ok()).toBeTruthy()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto(`/app/pages/${SA}`)
    await expect(page.getByTestId('page-start-switch')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await shot(page, 'start-page-phone-light')
  })
})
