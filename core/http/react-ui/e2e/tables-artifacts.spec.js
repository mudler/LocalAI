import { test, expect, request as playwrightRequest } from '@playwright/test'
import fs from 'node:fs'
import { randomUUID } from 'node:crypto'
import { chatOf, mockChat } from './chat-fixtures.js'
import { seedChats, CHATS_KEY } from './home-fixtures.js'

// Private CSV exports of the Tables add-on. Two UI test servers run the real
// add-on owner, the real Tables executable and the real built UI package, one
// with named accounts and real sessions (ADDONS_UI_URL) and one in local-owner
// mode (ADDONS_LOCAL_URL). The page asks for the file, the owner turns it into
// a private artifact, and every download here is a real HTTP request whose
// headers and bytes are checked. The replicas (owner and two frontends) are
// compared by the Go suite tests/addons/tablesexport.
//
// The specs change the owner's state and run in order on one worker.

const creds = JSON.parse(fs.readFileSync(process.env.ADDONS_FIXTURE, 'utf8'))
const BASE = process.env.ADDONS_UI_URL
const LOCAL = process.env.ADDONS_LOCAL_URL
const shots = process.env.ADDONS_SHOTS
const ROUTE = '/app/addons/tables/databases'
const UNKNOWN = 'a0123456789abcdef0123456789abcde'

// What the owner's table holds and what the export of the view must write:
// formulas, quotes, a comma, a newline, Unicode, a negative number, NULL and
// an empty string, sorted by Count descending with Count at least 2.
const ROWS = [
  { Name: '=HYPERLINK("https://evil.example","x")', Count: 5, Note: 'first\nsecond' },
  { Name: '+1', Count: -3 },
  { Name: 'Zoë, "Z"', Count: 12, Note: '日本語' },
  { Name: 'plain', Count: 7, Note: '' },
  { Name: '-cmd', Count: 1, Note: 'low' },
]
const VIEW_CSV = 'Name,Count,Note\r\n' +
  '"Zoë, ""Z""",12,日本語\r\n' +
  'plain,7,""\r\n' +
  '"\'=HYPERLINK(""https://evil.example"",""x"")",5,"first\nsecond"\r\n'
const FULL_CSV = 'Name,Count,Note\r\n' +
  '"\'=HYPERLINK(""https://evil.example"",""x"")",5,"first\nsecond"\r\n' +
  "'+1,-3,\r\n" +
  '"Zoë, ""Z""",12,日本語\r\n' +
  'plain,7,""\r\n' +
  "'-cmd,1,low\r\n"

async function shoot(page, name) {
  if (!shots) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(350)
  await page.screenshot({ path: `${shots}/${name}.png`, fullPage: false })
}

async function browserAs(browser, who, extra = {}) {
  const context = await browser.newContext({ baseURL: BASE, acceptDownloads: true, ...extra })
  await context.addCookies([{ name: 'session', value: creds.users[who].session, url: BASE }])
  const page = await context.newPage()
  return { context, page }
}

async function apiAs(who) {
  return playwrightRequest.newContext({ baseURL: BASE, extraHTTPHeaders: { Authorization: `Bearer ${creds.users[who].token}` } })
}

