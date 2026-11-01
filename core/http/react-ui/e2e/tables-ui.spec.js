import { test, expect, request as playwrightRequest } from '@playwright/test'
import fs from 'node:fs'
import { randomUUID } from 'node:crypto'

// The native Tables UI. Two UI test servers run the real add-on owner, the
// real Tables executable and the real built UI package (addons/tables/ui),
// installed after the host build from a catalog on loopback: one with named
// accounts and real sessions (ADDONS_UI_URL) and one in local-owner mode
// (ADDONS_LOCAL_URL). Every call of the page goes through the real host, the
// real owner and the real database. Only the chat model list and the system
// probes of the chat page are stubbed.
//
// The specs change the owner's state and run in order on one worker.

const creds = JSON.parse(fs.readFileSync(process.env.ADDONS_FIXTURE, 'utf8'))
const BASE = process.env.ADDONS_UI_URL
const LOCAL = process.env.ADDONS_LOCAL_URL
const shots = process.env.ADDONS_SHOTS
const SECRET = 'LEDGER-SECRET-5521'
const ROUTE = '/app/addons/tables/databases'

async function shoot(page, name) {
  if (!shots) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.waitForTimeout(350)
  await page.screenshot({ path: `${shots}/${name}.png`, fullPage: false })
}

async function browserAs(browser, who, extra = {}) {
  const context = await browser.newContext({ baseURL: BASE, ...extra })
  await context.addCookies([{ name: 'session', value: creds.users[who].session, url: BASE }])
  const page = await context.newPage()
  // A spec that needs to see what the host refused runs with TABLES_UI_DEBUG.
  if (process.env.TABLES_UI_DEBUG) {
    page.on('response', async r => {
      if (r.status() >= 400 && r.url().includes('/api/addons/')) console.log('refused', who, r.status(), r.request().method(), r.url().replace(BASE, ''), (await r.text().catch(() => '')).slice(0, 200))
    })
  }
  return { context, page }
}

async function apiAs(who) {
  return playwrightRequest.newContext({ baseURL: BASE, extraHTTPHeaders: { Authorization: `Bearer ${creds.users[who].token}` } })
}

// A fetch made by the page itself: a real same-origin request that carries the
// session cookie and the browser's own Sec-Fetch metadata, which is what the
// host's own-session rule requires.
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

