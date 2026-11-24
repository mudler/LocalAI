import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'
import { b64, composer, offerMockModel } from './journey-fixtures.js'

// The dock beside a chat against the real test server and the mock model:
// pages in tabs, opened from the Pages list, from a slot and by the assistant's
// open_page tool; the width and the tabs kept with the chat and restored; the
// resize bounds and the keyboard; live data; the shared cap of eight live
// frames; chat.send that the person sends; and the phone sheet. DOCK_SHOTS
// names a folder for screenshots.

test.describe.configure({ mode: 'serial', timeout: 120_000 })
holdHomeFiles(test)

const stamp = Date.now().toString(36)
const BOARD = `dboard-${stamp}`
const NOTES = `dnotes-${stamp}`
const caps = Array.from({ length: 8 }, (_, i) => `dcap${i}-${stamp}`)
const shots = process.env.DOCK_SHOTS || ''

const put = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
const del = (request, path) => request.delete(`/api/chats/files?path=${encodeURIComponent(path)}`)
const api = (request, path) => request.get(path).then((r) => r.json())
const newChat = async (request, title) => {
  const r = await request.post('/api/chats', { data: { model: 'mock-model' } })
  expect(r.ok(), await r.text()).toBeTruthy()
  const c = await r.json()
  if (title) await request.patch(`/api/chats/${c.id}`, { data: { title } })
  return c.id
}
const dockOf = async (request, id) => (await api(request, `/api/chats/${id}/dock`)).dock
const settle = (page) => page.evaluate(() => Promise.all(document.getAnimations().filter((a) => a.effect?.getTiming().iterations !== Infinity).map((a) => a.finished.catch(() => {}))))
const shot = async (page, name) => {
  if (!shots) return
  await settle(page)
  await page.screenshot({ path: `${shots}/${name}.png` })
}
const setTheme = (page, theme) => page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private mode */ } }, theme)

const STYLE = `<style>
  :root { --bg: #ffffff; --fg: #121417; --muted: #5d6673; --line: #d9dde3; --col: #f1f3f6; --accent: #1f6feb; }
  :root[data-theme="dark"] { --bg: #16191d; --fg: #e6e9ee; --muted: #8b95a3; --line: #29313b; --col: #1d2228; --accent: #58a6ff; }
  html, body { margin: 0; background: var(--bg); color: var(--fg); font: 14px/1.4 system-ui, sans-serif; }
  main { padding: 16px; } h1 { margin: 0 0 12px; font-size: 18px; } h1 small { color: var(--muted); font-weight: 400; font-size: 13px; }
  .cols { display: grid; grid-template-columns: repeat(auto-fit, minmax(120px, 1fr)); gap: 10px; }
  .col { background: var(--col); border-radius: 8px; padding: 10px; min-height: 220px; }
  .col h2 { margin: 0 0 8px; font-size: 12px; letter-spacing: .04em; color: var(--muted); display: flex; justify-content: space-between; }
  .card { background: var(--bg); border: 1px solid var(--line); border-radius: 6px; padding: 8px; margin-bottom: 8px; }
  .done .card { color: var(--muted); text-decoration: line-through; }
  button { font: inherit; padding: 4px 10px; border-radius: 6px; border: 1px solid var(--line); background: var(--bg); color: var(--fg); }
  .probe { position: absolute; left: -9999px; }
</style>`
const BOARD_HTML = `<!doctype html><html><head>${STYLE}</head><body><main>
<h1>This week <small>Mon 9 to Sun 15</small></h1>
<div class="cols" id="cols"></div>
<p><button id="ask" type="button">Ask the assistant</button> <span id="sent"></span></p>
<p class="probe"><span id="mode"></span> <span id="count"></span></p>
<script>
document.getElementById('mode').textContent = localai.page.mode
function draw(d) {
  const cols = document.getElementById('cols')
  cols.textContent = ''
  for (const c of d.columns) {
    const col = document.createElement('section'); col.className = 'col' + (c.done ? ' done' : '')
    const h = document.createElement('h2'); h.textContent = c.name
    const n = document.createElement('span'); n.textContent = String(c.cards.length); h.appendChild(n); col.appendChild(h)
    for (const t of c.cards) { const e = document.createElement('div'); e.className = 'card'; e.textContent = t; col.appendChild(e) }
    cols.appendChild(col)
  }
  document.getElementById('count').textContent = String(d.columns.reduce((s, c) => s + c.cards.length, 0))
}
function load() { fetch('tasks.json').then((r) => r.json()).then(draw) }
load()
localai.on('files.changed', load)
document.getElementById('ask').addEventListener('click', () => {
  localai.chat.send('Move the dentist to Friday').then((r) => { document.getElementById('sent').textContent = r.sent ? 'sent' : 'kept' })
})
</script></main></body></html>`
const TASKS = (extra = []) => ({ columns: [
  { name: 'TO DO', cards: ['Buy paint for the gate', 'Call the plumber', 'Book dentist', ...extra] },
  { name: 'DOING', cards: ['Draft invoice for Rossi', 'Sand the gate'] },
  { name: 'DONE', done: true, cards: ['Renew insurance'] },
] })
const NOTES_HTML = `<!doctype html><html><head>${STYLE}</head><body><main><h1>Reading list</h1>
<div class="card">The Mythical Man-Month</div><div class="card">A Pattern Language</div><div class="card">Thinking in Systems</div></main></body></html>`
const capHtml = (i) => `<!doctype html><html><head>${STYLE}</head><body><b>Tile ${i}</b></body></html>`

