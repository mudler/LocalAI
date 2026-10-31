import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'

// Pages as Home tiles, against the real test server: a page with a widget
// block runs in its compact mode in a tile, the same page runs its full layout
// in its own view, a tile whose page is gone or has no widget block says so
// calmly, a tile builds its frame only once it is in view, Customise Home
// offers the pages that can be a tile, and a link in a tile asks first.
// PAGES_SHOTS names a folder for screenshots.

test.describe.configure({ mode: 'serial', timeout: 60_000 })
holdHomeFiles(test)

const stamp = Date.now().toString(36)
const CLOCK = `tclock-${stamp}`
const NEWS = `tnews-${stamp}`
const BOARD = `tboard-${stamp}`
const PLAIN = `tplain-${stamp}`
const GONE = `tgone-${stamp}`
const shots = process.env.PAGES_SHOTS || ''

const put = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
const del = (request, path) => request.delete(`/api/chats/files?path=${encodeURIComponent(path)}`)

// Each page shows its mode and size, and has a compact layout of its own.
const STYLE = `<style>
  :root { --bg: #fff; --text: #121417; --muted: #5d6673; --line: #d9dde3; --accent: #1f6feb; }
  :root[data-theme="dark"] { --bg: #0b0d10; --text: #e6e9ee; --muted: #8b95a3; --line: #29313b; --accent: #58a6ff; }
  body { margin: 0; padding: 24px; background: var(--bg); color: var(--text); font: 14px/1.45 sans-serif; }
  .full { display: block; } .compact { display: none; }
  :root[data-mode="widget"] body { padding: 10px 14px; }
  :root[data-mode="widget"] .full { display: none; }
  :root[data-mode="widget"] .compact { display: block; }
  .big { font-size: 28px; font-weight: 600; letter-spacing: -0.02em; }
  .muted { color: var(--muted); font-size: 12px; }
  ul { margin: 6px 0 0; padding: 0; list-style: none; }
  li { padding: 4px 0; border-top: 1px solid var(--line); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  a { color: var(--accent); }
</style>`
const CLOCK_HTML = `<!doctype html><html><head>${STYLE}</head><body>
<p id="mode"></p>
<div class="full"><h1>Clock</h1><p>The full page shows the date, the time and the week.</p></div>
<div class="compact"><div class="big">09:41</div><div class="muted">Friday 31 October · <a id="ext" href="https://news.example.com/time">time zones</a></div></div>
<script>document.getElementById('mode').textContent = localai.page.mode + ' ' + localai.page.size
document.getElementById('mode').className = 'muted'</script>
</body></html>`
const NEWS_HTML = `<!doctype html><html><head>${STYLE}</head><body>
<div class="full"><h1>News</h1></div>
<div class="compact muted">From news.json · updated this morning</div>
<ul id="list"></ul>
<script>
fetch('news.json').then(r => r.json()).then(d => {
  const list = document.getElementById('list')
  for (const it of d.items) { const li = document.createElement('li'); li.textContent = it; list.appendChild(li) }
})
</script></body></html>`
const BOARD_HTML = `<!doctype html><html><head>${STYLE}</head><body>
<div class="full"><h1>Board</h1></div>
<div class="compact"><div class="big">4 <span class="muted">to do</span> · 2 <span class="muted">doing</span></div>
<ul><li>Write the release notes</li><li>Review the gallery PRs</li></ul></div>
</body></html>`

const widgets = (list) => ({ widgets: list })
const tasks = { id: 'tasks', type: 'list', title: 'Open tasks', source: `data/t-${stamp}.json` }
const tiles = [
  { id: 'clock', type: 'page', page: CLOCK },
  tasks,
  { id: 'board', type: 'page', page: BOARD },
  { id: 'news', type: 'page', page: NEWS },
  { id: 'chats', type: 'chats', title: 'Last chats' },
]

const tile = (page, id) => page.getByTestId(`home-widget-${id}`)
const tileFrame = (page, id) => tile(page, id).frameLocator('[data-testid="page-frame"]')

async function home(page, theme = 'light') {
  await page.route('**/api/chats/triggers*', (r) => r.fulfill({ json: { triggers: [] } }))
  await page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private mode */ } }, theme)
  await page.goto('/app')
}

