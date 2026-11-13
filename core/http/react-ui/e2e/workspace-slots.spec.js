import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'
import { mockChat } from './chat-fixtures.js'

// The slots of ui.json against the real test server: page tiles in the chat
// header, the sidebar footer and the composer toolbar, a card that grows from
// a tile without a reload, live data through the change feed, the cap of
// eight live frames for the whole app, the budget of a frame, tool buttons
// that ask first, the same sandbox as everywhere, and the Slots tab.
// SLOTS_SHOTS names a folder for screenshots.

test.describe.configure({ mode: 'serial', timeout: 90_000 })
holdHomeFiles(test)

const stamp = Date.now().toString(36)
const WEATHER = `sweather-${stamp}`
const READING = `sreading-${stamp}`
const FOCUS = `sfocus-${stamp}`
const NOTES = `snotes-${stamp}`
const SPIN = `sspin-${stamp}`
const many = Array.from({ length: 10 }, (_, i) => `scap${i}-${stamp}`)
const shots = process.env.SLOTS_SHOTS || ''
const AT = '2026-11-10T10:00:00Z'

const put = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
const del = (request, path) => request.delete(`/api/chats/files?path=${encodeURIComponent(path)}`)
const setSlots = async (request, slots) => {
  const r = await request.put('/api/chats/wellknown/ui', { data: { values: { slots } } })
  expect(r.ok(), await r.text()).toBeTruthy()
}

const STYLE = `<style>
  :root { --bg: #ffffff; --fg: #121417; --muted: #5d6673; --accent: #1f6feb; --line: #d9dde3; }
  :root[data-theme="dark"] { --bg: #16191d; --fg: #e6e9ee; --muted: #8b95a3; --accent: #58a6ff; --line: #29313b; }
  html, body { margin: 0; background: var(--bg); color: var(--fg); font: 13px/1.35 system-ui, sans-serif; }
  .strip { display: none; align-items: center; gap: 6px; height: 32px; padding: 0 10px; white-space: nowrap; overflow: hidden; }
  :root[data-size="compact"] .strip { display: flex; }
  :root[data-size="compact"] .full { display: none; }
  .full { padding: 6px 4px; }
  .big { font-size: 30px; font-weight: 600; letter-spacing: -0.02em; }
  .muted { color: var(--muted); }
  .days { display: grid; grid-template-columns: repeat(3, 1fr); gap: 6px; margin-top: 10px; }
  .days div { padding: 6px; border-radius: 6px; background: color-mix(in srgb, var(--fg) 6%, transparent); text-align: center; }
  ul { margin: 6px 0 0; padding: 0; list-style: none; } li { padding: 4px 0; border-top: 1px solid var(--line); }
  .probe { position: absolute; left: -9999px; }
</style>`
const WEATHER_HTML = `<!doctype html><html><head>${STYLE}</head><body>
<div class="strip"><span>&#9728;</span><b id="t">18°</b><span class="muted" id="city">Turin</span></div>
<div class="full"><div class="big">&#9728; <span id="t2">18°</span></div><div class="muted" id="desc">Sunny, Turin</div>
<div class="days"><div>Wed<br><b>17°</b></div><div>Thu<br><b>15°</b></div><div>Fri<br><b>12°</b></div></div></div>
<p class="probe"><span id="size"></span> <span id="boot"></span> <span id="modes"></span></p>
<script>
const boot = Math.random().toString(36).slice(2)
document.getElementById('boot').textContent = boot
const size = () => { document.getElementById('size').textContent = localai.page.mode + ' ' + localai.page.size }
size()
let modes = 0
localai.on('mode', () => { modes++; document.getElementById('modes').textContent = String(modes); size() })
function load() {
  fetch('weather.json').then((r) => r.json()).then((d) => {
    document.getElementById('t').textContent = d.temp + '°'
    document.getElementById('t2').textContent = d.temp + '°'
    document.getElementById('city').textContent = d.city
    document.getElementById('desc').textContent = d.sky + ', ' + d.city
  })
}
load()
localai.on('files.changed', load)
</script></body></html>`
const READING_HTML = `<!doctype html><html><head>${STYLE}</head><body>
<div class="strip"><span>&#128214;</span><b>Reading</b><span class="muted">3 left</span></div>
<div class="full"><b>Reading list</b><ul><li>The Mythical Man-Month</li><li>A Pattern Language</li><li>Thinking in Systems</li></ul></div>
</body></html>`
const FOCUS_HTML = `<!doctype html><html><head>${STYLE}</head><body>
<div class="strip"><span>&#9201;</span><b>Focus</b><span class="muted">25:00</span></div>
<div class="full"><div class="big">25:00</div><div class="muted">Next break at 11:30</div></div>
</body></html>`
// Busy for 450 ms out of every 500: far over the budget.
const SPIN_HTML = `<!doctype html><html><head>${STYLE}</head><body><div class="strip"><b>Spin</b></div>
<script>setInterval(() => { const end = Date.now() + 450; while (Date.now() < end) {} }, 500)</script></body></html>`
const capHtml = (i) => `<!doctype html><html><head>${STYLE}</head><body><div class="strip"><b>Tile ${i}</b></div></body></html>`

