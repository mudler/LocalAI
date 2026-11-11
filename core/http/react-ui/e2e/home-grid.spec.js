import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'

// The Home grid, against the real test server: every widget has a size (s, m,
// l), the grid has 4, 2 or 1 columns, and any mix of 12 widgets in any order
// packs with no overlap and a full last row (dense flow fills what it can; a
// hole higher up stays). The layout editor draws the same grid and puts
// an S/M/L control on every row. HOME_SHOTS names a folder for screenshots.

test.describe.configure({ mode: 'serial', timeout: 90_000 })
holdHomeFiles(test)

const stamp = Date.now().toString(36)
const shots = process.env.HOME_SHOTS || ''
const PAGES = [`gclock-${stamp}`, `gnews-${stamp}`, `gboard-${stamp}`]
const WIDTHS = [[1280, 4], [900, 2], [390, 1]]

const put = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
const del = (request, path) => request.delete(`/api/chats/files?path=${encodeURIComponent(path)}`)
const read = async (request, path) => JSON.parse(await (await request.get(`/api/chats/files/content?path=${encodeURIComponent(path)}`)).text())

const PAGE_HTML = (name) => `<!doctype html><html><body style="font:14px sans-serif;margin:10px"><b>${name}</b><p>Compact view</p></body></html>`
const LIST = `data/g-list-${stamp}.json`
const METRIC = `data/g-metric-${stamp}.json`

// Twelve widgets of every type; the order and the sizes come from the mix.
function twelve(sizes, order) {
  const all = [
    ...PAGES.map((p, i) => ({ id: `pg${i}`, type: 'page', page: p })),
    ...[0, 1, 2].map(i => ({ id: `ls${i}`, type: 'list', title: `List ${i}`, source: LIST })),
    ...[0, 1, 2].map(i => ({ id: `mt${i}`, type: 'metric', title: `Metric ${i}`, source: METRIC })),
    ...[0, 1, 2].map(i => ({ id: `ch${i}`, type: 'chats', title: `Chats ${i}` })),
  ]
  const ordered = order.map(i => all[i])
  return ordered.map((w, i) => (sizes[i] ? { ...w, size: sizes[i] } : w))
}

let seed = 7
const rnd = () => { seed = (seed * 1664525 + 1013904223) % 4294967296; return seed / 4294967296 }
const shuffle = () => {
  const a = [...Array(12).keys()]
  for (let i = 11; i > 0; i--) { const j = Math.floor(rnd() * (i + 1)); [a[i], a[j]] = [a[j], a[i]] }
  return a
}
const pick = () => Array.from({ length: 12 }, () => ['s', 'm', 'l'][Math.floor(rnd() * 3)])
const MIXES = [
  { name: 'all s', sizes: Array(12).fill('s'), order: [...Array(12).keys()], full: true },
  { name: 'all m', sizes: Array(12).fill('m'), order: [...Array(12).keys()], full: true },
  { name: 'all l', sizes: Array(12).fill('l'), order: [...Array(12).keys()], full: true },
  { name: 'alternating m and s', sizes: Array.from({ length: 12 }, (_, i) => (i % 2 ? 's' : 'm')), order: [...Array(12).keys()], full: true },
  { name: 'an s before an l', sizes: ['s', 'l', ...Array(10).fill('m')], order: [...Array(12).keys()] },
  ...[1, 2, 3, 4, 5].map(n => ({ name: `random ${n}`, sizes: pick(), order: shuffle() })),
]

// The cells of the grid that each tile covers, from where the tile is drawn,
// not from the data attributes: what a person sees.
async function cellsOf(page, gridId, tileSel) {
  return page.evaluate(([gid, sel]) => {
    const grid = document.querySelector(`[data-testid="${gid}"]`)
    const cs = getComputedStyle(grid)
    const gap = parseFloat(cs.columnGap)
    const unit = parseFloat(cs.gridAutoRows)
    const cols = cs.gridTemplateColumns.split(' ').length
    const g = grid.getBoundingClientRect()
    const colW = (g.width - gap * (cols - 1)) / cols
    const tiles = [...grid.querySelectorAll(`:scope > ${sel}`)].map((el) => {
      const r = el.getBoundingClientRect()
      return {
        id: el.dataset.testid,
        col: Math.round((r.left - g.left) / (colW + gap)),
        row: Math.round((r.top - g.top) / (unit + gap)),
        cw: Math.round((r.width + gap) / (colW + gap)),
        rh: Math.round((r.height + gap) / (unit + gap)),
        w: r.width,
        size: el.dataset.size || '',
        right: r.right,
      }
    })
    return { cols, rows: Math.round((g.height + gap) / (unit + gap)), gridW: g.width, gridRight: g.right, tiles, datacols: Number(grid.dataset.cols), holes: Number(grid.dataset.holes) }
  }, [gridId, tileSel])
}