test.beforeAll(async ({ request }) => {
  const files = {
    [`/pages/${CLOCK}/index.html`]: CLOCK_HTML,
    [`/pages/${CLOCK}/page.json`]: { title: 'Clock', icon: 'clock', widget: { size: 'small', title: 'Now' } },
    [`/pages/${NEWS}/index.html`]: NEWS_HTML,
    [`/pages/${NEWS}/page.json`]: { title: 'News ticker', icon: 'file-text', widget: { size: 'large' } },
    [`/pages/${NEWS}/news.json`]: { items: ['Local models get faster on small boards', 'A new release of the gallery', 'Three tips for long contexts', 'What changed in the scheduler'] },
    [`/pages/${BOARD}/index.html`]: BOARD_HTML,
    [`/pages/${BOARD}/page.json`]: { title: 'Task board', icon: 'checklist', widget: { size: 'medium', title: 'Board' } },
    [`/pages/${PLAIN}/index.html`]: '<p>no widget</p>',
    [`/pages/${PLAIN}/page.json`]: { title: 'Plain page' },
    [`/data/t-${stamp}.json`]: { items: [{ label: 'Call the dentist', meta: 'Fri' }, { label: 'Send the invoice', done: true }] },
  }
  for (const [p, body] of Object.entries(files)) expect((await put(request, p, body)).ok(), p).toBeTruthy()
  expect((await put(request, '/LocalAI/home.json', widgets(tiles))).ok()).toBeTruthy()
})
test.afterAll(async ({ request }) => {
  for (const p of ['/LocalAI/home.json', `/data/t-${stamp}.json`, `/pages/${CLOCK}`, `/pages/${NEWS}`, `/pages/${BOARD}`, `/pages/${PLAIN}`]) await del(request, p)
})
test.afterAll(() => unlockHomeFiles())

test('a page tile runs the page in its compact mode, at its size', async ({ page }) => {
  await home(page)
  await expect(tile(page, 'clock')).toHaveAttribute('data-size', 'small')
  await expect(tile(page, 'news')).toHaveAttribute('data-size', 'large')
  await expect(tile(page, 'clock').locator('.wk-ptile__title')).toHaveText('Now')
  await expect(tile(page, 'news').locator('.wk-ptile__title')).toHaveText('News ticker')
  const clock = tileFrame(page, 'clock')
  await expect(clock.locator('#mode')).toHaveText('widget small')
  await expect(clock.locator('html')).toHaveAttribute('data-mode', 'widget')
  await expect(clock.locator('.compact')).toBeVisible()
  await expect(clock.locator('.full')).toBeHidden()
  // The news tile reads its data with fetch, once it is in view.
  await tile(page, 'news').scrollIntoViewIfNeeded()
  await expect(tileFrame(page, 'news').locator('li')).toHaveCount(4)
  // Heights by size: small < medium < large, and a large tile spans two columns.
  const h = async (id) => (await tile(page, id).locator('.wk-ptile__body').boundingBox()).height
  expect(await h('clock')).toBeLessThan(await h('board'))
  expect(await h('board')).toBeLessThan(await h('news'))
  const open = tile(page, 'clock').getByTestId('page-tile-open')
  await expect(open).toHaveAttribute('href', `/app/pages/${CLOCK}`)
  await expect(tile(page, 'clock')).toHaveAttribute('data-frame', 'live')
  if (shots) {
    await tile(page, 'clock').scrollIntoViewIfNeeded()
    await page.waitForTimeout(300)
    await page.screenshot({ path: `${shots}/home-tiles-light.png`, fullPage: true })
  }
  await open.click()
  await expect(page).toHaveURL(new RegExp(`/app/pages/${CLOCK}$`))
  // The same page in its own view runs its full layout.
  const full = page.getByTestId('page-view').frameLocator('[data-testid="page-frame"]')
  await expect(full.locator('#mode')).toHaveText('page ')
  await expect(full.locator('html')).toHaveAttribute('data-mode', 'page')
  await expect(full.locator('.full')).toBeVisible()
  await expect(full.locator('.compact')).toBeHidden()
})

test('the refresh button builds the frame again and the tile stays in place', async ({ page }) => {
  await home(page)
  const clock = tileFrame(page, 'clock')
  await expect(clock.locator('#mode')).toHaveText('widget small')
  const before = await tile(page, 'clock').boundingBox()
  await tile(page, 'clock').getByTestId('page-tile-refresh').click()
  await expect(clock.locator('#mode')).toHaveText('widget small')
  const after = await tile(page, 'clock').boundingBox()
  expect(after.height).toBe(before.height)
})

