import { test, expect } from './coverage-fixtures.js'
import { mockDiscovery, newState } from './discovery-fixtures.js'
import { app, stubAddonRoutes } from './addons-fixtures.js'

// The preview pane of a chat, with every API call stubbed: it opens by itself
// on what a turn produces, never on a phone and never in a chat where the
// person closed it; the chips under an answer open it again; every type of
// file has its view; a failed read can be tried again; an add-on that
// previews a type replaces the built-in view.

// PREVIEW_SHOTS=<dir> saves the screenshots of the review; PREVIEW_THEME
// (light or dark) picks the theme for them.
const SHOTS = process.env.PREVIEW_SHOTS || ''
const shot = async (page, name) => {
  if (!SHOTS) return
  await page.evaluate(() => document.querySelector('.cx-files__body')?.scrollTo(0, 0))
  await page.waitForTimeout(500)
  await page.screenshot({ path: `${SHOTS}/${name}.png` })
}
if (process.env.PREVIEW_THEME) {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript((theme) => { window.localStorage.setItem('localai-theme', theme) }, process.env.PREVIEW_THEME)
  })
}

const AT = '2026-10-30T09:00:00Z'
const CHAT = { id: 'c1', title: 'Quarterly budget', model: 'qwen3-8b-instruct', kind: 'chat', created: AT, updated: AT, running: false }
const user = (seq, turn, text) => ({ seq, turn, role: 'user', text, at: AT })
const answer = (seq, turn, text) => ({ seq, turn, role: 'assistant', text, at: AT })
const file = (name, turn, over = {}) => ({
  name, path: `/Chats/2026-10-30 Quarterly budget/${name}`, guest_path: `/workspace/home/Chats/2026-10-30 Quarterly budget/${name}`,
  size: 2048, mime: '', origin: 'model', turn, at: `2026-10-30T09:0${turn}:00Z`, ...over,
})

const TABLE = {
  kind: 'table',
  sheets: [
    { name: 'Budget', rows: [['Item', 'Category', 'Q1', 'Q2', 'Total'], ['Laptops', 'Hardware', '12000', '8000', '20000'], ['Cloud', 'Services', '4300.5', '4700.25', '9000.75'], ['Training', 'People', '3500', '4200', '7700']], total_rows: 640, total_cols: 5 },
    { name: 'Notes', rows: [['Approved', 'TRUE']], total_rows: 1, total_cols: 2 },
  ],
}
const CSV = { kind: 'table', sheets: [{ name: 'q1.csv', rows: [['month', 'revenue'], ['Jan', '1200'], ['Feb', '1350']], total_rows: 3, total_cols: 2 }] }
const DOC = { kind: 'document', blocks: [{ type: 'heading', level: 1, text: 'Quarterly report' }, { type: 'paragraph', text: 'Revenue grew 12% over the quarter.' }, { type: 'item', text: 'Hardware costs' }] }
const SLIDES = { kind: 'slides', slides: [{ n: 1, title: 'Launch plan', lines: [{ text: 'October 2026' }] }, { n: 2, title: 'Timeline', lines: [{ text: 'Design review' }, { text: 'Pilot with two teams', level: 1 }], notes: 'Mention the dates.' }] }
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64')