test.beforeAll(async ({ request }) => {
  const files = {
    [`/pages/${BOARD}/index.html`]: BOARD_HTML,
    [`/pages/${BOARD}/page.json`]: { title: 'Task board', icon: 'columns' },
    [`/pages/${BOARD}/tasks.json`]: TASKS(),
    [`/pages/${NOTES}/index.html`]: NOTES_HTML,
    [`/pages/${NOTES}/page.json`]: { title: 'Reading list', icon: 'book' },
  }
  caps.forEach((name, i) => {
    files[`/pages/${name}/index.html`] = capHtml(i)
    files[`/pages/${name}/page.json`] = { title: `Tile ${i}`, widget: { size: 'small', compact: { width: 80 } } }
  })
  for (const [p, body] of Object.entries(files)) expect((await put(request, p, body)).ok(), p).toBeTruthy()
  await del(request, '/LocalAI/ui.json')
})
test.afterAll(async ({ request }) => {
  for (const p of ['/LocalAI/ui.json', `/pages/${BOARD}`, `/pages/${NOTES}`, ...caps.map((n) => `/pages/${n}`)]) await del(request, p)
})
test.afterAll(() => unlockHomeFiles())

const dock = (page) => page.getByTestId('page-dock')
const dockFrame = (page) => dock(page).frameLocator('[data-testid="page-frame"]')
const tabs = (page) => dock(page).getByTestId('dock-tab')

test('there is no dock until something opens it; Ctrl+\\ and the Dock button show the page list', async ({ page, request }) => {
  const id = await newChat(request)
  await page.goto(`/app/chats/${id}`)
  await expect(page.getByTestId('chat-header')).toBeVisible()
  await expect(dock(page)).toHaveCount(0)
  await expect(page.getByTestId('dock-toggle')).toHaveAttribute('aria-pressed', 'false')

  await page.keyboard.press('Control+Backslash')
  await expect(dock(page)).toBeVisible()
  await expect(dock(page).getByTestId('dock-picker')).toContainText('Task board')
  await page.keyboard.press('Control+Backslash')
  await expect(dock(page)).toHaveCount(0)

  await page.getByTestId('dock-toggle').click()
  await dock(page).locator(`[data-testid="dock-pick"][data-page="${BOARD}"]`).click()
  await expect(tabs(page)).toHaveCount(1)
  await expect(dockFrame(page).locator('#count')).toHaveText('6')
  await expect(dockFrame(page).locator('#mode')).toHaveText('page')
  await expect.poll(() => dockOf(request, id)).toMatchObject({ open: true, tabs: [{ page: BOARD }], active: 0 })
  await request.delete(`/api/chats/${id}`)
})