// A fetch made by the page itself: a real same-origin request with the session
// cookie and the browser's own Sec-Fetch metadata.
async function pageFetch(page, path, { method = 'GET', body, headers = {} } = {}) {
  return page.evaluate(async ({ path, method, body, headers }) => {
    const r = await fetch(path, {
      method, credentials: 'same-origin',
      headers: { Accept: 'application/json', ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    let json = null
    try { json = await r.json() } catch { json = null }
    return { status: r.status, json }
  }, { path, method, body, headers })
}

async function generation(api) {
  const r = await api.get('/api/addons/apps')
  expect(r.status()).toBe(200)
  return (await r.json()).apps.find(a => a.id === 'tables').generation
}

const tab = {
  async create(page, name, gen) {
    const op = `op-${randomUUID()}`
    const r = await pageFetch(page, '/api/addons/resources', { method: 'POST', body: { addonId: 'tables', operationId: op, generation: gen, arguments: { name } }, headers: { 'X-LocalAI-Confirm': op } })
    expect(r.status, JSON.stringify(r.json)).toBe(200)
    return r.json
  },
  apply(page, id, tool, args, revision, gen) {
    const op = `op-${randomUUID()}`
    return pageFetch(page, `/api/addons/resources/${id}/apply`, {
      method: 'POST', body: { operationId: op, generation: gen, revision, tool, arguments: args }, headers: { 'X-LocalAI-Confirm': id },
    })
  },
}

async function seed(page, api, name) {
  const gen = await generation(api)
  const db = await tab.create(page, name, gen)
  let rev = db.revision
  const step = async (tool, args) => {
    const r = await tab.apply(page, db.id, tool, args, rev, gen)
    expect(r.status, `${tool} ${JSON.stringify(r.json)}`).toBe(200)
    rev = r.json.revision
  }
  await step('tables-create', { table: 'Typed' })
  await step('columns-add', { table: 'Typed', column: 'Name', type: 'text' })
  await step('columns-add', { table: 'Typed', column: 'Count', type: 'integer' })
  await step('columns-add', { table: 'Typed', column: 'Note', type: 'text' })
  await step('rows-insert', { table: 'Typed', rows: ROWS })
  return { id: db.id, generation: gen }
}

async function openDb(page, id, table) {
  await page.goto(`${ROUTE}/${id}${table ? `?table=${encodeURIComponent(table)}` : ''}`)
  await expect(page.getByTestId('tbl-database-page')).toBeVisible({ timeout: 25_000 })
}

// exportFromPage runs the dialog and returns the host card it ends with.
async function exportFromPage(page, { rowId = false } = {}) {
  await page.getByRole('button', { name: 'Export CSV' }).click()
  const dialog = page.getByTestId('tbl-export-dialog')
  await expect(dialog).toBeVisible()
  if (rowId) await dialog.getByLabel('Include the row ID as the first column').check()
  await dialog.getByRole('button', { name: 'Create the file' }).click()
  const card = dialog.getByTestId('artifact-card')
  await expect(card).toBeVisible({ timeout: 30_000 })
  await expect(card).toHaveAttribute('data-state', 'complete', { timeout: 30_000 })
  return { dialog, card }
}

const b64url = (s) => Buffer.from(s).toString('base64url')
const toolMessage = (tool, args) => `Please run this ADDON_TOOL_CALL:addon__tables__${tool}:${b64url(JSON.stringify(args))}`

test.describe.configure({ mode: 'serial' })

let member
let state = {}
test.beforeAll(async () => { member = await apiAs('member') })
test.afterAll(async () => { await member.dispose() })

test.describe('Tables CSV exports', () => {
  test('tables-artifacts: the person exports the view they see and downloads exact bytes with attachment headers', async ({ browser }) => {
    const owner = await browserAs(browser, 'member')
    const p = owner.page
    await p.goto(ROUTE)
    await expect(p.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    state.db = await seed(p, member, 'Export source')
    await openDb(p, state.db.id, 'Typed')
    await expect(p.getByTestId('tbl-row')).toHaveCount(5, { timeout: 25_000 })

    // The view: Count at least 2, largest first.
    await p.getByRole('button', { name: 'Sort by Count' }).click()
    await p.getByRole('button', { name: 'Sort by Count' }).click()
    await p.getByRole('button', { name: 'Filter' }).click()
    const filters = p.getByTestId('tbl-filters')
    await filters.getByRole('button', { name: 'Add condition' }).click()
    const cond = filters.getByTestId('tbl-condition').nth(0)
    await cond.getByLabel('Column', { exact: true }).selectOption('Count')
    await cond.getByLabel('Condition', { exact: true }).selectOption('gte')
    await cond.getByLabel('Value', { exact: true }).fill('2')
    await filters.getByRole('button', { name: 'Apply' }).click()
    await expect(p.getByTestId('tbl-row')).toHaveCount(3, { timeout: 25_000 })

    await p.getByRole('button', { name: 'Export CSV' }).click()
    const dialog = p.getByTestId('tbl-export-dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog.getByTestId('tbl-export-scope')).toContainText('match the current filter')
    await shoot(p, 'tables-artifacts-dialog')
    await dialog.getByRole('button', { name: 'Create the file' }).click()
    const card = dialog.getByTestId('artifact-card')
    await expect(card).toHaveAttribute('data-state', 'complete', { timeout: 30_000 })
    // The card is the host's: name, size and expiry from its record.
    await expect(card.getByTestId('artifact-name')).toHaveText('Typed.csv')
    await expect(card.getByTestId('artifact-size')).toHaveText(`${Buffer.byteLength(VIEW_CSV)} B`)
    await expect(card.getByTestId('artifact-expires')).toBeVisible()
    await expect(card.getByTestId('artifact-state')).toHaveText('Ready')
    const link = card.getByTestId('artifact-download')
    const href = await link.getAttribute('href')
    expect(href).toMatch(/^\/api\/addons\/artifacts\/a[0-9a-f]{32}\/download$/)
    state.file = href.split('/')[4]
    // The label stays readable under the pointer: the global link hover colour
    // must not be the colour of the button.
    await link.hover()
    const colours = await link.evaluate((el) => { const c = getComputedStyle(el); return { text: c.color, fill: c.backgroundColor } })
    expect(colours.text).not.toBe(colours.fill)
    await shoot(p, 'tables-artifacts-ready')

    // The browser saves exactly what the owner holds.
    const [download] = await Promise.all([p.waitForEvent('download'), link.click()])
    expect(download.suggestedFilename()).toBe('Typed.csv')
    expect(fs.readFileSync(await download.path(), 'utf8')).toBe(VIEW_CSV)

    // Headers and bytes over real HTTP, as the person's own credential.
    const res = await member.get(href)
    expect(res.status()).toBe(200)
    expect((await res.body()).toString('utf8')).toBe(VIEW_CSV)
    const h = res.headers()
    expect(h['content-type']).toBe('text/csv')
    expect(h['content-disposition']).toMatch(/^attachment;.*Typed\.csv/)
    expect(h['x-content-type-options']).toBe('nosniff')
    expect(h['cache-control']).toContain('no-store')
    expect(h['content-length']).toBe(String(Buffer.byteLength(VIEW_CSV)))
    expect((await res.body())[0]).not.toBe(0xef)

    // Exporting the whole table gives every row in id order, with its NULLs.
    await owner.page.keyboard.press('Escape')
    await p.getByTestId('tbl-filters').getByRole('button', { name: 'Clear' }).click()
    await p.getByRole('button', { name: 'Sort by Count' }).click()
    await expect(p.getByTestId('tbl-row')).toHaveCount(5, { timeout: 25_000 })
    const all = await exportFromPage(p)
    const full = await member.get(await all.card.getByTestId('artifact-download').getAttribute('href'))
    expect((await full.body()).toString('utf8')).toBe(FULL_CSV)
    // The database was only read.
    const rows = await member.post('/api/addons/dispatch', {
      data: { operationId: `op-${randomUUID()}`, addonId: 'tables', resourceId: state.db.id, generation: state.db.generation, action: 'read', arguments: { tool: 'rows-query', table: 'Typed' } },
    })
    expect((await rows.json()).payload.rows).toHaveLength(5)
    await owner.context.close()
  })

  test('tables-artifacts: another person is denied exactly like an unknown file, shared or not', async ({ browser }) => {
    const outsider = await apiAs('outsider')
    const viewer = await apiAs('viewer')
    const unknown = await viewer.get(`/api/addons/artifacts/${UNKNOWN}/download`)
    expect(unknown.status()).toBe(403)
    for (const who of [outsider, viewer]) {
      for (const path of [`/api/addons/artifacts/${state.file}/download`, `/api/addons/artifacts/${state.file}`]) {
        expect((await who.get(path)).status()).toBe(403)
      }
    }
    expect(await (await viewer.get(`/api/addons/artifacts/${state.file}/download`)).text()).toBe(await unknown.text())
    // No credential at all is no better.
    const bare = await playwrightRequest.newContext({ baseURL: BASE })
    expect([401, 403]).toContain((await bare.get(`/api/addons/artifacts/${state.file}/download`)).status())
    await bare.dispose()
    // Sharing the database does not share its files, and a person who cannot
    // read the database cannot start an export of it.
    const own = await browserAs(browser, 'member')
    await own.page.goto(ROUTE)
    const refused = await outsider.post('/api/addons/dispatch', {
      data: { operationId: `op-${randomUUID()}`, addonId: 'tables', resourceId: state.db.id, generation: state.db.generation, action: 'read', arguments: { tool: 'rows-export', table: 'Typed' } },
    })
    expect(refused.status()).toBe(403)
    const grant = await pageFetch(own.page, `/api/addons/resources/${state.db.id}/grants/${creds.users.viewer.id}`, { method: 'PUT', body: { role: 'viewer' }, headers: { 'X-LocalAI-Confirm': state.db.id } })
    expect(grant.status).toBe(200)
    expect((await viewer.get(`/api/addons/artifacts/${state.file}/download`)).status()).toBe(403)
    await own.context.close()
    await outsider.dispose()
    await viewer.dispose()
  })

  test('tables-artifacts: a viewer exports what they may read and withdrawing the share ends the download', async ({ browser }) => {
    const v = await browserAs(browser, 'viewer')
    const p = v.page
    await openDb(p, state.db.id, 'Typed')
    await expect(p.getByTestId('tbl-role')).toHaveAttribute('data-role', 'viewer')
    await expect(p.getByTestId('tbl-row')).toHaveCount(5, { timeout: 25_000 })
    const { card } = await exportFromPage(p, { rowId: true })
    const viewerApi = await apiAs('viewer')
    const href = await card.getByTestId('artifact-download').getAttribute('href')
    const ok = await viewerApi.get(href)
    expect(ok.status()).toBe(200)
    expect((await ok.text()).startsWith('_id,Name,Count,Note\r\n1,')).toBe(true)

    // The owner withdraws the share. The viewer's file is not downloadable any
    // more, by the page or by the route, and the owner's own file still is.
    const own = await browserAs(browser, 'member')
    await own.page.goto(ROUTE)
    const gone = await pageFetch(own.page, `/api/addons/resources/${state.db.id}/grants/${creds.users.viewer.id}`, { method: 'DELETE', headers: { 'X-LocalAI-Confirm': state.db.id } })
    expect(gone.status).toBe(200)
    expect((await viewerApi.get(href)).status()).toBe(403)
    await card.getByTestId('artifact-download').click()
    await expect(card.getByTestId('artifact-download-denied')).toBeVisible({ timeout: 15_000 })
    await shoot(p, 'tables-artifacts-denied')
    expect((await member.get(`/api/addons/artifacts/${state.file}/download`)).status()).toBe(200)
    // A person without access can no longer start one either.
    const again = await viewerApi.post('/api/addons/dispatch', {
      data: { operationId: `op-${randomUUID()}`, addonId: 'tables', resourceId: state.db.id, generation: state.db.generation, action: 'read', arguments: { tool: 'rows-export', table: 'Typed' } },
    })
    expect(again.status()).toBe(403)
    await viewerApi.dispose()
    await v.context.close()
    await own.context.close()
  })

  test('tables-artifacts: a result past the export limit gives a message and no file', async () => {
    const r = await member.post('/api/addons/dispatch', {
      data: { operationId: `op-${randomUUID()}`, addonId: 'tables', resourceId: state.db.id, generation: state.db.generation, action: 'read', arguments: { tool: 'rows-export', table: 'Typed', maxRows: 5000000 } },
    })
    expect(r.status()).toBe(200)
    const body = await r.json()
    expect(body.outputs || []).toEqual([])
    expect(body.payload.error).toBe('too-large')
    const bad = await member.post('/api/addons/dispatch', {
      data: { operationId: `op-${randomUUID()}`, addonId: 'tables', resourceId: state.db.id, generation: state.db.generation, action: 'read', arguments: { tool: 'rows-export', table: 'Typed', filter: { column: 'Nope', op: 'eq', value: 1 } } },
    })
    expect(bad.status()).toBeGreaterThanOrEqual(400)
  })

  test('tables-artifacts: the chat draws the host card for an export and stores only its identifier', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await mockChat(page, { models: [{ id: 'mock-model', capabilities: ['FLAG_CHAT'] }], loaded: ['mock-model'] })
    await seedChats(page, [chatOf('c1', 'Tables', 'mock-model', [], { addonTools: true })], 'c1')
    await page.goto('/app/chat')
    await page.getByTestId('chat-composer').waitFor()
    await page.getByTestId('chat-input').fill(toolMessage('rows-export', { resourceId: state.db.id, table: 'Typed', columns: ['Name'] }))
    await page.getByTestId('chat-send').click()
    const card = page.getByTestId('artifact-card')
    await expect(card).toHaveCount(1, { timeout: 40_000 })
    await expect(card).toHaveAttribute('data-state', 'complete', { timeout: 30_000 })
    await expect(card.getByTestId('artifact-name')).toHaveText('Typed.csv')
    await expect(card.getByTestId('artifact-expires')).toBeVisible()
    const href = await card.getByTestId('artifact-download').getAttribute('href')
    expect(href).toMatch(/^\/api\/addons\/artifacts\/a[0-9a-f]{32}\/download$/)
    const res = await member.get(href)
    expect(res.status()).toBe(200)
    expect((await res.text()).startsWith('Name\r\n')).toBe(true)
    await shoot(page, 'tables-artifacts-chat')
    // The conversation keeps the identifier and nothing of the file.
    const readStored = () => page.evaluate((key) => localStorage.getItem(key) || '', CHATS_KEY)
    await expect.poll(readStored, { timeout: 15_000 }).toContain('tool_result')
    const chats = JSON.parse(await readStored()).chats
    const results = chats.flatMap(c => c.history).filter(m => m.role === 'tool_result')
    expect(results.length).toBeGreaterThan(0)
    for (const m of results) {
      const held = JSON.parse(JSON.parse(m.content).result)
      expect(held).toEqual({ kind: 'artifact', artifacts: [{ id: expect.stringMatching(/^a[0-9a-f]{32}$/) }] })
    }
    expect(await readStored()).not.toContain('=HYPERLINK')
    await context.close()
  })

  test('tables-artifacts: a person at the file quota is told so and no file is made', async ({ browser }) => {
    const viewerGrant = await browserAs(browser, 'member')
    await viewerGrant.page.goto(ROUTE)
    const grant = await pageFetch(viewerGrant.page, `/api/addons/resources/${state.db.id}/grants/${creds.users.viewer.id}`, { method: 'PUT', body: { role: 'viewer' }, headers: { 'X-LocalAI-Confirm': state.db.id } })
    expect(grant.status).toBe(200)
    await viewerGrant.context.close()

    const viewer = await apiAs('viewer')
    let made = 0
    let denied = null
    for (let i = 0; i < 140 && !denied; i++) {
      const r = await viewer.post('/api/addons/dispatch', {
        data: { operationId: `op-${randomUUID()}`, addonId: 'tables', resourceId: state.db.id, generation: state.db.generation, action: 'read', arguments: { tool: 'rows-export', table: 'Typed', columns: ['Name'] } },
      })
      if (r.status() === 200) made++
      else if (r.status() === 403) denied = await r.json()
      else await new Promise(resolve => setTimeout(resolve, 100))
    }
    expect(denied, `a refusal after ${made} files`).toBeTruthy()
    expect(made).toBeGreaterThan(50)
    expect(made).toBeLessThanOrEqual(100)

    // The page shows the refusal and no card.
    const v = await browserAs(browser, 'viewer')
    await openDb(v.page, state.db.id, 'Typed')
    await v.page.getByRole('button', { name: 'Export CSV' }).click()
    const dialog = v.page.getByTestId('tbl-export-dialog')
    await dialog.getByRole('button', { name: 'Create the file' }).click()
    // The refusal is not told as a lost database: the person still reads it.
    await expect(dialog.getByTestId('tbl-export-refused')).toBeVisible({ timeout: 25_000 })
    await expect(dialog.getByTestId('artifact-card')).toHaveCount(0)
    await expect(v.page.getByTestId('tbl-database-page')).toBeVisible()
    await expect(v.page.getByTestId('tbl-unavailable')).toHaveCount(0)
    await shoot(v.page, 'tables-artifacts-quota')
    await v.context.close()
    await viewer.dispose()
  })

  test('tables-artifacts: in local-owner mode the owner exports and downloads with no account', async ({ browser }) => {
    const context = await browser.newContext({ baseURL: LOCAL, acceptDownloads: true })
    const page = await context.newPage()
    await page.goto(`${LOCAL}${ROUTE}`)
    await expect(page.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    const apps = await pageFetch(page, '/api/addons/apps')
    const gen = apps.json.apps.find(a => a.id === 'tables').generation
    const db = await tab.create(page, 'Local export', gen)
    let rev = db.revision
    for (const [tool, args] of [
      ['tables-create', { table: 'Typed' }],
      ['columns-add', { table: 'Typed', column: 'Name', type: 'text' }],
      ['rows-insert', { table: 'Typed', rows: [{ Name: '=1+1' }, { Name: 'ok' }] }],
    ]) {
      const r = await tab.apply(page, db.id, tool, args, rev, gen)
      expect(r.status, JSON.stringify(r.json)).toBe(200)
      rev = r.json.revision
    }
    await page.goto(`${LOCAL}${ROUTE}/${db.id}?table=Typed`)
    await expect(page.getByTestId('tbl-row')).toHaveCount(2, { timeout: 25_000 })
    const { card } = await exportFromPage(page)
    const href = await card.getByTestId('artifact-download').getAttribute('href')
    const [download] = await Promise.all([page.waitForEvent('download'), card.getByTestId('artifact-download').click()])
    expect(fs.readFileSync(await download.path(), 'utf8')).toBe("Name\r\n'=1+1\r\nok\r\n")
    const res = await context.request.get(`${LOCAL}${href}`)
    expect(res.status()).toBe(200)
    expect(res.headers()['content-disposition']).toMatch(/^attachment;/)
    expect(res.headers()['x-content-type-options']).toBe('nosniff')
    await context.close()
  })
})