// A one-page PDF with a correct cross-reference table.
function pdf(text) {
  const objs = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
    null,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ]
  const stream = `BT /F1 24 Tf 72 720 Td (${text}) Tj ET`
  objs[3] = `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`
  let out = '%PDF-1.4\n'
  const offsets = []
  objs.forEach((o, i) => { offsets.push(out.length); out += `${i + 1} 0 obj\n${o}\nendobj\n` })
  const xref = out.length
  out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n` + offsets.map(o => `${String(o).padStart(10, '0')} 00000 n \n`).join('')
  out += `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`
  return Buffer.from(out)
}

const TEXTS = {
  'notes.md': '# Plan\n\n- **Hire** two engineers\n- Renew the contract\n',
  'config.json': JSON.stringify({ name: 'budget', quarters: ['Q1', 'Q2'], owner: { team: 'finance', approved: true } }),
  'report.py': 'def total(rows):\n    return sum(r["cost"] for r in rows)\n',
  'page.html': '<!doctype html><html><body><h1 id="hello">Hello from the page</h1></body></html>',
}

// stubChat answers the chat c1 and its files. endTurn() delivers the events
// a turn sends when it ends; the page reads the transcript and the files
// again.
async function stubChat(page, state) {
  state.previewCalls = {}
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [CHAT], persistent: true, files: true } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: { ...CHAT, pending: [] } }))
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: state.messages } }))
  await page.route(/\/api\/chats\/c1\/files(\?.*)?$/, route => route.fulfill({ json: { files: state.chatFiles, folder: '/workspace/home/Chats/2026-10-30 Quarterly budget/', folder_path: '/Chats/2026-10-30 Quarterly budget' } }))
  await page.route(/\/api\/chats\/files\/preview\?/, (route) => {
    const path = new URL(route.request().url()).searchParams.get('path')
    const name = path.split('/').pop()
    state.previewCalls[name] = (state.previewCalls[name] || 0) + 1
    const custom = state.preview?.(name, state.previewCalls[name])
    if (custom) return route.fulfill(custom)
    const body = { 'budget.xlsx': TABLE, 'q1.csv': CSV, 'report.docx': DOC, 'deck.pptx': SLIDES }[name]
    return body ? route.fulfill({ json: body }) : route.fulfill({ status: 415, json: { error: { message: 'no preview', type: 'unsupported_preview' } } })
  })
  await page.route(/\/api\/chats\/files\/content\?/, (route) => {
    const path = new URL(route.request().url()).searchParams.get('path')
    const name = path.split('/').pop()
    if (name.endsWith('.pdf')) return route.fulfill({ status: 200, contentType: 'application/pdf', headers: { 'Content-Security-Policy': "sandbox; default-src 'none'" }, body: pdf('Invoice 2026-104') })
    if (name.endsWith('.png')) return route.fulfill({ status: 200, contentType: 'image/png', body: PNG })
    if (name.endsWith('.svg')) return route.fulfill({ status: 200, contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="120" height="60"><rect width="120" height="60" fill="#3b82f6"/></svg>' })
    if (TEXTS[name] !== undefined) return route.fulfill({ status: 200, contentType: 'text/plain', body: TEXTS[name] })
    return route.fulfill({ status: 200, contentType: 'application/octet-stream', body: Buffer.from('bytes') })
  })
  const queue = []
  let wake = null
  await page.route('**/api/chats/c1/events', async (route) => {
    while (queue.length === 0) await new Promise((resolve) => { wake = resolve })
    const body = queue.splice(0).join('')
    await route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body }).catch(() => {})
  })
  return (turn) => {
    queue.push(`event: turn_start\ndata: ${JSON.stringify({ kind: 'turn_start', turn })}\n\n`)
    queue.push(`event: files\ndata: ${JSON.stringify({ kind: 'files' })}\n\n`)
    queue.push(`event: turn_end\ndata: ${JSON.stringify({ kind: 'turn_end', turn })}\n\n`)
    wake?.()
  }
}

async function open(page, state, { features } = {}) {
  if (features) state.features = { ...state.features, ...features }
  await mockDiscovery(page, state)
  const endTurn = await stubChat(page, state)
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-thread')).toBeVisible({ timeout: 15_000 })
  return endTurn
}

const pane = (page) => page.getByTestId('chat-files-pane')
const composer = (page) => page.getByTestId('chat-input')
const firstTurn = () => [user(1, 1, 'Plan the quarterly budget'), answer(2, 1, 'Here is how I would split it.')]
const budgetTurn = () => [user(3, 2, 'Put it in a spreadsheet'), answer(4, 2, 'I created budget.xlsx in the chat folder: `/Chats/2026-10-30 Quarterly budget/budget.xlsx`.')]

test('a spreadsheet the assistant creates opens in the pane, without taking the focus from the composer', async ({ page }) => {
  const state = newState({ chats: [CHAT], messages: firstTurn(), chatFiles: [] })
  const endTurn = await open(page, state)
  await expect(page.getByText('Here is how I would split it.')).toBeVisible()
  await expect(pane(page)).toHaveCount(0)
  await expect(page.getByTestId('chat-canvas-chip')).toHaveCount(0)

  await composer(page).click()
  await composer(page).pressSequentially('next I want ')
  state.messages = [...state.messages, ...budgetTurn()]
  state.chatFiles = [file('budget.xlsx', 2)]
  endTurn(2)

  await expect(pane(page)).toBeVisible()
  const preview = pane(page).getByTestId('file-preview')
  await expect(preview.getByRole('heading', { name: 'budget.xlsx' })).toBeVisible()
  const table = preview.getByTestId('preview-table')
  await expect(table.getByRole('cell', { name: 'Laptops' })).toBeVisible()
  await expect(table.getByRole('columnheader', { name: 'Category' })).toBeVisible()
  await expect(table.getByTestId('preview-table-more')).toContainText('Showing 4 of 640 rows')
  await expect(table.getByTestId('preview-sheet-tab')).toHaveText(['Budget', 'Notes'])
  await expect(page.getByTestId('chat-preview-status')).toHaveText('Preview opened: budget.xlsx')
  // The person kept typing: the focus and the text are where they were.
  await expect(composer(page)).toBeFocused()
  await composer(page).pressSequentially('a chart')
  await expect(composer(page)).toHaveValue('next I want a chart')
  // The chip under the answer, and the groups of the pane.
  await expect(page.getByTestId('chat-file-chip')).toHaveText(/Created\s*budget\.xlsx\s*·\s*Open preview/)
  await expect(pane(page).getByTestId('chat-files-created')).toContainText('Created and changed')
  await shot(page, 'pane-xlsx')

  // The second sheet, from the keyboard.
  await table.getByTestId('preview-sheet-tab').first().focus()
  await page.keyboard.press('ArrowRight')
  await expect(table.getByTestId('preview-sheet-tab').nth(1)).toHaveAttribute('aria-selected', 'true')
  await expect(table.getByRole('columnheader', { name: 'Approved' })).toBeVisible()
})

test('a closed pane stays closed in that chat, and the chip opens it again on the file', async ({ page }) => {
  const state = newState({ chats: [CHAT], messages: firstTurn(), chatFiles: [] })
  const endTurn = await open(page, state)
  state.messages = [...state.messages, ...budgetTurn()]
  state.chatFiles = [file('budget.xlsx', 2)]
  endTurn(2)
  await expect(pane(page)).toBeVisible()
  await pane(page).getByTestId('chat-files-close').click()
  await expect(pane(page)).toHaveCount(0)
  const stored = await page.evaluate(() => Object.keys(window.localStorage).filter(k => k.startsWith('localai_chat_preview_closed')).map(k => [k, JSON.parse(window.localStorage.getItem(k))]))
  expect(stored).toHaveLength(1)
  expect(Object.keys(stored[0][1])).toEqual(['c1'])

  // The next file of the turn does not open it again.
  state.messages = [...state.messages, user(5, 3, 'And as CSV'), answer(6, 3, 'Saved q1.csv as well.')]
  state.chatFiles = [...state.chatFiles, file('q1.csv', 3)]
  endTurn(3)
  await expect(page.getByTestId('chat-file-chip').filter({ hasText: 'q1.csv' })).toBeVisible()
  await page.waitForTimeout(500)
  await expect(pane(page)).toHaveCount(0)
  // A reload remembers it too.
  await page.reload()
  await expect(page.getByTestId('chat-file-chip')).toHaveCount(2, { timeout: 15_000 })
  await expect(pane(page)).toHaveCount(0)
  await shot(page, 'closed-chips')

  // The chip opens the pane on its file and moves the focus there.
  await page.getByTestId('chat-file-chip').filter({ hasText: 'budget.xlsx' }).click()
  await expect(pane(page)).toBeVisible()
  await expect(pane(page).getByRole('heading', { name: 'budget.xlsx' })).toBeFocused()
  const after = await page.evaluate(() => Object.keys(window.localStorage).filter(k => k.startsWith('localai_chat_preview_closed')))
  expect(after).toEqual([])
  // Escape closes it again and that is remembered.
  await page.keyboard.press('Escape')
  await expect(pane(page)).toHaveCount(0)
})

test('on a phone the pane never opens by itself; the chip opens the bottom sheet', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const state = newState({ chats: [CHAT], messages: firstTurn(), chatFiles: [] })
  const endTurn = await open(page, state)
  state.messages = [...state.messages, ...budgetTurn()]
  state.chatFiles = [file('budget.xlsx', 2)]
  endTurn(2)
  const chip = page.getByTestId('chat-file-chip')
  await expect(chip).toBeVisible()
  await page.waitForTimeout(500)
  await expect(pane(page)).toHaveCount(0)
  await shot(page, 'phone-chip')
  await chip.click()
  await expect(pane(page)).toBeVisible()
  const box = await pane(page).boundingBox()
  expect(box.width).toBeGreaterThan(380)
  expect(box.y).toBeGreaterThan(40)
  await expect(pane(page).getByRole('cell', { name: 'Laptops' })).toBeVisible()
  await shot(page, 'phone-sheet-xlsx')
})

test('an old chat opens nothing, and every type of file has its view', async ({ page }) => {
  const names = ['q1.csv', 'report.docx', 'deck.pptx', 'invoice.pdf', 'page.html', 'chart.png', 'logo.svg', 'notes.md', 'config.json', 'report.py', 'take.mp3', 'archive.zip']
  const state = newState({
    chats: [CHAT],
    messages: [user(1, 1, 'Make me everything'), answer(2, 1, 'Done.')],
    chatFiles: names.map(n => file(n, 1, n === 'q1.csv' ? { origin: 'user' } : {})),
  })
  await open(page, state)
  await expect(page.getByTestId('chat-file-chip')).toHaveCount(names.length - 1)
  await page.waitForTimeout(300)
  await expect(pane(page)).toHaveCount(0)

  await page.getByTestId('chat-files-chip').click()
  await expect(pane(page).getByTestId('chat-files-uploaded')).toContainText('You gave it')
  const pick = async (name) => {
    await pane(page).getByRole('button', { name, exact: true }).click()
    return pane(page).getByTestId('file-preview')
  }
  let p = await pick('q1.csv')
  await expect(p.getByRole('cell', { name: 'Feb' })).toBeVisible()
  await expect(p.getByTestId('preview-table-more')).toHaveCount(0)
  await shot(page, 'pane-csv')

  p = await pick('report.docx')
  await expect(p.getByTestId('preview-text-note')).toHaveText(/Text preview, formatting not shown/)
  await expect(p.getByRole('heading', { name: 'Quarterly report' })).toBeVisible()
  await expect(p.getByTestId('preview-document')).toContainText('Hardware costs')
  await shot(page, 'pane-docx')

  p = await pick('deck.pptx')
  await expect(p.getByRole('heading', { name: 'Timeline' })).toBeVisible()
  await p.getByText('Speaker notes').click()
  await expect(p.getByText('Mention the dates.')).toBeVisible()

  p = await pick('invoice.pdf')
  const frame = p.getByTestId('preview-pdf').locator('iframe')
  await expect(frame).toHaveAttribute('src', /files\/content\?path=.*invoice\.pdf&inline=1/)
  await expect(frame).not.toHaveAttribute('sandbox', /.*/)
  await shot(page, 'pane-pdf')

  p = await pick('page.html')
  const html = p.getByTestId('preview-html')
  await expect(html).toHaveAttribute('sandbox', 'allow-scripts')
  expect(await html.getAttribute('srcdoc')).toContain("connect-src 'none'")
  await expect(page.frameLocator('[data-testid="preview-html"]').locator('#hello')).toHaveText('Hello from the page')
  await shot(page, 'pane-html')

  p = await pick('chart.png')
  const img = p.getByTestId('preview-image')
  await expect(img.locator('img')).toHaveAttribute('src', /chart\.png&inline=1/)
  await img.getByRole('button').click()
  await expect(img).toHaveClass(/pv-image--zoom/)
  await expect(img.getByRole('button')).toHaveAttribute('aria-pressed', 'true')

  p = await pick('logo.svg')
  await expect(p.getByTestId('preview-image').locator('img')).toHaveAttribute('src', /logo\.svg&inline=1/)
  await shot(page, 'pane-image')

  p = await pick('notes.md')
  await expect(p.getByTestId('preview-markdown').getByRole('heading', { name: 'Plan' })).toBeVisible()
  await expect(p.getByTestId('preview-markdown').locator('strong')).toHaveText('Hire')

  p = await pick('config.json')
  const json = p.getByTestId('preview-json')
  await expect(json).toContainText('"finance"')
  await json.locator('summary').first().click()
  await expect(json.locator('details').first()).not.toHaveAttribute('open', '')
  await expect(p.getByTestId('chat-preview-copy')).toBeVisible()

  p = await pick('report.py')
  await expect(p.getByTestId('preview-code').locator('.hljs-keyword').first()).toHaveText('def')
  await shot(page, 'pane-code')

  p = await pick('take.mp3')
  await expect(p.getByTestId('preview-audio')).toHaveAttribute('controls', '')

  p = await pick('archive.zip')
  await expect(p.getByTestId('preview-unknown')).toContainText('ZIP file')
  await expect(p.getByTestId('preview-unknown').getByRole('link', { name: 'Download' })).toHaveAttribute('href', /archive\.zip/)
  await expect(p.getByTestId('chat-preview-copy')).toHaveCount(0)
  await expect(p.getByTestId('chat-preview-files')).toHaveAttribute('href', /\/app\/files\?path=%2FChats%2F2026-10-30%20Quarterly%20budget/)
})

test('a preview that fails says so and reads again on Try again', async ({ page }) => {
  const state = newState({ chats: [CHAT], messages: firstTurn(), chatFiles: [] })
  state.preview = (name, n) => (name === 'budget.xlsx' && n === 1 ? { status: 500, json: { error: { message: 'the request failed', type: 'server_error' } } } : null)
  const endTurn = await open(page, state)
  state.messages = [...state.messages, ...budgetTurn()]
  state.chatFiles = [file('budget.xlsx', 2)]
  endTurn(2)
  const error = pane(page).getByTestId('preview-error')
  await expect(error).toBeVisible()
  await expect(error).toContainText('Could not show budget.xlsx.')
  await expect(error).toContainText('the request failed')
  await shot(page, 'pane-error')
  await error.getByRole('button', { name: 'Try again' }).click()
  await expect(pane(page).getByRole('cell', { name: 'Laptops' })).toBeVisible()
  expect(state.previewCalls['budget.xlsx']).toBe(2)
})

test('an add-on that previews spreadsheets replaces the built-in table; other types keep theirs', async ({ page }) => {
  const DIGEST = 'f'.repeat(64)
  const state = newState({
    chats: [CHAT], messages: [user(1, 1, 'Files'), answer(2, 1, 'Here.')],
    chatFiles: [file('budget.xlsx', 1), file('q1.csv', 1)],
  })
  state.features = { ...state.features, addons: true }
  await mockDiscovery(page, state)
  await stubAddonRoutes(page)
  const viewer = app({
    id: 'sheets', name: 'sheets', title: 'Sheets', icon: undefined, generation: 1, version: '1.0.0', mainPage: undefined, tools: [], toolCount: 0,
    ui: { entry: `/api/addons/sheets/assets/${DIGEST}/main.js`, css: [], sdk: { min: 2, max: 2 }, slots: [{ id: 'preview', slot: 'files.preview', component: 'SheetPreview', extensions: ['xlsx'] }] },
  })
  await page.route(/\/api\/addons\/apps$/, route => route.fulfill({ json: { apps: [viewer] } }))
  await page.route(/\/api\/addons\/sheets\/assets\//, route => route.fulfill({
    status: 200, contentType: 'text/javascript',
    body: `export function createAddon(sdk) {
      const h = sdk.React.createElement
      const SheetPreview = (props) => h('div', { 'data-testid': 'addon-sheet-preview' }, 'Rich view of ' + props.name)
      return { components: { SheetPreview }, dispose() {} }
    }`,
  }))
  await stubChat(page, state)
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-thread')).toBeVisible({ timeout: 15_000 })
  await page.getByTestId('chat-file-chip').filter({ hasText: 'budget.xlsx' }).click()
  const p = pane(page).getByTestId('file-preview')
  await expect(p.getByTestId('addon-sheet-preview')).toHaveText('Rich view of budget.xlsx')
  await expect(p.getByTestId('preview-table')).toHaveCount(0)
  await page.getByTestId('chat-file-chip').filter({ hasText: 'q1.csv' }).click()
  await expect(p.getByTestId('preview-table').getByRole('cell', { name: 'Feb' })).toBeVisible()
  await expect(p.getByTestId('addon-sheet-preview')).toHaveCount(0)
})

test('a long code block opens in the pane; every block has Download, and only results have Open preview', async ({ page }) => {
  const long = Array.from({ length: 30 }, (_, i) => `    total += row[${i}]`).join('\n')
  const state = newState({ chats: [CHAT], messages: firstTurn(), chatFiles: [] })
  const endTurn = await open(page, state)
  state.messages = [...state.messages, user(3, 2, 'Write the script'), answer(4, 2, `Run it with:\n\n\`\`\`sh\npython sum.py\n\`\`\`\n\nThe script:\n\n\`\`\`python\ndef total(row):\n    total = 0\n${long}\n    return total\n\`\`\``)]
  endTurn(2)
  await expect(pane(page)).toBeVisible()
  const p = pane(page).getByTestId('file-preview')
  await expect(p).toHaveAttribute('data-type', 'block')
  await expect(p.getByRole('heading', { name: 'script-2.py' })).toBeVisible()
  await expect(p.getByTestId('preview-code')).toContainText('return total')
  await expect(p.getByTestId('chat-preview-copy')).toBeVisible()
  await expect(p.getByTestId('chat-preview-files')).toHaveCount(0)
  await shot(page, 'pane-codeblock')

  const blocks = page.locator('.cx-prose .code-block')
  await expect(blocks).toHaveCount(2)
  await expect(blocks.nth(0).locator('.code-download-btn')).toHaveCount(1)
  await expect(blocks.nth(0).locator('.code-preview-btn')).toHaveCount(0)
  await expect(blocks.nth(1).locator('.code-preview-btn')).toHaveText('Open preview')
  // Closed by hand, the block's own button opens it again.
  await pane(page).getByTestId('chat-files-close').click()
  await blocks.nth(1).locator('.code-preview-btn').click()
  await expect(pane(page).getByRole('heading', { name: 'script-2.py' })).toBeFocused()

  // The slash menu has no Canvas any more.
  await composer(page).fill('/')
  await expect(page.getByRole('listbox', { name: 'Actions' })).toBeVisible()
  const commands = await page.locator('.dk-cmd-name').allTextContents()
  expect(commands).toContain('/find')
  expect(commands).not.toContain('/canvas')
})

test('on a phone every view fits the bottom sheet without a sideways scroll of the page', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const names = ['q1.csv', 'report.docx', 'invoice.pdf', 'page.html', 'report.py', 'chart.png', 'budget.xlsx']
  const state = newState({ chats: [CHAT], messages: [user(1, 1, 'Make me everything'), answer(2, 1, 'Done.')], chatFiles: names.map(n => file(n, 1)) })
  await open(page, state)
  for (const name of names) {
    await page.getByTestId('chat-file-chip').filter({ hasText: name }).click()
    const sheet = pane(page)
    await expect(sheet.getByRole('heading', { name })).toBeVisible()
    await expect(sheet.getByTestId('preview-body')).toBeVisible()
    const box = await sheet.boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    await shot(page, `phone-${name.replace('.', '-')}`)
    await sheet.getByTestId('chat-files-close').click()
    await expect(sheet).toHaveCount(0)
  }
})