test('Open in dock on the Pages list opens the page beside a new chat, which keeps it after its first message', async ({ page, request }) => {
  await setTheme(page, 'light')
  await offerMockModel(page)
  await page.goto('/app/pages')
  const row = page.locator(`[data-testid="page-card"][data-page="${BOARD}"]`)
  await row.getByRole('button', { name: /More for Task board/ }).click()
  if (shots) await shot(page, 'dock-pages-list-action')
  await page.getByRole('menuitem', { name: 'Open in dock' }).click()
  await expect(page.getByTestId('chats-page')).toBeVisible()
  await expect(tabs(page)).toHaveCount(1)
  await expect(tabs(page).first()).toContainText('Task board')
  await expect(dockFrame(page).locator('#count')).toHaveText('6')

  await composer(page).fill('what is on the board?')
  await page.getByRole('button', { name: 'Send' }).click()
  await page.waitForURL(/\/app\/chats\/[0-9a-f]+$/)
  const id = page.url().split('/').pop()
  await expect.poll(() => dockOf(request, id)).toMatchObject({ open: true, tabs: [{ page: BOARD }] })
  await expect(dockFrame(page).locator('#count')).toHaveText('6')
  await request.delete(`/api/chats/${id}`)
})

test('the assistant opens a page in the dock with open_page, without asking; a shared page is refused', async ({ page, request }) => {
  await offerMockModel(page)
  const id = await newChat(request, 'Plan the week')
  await page.goto(`/app/chats/${id}`)
  await expect(page.getByTestId('chat-header')).toBeVisible()
  await composer(page).fill(`show me the board ADDON_TOOL_CALL:open_page:${b64({ name: BOARD })}`)
  await page.getByRole('button', { name: 'Send' }).click()
  await expect(dock(page)).toBeVisible({ timeout: 30_000 })
  await expect(tabs(page)).toHaveCount(1)
  await expect(dockFrame(page).locator('#count')).toHaveText('6')
  await expect(page.getByTestId('host-exec-consent')).toHaveCount(0)
  await expect.poll(async () => (await api(request, `/api/chats/${id}`)).running, { timeout: 60_000 }).toBe(false)
  expect(await dockOf(request, id)).toMatchObject({ open: true, tabs: [{ page: BOARD }] })

  await composer(page).fill(`and the shared one ADDON_TOOL_CALL:open_page:${b64({ name: NOTES, folder: 3 })}`)
  await page.getByRole('button', { name: 'Send' }).click()
  await expect.poll(async () => (await api(request, `/api/chats/${id}`)).running, { timeout: 60_000 }).toBe(false)
  expect((await dockOf(request, id)).tabs).toEqual([{ page: BOARD }])
  await request.delete(`/api/chats/${id}`)
})

test('a page button and a compact tile of a slot open the page in the dock of the chat', async ({ page, request }) => {
  const r = await request.put('/api/chats/wellknown/ui', { data: { values: { slots: { chatHeader: [{ type: 'page', page: NOTES }, { type: 'page', page: caps[0] }] } } } })
  expect(r.ok(), await r.text()).toBeTruthy()
  const id = await newChat(request)
  try {
    await page.goto(`/app/chats/${id}`)
    const bar = page.getByTestId('slot-chatHeader')
    await bar.getByTestId('slot-tile-button').click()
    await expect(page).toHaveURL(new RegExp(`/app/chats/${id}$`), { timeout: 2000 })
    await expect(tabs(page)).toHaveCount(1)
    await expect(tabs(page).first()).toContainText('Reading list')

    await bar.locator(`[data-testid="slot-tile"][data-page="${caps[0]}"]`).getByTestId('slot-tile-hit').click()
    await page.getByTestId('slot-card-open').click()
    await expect(tabs(page)).toHaveCount(2)
    await expect(tabs(page).nth(1)).toHaveAttribute('data-active', 'true')
    await expect.poll(async () => (await dockOf(request, id))?.tabs).toEqual([{ page: NOTES }, { page: caps[0] }])
  } finally {
    await del(request, '/LocalAI/ui.json')
    await request.delete(`/api/chats/${id}`)
  }
})