test('a link inside a tile asks first, in the tile', async ({ page, context }) => {
  await context.route('https://news.example.com/**', (r) => r.fulfill({ contentType: 'text/html', body: '<title>t</title><p>ok</p>' }))
  await home(page)
  await tileFrame(page, 'clock').locator('#ext').click()
  const ask = tile(page, 'clock').getByTestId('page-link-ask')
  await expect(ask).toBeVisible()
  await expect(ask.getByTestId('page-link-host')).toHaveText('news.example.com')
  if (shots) await tile(page, 'clock').screenshot({ path: `${shots}/home-tile-link-ask.png` })
  let opened = 0
  context.on('page', () => { opened++ })
  await ask.getByTestId('page-link-cancel').click()
  await expect(ask).toHaveCount(0)
  await page.waitForTimeout(200)
  expect(opened).toBe(0)
  await tileFrame(page, 'clock').locator('#ext').click()
  const [tab] = await Promise.all([context.waitForEvent('page'), tile(page, 'clock').getByTestId('page-link-open').click()])
  await tab.waitForLoadState()
  expect(tab.url()).toBe('https://news.example.com/time')
  expect(await tab.evaluate(() => window.opener)).toBeNull()
  await tab.close()
})

test('the tiles in the dark theme and on a phone', async ({ page }) => {
  await home(page, 'dark')
  await tile(page, 'news').scrollIntoViewIfNeeded()
  await expect(tileFrame(page, 'news').locator('li')).toHaveCount(4)
  await expect(tileFrame(page, 'clock').locator('html')).toHaveAttribute('data-theme', 'dark')
  if (shots) {
    await tile(page, 'clock').scrollIntoViewIfNeeded()
    await page.waitForTimeout(300)
    await page.screenshot({ path: `${shots}/home-tiles-dark.png`, fullPage: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await tile(page, 'clock').scrollIntoViewIfNeeded()
  await expect(tileFrame(page, 'clock').locator('#mode')).toHaveText('widget small')
  // One column: no tile is wider than the screen.
  for (const id of ['clock', 'board', 'news']) {
    const box = await tile(page, id).boundingBox()
    expect(box.x + box.width).toBeLessThanOrEqual(390)
  }
  if (shots) {
    // A tall phone screen, so every tile is in view and has its frame.
    await page.setViewportSize({ width: 390, height: 2400 })
    await expect(tileFrame(page, 'news').locator('li')).toHaveCount(4)
    await expect(tileFrame(page, 'clock').locator('#mode')).toHaveText('widget small')
    await page.waitForTimeout(300)
    await page.screenshot({ path: `${shots}/home-tiles-phone-dark.png`, fullPage: true })
  }
})

test('the tiles on a phone in the light theme', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await home(page)
  await tile(page, 'news').scrollIntoViewIfNeeded()
  await expect(tileFrame(page, 'news').locator('li')).toHaveCount(4)
  // Every tile was in view once, so each has its frame for the shot.
  await tile(page, 'clock').scrollIntoViewIfNeeded()
  await expect(tileFrame(page, 'clock').locator('#mode')).toHaveText('widget small')
  if (shots) {
    // A tall phone screen, so every tile is in view and has its frame.
    await page.setViewportSize({ width: 390, height: 2400 })
    await expect(tileFrame(page, 'news').locator('li')).toHaveCount(4)
    await expect(tileFrame(page, 'clock').locator('#mode')).toHaveText('widget small')
    await page.waitForTimeout(300)
    await page.screenshot({ path: `${shots}/home-tiles-phone-light.png`, fullPage: true })
  }
})

test('Customise Home lists the pages that can be a tile, with their size', async ({ page }) => {
  await home(page)
  await page.getByTestId('home-edit-layout').click()
  const editor = page.getByTestId('home-layout-editor')
  await expect(editor).toBeVisible()
  const picks = editor.getByTestId('home-layout-page')
  await expect(picks).toHaveCount(3)
  const options = await picks.first().locator('option').allTextContents()
  expect(options).toEqual(expect.arrayContaining(['Clock', 'News ticker', 'Task board']))
  expect(options).not.toContain('Plain page')
  await expect(editor.getByTestId('home-layout-page-size').first()).toHaveAttribute('data-size', 'small')
  // Three page widgets: the limit says so and no fourth can be added.
  await expect(editor.getByTestId('home-layout-page-limit')).toBeVisible()
  await expect(editor.getByTestId('home-layout-add-page')).toBeDisabled()
  if (shots) await editor.screenshot({ path: `${shots}/home-tiles-editor.png` })
  // Swap the board for the clock... and back: a save keeps the layout valid.
  await picks.nth(1).selectOption({ label: 'News ticker' })
  await expect(editor.getByTestId('home-layout-page-size').nth(1)).toHaveAttribute('data-size', 'large')
  await picks.nth(1).selectOption({ label: 'Task board' })
  await editor.getByTestId('home-layout-save').click()
  await expect(editor).toHaveCount(0)
  await expect(tile(page, 'board')).toHaveAttribute('data-size', 'medium')
})

test('the server takes at most three page widgets and says why', async ({ request }) => {
  const four = [1, 2, 3, 4].map((n) => ({ id: `p${n}`, type: 'page', page: CLOCK }))
  const res = await request.put('/api/chats/wellknown/home', { data: { values: { widgets: four }, replace: true } })
  expect(res.ok()).toBeFalsy()
  expect(JSON.stringify(await res.json())).toContain('Home shows at most 3 page widgets, because each one runs its page; this layout has 4')
  const bad = await request.put('/api/chats/wellknown/home', { data: { values: { widgets: [{ id: 'p', type: 'page', page: '../x' }] }, replace: true } })
  expect(JSON.stringify(await bad.json())).toContain('is not a page name')
  const schema = await (await request.get('/api/chats/wellknown/home/schema')).json()
  expect(JSON.stringify(schema)).toContain('"maxContains":3')
})

test('a tile whose page is gone, or has no widget block, says so and links to Customise Home', async ({ page, request }) => {
  expect((await put(request, '/LocalAI/home.json', widgets([
    { id: 'gone', type: 'page', page: GONE },
    { id: 'plain', type: 'page', page: PLAIN },
    tasks,
  ]))).ok()).toBeTruthy()
  await home(page)
  const gone = tile(page, 'gone')
  await expect(gone.getByTestId('page-tile-gone')).toContainText('This page is no longer available')
  await expect(gone.locator('iframe')).toHaveCount(0)
  await expect(gone.getByTestId('page-tile-open')).toHaveCount(0)
  const plain = tile(page, 'plain')
  await expect(plain.getByTestId('page-tile-noWidget')).toContainText('This page has no widget view')
  await expect(plain.locator('iframe')).toHaveCount(0)
  await expect(plain.getByTestId('page-tile-open')).toHaveAttribute('href', `/app/pages/${PLAIN}`)
  if (shots) {
    await gone.scrollIntoViewIfNeeded()
    await page.screenshot({ path: `${shots}/home-tiles-calm-light.png`, fullPage: true })
  }
  await gone.getByTestId('page-tile-fix').click()
  const editor = page.getByTestId('home-layout-editor')
  await expect(editor).toBeVisible()
  // The gone page stays in the row, marked, so a save does not drop it unseen.
  await expect(editor.getByTestId('home-layout-page').first()).toContainText(`${GONE} (not available)`)
})

test('the calm states in the dark theme', async ({ page }) => {
  await home(page, 'dark')
  await expect(tile(page, 'gone').getByTestId('page-tile-gone')).toBeVisible()
  if (shots) {
    await tile(page, 'gone').scrollIntoViewIfNeeded()
    await page.screenshot({ path: `${shots}/home-tiles-calm-dark.png`, fullPage: true })
  }
})

test('a tile builds its frame only once it scrolls into view', async ({ page, request }) => {
  const lists = Array.from({ length: 11 }, (_, i) => ({ ...tasks, id: `l${i}`, title: `List ${i + 1}` }))
  expect((await put(request, '/LocalAI/home.json', widgets([...lists, { id: 'clock', type: 'page', page: CLOCK }]))).ok()).toBeTruthy()
  await page.setViewportSize({ width: 1280, height: 480 })
  await home(page)
  const clock = tile(page, 'clock')
  await expect(clock).toHaveAttribute('data-state', 'ready')
  await expect(clock).not.toBeInViewport()
  await expect(clock).toHaveAttribute('data-frame', 'none')
  await expect(clock.locator('iframe')).toHaveCount(0)
  const reserved = (await clock.locator('.wk-ptile__body').boundingBox()).height
  await clock.scrollIntoViewIfNeeded()
  await expect(clock.locator('iframe')).toHaveCount(1)
  await expect(tileFrame(page, 'clock').locator('#mode')).toHaveText('widget small')
  expect((await clock.locator('.wk-ptile__body').boundingBox()).height).toBe(reserved)
  // Far away again: the frame goes; back in view: it comes back.
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.setViewportSize({ width: 1280, height: 200 })
  await page.evaluate(() => window.scrollTo(0, 0))
  await expect(clock).toHaveAttribute('data-frame', /none|paused/)
  await clock.scrollIntoViewIfNeeded()
  await expect(clock).toHaveAttribute('data-frame', 'live')
})