// What the person's own page may do through the host, for seeding and for
// crafted requests: the same calls the add-on page makes.
const tab = {
  async list(page) {
    const r = await pageFetch(page, '/api/addons/resources?addonId=tables')
    expect(r.status).toBe(200)
    return r.json
  },
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

async function read(api, id, gen, args) {
  const r = await api.post('/api/addons/dispatch', {
    data: { operationId: `op-${randomUUID()}`, addonId: 'tables', resourceId: id, generation: gen, action: 'read', arguments: args },
  })
  return { status: r.status(), json: await r.json().catch(() => null) }
}

// A database with the five column types and `rows` rows, made through the
// person's own page so the owner of it is `page`'s user.
async function seedTyped(page, api, name, rows = 0) {
  const gen = await generation(api)
  const db = await tab.create(page, name, gen)
  let rev = db.revision
  const step = async (tool, args) => {
    const r = await tab.apply(page, db.id, tool, args, rev, gen)
    expect(r.status, `${tool} ${JSON.stringify(r.json)}`).toBe(200)
    rev = r.json.revision
  }
  await step('tables-create', { table: 'Typed' })
  await step('columns-add', { table: 'Typed', column: 'Name', type: 'text', required: true })
  await step('columns-add', { table: 'Typed', column: 'Count', type: 'integer' })
  await step('columns-add', { table: 'Typed', column: 'Price', type: 'real' })
  await step('columns-add', { table: 'Typed', column: 'Paid', type: 'boolean' })
  await step('columns-add', { table: 'Typed', column: 'Due', type: 'datetime' })
  if (rows > 0) {
    const batch = Array.from({ length: rows }, (_, i) => ({ Name: `item-${String(i + 1).padStart(3, '0')}`, Count: i + 1, Price: (i + 1) / 4, Paid: i % 2 === 0, Due: '2026-10-14T09:30:00Z' }))
    await step('rows-insert', { table: 'Typed', rows: batch })
  }
  return { id: db.id, revision: rev, generation: gen }
}

const noOverflow = (page) => page.evaluate(() => document.scrollingElement.scrollWidth <= window.innerWidth)
const rowsOf = (page) => page.getByTestId('tbl-row')
const names = async (page, column = 'Name') => page.locator(`[data-testid="tbl-row"] td[data-column="${column}"]`).allInnerTexts()

async function openDb(page, id, table) {
  await page.goto(`${ROUTE}/${id}${table ? `?table=${encodeURIComponent(table)}` : ''}`)
  await expect(page.getByTestId('tbl-database-page')).toBeVisible({ timeout: 25_000 })
}

test.describe.configure({ mode: 'serial' })

let admin
let member
let state = {}
test.beforeAll(async () => {
  admin = await apiAs('admin')
  member = await apiAs('member')
})
test.afterAll(async () => {
  await admin.dispose()
  await member.dispose()
})

test.describe('Tables UI', () => {
  test('tables-ui: a new database is private to its owner and a second person sees nothing of it', async ({ browser }) => {
    const owner = await browserAs(browser, 'member')
    await owner.page.goto(ROUTE)
    await expect(owner.page.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    await expect(owner.page.getByText('No databases yet')).toBeVisible()
    await shoot(owner.page, 'tables-ui-empty')
    await owner.page.getByRole('button', { name: 'New database' }).click()
    const form = owner.page.getByTestId('tbl-name-dialog')
    await expect(form).toBeVisible()
    // The name is bounded and must not be blank.
    await expect(form.getByRole('button', { name: 'Create database' })).toBeDisabled()
    await form.getByLabel('Name').fill('Household budget')
    await form.getByRole('button', { name: 'Create database' }).click()
    await expect(owner.page.getByTestId('tbl-database-page')).toBeVisible({ timeout: 25_000 })
    await expect(owner.page.getByTestId('tbl-db-name')).toHaveText('Household budget')
    await expect(owner.page.getByTestId('tbl-role')).toHaveAttribute('data-role', 'owner')
    const id = await owner.page.getByTestId('tbl-database-page').getAttribute('data-resource')
    state.budget = id
    await expect(owner.page.getByText('No tables yet')).toBeVisible()
    await shoot(owner.page, 'tables-ui-new-database')

    // The list shows it for the owner, with who it is shared with.
    await owner.page.goto(ROUTE)
    const mine = owner.page.getByTestId('tbl-database')
    await expect(mine).toHaveCount(1)
    await expect(mine).toHaveAttribute('data-role', 'owner')
    await expect(owner.page.getByTestId('tbl-database-shared')).toHaveText('Private')

    // Another person with the feature sees an empty list, the same address is
    // an unavailable database, and the host's own listing says the same.
    const other = await browserAs(browser, 'editor')
    await other.page.goto(ROUTE)
    await expect(other.page.getByText('No databases yet')).toBeVisible({ timeout: 25_000 })
    await expect(other.page.getByTestId('tbl-database')).toHaveCount(0)
    await other.page.goto(`${ROUTE}/${id}`)
    await expect(other.page.getByTestId('tbl-unavailable')).toBeVisible({ timeout: 25_000 })
    await expect(other.page.getByTestId('tbl-db-name')).toHaveCount(0)
    expect((await tab.list(other.page)).resources).toEqual([])
    // A key of the owner may list the owner's own database and nothing else.
    const listed = await (await member.get('/api/addons/resources?addonId=tables')).json()
    expect(listed.resources.map(r => r.id)).toEqual([id])
    await owner.context.close()
    await other.context.close()
  })

  test('tables-ui: the owner builds a schema and rows, shares by name and each role gets its own controls', async ({ browser }) => {
    const owner = await browserAs(browser, 'member')
    const op = owner.page
    await openDb(op, state.budget)
    // Schema controls are the owner's: a table, then typed columns.
    await op.getByRole('button', { name: 'New table' }).click()
    const tableForm = op.getByTestId('tbl-table-dialog')
    await tableForm.getByLabel('Table name').fill('Entries')
    await tableForm.getByRole('button', { name: 'Create table' }).click()
    await expect(op.getByTestId('tbl-table-tab')).toHaveText(['Entries'], { timeout: 25_000 })
    const addColumn = async (name, type, { required = false } = {}) => {
      await op.getByRole('button', { name: 'Add column' }).click()
      const form = op.getByTestId('tbl-column-dialog')
      await form.getByLabel('Column name').fill(name)
      await form.getByLabel('Type').selectOption(type)
      if (required) await form.getByLabel('Values must not be empty').check()
      await form.getByRole('button', { name: 'Add column' }).click()
      await expect(op.getByTestId('tbl-columns').locator(`[data-column="${name}"]`)).toBeVisible({ timeout: 25_000 })
    }
    await addColumn('Item', 'text', { required: true })
    await addColumn('Qty', 'integer')
    await addColumn('Memo', 'text')

    // Rows are added in a typed form; a value of the wrong type is refused in
    // the form before anything is sent.
    await op.getByRole('button', { name: 'Add rows' }).first().click()
    const add = op.getByTestId('tbl-add-dialog')
    await add.getByLabel('Item').fill('pen')
    await add.getByLabel('Qty').fill('three')
    await add.getByLabel('Memo').fill(SECRET)
    await add.getByRole('button', { name: 'Add row' }).click()
    await expect(add.getByRole('alert')).toContainText('whole number')
    await add.getByLabel('Qty').fill('3')
    await add.getByRole('button', { name: 'Add another row' }).click()
    const second = add.getByTestId('tbl-add-row').nth(1)
    await second.getByLabel('Item').fill('ink')
    await second.getByLabel('Qty').fill('1')
    await add.getByRole('button', { name: 'Add 2 rows' }).click()
    await expect(rowsOf(op)).toHaveCount(2, { timeout: 25_000 })
    await expect(op.getByTestId('tbl-notice')).toBeVisible()
    expect(await names(op, 'Item')).toEqual(['pen', 'ink'])
    await shoot(op, 'tables-ui-owner-grid')

    // Sharing: the picker finds a named account and the owner names the role.
    await op.getByRole('button', { name: 'Share' }).click()
    const sharing = op.getByTestId('tbl-sharing')
    await expect(sharing.getByTestId('tbl-no-grants')).toBeVisible({ timeout: 25_000 })
    await sharing.getByLabel('Find a person by name').fill('ed')
    const candidate = sharing.getByTestId('tbl-user').filter({ hasText: 'editor' })
    await expect(candidate).toHaveCount(1, { timeout: 25_000 })
    await candidate.getByRole('button', { name: 'Add as editor' }).click()
    await expect(sharing.getByTestId('tbl-grant')).toHaveCount(1, { timeout: 25_000 })
    await sharing.getByLabel('Find a person by name').fill('vi')
    await sharing.getByTestId('tbl-user').filter({ hasText: 'viewer' }).getByRole('button', { name: 'Add as viewer' }).click()
    await expect(sharing.getByTestId('tbl-grant')).toHaveCount(2, { timeout: 25_000 })
    await expect(sharing.locator(`[data-testid="tbl-grant"][data-user="${creds.users.editor.id}"]`)).toHaveAttribute('data-role', 'editor')
    await expect(sharing.locator(`[data-testid="tbl-grant"][data-user="${creds.users.viewer.id}"]`)).toHaveAttribute('data-role', 'viewer')
    // A person is found by name only: an address never shows.
    await expect(sharing).not.toContainText('@')
    await shoot(op, 'tables-ui-sharing')
    await op.keyboard.press('Escape')
    await expect(sharing).toHaveCount(0)

    // The editor changes rows and has no schema, sharing or delete controls.
    const ed = await browserAs(browser, 'editor')
    await ed.page.goto(ROUTE)
    await expect(ed.page.getByTestId('tbl-database')).toHaveCount(1, { timeout: 25_000 })
    await expect(ed.page.getByTestId('tbl-database')).toHaveAttribute('data-role', 'editor')
    await expect(ed.page.getByTestId('tbl-database-shared')).toHaveText('Shared by member')
    await ed.page.getByTestId('tbl-database-open').click()
    await expect(ed.page.getByTestId('tbl-database-page')).toHaveAttribute('data-role', 'editor', { timeout: 25_000 })
    await expect(rowsOf(ed.page)).toHaveCount(2, { timeout: 25_000 })
    for (const name of ['Share', 'Rename', 'Delete', 'New table', 'Add column']) {
      await expect(ed.page.getByRole('button', { name, exact: true })).toHaveCount(0)
    }
    await expect(ed.page.getByTestId('tbl-schema')).toContainText('Only the owner of the database changes its schema')
    await ed.page.getByRole('button', { name: 'Add rows' }).first().click()
    const eadd = ed.page.getByTestId('tbl-add-dialog')
    await eadd.getByLabel('Item').fill('cup')
    await eadd.getByLabel('Qty').fill('9')
    await eadd.getByRole('button', { name: 'Add row' }).click()
    await expect(rowsOf(ed.page)).toHaveCount(3, { timeout: 25_000 })
    await shoot(ed.page, 'tables-ui-editor')

    // The viewer reads and sees no control that changes anything.
    const vw = await browserAs(browser, 'viewer')
    await vw.page.goto(`${ROUTE}/${state.budget}`)
    await expect(vw.page.getByTestId('tbl-database-page')).toHaveAttribute('data-role', 'viewer', { timeout: 25_000 })
    await expect(rowsOf(vw.page)).toHaveCount(3, { timeout: 25_000 })
    await expect(vw.page.getByTestId('tbl-readonly')).toBeVisible()
    for (const name of ['Add rows', 'Share', 'Rename', 'Delete', 'New table', 'Add column', 'Delete selected']) {
      await expect(vw.page.getByRole('button', { name })).toHaveCount(0)
    }
    await expect(vw.page.locator('input[type="checkbox"]')).toHaveCount(0)
    await expect(vw.page.getByRole('button', { name: /^Edit row/ })).toHaveCount(0)
    await shoot(vw.page, 'tables-ui-viewer')

    // The server, not the page, decides: a crafted write of the viewer, a
    // crafted schema change and a crafted share of the editor are all refused
    // and nothing changed.
    const gen = await generation(member)
    const info = await read(member, state.budget, gen, { tool: 'databases-inspect' })
    const rev = info.json.payload.revision
    const crafted = await tab.apply(vw.page, state.budget, 'rows-insert', { table: 'Entries', rows: [{ Item: 'forged' }] }, rev, gen)
    expect(crafted.status).toBe(403)
    const schema = await tab.apply(ed.page, state.budget, 'tables-drop', { table: 'Entries' }, rev, gen)
    expect(schema.status).toBe(403)
    const share = await pageFetch(ed.page, `/api/addons/resources/${state.budget}/grants/${creds.users.viewer.id}`, { method: 'PUT', body: { role: 'editor' }, headers: { 'X-LocalAI-Confirm': state.budget } })
    expect(share.status).toBe(403)
    const rename = await pageFetch(vw.page, `/api/addons/resources/${state.budget}/rename`, { method: 'POST', body: { operationId: 'op-x', generation: gen, revision: rev, name: 'Mine now' }, headers: { 'X-LocalAI-Confirm': state.budget } })
    expect(rename.status).toBe(403)
    // The viewer's own key is not a browser tab either.
    const keyed = await (await apiAs('viewer')).post(`/api/addons/resources/${state.budget}/apply`, { data: { operationId: 'op-y', generation: gen, revision: rev, tool: 'rows-insert', arguments: { table: 'Entries', rows: [{ Item: 'forged' }] } }, headers: { 'X-LocalAI-Confirm': state.budget } })
    expect(keyed.status()).toBe(403)
    const page = await read(member, state.budget, gen, { tool: 'rows-query', table: 'Entries' })
    expect(page.json.payload.rows).toHaveLength(3)
    const after = await read(member, state.budget, gen, { tool: 'databases-inspect' })
    expect(after.json.payload.detail[0].name).toBe('Entries')
    await owner.context.close()
    await ed.context.close()
    await vw.context.close()
  })

  test('tables-ui: typed editors, structured filters, sort, paging and bounded batches work on real rows', async ({ browser }) => {
    const owner = await browserAs(browser, 'member')
    const p = owner.page
    await p.goto(ROUTE)
    await expect(p.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    const typed = await seedTyped(p, member, 'Typed values', 60)
    state.typed = typed
    await openDb(p, typed.id, 'Typed')
    await expect(rowsOf(p)).toHaveCount(50, { timeout: 25_000 })
    await expect(p.getByTestId('tbl-page')).toHaveText('Page 1')

    // Paging: 25 rows a page, the add-on's cursor forward and back.
    await p.getByLabel('Rows per page').selectOption('25')
    await expect(rowsOf(p)).toHaveCount(25)
    await expect.poll(async () => (await names(p))[0]).toBe('item-001')
    await p.getByRole('button', { name: 'Next' }).click()
    await expect(p.getByTestId('tbl-page')).toHaveText('Page 2')
    await expect.poll(async () => (await names(p))[0]).toBe('item-026')
    await p.getByRole('button', { name: 'Next' }).click()
    await expect(rowsOf(p)).toHaveCount(10)
    await expect(p.getByRole('button', { name: 'Next' })).toBeDisabled()
    await p.getByRole('button', { name: 'Previous' }).click()
    await expect(p.getByTestId('tbl-page')).toHaveText('Page 2')
    await p.getByRole('button', { name: 'Previous' }).click()
    await expect(p.getByTestId('tbl-page')).toHaveText('Page 1')
    await expect(rowsOf(p)).toHaveCount(25)
    await expect.poll(async () => (await names(p))[0]).toBe('item-001')

    // Sort: the column header cycles ascending, descending and none.
    await p.getByRole('button', { name: 'Sort by Count' }).click()
    await p.getByRole('button', { name: 'Sort by Count' }).click()
    await expect(p.locator('th[aria-sort="descending"]')).toContainText('Count')
    await expect.poll(async () => (await names(p))[0]).toBe('item-060')
    await p.getByRole('button', { name: 'Sort by Count' }).click()
    await expect(p.locator('th[aria-sort="descending"]')).toHaveCount(0)

    // Filter: structured conditions over typed columns, never free text.
    await p.getByRole('button', { name: 'Filter' }).click()
    const filters = p.getByTestId('tbl-filters')
    await filters.getByRole('button', { name: 'Add condition' }).click()
    const first = filters.getByTestId('tbl-condition').nth(0)
    await first.getByLabel('Column', { exact: true }).selectOption('Count')
    await first.getByLabel('Condition', { exact: true }).selectOption('gte')
    await first.getByLabel('Value', { exact: true }).fill('x')
    await filters.getByRole('button', { name: 'Apply' }).click()
    await expect(first.getByRole('alert')).toContainText('whole number')
    await first.getByLabel('Value', { exact: true }).fill('55')
    await filters.getByRole('button', { name: 'Apply' }).click()
    await expect(rowsOf(p)).toHaveCount(6, { timeout: 25_000 })
    await filters.getByRole('button', { name: 'Add condition' }).click()
    const secondCond = filters.getByTestId('tbl-condition').nth(1)
    await secondCond.getByLabel('Column', { exact: true }).selectOption('Paid')
    await secondCond.getByLabel('Value', { exact: true }).selectOption('true')
    await filters.getByRole('button', { name: 'Apply' }).click()
    // Paid is true for the odd item numbers: 55, 57, 59.
    await expect(rowsOf(p)).toHaveCount(3, { timeout: 25_000 })
    await expect.poll(() => names(p)).toEqual(['item-055', 'item-057', 'item-059'])
    await shoot(p, 'tables-ui-filtered')
    await filters.getByRole('button', { name: 'Clear' }).click()
    await expect(rowsOf(p)).toHaveCount(25, { timeout: 25_000 })
    await p.getByRole('button', { name: 'Filter' }).first().click()

    // Typed editors: one row, every type, refused values named in the form.
    await p.getByRole('button', { name: 'Edit row 2', exact: true }).click()
    const edit = p.getByTestId('tbl-edit-dialog')
    await expect(edit.getByLabel('Name')).toHaveValue('item-002')
    await expect(edit.getByLabel('Due')).toHaveValue('2026-10-14T09:30')
    await edit.getByLabel('Price').fill('abc')
    await edit.getByRole('button', { name: 'Save' }).click()
    await expect(edit.getByRole('alert').first()).toContainText('Enter a number')
    await edit.getByLabel('Price').fill('19.75')
    await edit.getByLabel('Paid').selectOption('true')
    await edit.getByLabel('Due').fill('2027-01-02T03:04:05')
    await edit.getByLabel('Count').fill('9007199254740993')
    await edit.getByRole('button', { name: 'Save' }).click()
    await expect(edit).toHaveCount(0, { timeout: 25_000 })
    const row2 = p.locator('[data-row-id="2"]')
    await expect(row2.locator('[data-column="Price"]')).toHaveText('19.75')
    await expect(row2.locator('[data-column="Paid"]')).toHaveText('true')
    await expect(row2.locator('[data-column="Due"]')).toHaveText('2027-01-02 03:04:05 UTC')
    await expect(row2.locator('[data-column="Count"]')).toHaveText('9007199254740993')
    await shoot(p, 'tables-ui-edited')

    // Batch: select rows, set a value on all, then delete them.
    for (const id of [3, 4, 5]) await p.getByLabel(`Select row ${id}`, { exact: true }).check()
    await expect(p.getByTestId('tbl-selected-count')).toHaveText('3 selected')
    await p.getByTestId('tbl-selbar').getByRole('button', { name: 'Set a value' }).click()
    const set = p.getByTestId('tbl-set-dialog')
    await set.getByLabel('Column', { exact: true }).selectOption('Price')
    await set.getByLabel('New value').fill('1.5')
    await set.getByRole('button', { name: 'Change 3 rows' }).click()
    await expect(set).toHaveCount(0, { timeout: 25_000 })
    for (const id of [3, 4, 5]) await expect(p.locator(`[data-row-id="${id}"] [data-column="Price"]`)).toHaveText('1.5')
    for (const id of [3, 4, 5]) await p.getByLabel(`Select row ${id}`, { exact: true }).check()
    await p.getByTestId('tbl-selbar').getByRole('button', { name: 'Delete selected' }).click()
    const del = p.getByTestId('tbl-delete-rows-dialog')
    await expect(del).toContainText('Delete 3 selected rows')
    await del.getByRole('button', { name: 'Delete 3 rows' }).click()
    await expect(p.locator('[data-row-id="3"]')).toHaveCount(0, { timeout: 25_000 })
    const gone = await read(member, typed.id, typed.generation, { tool: 'rows-query', table: 'Typed', filter: { column: '_id', op: 'in', values: [3, 4, 5] } })
    expect(gone.json.payload.rows).toEqual([])

    // A stale revision is recovered in the dialog: another tab changed the
    // database, the save is refused as stale and names the way out.
    const second = await browserAs(browser, 'member')
    await second.page.goto(ROUTE)
    await expect(second.page.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    const listed = await tab.list(second.page)
    const current = await read(member, typed.id, typed.generation, { tool: 'databases-inspect' })
    const bump = await tab.apply(second.page, typed.id, 'rows-insert', { table: 'Typed', rows: [{ Name: 'from another tab' }] }, current.json.payload.revision, typed.generation)
    expect(bump.status).toBe(200)
    expect(listed.resources.length).toBeGreaterThan(0)
    await p.getByRole('button', { name: 'Edit row 6', exact: true }).click()
    const stale = p.getByTestId('tbl-edit-dialog')
    await stale.getByLabel('Name').fill('renamed item')
    await stale.getByRole('button', { name: 'Save' }).click()
    await expect(stale.getByTestId('tbl-error')).toContainText('changed since you loaded it', { timeout: 25_000 })
    await shoot(p, 'tables-ui-stale')
    await stale.getByRole('button', { name: 'Reload and apply again' }).click()
    await expect(stale).toHaveCount(0, { timeout: 25_000 })
    await expect(p.locator('[data-row-id="6"] [data-column="Name"]')).toHaveText('renamed item')
    await owner.context.close()
    await second.context.close()
  })

  test('tables-ui: editing another column leaves a datetime with a fraction of a second byte-identical', async ({ browser }) => {
    const owner = await browserAs(browser, 'member')
    const p = owner.page
    await p.goto(ROUTE)
    await expect(p.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    const db = await seedTyped(p, member, 'Fractions', 0)
    const seeded = await tab.apply(p, db.id, 'rows-insert', { table: 'Typed', rows: [
      { Name: 'nanos', Due: '2026-10-14T09:30:00.123456789Z' },
      { Name: 'half', Due: '2026-10-14T09:30:00.5Z' },
      { Name: 'offset', Due: '2026-10-14T11:30:00.25+02:00' },
    ] }, db.revision, db.generation)
    expect(seeded.status, JSON.stringify(seeded.json)).toBe(200)
    const ids = seeded.json.result.ids
    const dueOf = async () => {
      const r = await read(member, db.id, db.generation, { tool: 'rows-query', table: 'Typed' })
      return r.json.payload.rows.map(x => x.values[4])
    }
    const before = await dueOf()
    expect(before).toHaveLength(3)
    expect(before[0]).toContain('.123456789')
    await openDb(p, db.id, 'Typed')
    await expect(rowsOf(p)).toHaveCount(3, { timeout: 25_000 })
    for (const [i, id] of ids.entries()) {
      await p.getByRole('button', { name: `Edit row ${id}`, exact: true }).click()
      const edit = p.getByTestId('tbl-edit-dialog')
      await edit.getByLabel('Name').fill(`renamed-${i}`)
      await edit.getByRole('button', { name: 'Save' }).click()
      await expect(edit).toHaveCount(0, { timeout: 25_000 })
    }
    // The other column changed and the datetime column was never sent.
    const named = (await read(member, db.id, db.generation, { tool: 'rows-query', table: 'Typed' })).json.payload.rows.map(x => x.values[0])
    expect(named).toEqual(['renamed-0', 'renamed-1', 'renamed-2'])
    expect(await dueOf()).toEqual(before)
    await owner.context.close()
  })

  test('tables-ui: a revoke while a person is editing denies the next save and the page says it is unavailable', async ({ browser }) => {
    const ed = await browserAs(browser, 'editor')
    const own = await browserAs(browser, 'member')
    await own.page.goto(ROUTE)
    await expect(own.page.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    // Share the typed database with the editor through the owner's own page.
    const grant = await pageFetch(own.page, `/api/addons/resources/${state.typed.id}/grants/${creds.users.editor.id}`, { method: 'PUT', body: { role: 'editor' }, headers: { 'X-LocalAI-Confirm': state.typed.id } })
    expect(grant.status, JSON.stringify(grant.json)).toBe(200)
    await openDb(ed.page, state.typed.id, 'Typed')
    await expect(rowsOf(ed.page).first()).toBeVisible({ timeout: 25_000 })
    await ed.page.getByRole('button', { name: /^Edit row 7$/ }).click()
    const form = ed.page.getByTestId('tbl-edit-dialog')
    await form.getByLabel('Name').fill('edited while revoked')
    // The owner revokes between the open and the save.
    const revoke = await pageFetch(own.page, `/api/addons/resources/${state.typed.id}/grants/${creds.users.editor.id}`, { method: 'DELETE', headers: { 'X-LocalAI-Confirm': state.typed.id } })
    expect(revoke.status).toBe(200)
    await form.getByRole('button', { name: 'Save' }).click()
    await expect(form.getByTestId('tbl-error')).toContainText('do not have access', { timeout: 25_000 })
    await expect(ed.page.getByTestId('tbl-unavailable')).toBeVisible({ timeout: 25_000 })
    await shoot(ed.page, 'tables-ui-revoked')
    // Nothing of the failed save reached the database.
    const gen = await generation(member)
    const row = await read(member, state.typed.id, gen, { tool: 'rows-query', table: 'Typed', filter: { column: '_id', op: 'eq', value: 7 } })
    expect(row.json.payload.rows[0].values[0]).toBe('item-007')
    // The revoked person's own listing and a crafted read agree.
    expect((await tab.list(ed.page)).resources.map(r => r.id)).not.toContain(state.typed.id)
    const crafted = await tab.apply(ed.page, state.typed.id, 'rows-insert', { table: 'Typed', rows: [{ Name: 'late' }] }, row.json.payload.revision, gen)
    expect(crafted.status).toBe(403)
    await ed.context.close()
    await own.context.close()
  })

  test('tables-ui: an installation administrator sees no database of other people and has no data privilege', async ({ browser }) => {
    const adm = await browserAs(browser, 'admin')
    await adm.page.goto(ROUTE)
    await expect(adm.page.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    await expect(adm.page.getByText('No databases yet')).toBeVisible()
    await expect(adm.page.getByTestId('tbl-database')).toHaveCount(0)
    await adm.page.goto(`${ROUTE}/${state.budget}`)
    await expect(adm.page.getByTestId('tbl-unavailable')).toBeVisible({ timeout: 25_000 })
    await expect(adm.page.getByText('Entries')).toHaveCount(0)
    // Every route answers as it does for any person without a grant.
    const gen = await generation(admin)
    const dispatch = await read(admin, state.budget, gen, { tool: 'rows-query', table: 'Entries' })
    expect(dispatch.status).toBe(403)
    const apply = await tab.apply(adm.page, state.budget, 'rows-delete', { table: 'Entries', ids: [1] }, { schema: 1, data: 0 }, gen)
    expect(apply.status).toBe(403)
    const grants = await pageFetch(adm.page, `/api/addons/resources/${state.budget}/grants`, { headers: { 'X-LocalAI-Confirm': state.budget } })
    expect(grants.status).toBe(403)
    const remove = await pageFetch(adm.page, `/api/addons/resources/${state.budget}/delete`, { method: 'POST', body: { operationId: 'op-z', generation: gen, revision: { schema: 1, data: 0 }, confirm: 'Household budget' }, headers: { 'X-LocalAI-Confirm': state.budget } })
    expect(remove.status).toBe(403)
    // The administrator's own database is theirs and nobody else's.
    await adm.page.goto(ROUTE)
    await adm.page.getByRole('button', { name: 'New database' }).click()
    await adm.page.getByTestId('tbl-name-dialog').getByLabel('Name').fill('Admin notes')
    await adm.page.getByTestId('tbl-name-dialog').getByRole('button', { name: 'Create database' }).click()
    await expect(adm.page.getByTestId('tbl-db-name')).toHaveText('Admin notes', { timeout: 25_000 })
    const mine = await (await member.get('/api/addons/resources?addonId=tables')).json()
    expect(mine.resources.map(r => r.name)).not.toContain('Admin notes')
    await adm.context.close()
  })

  test('tables-ui: deleting a database shows the warnings, needs the typed name and removes data and shares', async ({ browser }) => {
    const own = await browserAs(browser, 'member')
    const p = own.page
    const gen = await generation(member)
    await p.goto(ROUTE)
    await expect(p.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    const doomed = await tab.create(p, 'Short lived', gen)
    // Share it so the shares are part of what is removed.
    const g = await pageFetch(p, `/api/addons/resources/${doomed.id}/grants/${creds.users.viewer.id}`, { method: 'PUT', body: { role: 'viewer' }, headers: { 'X-LocalAI-Confirm': doomed.id } })
    expect(g.status).toBe(200)
    await p.reload()
    const entry = p.locator(`[data-testid="tbl-database"][data-resource="${doomed.id}"]`)
    await expect(entry).toBeVisible({ timeout: 25_000 })
    await entry.getByRole('button', { name: /^Delete / }).click()
    const form = p.getByTestId('tbl-delete-dialog')
    await expect(form).toBeVisible()
    const warnings = form.getByTestId('tbl-delete-warnings')
    await expect(warnings).toContainText('deletes the database "Short lived"')
    await expect(warnings).toContainText('cannot be undone')
    await expect(warnings).toContainText('every share')
    await shoot(p, 'tables-ui-delete')
    const confirm = form.getByRole('button', { name: 'Delete the database' })
    await expect(confirm).toBeDisabled()
    await form.getByLabel('Type the name of the database to confirm').fill('short lived')
    await expect(confirm).toBeDisabled()
    await form.getByLabel('Type the name of the database to confirm').fill('Short lived')
    await expect(confirm).toBeEnabled()
    await confirm.click()
    await expect(entry).toHaveCount(0, { timeout: 25_000 })
    // Gone for everybody: the owner's list, the viewer's list, a read, and the
    // add-on stays installed and running.
    const mine = await (await member.get('/api/addons/resources?addonId=tables')).json()
    expect(mine.resources.map(r => r.id)).not.toContain(doomed.id)
    const viewer = await apiAs('viewer')
    expect((await (await viewer.get('/api/addons/resources?addonId=tables')).json()).resources.map(r => r.id)).not.toContain(doomed.id)
    expect((await read(member, doomed.id, gen, { tool: 'databases-inspect' })).status).toBe(403)
    const apps = await (await admin.get('/api/addons/apps')).json()
    expect(apps.apps.find(a => a.id === 'tables').state).toBe('running')
    await viewer.dispose()
    await own.context.close()
  })

  test('tables-ui: a tab of an older generation is denied after an upgrade and after a disable', async ({ browser }) => {
    const own = await browserAs(browser, 'member')
    const p = own.page
    const oldGen = await generation(admin)
    await openDb(p, state.budget, 'Entries')
    await expect(rowsOf(p).first()).toBeVisible({ timeout: 25_000 })
    const current = await read(member, state.budget, oldGen, { tool: 'databases-inspect' })
    const revision = current.json.payload.revision

    // The administrator installs the next release.
    const up = await admin.post('/api/addons/dispatch', {
      data: { operationId: `op-${randomUUID()}`, addonId: 'tables', generation: oldGen, action: 'upgrade', arguments: { sourceId: 'fixture', version: '0.1.1' } },
    })
    expect(up.status(), await up.text()).toBe(200)
    await expect.poll(() => generation(admin), { timeout: 30_000 }).toBeGreaterThan(oldGen)
    const newGen = await generation(admin)
    // The open page is withdrawn at the next list, and a request that still
    // names the old generation is refused as stale by the server.
    await expect(p.getByTestId('addon-unavailable').or(p.getByTestId('addon-stale'))).toBeVisible({ timeout: 30_000 })
    await shoot(p, 'tables-ui-upgraded')
    const old = await tab.apply(p, state.budget, 'rows-insert', { table: 'Entries', rows: [{ Item: 'old tab' }] }, revision, oldGen)
    expect(old.status).toBe(409)
    expect(old.json.error.type).toBe('stale')
    const oldRead = await read(member, state.budget, oldGen, { tool: 'rows-query', table: 'Entries' })
    expect(oldRead.status).toBe(403)
    // A reload loads the new package and works again.
    await openDb(p, state.budget, 'Entries')
    await expect(rowsOf(p).first()).toBeVisible({ timeout: 25_000 })
    expect((await read(member, state.budget, newGen, { tool: 'rows-query', table: 'Entries' })).status).toBe(200)

    // Disabling withdraws the page and every call.
    const stop = await admin.post('/api/addons/apps/tables/stop', { data: { operationId: randomUUID(), generation: newGen } })
    expect(stop.status(), await stop.text()).toBe(200)
    await expect(p.getByTestId('addon-unavailable').or(p.getByTestId('addon-stale'))).toBeVisible({ timeout: 30_000 })
    const disabled = await tab.apply(p, state.budget, 'rows-insert', { table: 'Entries', rows: [{ Item: 'disabled' }] }, revision, newGen)
    expect([403, 409, 503]).toContain(disabled.status)
    const unread = await read(member, state.budget, newGen, { tool: 'rows-query', table: 'Entries' })
    expect([403, 503]).toContain(unread.status)
    expect(unread.json && unread.json.payload).toBeFalsy()
    const start = await admin.post('/api/addons/apps/tables/start', { data: { operationId: randomUUID(), generation: newGen } })
    expect(start.status(), await start.text()).toBe(200)
    await expect.poll(async () => (await (await admin.get('/api/addons/apps')).json()).apps.find(a => a.id === 'tables')?.state, { timeout: 30_000 }).toBe('running')
    await own.context.close()
  })

  test('tables-ui: the pages work by keyboard, at 400 pixels and in the light and dark themes', async ({ browser }) => {
    const own = await browserAs(browser, 'member', { viewport: { width: 400, height: 800 } })
    const p = own.page
    await p.goto(ROUTE)
    await expect(p.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    expect(await noOverflow(p)).toBe(true)
    await shoot(p, 'tables-ui-400-list')

    // Keyboard: Tab reaches the New database button, Enter opens the
    // dialog, the field has focus, Escape closes and focus comes back.
    await p.getByRole('button', { name: 'New database' }).first().focus()
    await p.keyboard.press('Enter')
    const form = p.getByTestId('tbl-name-dialog')
    await expect(form).toBeVisible()
    await expect(form.getByLabel('Name')).toBeFocused()
    await p.keyboard.type('Keyboard only')
    await p.keyboard.press('Escape')
    await expect(form).toHaveCount(0)

    // A database page at 400 pixels: the grid scrolls inside its box.
    await openDb(p, state.typed.id, 'Typed')
    await expect(rowsOf(p).first()).toBeVisible({ timeout: 25_000 })
    expect(await noOverflow(p)).toBe(true)
    await shoot(p, 'tables-ui-400-grid')
    const wrap = p.getByTestId('tbl-grid-wrap')
    expect(await wrap.evaluate(el => el.scrollWidth > el.clientWidth)).toBe(true)

    // Keyboard on the grid: a header button sorts, an edit button opens the
    // row, Escape closes it.
    await p.getByRole('button', { name: 'Sort by Name' }).focus()
    await p.keyboard.press('Enter')
    await expect(p.locator('th[aria-sort="ascending"]')).toContainText('Name')
    await p.getByRole('button', { name: /^Edit row/ }).first().focus()
    await p.keyboard.press('Enter')
    await expect(p.getByTestId('tbl-edit-dialog')).toBeVisible()
    await expect(p.getByTestId('tbl-edit-dialog').getByLabel('Name')).toBeFocused()
    await shoot(p, 'tables-ui-400-edit')
    await p.keyboard.press('Escape')
    await expect(p.getByTestId('tbl-edit-dialog')).toHaveCount(0)
    expect(await noOverflow(p)).toBe(true)
    await own.context.close()

    // The same pages in both themes, at a desktop width.
    for (const mode of ['light', 'dark']) {
      const ctx = await browser.newContext({ baseURL: BASE, viewport: { width: 1280, height: 800 } })
      await ctx.addCookies([{ name: 'session', value: creds.users.member.session, url: BASE }])
      await ctx.addInitScript((m) => localStorage.setItem('localai-theme', m), mode)
      const page = await ctx.newPage()
      if (process.env.TABLES_UI_DEBUG) {
        page.on('response', async r => { if (r.status() >= 400) console.log('refused', mode, r.status(), r.request().method(), r.url().replace(BASE, ''), (await r.text().catch(() => '')).slice(0, 200)) })
        page.on('pageerror', e => console.log('pageerror', mode, e.message))
        page.on('console', m => { if (m.type() === 'error') console.log('console', mode, m.text().slice(0, 200)) })
        page.on('requestfailed', r => console.log('requestfailed', mode, r.url().replace(BASE, ''), r.failure() && r.failure().errorText))
      }
      try {
        await openDb(page, state.typed.id, 'Typed')
      } catch (e) {
        if (process.env.TABLES_UI_DEBUG) console.log('body', mode, (await page.locator('main').innerText().catch(() => 'no main')).slice(0, 400))
        throw e
      }
      await expect(rowsOf(page).first()).toBeVisible({ timeout: 25_000 })
      await expect(page.locator('html')).toHaveAttribute('data-theme', mode)
      await shoot(page, `tables-ui-${mode}-grid`)
      // The text and the surface contrast in both themes.
      const colors = await page.getByTestId('tbl-grid').evaluate((el) => {
        const cell = el.querySelector('td.tbl-td--id') || el.querySelector('td')
        const style = getComputedStyle(el.querySelector('tbody td[data-column="Name"]'))
        const head = getComputedStyle(el.querySelector('th'))
        return { text: style.color, headBackground: head.backgroundColor, cell: Boolean(cell) }
      })
      expect(colors.text).not.toBe('')
      expect(colors.headBackground).not.toBe('rgba(0, 0, 0, 0)')
      await ctx.close()
    }
  })

  test('tables-ui: logging out clears what was read and the next person on the same tab sees none of it', async ({ browser }) => {
    const own = await browserAs(browser, 'member')
    const p = own.page
    const gen = await generation(admin)
    await p.goto(ROUTE)
    await expect(p.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    const db = await tab.create(p, 'Sensitive', gen)
    let rev = db.revision
    for (const [tool, args] of [['tables-create', { table: 'Vault' }], ['columns-add', { table: 'Vault', column: 'Secret', type: 'text' }], ['rows-insert', { table: 'Vault', rows: [{ Secret: SECRET }] }]]) {
      const r = await tab.apply(p, db.id, tool, args, rev, gen)
      expect(r.status).toBe(200)
      rev = r.json.revision
    }
    // A second person reads it too, for the sign-out of their own tab.
    const share = await pageFetch(p, `/api/addons/resources/${db.id}/grants/${creds.users.viewer.id}`, { method: 'PUT', body: { role: 'viewer' }, headers: { 'X-LocalAI-Confirm': db.id } })
    expect(share.status).toBe(200)
    await openDb(p, db.id, 'Vault')
    await expect(p.getByText(SECRET)).toBeVisible({ timeout: 25_000 })
    await expect(p.locator('style[data-addon-style="tables"]')).toHaveCount(1)
    // The add-on keeps nothing in browser storage.
    const storage = async () => p.evaluate(async () => {
      const dbs = (indexedDB.databases ? await indexedDB.databases() : []).map(d => d.name)
      return JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage }, idb: dbs })
    })
    expect(await storage()).not.toContain(SECRET)

    // The person's own Logout button does the same at once.
    const second = await browserAs(browser, 'viewer')
    await openDb(second.page, db.id, 'Vault')
    await expect(second.page.getByText(SECRET)).toBeVisible({ timeout: 25_000 })
    await second.page.getByRole('button', { name: 'Logout' }).click()
    await expect(second.page.getByText(SECRET)).toHaveCount(0, { timeout: 25_000 })
    await expect(second.page.locator('style[data-addon-style="tables"]')).toHaveCount(0)
    await expect(second.page.getByTestId('tbl-database-page')).toHaveCount(0)
    await shoot(second.page, 'tables-ui-logged-out')
    await second.context.close()

    // The session ends in another tab: the next read of the host is refused,
    // and the page, its style and what it read are gone with it.
    const out = await pageFetch(p, '/api/auth/logout', { method: 'POST' })
    expect([200, 204]).toContain(out.status)
    await expect(p.getByText(SECRET)).toHaveCount(0, { timeout: 40_000 })
    await expect(p.locator('style[data-addon-style="tables"]')).toHaveCount(0, { timeout: 40_000 })
    await expect(p.getByTestId('tbl-database-page')).toHaveCount(0)
    expect(await storage()).not.toContain(SECRET)

    // Another person signs in on the same tab and opens the same address:
    // while their request is held, nothing of the first person is drawn.
    await p.context().clearCookies()
    await p.context().addCookies([{ name: 'session', value: creds.users.editor.session, url: BASE }])
    let release
    const held = new Promise(r => { release = r })
    await p.route('**/api/addons/dispatch', async (route) => { await held; await route.continue() })
    await p.goto(`${ROUTE}/${db.id}?table=Vault`)
    await expect(p.getByText(SECRET)).toHaveCount(0)
    await p.waitForTimeout(500)
    await expect(p.getByText(SECRET)).toHaveCount(0)
    release()
    await expect(p.getByTestId('tbl-unavailable')).toBeVisible({ timeout: 30_000 })
    await expect(p.getByText(SECRET)).toHaveCount(0)
    expect(await storage()).not.toContain(SECRET)
    await own.context.close()
  })

  test('tables-ui: in local-owner mode sharing is disabled and the owner still manages databases', async ({ browser }) => {
    const context = await browser.newContext({ baseURL: LOCAL })
    const page = await context.newPage()
    const requests = []
    page.on('request', r => requests.push(r.method() + ' ' + new URL(r.url()).pathname))
    await page.goto(`${LOCAL}${ROUTE}`)
    await expect(page.getByTestId('tbl-list-page')).toBeVisible({ timeout: 25_000 })
    await page.getByRole('button', { name: 'New database' }).click()
    await page.getByTestId('tbl-name-dialog').getByLabel('Name').fill('Local notes')
    await page.getByTestId('tbl-name-dialog').getByRole('button', { name: 'Create database' }).click()
    await expect(page.getByTestId('tbl-db-name')).toHaveText('Local notes', { timeout: 25_000 })
    // Nobody to share with, so the page does not offer the button at all.
    await expect(page.getByRole('button', { name: 'Rename' })).toBeVisible({ timeout: 25_000 })
    await expect(page.getByRole('button', { name: 'Share' })).toHaveCount(0)
    await shoot(page, 'tables-ui-local-sharing')
    // No grant or picker request is ever made, and the host says the same.
    expect(requests.filter(r => /\/grants|\/users/.test(r))).toEqual([])
    const id = await page.getByTestId('tbl-database-page').getAttribute('data-resource')
    const direct = await pageFetch(page, `/api/addons/resources/${id}/grants`, { headers: { 'X-LocalAI-Confirm': id } })
    expect(direct.status).toBe(409)
    expect(direct.json.error.type).toBe('sharing_disabled')
    const list = await tab.list(page)
    expect(list.sharing).toBe(false)
    await context.close()
  })
})