test('the edge resizes the dock between 320 px and 60% of the window, by mouse and keys; the thread narrows', async ({ page, request }) => {
  await setTheme(page, 'light')
  await page.setViewportSize({ width: 1440, height: 900 })
  const id = await newChat(request, 'Plan the week')
  await request.put(`/api/chats/${id}/dock`, { data: { open: true, tabs: [{ page: BOARD }], active: 0 } })
  await page.goto(`/app/chats/${id}`)
  const edge = dock(page).getByTestId('dock-edge')
  await expect(edge).toHaveAttribute('aria-valuenow', '452')
  const width = async () => Math.round((await dock(page).boundingBox()).width)
  expect(await width()).toBe(452)

  const box = await edge.boundingBox()
  const y = box.y + box.height / 2
  await page.mouse.move(box.x + 4, y)
  await page.mouse.down()
  await page.mouse.move(100, y, { steps: 6 })
  await page.mouse.up()
  expect(await width()).toBe(864)
  if (shots) await shot(page, 'dock-resize-max-light')
  const b2 = await edge.boundingBox()
  await page.mouse.move(b2.x + 4, y)
  await page.mouse.down()
  await page.mouse.move(1430, y, { steps: 6 })
  await page.mouse.up()
  expect(await width()).toBe(320)

  await edge.dblclick()
  expect(await width()).toBe(452)
  await edge.focus()
  await page.keyboard.press('ArrowLeft')
  await expect(edge).toHaveAttribute('aria-valuenow', '468')
  await page.keyboard.press('End')
  await expect(edge).toHaveAttribute('aria-valuenow', '864')
  await page.keyboard.press('Home')
  await expect(edge).toHaveAttribute('aria-valuenow', '320')
  await page.keyboard.press('Shift+ArrowLeft')
  await expect(edge).toHaveAttribute('aria-valuenow', '384')
  await expect.poll(async () => (await dockOf(request, id)).width).toBe(384)

  // Nothing of the conversation lies under the dock: the header, the thread
  // and the composer end where it starts.
  const d = await dock(page).boundingBox()
  for (const tid of ['chat-header', 'chat-composer']) {
    const b = await page.getByTestId(tid).boundingBox()
    expect(b.x + b.width, tid).toBeLessThanOrEqual(d.x + 1)
  }
  await request.delete(`/api/chats/${id}`)
})

test('each chat keeps its own dock: tabs and width come back after a reload', async ({ page, request }) => {
  const a = await newChat(request, 'Plan the week')
  const b = await newChat(request, 'Trip to Lisbon')
  await request.put(`/api/chats/${a}/dock`, { data: { open: true, tabs: [{ page: BOARD }, { page: NOTES }], active: 1, width: 520 } })
  await page.goto(`/app/chats/${a}`)
  await expect(tabs(page)).toHaveCount(2)
  await expect(tabs(page).nth(1)).toHaveAttribute('data-active', 'true')
  expect(Math.round((await dock(page).boundingBox()).width)).toBe(520)

  await tabs(page).first().getByRole('tab').click()
  await expect.poll(async () => (await dockOf(request, a)).active).toBe(0)
  await page.reload()
  await expect(tabs(page).first()).toHaveAttribute('data-active', 'true')
  await expect(dockFrame(page).locator('#count')).toHaveText('6')
  expect(Math.round((await dock(page).boundingBox()).width)).toBe(520)

  await page.goto(`/app/chats/${b}`)
  await expect(page.getByTestId('chat-header')).toBeVisible()
  await expect(dock(page)).toHaveCount(0)
  await page.goto(`/app/chats/${a}`)
  await expect(tabs(page)).toHaveCount(2)

  // Closing the last tabs closes the dock; the chat keeps that too.
  await tabs(page).nth(1).getByTestId('dock-tab-close').click()
  await tabs(page).first().getByTestId('dock-tab-close').click()
  await expect(dock(page)).toHaveCount(0)
  await expect.poll(() => dockOf(request, a)).toBeNull()
  await request.delete(`/api/chats/${a}`)
  await request.delete(`/api/chats/${b}`)
})