// No cell is covered twice, the last row is full, and the grid says how many
// cells are empty: a hole above the last row stays, as dense flow left it.
function expectPacked(c, label) {
  expect(c.cols, `${label}: columns`).toBe(c.datacols)
  const seen = new Map()
  for (const t of c.tiles) {
    expect(t.col, `${label}: ${t.id} starts inside the grid`).toBeGreaterThanOrEqual(0)
    expect(t.col + t.cw, `${label}: ${t.id} ends inside the grid`).toBeLessThanOrEqual(c.cols)
    expect(t.right, `${label}: ${t.id} is not wider than the grid`).toBeLessThanOrEqual(c.gridRight + 1)
    for (let y = t.row; y < t.row + t.rh; y++) for (let x = t.col; x < t.col + t.cw; x++) {
      const k = `${y},${x}`
      expect(seen.has(k), `${label}: ${t.id} overlaps ${seen.get(k)} at ${k}`).toBe(false)
      seen.set(k, t.id)
    }
  }
  for (let x = 0; x < c.cols; x++) expect(seen.has(`${c.rows - 1},${x}`), `${label}: empty cell in the last row, column ${x}`).toBe(true)
  expect(c.holes, `${label}: holes`).toBe(c.rows * c.cols - seen.size)
}

async function openHome(page, theme = 'light') {
  await page.route('**/api/chats/triggers*', (r) => r.fulfill({ json: { triggers: [] } }))
  await page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private mode */ } }, theme)
  await page.goto('/app')
  await expect(page.getByTestId('home-widget-grid')).toBeVisible()
}

test.beforeAll(async ({ request }) => {
  const files = {
    [`/data/g-list-${stamp}.json`]: { items: Array.from({ length: 10 }, (_, i) => ({ label: `Task number ${i + 1}`, meta: 'Fri', done: i % 3 === 0 })) },
    [`/data/g-metric-${stamp}.json`]: { value: 42, note: 'since Monday', series: [3, 5, 4, 8, 7, 12, 11] },
  }
  PAGES.forEach((p, i) => {
    files[`/pages/${p}/index.html`] = PAGE_HTML(p)
    files[`/pages/${p}/page.json`] = { title: ['Clock', 'News', 'Board'][i], icon: 'file-text', widget: { size: ['small', 'large', 'medium'][i] } }
  })
  for (const [p, body] of Object.entries(files)) expect((await put(request, p, body)).ok(), p).toBeTruthy()
})
test.afterAll(async ({ request }) => {
  for (const p of ['/LocalAI/home.json', `/data/g-list-${stamp}.json`, `/data/g-metric-${stamp}.json`, ...PAGES.map(n => `/pages/${n}`)]) await del(request, p)
})
test.afterAll(() => unlockHomeFiles())

for (const mix of MIXES) {
  test(`12 widgets, ${mix.name}: a full last row at 1280, 900 and 390 px`, async ({ page, request }) => {
    const widgets = twelve(mix.sizes, mix.order)
    expect((await put(request, '/LocalAI/home.json', { widgets })).ok()).toBeTruthy()
    await page.setViewportSize({ width: 1280, height: 900 })
    await openHome(page)
    for (const [w, cols] of WIDTHS) {
      await page.setViewportSize({ width: w, height: 900 })
      await expect(page.getByTestId('home-widget-grid')).toHaveAttribute('data-cols', String(cols))
      const c = await cellsOf(page, 'home-widget-grid', '.wk-widget')
      expect(c.tiles).toHaveLength(12)
      if (mix.full) expect(c.holes, `${mix.name} at ${w}`).toBe(0)
      expectPacked(c, `${mix.name} at ${w}`)
      // An l fills a full row, whatever its place in the order.
      widgets.forEach((wd, i) => {
        if (wd.size === 'l') {
          expect(c.tiles[i].col, `${mix.name} at ${w}: l ${wd.id}`).toBe(0)
          expect(c.tiles[i].cw).toBe(cols)
        }
      })
    }
  })
}

test('old home.json without size renders with the page size and the defaults of its type', async ({ page, request }) => {
  const widgets = [
    { id: 'ls', type: 'list', title: 'Tasks', source: LIST },
    { id: 'mt', type: 'metric', title: 'Number', source: METRIC },
    { id: 'ch', type: 'chats', title: 'Chats' },
    { id: 'pg0', type: 'page', page: PAGES[0] },
    { id: 'pg1', type: 'page', page: PAGES[1] },
    { id: 'pg2', type: 'page', page: PAGES[2] },
  ]
  expect((await put(request, '/LocalAI/home.json', { widgets })).ok()).toBeTruthy()
  await page.setViewportSize({ width: 1280, height: 900 })
  await openHome(page)
  const c = await cellsOf(page, 'home-widget-grid', '.wk-widget')
  const byId = Object.fromEntries(c.tiles.map(t => [t.id.replace('home-widget-', ''), t]))
  // list and chats: m (2 wide); metric: s; pages: their widget block (small, large, medium)
  expect(byId.ls.cw).toBeGreaterThanOrEqual(2)
  expect(byId.ch.cw).toBeGreaterThanOrEqual(2)
  expect(byId.pg1.cw).toBe(4)
  expect(byId.pg1.rh).toBeGreaterThanOrEqual(3)
  expect(byId.pg0.cw).toBeGreaterThanOrEqual(1)
  expectPacked(c, 'old file')
})