const compact = (title, extra = {}) => ({ title, widget: { size: 'medium', compact: { width: 132 }, ...extra } })

async function openChat(page, { theme = 'light', sent = null } = {}) {
  await page.addInitScript((t) => {
    try { localStorage.setItem('localai-theme', t) } catch { /* private mode */ }
    const Real = window.EventSource
    window.EventSource = function (url, opts) {
      if (!String(url).includes('/api/chats/c1/events')) return new Real(url, opts)
      const fake = { addEventListener() {}, close() {}, onopen: null }
      setTimeout(() => fake.onopen && fake.onopen(), 0)
      return fake
    }
    window.EventSource.prototype = Real.prototype
  }, theme)
  await mockChat(page)
  const detail = { id: 'c1', title: 'Trip to Lisbon', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done', pending: [] }
  const history = [
    { seq: 1, turn: 1, role: 'user', text: 'Is it worth going to Lisbon the second week of December?', at: AT },
    { seq: 2, turn: 1, role: 'assistant', text: 'Yes, if you like quiet streets. Daytime highs are around 15 degrees and the crowds are gone. Pack a light rain jacket; it is the wettest month.', at: AT },
  ]
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, (route) => {
    if (route.request().method() === 'POST') {
      sent?.push(route.request().postDataJSON())
      return route.fulfill({ json: { started: true } })
    }
    return route.fulfill({ json: { messages: history } })
  })
  await page.route('**/api/chats/c1', (route) => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, (route) => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', (route) => route.fulfill({ json: { agents: [] } }))
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-thread')).toBeVisible()
}

const slot = (page, name) => page.getByTestId(`slot-${name}`)
const tileOf = (page, name, pageName) => slot(page, name).locator(`[data-testid="slot-tile"][data-page="${pageName}"]`)
const frameOf = (tile) => tile.frameLocator('[data-testid="page-frame"]')
const settle = (page) => page.evaluate(() => Promise.all(document.getAnimations().map((a) => a.finished.catch(() => {}))))

test.beforeAll(async ({ request }) => {
  const files = {
    [`/pages/${WEATHER}/index.html`]: WEATHER_HTML,
    [`/pages/${WEATHER}/page.json`]: { title: 'Weather', icon: 'sun', widget: { size: 'medium', compact: { width: 124 } } },
    [`/pages/${WEATHER}/weather.json`]: { temp: 18, city: 'Turin', sky: 'Sunny' },
    [`/pages/${READING}/index.html`]: READING_HTML,
    [`/pages/${READING}/page.json`]: { title: 'Reading list', icon: 'book', widget: { size: 'small', compact: { width: 136 } } },
    [`/pages/${FOCUS}/index.html`]: FOCUS_HTML,
    [`/pages/${FOCUS}/page.json`]: { title: 'Focus timer', icon: 'clock', widget: { size: 'small', compact: { width: 124 } } },
    [`/pages/${NOTES}/index.html`]: '<!doctype html><h1>Notes</h1>',
    [`/pages/${NOTES}/page.json`]: { title: 'Notes', icon: 'file-text' },
    [`/pages/${SPIN}/index.html`]: SPIN_HTML,
    [`/pages/${SPIN}/page.json`]: compact('Spin'),
  }
  many.forEach((name, i) => {
    files[`/pages/${name}/index.html`] = capHtml(i)
    files[`/pages/${name}/page.json`] = compact(`Tile ${i}`)
  })
  for (const [p, body] of Object.entries(files)) expect((await put(request, p, body)).ok(), p).toBeTruthy()
})
test.afterAll(async ({ request }) => {
  for (const p of ['/LocalAI/ui.json', '/LocalAI/commands.json', `/pages/${WEATHER}`, `/pages/${READING}`, `/pages/${FOCUS}`, `/pages/${NOTES}`, `/pages/${SPIN}`, ...many.map((n) => `/pages/${n}`)]) await del(request, p)
})
test.afterAll(() => unlockHomeFiles())

test('without slots the chat header and the composer look as before', async ({ page, request }) => {
  await del(request, '/LocalAI/ui.json')
  await openChat(page)
  await expect(page.getByTestId('chat-header')).toBeVisible()
  await expect(slot(page, 'chatHeader')).toHaveCount(0)
  await expect(slot(page, 'composerToolbar')).toHaveCount(0)
  await expect(slot(page, 'sidebarFooter')).toHaveCount(0)
})

test('page tiles run compact in a slot; a click grows the same frame into a card, without a reload', async ({ page, request }) => {
  await setSlots(request, {
    chatHeader: [{ type: 'page', page: WEATHER }, { type: 'page', page: READING }, { type: 'page', page: NOTES }],
    sidebarFooter: [{ type: 'page', page: FOCUS }],
    composerToolbar: [{ type: 'tool', tool: 'summarize_chat', label: 'Summarize chat', icon: 'sparkles' }],
  })
  await openChat(page)
  const weather = tileOf(page, 'chatHeader', WEATHER)
  await expect(weather).toHaveAttribute('data-frame', 'live')
  const frame = frameOf(weather)
  await expect(frame.locator('#size')).toHaveText('widget compact')
  await expect(frame.locator('html')).toHaveAttribute('data-size', 'compact')
  await expect(frame.locator('#t')).toHaveText('18°')
  const boot = await frame.locator('#boot').textContent()

  // The slot keeps to its budget: about 40 px high, the tile at its width.
  const box = await weather.boundingBox()
  expect(box.height).toBeLessThanOrEqual(40)
  expect(Math.round(box.width)).toBe(124)
  // A page without compact is a button that opens the page.
  await expect(tileOf(page, 'chatHeader', NOTES).getByTestId('slot-tile-button')).toContainText('Notes')
  // The chat page folds the sidebar: a folded sidebar has no room for its slot.
  await expect(slot(page, 'sidebarFooter')).toHaveCount(0)

  if (shots) {
    await settle(page)
    await page.screenshot({ path: `${shots}/slots-header-light.png` })
  }

  await weather.getByTestId('slot-tile-hit').click()
  const card = weather.getByTestId('slot-card')
  await expect(card).toBeVisible()
  await expect(frame.locator('#size')).toHaveText('widget medium')
  await expect(frame.locator('html')).toHaveAttribute('data-size', 'medium')
  await expect(frame.locator('#boot')).toHaveText(boot, { timeout: 2000 })
  await expect(frame.locator('#modes')).toHaveText('1')
  // The card hangs under its tile, aligned to its right edge, and covers
  // nothing of the bar: neither the title nor the tile itself.
  const header = await page.getByTestId('chat-header').boundingBox()
  const c = await card.boundingBox()
  const tb = await weather.boundingBox()
  expect(c.y).toBeGreaterThanOrEqual(tb.y + tb.height)
  expect(c.y).toBeGreaterThanOrEqual(header.y + header.height - 8)
  expect(Math.abs((c.x + c.width) - (tb.x + tb.width))).toBeLessThanOrEqual(2)
  // The frame sits inside the card's body.
  const fb = await weather.locator('[data-testid="page-frame"]').boundingBox()
  expect(fb.y).toBeGreaterThan(c.y)
  expect(fb.y + fb.height).toBeLessThan(c.y + c.height)
  expect(fb.x).toBeGreaterThan(c.x)
  // The card covers no text: the thread keeps its column left of the card.
  const firstText = page.getByTestId('chat-thread').locator('p').first()
  await expect.poll(async () => { const b = await firstText.boundingBox(); return b.x + b.width }).toBeLessThanOrEqual(c.x)
  if (shots) {
    await settle(page)
    await page.screenshot({ path: `${shots}/slots-card-light.png` })
  }

  // Escape closes it; the page goes back to its strip, still not reloaded.
  await page.keyboard.press('Escape')
  await expect(card).toHaveCount(0)
  await expect(frame.locator('#size')).toHaveText('widget compact')
  await expect(frame.locator('#boot')).toHaveText(boot)
  await expect(weather.getByTestId('slot-tile-hit')).toBeFocused()

  // The page's data changes: the tile shows it, through the change feed.
  expect((await put(request, `/pages/${WEATHER}/weather.json`, { temp: 21, city: 'Lisbon', sky: 'Clear' })).ok()).toBeTruthy()
  await expect(frame.locator('#t')).toHaveText('21°')
  await expect(frame.locator('#city')).toHaveText('Lisbon')
  await expect(frame.locator('#boot')).toHaveText(boot)

  // In a chat the button opens the page in the dock beside it (page-dock.spec.js).
  await page.route('**/api/chats/c1/dock', (route) => route.fulfill({ json: { dock: route.request().postDataJSON() } }))
  await tileOf(page, 'chatHeader', NOTES).getByTestId('slot-tile-button').click()
  await expect(page).toHaveURL(/\/app\/chats\/c1$/)
  await expect(page.getByTestId('page-dock').locator(`[data-testid="dock-tab"][data-page="${NOTES}"]`)).toBeVisible()
})

test('screenshots: dark, phone, the sidebar footer and the composer toolbar', async ({ page, request, browser }) => {
  test.skip(!shots, 'SLOTS_SHOTS is not set')
  await setSlots(request, {
    chatHeader: [{ type: 'page', page: WEATHER }, { type: 'page', page: READING }],
    sidebarFooter: [{ type: 'page', page: FOCUS }, { type: 'tool', tool: 'plan_day', label: 'Plan my day', icon: 'calendar' }],
    composerToolbar: [{ type: 'tool', tool: 'summarize_chat', label: 'Summarize chat', icon: 'sparkles' }, { type: 'tool', tool: 'translate', label: 'Translate', icon: 'globe', arguments: { to: 'it' } }],
  })
  await openChat(page, { theme: 'dark' })
  const weather = tileOf(page, 'chatHeader', WEATHER)
  await expect(weather).toHaveAttribute('data-frame', 'live')
  await settle(page)
  await page.screenshot({ path: `${shots}/slots-header-dark.png` })
  await weather.getByTestId('slot-tile-hit').click()
  await expect(frameOf(weather).locator('#size')).toHaveText('widget medium')
  await settle(page)
  await page.screenshot({ path: `${shots}/slots-card-dark.png` })
  await page.keyboard.press('Escape')
  await settle(page)
  await page.screenshot({ path: `${shots}/slots-composer-dark.png` })
  // The sidebar footer, on a screen with the sidebar open.
  await page.goto('/app/pages')
  const focus = tileOf(page, 'sidebarFooter', FOCUS)
  await expect(focus).toHaveAttribute('data-frame', 'live')
  await settle(page)
  await page.screenshot({ path: `${shots}/slots-sidebar-dark.png` })
  await focus.getByTestId('slot-tile-hit').click()
  await expect(frameOf(focus).locator('#size')).toHaveCount(0)
  await expect(frameOf(focus).locator('html')).toHaveAttribute('data-size', 'small')
  await settle(page)
  await page.screenshot({ path: `${shots}/slots-sidebar-card-dark.png` })
  await page.keyboard.press('Escape')

  const phone = await browser.newContext({ viewport: { width: 390, height: 844 } })
  const p2 = await phone.newPage()
  await openChat(p2)
  const w2 = tileOf(p2, 'chatHeader', WEATHER)
  await expect(w2).toHaveAttribute('data-frame', 'live')
  await settle(p2)
  await p2.screenshot({ path: `${shots}/slots-header-phone.png` })
  await w2.getByTestId('slot-tile-hit').click()
  await expect(frameOf(w2).locator('#size')).toHaveText('widget medium')
  const card = await w2.getByTestId('slot-card').boundingBox()
  expect(card.x).toBeGreaterThanOrEqual(15)
  expect(card.x + card.width).toBeLessThanOrEqual(375)
  const scroll = await p2.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
  expect(scroll).toBeLessThanOrEqual(0)
  await settle(p2)
  await p2.screenshot({ path: `${shots}/slots-card-phone.png` })
  await phone.close()
})

test('at most eight frames run in the whole app; the rest are static cards that run on a click', async ({ page, request }) => {
  await setSlots(request, {
    chatHeader: many.slice(0, 4).map((p) => ({ type: 'page', page: p })),
    composerToolbar: many.slice(4, 10).map((p) => ({ type: 'page', page: p })),
  })
  await openChat(page)
  const tiles = page.locator('[data-testid="slot-tile"]')
  await expect(tiles).toHaveCount(10)
  const live = page.locator('[data-testid="slot-tile"][data-frame="live"]')
  const still = page.locator('[data-testid="slot-tile"][data-frame="static"]')
  await expect(live).toHaveCount(8)
  await expect(still).toHaveCount(2)
  await expect(page.locator('iframe[data-testid="page-frame"]')).toHaveCount(8)
  // A static card runs on a click; the frame that started first stops.
  const waiting = await still.first().getAttribute('data-page')
  await still.first().getByTestId('slot-tile-static').click()
  await expect(page.locator(`[data-testid="slot-tile"][data-page="${waiting}"]`)).toHaveAttribute('data-frame', 'live')
  await expect(live).toHaveCount(8)
  await expect(still).toHaveCount(2)
  await expect(page.locator('iframe[data-testid="page-frame"]')).toHaveCount(8)
  // Fewer items free the frames: every tile runs again.
  await setSlots(request, { chatHeader: many.slice(0, 2).map((p) => ({ type: 'page', page: p })) })
  await expect(tiles).toHaveCount(2)
  await expect(live).toHaveCount(2)
})

test('a frame that keeps the computer busy is stopped, with Reload', async ({ page, request }) => {
  test.setTimeout(120_000)
  await setSlots(request, { chatHeader: [{ type: 'page', page: SPIN }] })
  await openChat(page)
  const spin = tileOf(page, 'chatHeader', SPIN)
  await expect(spin).toHaveAttribute('data-frame', 'live')
  await expect(spin.getByTestId('slot-tile-stopped')).toBeVisible({ timeout: 30_000 })
  await expect(spin.getByTestId('slot-tile-stopped')).toHaveAttribute('data-reason', 'busy')
  await expect(spin.locator('iframe')).toHaveCount(0)
  await spin.getByTestId('slot-tile-stopped').click()
  await expect(spin).toHaveAttribute('data-frame', 'live')
})

test('a tool button asks first, then sends a visible request; it never runs on its own', async ({ page, request }) => {
  await setSlots(request, {
    composerToolbar: [{ type: 'tool', tool: 'summarize_chat', label: 'Summarize chat', icon: 'sparkles', arguments: { words: 50 } }],
    sidebarFooter: [{ type: 'tool', tool: 'plan_day', label: 'Plan my day', icon: 'calendar' }],
  })
  const sent = []
  await openChat(page, { sent })
  const button = slot(page, 'composerToolbar').getByTestId('slot-tool')
  await expect(button).toHaveText('Summarize chat')
  await button.click()
  const approve = page.getByTestId('slot-tool-approve')
  await expect(approve).toBeVisible()
  await expect(approve).toContainText('summarize_chat')
  await expect(approve).toContainText('{"words":50}')
  if (shots) {
    await settle(page)
    await page.screenshot({ path: `${shots}/slots-tool-approve-light.png` })
  }
  await page.getByRole('button', { name: 'Cancel' }).click()
  await expect(approve).toHaveCount(0)
  await page.waitForTimeout(300)
  expect(sent).toEqual([])
  await button.click()
  await page.getByRole('button', { name: 'Run', exact: true }).click()
  await expect.poll(() => sent.length).toBe(1)
  expect(sent[0].text).toBe('Run the tool summarize_chat with the arguments {"words":50}.')

  // Outside a chat the request becomes the draft of a new chat: the person sends it.
  await page.goto('/app/pages')
  await slot(page, 'sidebarFooter').getByTestId('slot-tool').click()
  await page.getByRole('button', { name: 'Run', exact: true }).click()
  await expect(page.locator('textarea').first()).toHaveValue('Run the tool plan_day.')
  expect(sent.length).toBe(1)
})

test('a command item puts the text of the command in the message box; a gone command shows nothing', async ({ page, request }) => {
  expect((await put(request, '/LocalAI/commands.json', { commands: [
    { name: 'standup', description: 'Write my standup', type: 'prompt', template: 'Write my standup for the last {{days}} day(s).', args: [{ name: 'days', default: 1 }] },
    { name: 'summarize', description: 'Summarize this chat', type: 'tool', tool: 'summarize_chat' },
  ] })).ok()).toBeTruthy()
  await setSlots(request, {
    composerToolbar: [{ type: 'command', command: 'standup' }, { type: 'command', command: 'gone' }, { type: 'command', command: 'summarize', label: 'Sum up', icon: 'sparkles' }],
    sidebarFooter: [{ type: 'command', command: 'standup' }],
  })
  const sent = []
  await openChat(page, { sent })
  const bar = slot(page, 'composerToolbar')
  const items = bar.getByTestId('slot-command')
  await expect(items).toHaveCount(2)
  await expect(items.first()).toHaveText('/standup')
  await expect(items.first()).toHaveAttribute('title', 'Write my standup')
  await expect(items.nth(1)).toHaveText('Sum up')
  const box = page.getByTestId('chat-composer').locator('textarea').first()
  await items.first().click()
  await expect(box).toHaveValue('Write my standup for the last 1 day(s).')
  await expect(box).toBeFocused()
  await items.nth(1).click()
  await expect(box).toHaveValue('Use the summarize_chat tool.')
  await page.waitForTimeout(300)
  expect(sent).toEqual([], 'a command item never sends')

  // Outside a chat the text becomes the draft of a new chat.
  await page.goto('/app/pages')
  await slot(page, 'sidebarFooter').getByTestId('slot-command').click()
  await expect(page.locator('textarea').first()).toHaveValue('Write my standup for the last 1 day(s).')
  expect(sent).toEqual([])

  // A command that leaves commands.json leaves the slot too.
  expect((await put(request, '/LocalAI/commands.json', { commands: [] })).ok()).toBeTruthy()
  await openChat(page, { sent })
  await expect(slot(page, 'composerToolbar')).toHaveCount(0)
})

test('the same sandbox as everywhere, and a slot item that is not a page shows nothing', async ({ page, request }) => {
  // The server refuses a traversal and markup in the file.
  for (const bad of [{ type: 'page', page: '../../etc' }, { type: 'page', page: 'x', tool: 'y' }, { type: 'tool', tool: 'a', label: 'A', icon: '<img src=x onerror=alert(1)>' }]) {
    const r = await request.put('/api/chats/wellknown/ui', { data: { values: { slots: { chatHeader: [bad] } } } })
    expect(r.status(), JSON.stringify(bad)).toBe(400)
  }
  // A label is text, never markup.
  await setSlots(request, { chatHeader: [{ type: 'page', page: WEATHER, label: '<img src=x onerror="window.__xss=1">' }] })
  // A file written around the validation (as a file tool would) is not used:
  // the bar keeps the last good version and never shows the bad items.
  expect((await put(request, '/LocalAI/ui.json', { slots: { chatHeader: [
    { type: 'page', page: '../../etc/passwd' },
    { type: 'frame', url: 'https://example.com' },
  ] } })).ok()).toBeTruthy()
  await openChat(page)
  const bar = slot(page, 'chatHeader')
  await expect(bar.locator('[data-testid="slot-tile"]')).toHaveCount(1)
  await expect(bar.locator('[data-testid="slot-tile"]')).toHaveAttribute('data-page', WEATHER)
  await expect(bar.locator('img')).toHaveCount(0)
  await expect(bar.getByTestId('slot-tile-hit')).toHaveAttribute('title', '<img src=x onerror="window.__xss=1">')
  expect(await page.evaluate(() => window.__xss)).toBeUndefined()
  const iframe = bar.locator('iframe[data-testid="page-frame"]')
  await expect(iframe).toHaveAttribute('sandbox', 'allow-scripts')
  await expect(iframe).not.toHaveAttribute('allow', /./)
  const doc = await iframe.getAttribute('srcdoc')
  expect(doc).toContain("default-src 'none'")
  expect(doc).toContain("connect-src 'none'")
  // The page cannot reach the window of LocalAI or the network.
  const probe = await frameOf(bar.locator('[data-testid="slot-tile"]')).locator('body').evaluate(async () => {
    const out = {}
    try { out.parent = String(window.parent.document.title) } catch (e) { out.parent = 'blocked' }
    try { await fetch('https://example.com/'); out.net = 'open' } catch (e) { out.net = 'blocked' }
    try { window.top.location.href = 'https://example.com/'; out.top = 'moved' } catch (e) { out.top = 'blocked' }
    out.popup = window.open('https://example.com/') === null ? 'blocked' : 'open'
    return out
  })
  expect(probe).toEqual({ parent: 'blocked', net: 'blocked', top: 'blocked', popup: 'blocked' })
  await expect(page).toHaveURL(/\/app\/chats\/c1$/)
})

test('the Slots tab adds a page tile and a tool button, and saves them in ui.json', async ({ page, request, browser }) => {
  await setSlots(request, { chatHeader: [{ type: 'page', page: WEATHER }] })
  await page.addInitScript(() => { try { localStorage.setItem('localai-theme', 'light') } catch { /* private mode */ } })
  await page.goto('/app/account?tab=slots')
  const card = page.getByTestId('wk-card-slots')
  await expect(card).toBeVisible()
  await expect(card.getByTestId('slots-cap')).toHaveText('1 of 4')
  await expect(card.getByTestId('slots-row')).toHaveCount(1)
  await expect(card.getByTestId('slots-row').first()).toContainText('Page tile, compact')
  await card.getByTestId('slots-add').click()
  await expect(card.getByTestId('slots-add-menu')).toBeVisible()
  if (shots) {
    await settle(page)
    await page.screenshot({ path: `${shots}/slots-editor-menu-light.png` })
  }
  await card.getByTestId('slots-add-page').click()
  await card.getByTestId('slots-form-page').selectOption(NOTES)
  await expect(card.getByTestId('slots-row').nth(1)).toContainText('Page, opens when clicked')
  // The composer toolbar: a tool button needs its tool and text.
  await card.getByTestId('slots-pick-composerToolbar').click()
  await card.getByTestId('slots-add').click()
  await card.getByTestId('slots-add-tool').click()
  await expect(card.getByTestId('wk-save-slots')).toBeDisabled()
  await card.getByTestId('slots-form-tool').fill('summarize_chat')
  await card.getByTestId('slots-form-label').fill('Summarize')
  await card.getByTestId('slots-form-arguments').fill('{"words": 80}')
  await expect(card.getByTestId('wk-save-slots')).toBeEnabled()
  if (shots) await page.screenshot({ path: `${shots}/slots-editor-light.png` })
  await card.getByTestId('wk-save-slots').click()
  await expect(card.getByRole('status')).toContainText('Saved')
  const ui = await request.get('/api/chats/wellknown/ui').then((r) => r.json())
  expect(ui.data.slots).toEqual({
    chatHeader: [{ type: 'page', page: WEATHER }, { type: 'page', page: NOTES }],
    composerToolbar: [{ type: 'tool', tool: 'summarize_chat', arguments: { words: 80 }, label: 'Summarize' }],
  })
  if (shots) {
    for (const [theme, viewport, name] of [['dark', { width: 1280, height: 800 }, 'dark'], ['light', { width: 390, height: 844 }, 'phone']]) {
      const ctx = await browser.newContext({ viewport })
      const p2 = await ctx.newPage()
      await p2.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private mode */ } }, theme)
      await p2.goto('/app/account?tab=slots')
      await expect(p2.getByTestId('slots-rows')).toBeVisible()
      await p2.getByTestId('slots-row-open').first().click()
      await settle(p2)
      await p2.screenshot({ path: `${shots}/slots-editor-${name}.png`, fullPage: name === 'phone' })
      const scroll = await p2.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      expect(scroll).toBeLessThanOrEqual(0)
      await ctx.close()
    }
  }
})