test('a docked page is live, sandboxed, and its chat.send waits for the person to send it to this chat', async ({ page, request }) => {
  await setTheme(page, 'light')
  await offerMockModel(page)
  await page.setViewportSize({ width: 1280, height: 800 })
  const id = await newChat(request, 'Plan the week')
  await request.put(`/api/chats/${id}/dock`, { data: { open: true, tabs: [{ page: BOARD }, { page: NOTES }], active: 0 } })
  await page.goto(`/app/chats/${id}`)
  await expect(dockFrame(page).locator('#count')).toHaveText('6')
  await shot(page, 'dock-open-light')
  await expect(dock(page).getByTestId('page-frame')).toHaveAttribute('sandbox', 'allow-scripts')
  await expect(dock(page).getByTestId('dock-origin')).toHaveText('Page by you')
  await expect(dock(page).getByTestId('dock-runs-as-you')).toHaveText('Runs as you')
  await expect(dock(page).getByTestId('dock-live')).toHaveText('Live: updates as files change')
  await expect(dock(page)).toContainText('Sandboxed, no network')

  expect((await put(request, `/pages/${BOARD}/tasks.json`, TASKS(['Fix the garden gate']))).ok()).toBeTruthy()
  await expect(dockFrame(page).locator('#count')).toHaveText('7', { timeout: 15_000 })
  await expect(dockFrame(page).getByText('Fix the garden gate')).toBeVisible()

  // Ask the assistant to change this: a request naming the page, not sent.
  await dock(page).getByTestId('dock-ask').click()
  await expect(composer(page)).toHaveValue(`Change the page Task board in pages/${BOARD}/ so that `)
  await composer(page).fill('')

  const frameBox = await dock(page).getByTestId('page-frame').boundingBox()
  await dockFrame(page).locator('#ask').click()
  const ask = dock(page).getByTestId('dock-send-ask')
  await expect(ask).toContainText('Move the dentist to Friday')
  // Send works only after a short delay, and the question lies over the
  // frame: the frame does not move under the pointer.
  await expect(ask.getByTestId('dock-send')).toBeDisabled()
  expect(await dock(page).getByTestId('page-frame').boundingBox()).toEqual(frameBox)
  await expect(ask.getByTestId('dock-send')).toBeEnabled()
  await shot(page, 'dock-send-light')
  const before = (await api(request, `/api/chats/${id}/messages`)).messages.length
  await page.waitForTimeout(500)
  expect((await api(request, `/api/chats/${id}/messages`)).messages.length).toBe(before)
  await ask.getByTestId('dock-send').click()
  await expect(dockFrame(page).locator('#sent')).toHaveText('sent')
  await expect(page.getByTestId('chat-thread')).toContainText('Move the dentist to Friday', { timeout: 15_000 })
  await expect.poll(async () => (await api(request, `/api/chats/${id}`)).running, { timeout: 60_000 }).toBe(false)
  await expect(dockFrame(page).locator('#count')).toHaveText('7')
  await put(request, `/pages/${BOARD}/tasks.json`, TASKS())
  await request.delete(`/api/chats/${id}`)
})