test('the layout editor draws the grid of Home and sets the size of every kind of widget', async ({ page, request }) => {
  const widgets = twelve(['m', 's', 'l', 's', 's', 'm', 's', 'm', 's', 's', 'l', 's'], [0, 3, 4, 6, 1, 7, 9, 2, 5, 8, 10, 11])
  expect((await put(request, '/LocalAI/home.json', { widgets })).ok()).toBeTruthy()
  for (const [w, cols] of WIDTHS) {
    await page.setViewportSize({ width: w, height: 900 })
    await openHome(page)
    await page.getByTestId('home-edit-layout').click()
    await expect(page.getByTestId('home-layout-grid')).toHaveAttribute('data-cols', String(cols))
    const home = await cellsOf(page, 'home-widget-grid', '.wk-widget')
    const pv = await cellsOf(page, 'home-layout-grid', '.wk-pv')
    expect(pv.tiles).toHaveLength(12)
    expectPacked(pv, `editor at ${w}`)
    // The same cell for every widget, in Home and in the preview.
    expect(pv.tiles.map(t => [t.col, t.row, t.cw, t.rh])).toEqual(home.tiles.map(t => [t.col, t.row, t.cw, t.rh]))
    expect(await page.getByTestId('home-layout-row').count()).toBe(12)
    expect(await page.getByTestId('home-layout-size').count()).toBe(12)
    if (shots) {
      await page.getByTestId('home-layout-editor').scrollIntoViewIfNeeded()
      await page.waitForTimeout(300)
      await page.screenshot({ path: `${shots}/editor-${w}-light.png`, fullPage: true })
    }
    await page.goto('about:blank')
  }
})

test('the S M L control of a row changes its block and Home, and Save writes size', async ({ page, request }) => {
  const widgets = [
    { id: 'ls', type: 'list', title: 'Tasks', source: LIST },
    { id: 'mt', type: 'metric', title: 'Number', source: METRIC },
    { id: 'ch', type: 'chats', title: 'Chats' },
    { id: 'pg0', type: 'page', page: PAGES[0] },
  ]
  expect((await put(request, '/LocalAI/home.json', { widgets })).ok()).toBeTruthy()
  await page.setViewportSize({ width: 1280, height: 900 })
  await openHome(page)
  await page.getByTestId('home-edit-layout').click()
  const rows = page.getByTestId('home-layout-row')
  // The control shows the size in force: the defaults of the types and the page's.
  const pressed = async (i) => rows.nth(i).locator('[aria-pressed="true"]').getAttribute('data-filter')
  expect([await pressed(0), await pressed(1), await pressed(2), await pressed(3)]).toEqual(['m', 's', 'm', 's'])
  // Every kind: set the metric to L, the list to S, the chats to L, the page to M.
  await rows.nth(1).getByTestId('home-layout-size-l').click()
  await rows.nth(0).getByTestId('home-layout-size-s').click()
  await rows.nth(2).getByTestId('home-layout-size-l').click()
  await rows.nth(3).getByTestId('home-layout-size-m').click()
  const blocks = page.getByTestId('home-layout-block')
  await expect(blocks.nth(1)).toHaveAttribute('data-cw', '4')
  await expect(blocks.nth(0)).toHaveAttribute('data-size', 's')
  await page.getByTestId('home-layout-save').click()
  await expect(page.getByTestId('home-layout-editor')).toHaveCount(0)
  expect((await read(request, '/LocalAI/home.json')).widgets.map(w => w.size)).toEqual(['s', 'l', 'l', 'm'])
  const c = await cellsOf(page, 'home-widget-grid', '.wk-widget')
  expect(c.tiles.map(t => t.cw)).toContain(4)
  expectPacked(c, 'after save')
  const byId = Object.fromEntries(c.tiles.map(t => [t.id.replace('home-widget-', ''), t]))
  expect(byId.mt.cw).toBe(4)
  expect(byId.ch.cw).toBe(4)
})

test('screenshots of Home with a mix of sizes at 1280, 900 and 390 px, light and dark', async ({ page, request }) => {
  test.skip(!shots, 'HOME_SHOTS names a folder')
  const widgets = twelve(['m', 's', 'l', 's', 's', 'm', 's', 'm', 's', 's', 'l', 's'], [0, 3, 4, 6, 1, 7, 9, 2, 5, 8, 10, 11])
  expect((await put(request, '/LocalAI/home.json', { widgets })).ok()).toBeTruthy()
  for (const theme of ['light', 'dark']) {
    for (const [w] of WIDTHS) {
      await page.setViewportSize({ width: w, height: 900 })
      await openHome(page, theme)
      await page.waitForTimeout(600)
      await page.screenshot({ path: `${shots}/home-${w}-${theme}.png`, fullPage: true })
      if (theme === 'dark') {
        await page.getByTestId('home-edit-layout').click()
        await page.waitForTimeout(300)
        await page.screenshot({ path: `${shots}/editor-${w}-dark.png`, fullPage: true })
      }
      await page.goto('about:blank')
    }
  }
})