test('a docked page keeps running past the start budget of its frame', async ({ page, request }) => {
  const id = await newChat(request, 'Long look')
  await request.put(`/api/chats/${id}/dock`, { data: { open: true, tabs: [{ page: BOARD }], active: 0 } })
  await page.goto(`/app/chats/${id}`)
  await expect(dockFrame(page).locator('#count')).toHaveText('6')
  // The watch stops a frame with no first beat after 10 s.
  await page.waitForTimeout(13_000)
  await expect(dock(page).getByTestId('dock-stopped')).toHaveCount(0)
  await expect(dock(page).getByTestId('dock-body')).toHaveAttribute('data-frame', 'live')
  await expect(dockFrame(page).locator('#count')).toHaveText('6')
  await request.delete(`/api/chats/${id}`)
})

test('the dock looks right in the dark theme', async ({ page, request }) => {
  await setTheme(page, 'dark')
  await page.setViewportSize({ width: 1280, height: 800 })
  const id = await newChat(request, 'Plan the week')
  await request.put(`/api/chats/${id}/dock`, { data: { open: true, tabs: [{ page: BOARD }, { page: NOTES }], active: 0 } })
  await page.goto(`/app/chats/${id}`)
  await expect(dockFrame(page).locator('#count')).toHaveText('6')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await shot(page, 'dock-open-dark')
  await request.delete(`/api/chats/${id}`)
})

test('dock frames count toward the cap of eight live frames', async ({ page, request }) => {
  await page.setViewportSize({ width: 1600, height: 900 })
  const slots = {
    chatHeader: caps.slice(0, 4).map((p) => ({ type: 'page', page: p })),
    composerToolbar: caps.slice(4, 8).map((p) => ({ type: 'page', page: p })),
  }
  expect((await request.put('/api/chats/wellknown/ui', { data: { values: { slots } } })).ok()).toBeTruthy()
  const id = await newChat(request)
  try {
    await page.goto(`/app/chats/${id}`)
    const live = page.locator('[data-testid="slot-tile"][data-frame="live"]')
    await expect(live).toHaveCount(8)
    const frames = page.locator('iframe[data-testid="page-frame"]')
    await expect(frames).toHaveCount(8)

    // A page the person opens takes a frame: the oldest tile stops.
    await page.getByTestId('dock-toggle').click()
    await dock(page).locator(`[data-testid="dock-pick"][data-page="${BOARD}"]`).click()
    await expect(dockFrame(page).locator('#count')).toHaveText('6')
    await expect(live).toHaveCount(7)
    await expect(frames).toHaveCount(8)

    // On a reload the dock waits for a free frame like any other: never nine.
    await page.reload()
    await expect(page.locator('[data-testid="slot-tile"]')).toHaveCount(8)
    await expect.poll(() => frames.count()).toBe(8)
    const body = dock(page).getByTestId('dock-body')
    if ((await body.getAttribute('data-frame')) !== 'live') {
      await expect(dock(page).getByTestId('dock-static')).toBeVisible()
      await dock(page).getByTestId('dock-run').click()
      await expect(dockFrame(page).locator('#count')).toHaveText('6')
    }
    await expect(frames).toHaveCount(8)
  } finally {
    await del(request, '/LocalAI/ui.json')
    await request.delete(`/api/chats/${id}`)
  }
})

test('on a phone the dock is a bottom sheet, and nothing scrolls sideways', async ({ page, request }) => {
  await setTheme(page, 'light')
  await page.setViewportSize({ width: 390, height: 844 })
  const id = await newChat(request, 'Plan the week')
  await request.put(`/api/chats/${id}/dock`, { data: { open: true, tabs: [{ page: BOARD }], active: 0, width: 900 } })
  await page.goto(`/app/chats/${id}`)
  await expect(dockFrame(page).locator('#count')).toHaveText('6')
  const d = await dock(page).boundingBox()
  expect(Math.round(d.x)).toBe(0)
  expect(Math.round(d.width)).toBe(390)
  expect(Math.round(d.y + d.height)).toBe(844)
  expect(d.y).toBeGreaterThan(56)
  await expect(dock(page).getByTestId('dock-edge')).toHaveCount(0)
  const overflow = await page.evaluate(() => document.scrollingElement.scrollWidth - document.scrollingElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
  await shot(page, 'dock-phone-light')
  await dock(page).getByTestId('dock-handle').click()
  await expect(dock(page)).toHaveAttribute('data-full', 'true')
  await shot(page, 'dock-phone-full-light')
  await dock(page).getByTestId('dock-handle').click()

  // The handle makes it full screen, under the chat header, and back.
  await dock(page).getByTestId('dock-handle').click()
  await expect(dock(page)).toHaveAttribute('data-full', 'true')
  const header = await page.getByTestId('chat-header').boundingBox()
  expect((await dock(page).boundingBox()).y).toBeGreaterThanOrEqual(header.y + header.height - 1)
  await dock(page).getByTestId('dock-handle').click()
  await expect(dock(page)).not.toHaveAttribute('data-full', 'true')
  await dock(page).getByTestId('dock-hide').click()
  await expect(dock(page)).toHaveCount(0)
  await request.delete(`/api/chats/${id}`)
})

test('the + of the tabs always opens the page list for a new tab, and a page of the list opens and closes it', async ({ page, request }) => {
  const id = await newChat(request)
  try {
    await page.goto(`/app/chats/${id}`)
    const add = dock(page).getByTestId('dock-add')
    const picker = dock(page).getByTestId('dock-picker')
    const row = (name) => dock(page).locator(`[data-testid="dock-pick"][data-page="${name}"]`)

    // A dock with no tab already shows the list: the + still answers, by
    // keeping it and putting the focus on its first page.
    await page.getByTestId('dock-toggle').click()
    await expect(picker).toBeVisible()
    await add.click()
    await expect(picker).toBeVisible()
    await expect(add).toHaveAttribute('aria-pressed', 'true')
    await expect(dock(page).locator('[data-testid="dock-pick"]:focus')).toHaveCount(1)
    await add.click()
    await expect(picker).toBeVisible()

    // Picking opens the page and closes the list.
    await row(BOARD).click()
    await expect(tabs(page)).toHaveCount(1)
    await expect(picker).toHaveCount(0)
    await expect(add).toHaveAttribute('aria-pressed', 'false')
    await expect(dockFrame(page).locator('#count')).toHaveText('6')

    // With a tab, the + opens the list over it; a second press keeps it.
    await add.click()
    await expect(picker).toBeVisible()
    await expect(add).toHaveAttribute('aria-pressed', 'true')
    await expect(tabs(page).first()).not.toHaveAttribute('data-active', 'true')
    await add.click()
    await expect(picker).toBeVisible()
    await shot(page, 'dock-plus-picker')

    // Escape, and the tab in front, go back to the page.
    await page.keyboard.press('Escape')
    await expect(picker).toHaveCount(0)
    await expect(tabs(page).first()).toHaveAttribute('data-active', 'true')
    await add.click()
    await expect(picker).toBeVisible()
    await tabs(page).first().getByRole('tab').click()
    await expect(picker).toHaveCount(0)

    // A second page: new tab in front, list closed.
    await add.click()
    await row(NOTES).click()
    await expect(tabs(page)).toHaveCount(2)
    await expect(picker).toHaveCount(0)
    await expect(tabs(page).nth(1)).toHaveAttribute('data-active', 'true')
    await expect.poll(async () => (await dockOf(request, id))?.tabs).toEqual([{ page: BOARD }, { page: NOTES }])

    // A tab that closes while the list is open ends the request.
    await add.click()
    await expect(picker).toBeVisible()
    await tabs(page).first().getByTestId('dock-tab-close').click()
    await expect(tabs(page)).toHaveCount(1)
    await expect(picker).toHaveCount(0)
    await expect(add).toHaveAttribute('aria-pressed', 'false')
  } finally {
    await request.delete(`/api/chats/${id}`)
  }
})

test('Files and Dock are two buttons of the header that share the right side, one panel at a time', async ({ page, request }) => {
  await setTheme(page, 'light')
  const id = await newChat(request)
  try {
    await page.goto(`/app/chats/${id}`)
    const header = page.getByTestId('chat-header')
    const filesBtn = header.getByTestId('chat-files-toggle')
    const dockBtn = header.getByTestId('dock-toggle')
    const filesPane = page.getByTestId('chat-files-pane')
    await expect(filesBtn).toBeVisible()
    // The Files button sits just before the Dock button, in the same style.
    const [a, b] = [await filesBtn.boundingBox(), await dockBtn.boundingBox()]
    expect(a.x + a.width).toBeLessThanOrEqual(b.x)
    expect(Math.abs(a.height - b.height)).toBeLessThanOrEqual(1)
    await expect(filesBtn).toHaveAttribute('aria-pressed', 'false')
    await expect(filesBtn).toHaveAttribute('title', /Files in this chat/)
    // The old entry in the message box is gone.
    await expect(page.locator('.cx-dock').getByTestId('chat-files-toggle')).toHaveCount(0)

    await filesBtn.click()
    await expect(filesPane).toBeVisible()
    await expect(filesBtn).toHaveAttribute('aria-pressed', 'true')
    await shot(page, 'dock-files-header-files')

    // The dock takes the side; the pane hides.
    await dockBtn.click()
    await expect(dock(page)).toBeVisible()
    await expect(filesPane).toHaveCount(0)
    await expect(filesBtn).toHaveAttribute('aria-pressed', 'false')
    await expect(dockBtn).toHaveAttribute('aria-pressed', 'true')
    await shot(page, 'dock-files-header-dock')

    // Files takes it back; the dock hides, and its choice is kept with the chat.
    await filesBtn.click()
    await expect(filesPane).toBeVisible()
    await expect(dock(page)).toHaveCount(0)
    await expect(dockBtn).toHaveAttribute('aria-pressed', 'false')
    await expect.poll(async () => (await dockOf(request, id))?.open).toBeFalsy()

    // Ctrl+\ is still the dock's key, and it also gives the side back.
    await page.keyboard.press('Control+Backslash')
    await expect(dock(page)).toBeVisible()
    await expect(filesPane).toHaveCount(0)
    await page.keyboard.press('Control+Backslash')
    await expect(dock(page)).toHaveCount(0)
    await expect(filesPane).toHaveCount(0)

    // Never two panels: the right side holds at most one.
    await filesBtn.click()
    await expect(page.locator('[data-testid="page-dock"], [data-testid="chat-files-pane"]')).toHaveCount(1)
  } finally {
    await request.delete(`/api/chats/${id}`)
  }
})

test('a file the assistant writes never takes the side from an open dock: the Files count grows and the pane stays closed', async ({ page, request }) => {
  await offerMockModel(page)
  const id = await newChat(request, 'Report')
  await request.put(`/api/chats/${id}/dock`, { data: { open: true, tabs: [{ page: BOARD }], active: 0 } })
  const report = `/dock-files-${stamp}/report.md`
  try {
    await page.goto(`/app/chats/${id}`)
    await expect(dockFrame(page).locator('#count')).toHaveText('6')
    await composer(page).fill(`write a report ADDON_TOOL_CALL:write_file:${b64({ path: report, content: '# Report' })}`)
    await page.getByRole('button', { name: 'Send' }).click()
    await expect(page.getByTestId('chat-files-count')).toHaveText('1', { timeout: 30_000 })
    await expect(dock(page)).toBeVisible()
    await expect(page.getByTestId('chat-files-pane')).toHaveCount(0)
    await expect.poll(async () => (await api(request, `/api/chats/${id}`)).running, { timeout: 60_000 }).toBe(false)
    expect(await dockOf(request, id)).toMatchObject({ open: true })
    // Files is one press away, and takes the side.
    await page.getByTestId('chat-files-toggle').click()
    await expect(page.getByTestId('chat-files-pane').getByTestId('chat-files-created')).toContainText('report.md')
    await expect(dock(page)).toHaveCount(0)
  } finally {
    await del(request, `/dock-files-${stamp}`)
    await request.delete(`/api/chats/${id}`)
  }
})
