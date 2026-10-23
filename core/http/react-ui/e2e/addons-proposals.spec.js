import { test, expect, request as playwrightRequest } from '@playwright/test'
import fs from 'node:fs'
import { randomUUID } from 'node:crypto'
import { chatOf, mockChat } from './chat-fixtures.js'
import { seedChats, CHATS_KEY } from './home-fixtures.js'

// Add-on proposals from chat. The UI test server runs with real
// authentication, a real add-on owner and the real Tables executable. The
// scripted model (tests/e2e/mock-backend) answers its first turn with the tool
// call the spec encodes in the message, as a model that chose the tool would;
// the call then goes through the real chat handler, the real broker and the
// real owner. Only the chat model list and system probes are stubbed.
//
// The specs change the owner's state and run in order on one worker.

const creds = JSON.parse(fs.readFileSync(process.env.ADDONS_FIXTURE, 'utf8'))
const BASE = process.env.ADDONS_UI_URL
const shots = process.env.ADDONS_SHOTS
const SECRET = 'ROW-SECRET-4411'

async function shoot(locator, name, page) {
  if (!shots) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(300)
  await locator.screenshot({ path: `${shots}/${name}.png` })
}

async function browserAs(browser, who, extra = {}) {
  const context = await browser.newContext({ baseURL: BASE, ...extra })
  await context.addCookies([{ name: 'session', value: creds.users[who].session, url: BASE }])
  const page = await context.newPage()
  return { context, page }
}

async function apiAs(who) {
  const user = creds.users[who]
  return playwrightRequest.newContext({ baseURL: BASE, extraHTTPHeaders: { Authorization: `Bearer ${user.token}` } })
}

// A fetch made by the page itself: a real same-origin request that carries the
// session cookie and the browser's own Sec-Fetch metadata, which is what the
// host confirmation surface requires.
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

const b64url = (s) => Buffer.from(s).toString('base64url')
const toolMessage = (tool, args) => `Please run this ADDON_TOOL_CALL:addon__tables__${tool}:${b64url(JSON.stringify(args))}`

async function openChat(page, { id = 'c1' } = {}) {
  await mockChat(page, { models: [{ id: 'mock-model', capabilities: ['FLAG_CHAT'] }], loaded: ['mock-model'] })
  await seedChats(page, [chatOf(id, 'Tables', 'mock-model', [], { addonTools: true })], id)
  await page.goto('/app/chat')
  await page.getByTestId('chat-composer').waitFor()
}

async function say(page, text) {
  await page.getByTestId('chat-input').fill(text)
  await page.getByTestId('chat-send').click()
}

const cards = (page) => page.getByTestId('proposal-card')

async function tool(page, name, args) {
  const before = await cards(page).count()
  await say(page, toolMessage(name, args))
  await expect(cards(page)).toHaveCount(before + 1, { timeout: 40_000 })
  return cards(page).nth(before)
}

async function pendingOf(api) {
  const r = await api.get('/api/addons/proposals?state=pending')
  expect(r.status()).toBe(200)
  return (await r.json()).proposals
}

// Every confirmation the specs make by hand is the person's own tab.
async function confirmIn(page, p) {
  return pageFetch(page, `/api/addons/proposals/${encodeURIComponent(p.id)}/confirm`, {
    method: 'POST', body: { digest: p.digest }, headers: { 'X-LocalAI-Confirm': p.id },
  })
}

// What the chat stores for a tool result of an add-on is the reference of the
// proposal: no rows, no preview, no digest. (The model's own words are the
// conversation and are not a host result.)
function expectReferenceOnly(stored) {
  const chats = JSON.parse(stored).chats
  const results = chats.flatMap(c => c.history).filter(m => m.role === 'tool_result')
  expect(results.length).toBeGreaterThan(0)
  for (const m of results) {
    const doc = JSON.parse(m.content)
    expect(Object.keys(doc).sort()).toEqual(['name', 'result', 'type'])
    const held = JSON.parse(doc.result)
    expect(held).toEqual({ kind: 'proposal', proposal: { id: expect.any(String) } })
  }
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

async function rowCount(resource) {
  const app = (await (await member.get('/api/addons/apps')).json()).apps.find(a => a.id === 'tables')
  const r = await member.post('/api/addons/dispatch', {
    data: { operationId: `op-${randomUUID()}`, addonId: 'tables', resourceId: resource, generation: app.generation, action: 'read', arguments: { tool: 'rows-query', table: 'Entries' } },
  })
  expect(r.status()).toBe(200)
  return (await r.json()).payload.rows.length
}

test.describe('Add-on proposals in chat', () => {
  test('proposals: a tool call in chat stores a pending proposal and the card shows the host record, not the model text', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    const card = await tool(page, 'databases-create', { name: 'Ledger' })
    await expect(card.getByTestId('proposal-addon')).toHaveText('tables')
    await expect(card.getByTestId('proposal-database')).toHaveText('Ledger')
    await expect(card.getByTestId('proposal-operation')).toHaveText('databases-create')
    await expect(card.getByTestId('proposal-count')).toHaveText('1')
    await expect(card.getByTestId('proposal-state')).toHaveText('Waiting for your decision')
    await expect(card.getByTestId('proposal-confirm')).toBeEnabled()
    await expect(card.getByTestId('proposal-cancel')).toBeEnabled()
    await shoot(card, 'proposal-pending-create', page)
    // The host holds exactly this proposal, pending, for the proposer only.
    const list = await pendingOf(member)
    expect(list).toHaveLength(1)
    expect(list[0].action).toBe('create')
    expect(list[0].state).toBe('pending')
    expect(await pendingOf(admin)).toHaveLength(0)
    await expect(card).toHaveAttribute('data-proposal', list[0].id)
    state.create = list[0]
    // Nothing exists for anybody before the person decides.
    const apps = await (await member.get('/api/addons/apps')).json()
    expect(apps.apps.find(a => a.id === 'tables').toolDefinitions.length).toBeGreaterThan(0)
    await context.close()
  })

  test('proposals: confirming in the card applies the change exactly once and a repeat returns the stored receipt', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    const card = await tool(page, 'databases-create', { name: 'Books' })
    await expect(card.getByTestId('proposal-database')).toHaveText('Books')
    const p = (await pendingOf(member)).find(x => x.effects.sample && x.effects.sample.name === 'Books')
    await card.getByTestId('proposal-confirm').click()
    await expect(card.getByTestId('proposal-state')).toHaveText('Applied', { timeout: 20_000 })
    await expect(card.getByTestId('proposal-confirm')).toBeDisabled()
    await expect(card.getByTestId('proposal-cancel')).toBeDisabled()
    await shoot(card, 'proposal-applied', page)
    state.db = p.resourceId
    // A second confirmation of the same proposal returns the stored receipt.
    const again = await confirmIn(page, p)
    expect(again.status).toBe(200)
    expect(again.json.state).toBe('applied')
    // The database exists now, for its owner only.
    const inspect = await member.post('/api/addons/dispatch', {
      data: { operationId: `op-${randomUUID()}`, addonId: 'tables', resourceId: state.db, generation: p.generation, action: 'read', arguments: { tool: 'databases-inspect' } },
    })
    expect(inspect.status(), await inspect.text()).toBe(200)
    expect((await inspect.json()).payload.name).toBe('Books')
    await context.close()
  })

  test('proposals: row changes show the exact count and a bounded sample, and nothing changes until the person confirms', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    const app = (await (await member.get('/api/addons/apps')).json()).apps.find(a => a.id === 'tables')
    const schema = async (args) => {
      const op = `op-${randomUUID()}`
      const r = await member.post('/api/addons/proposals', { data: { operationId: op, addonId: 'tables', resourceId: state.db, generation: app.generation, action: 'schema', arguments: args } })
      expect(r.status()).toBe(200)
      const p = await r.json()
      const done = await confirmIn(page, p)
      expect(done.json.state).toBe('applied')
    }
    await schema({ tool: 'tables-create', table: 'Entries' })
    await schema({ tool: 'columns-add', table: 'Entries', column: 'Memo', type: 'text' })
    const card = await tool(page, 'rows-insert', {
      resourceId: state.db, table: 'Entries', rows: [{ Memo: SECRET }, { Memo: 'two' }, { Memo: 'three' }],
    })
    await expect(card.getByTestId('proposal-operation')).toHaveText('rows-insert')
    await expect(card.getByTestId('proposal-count')).toHaveText('3')
    await expect(card.getByTestId('proposal-database')).toHaveText(state.db)
    await expect(card.getByTestId('proposal-sample')).toContainText(SECRET)
    await shoot(card, 'proposal-rows-pending', page)
    expect(await rowCount(state.db)).toBe(0)
    await card.getByTestId('proposal-confirm').click()
    await expect(card.getByTestId('proposal-state')).toHaveText('Applied', { timeout: 20_000 })
    expect(await rowCount(state.db)).toBe(3)
    // The stored conversation keeps the reference and no data.
    const readStored = () => page.evaluate((key) => localStorage.getItem(key) || '', CHATS_KEY)
    // The chat writes its storage after a short delay.
    await expect.poll(readStored, { timeout: 15_000 }).toContain('tool_result')
    const stored = await readStored()
    expect(stored).toContain('addon__tables__rows-insert')
    expect(stored).not.toContain('digest')
    expectReferenceOnly(stored)
    // Reloading the page draws the card again from the host.
    await page.reload()
    const again = page.getByTestId('proposal-card').last()
    await expect(again.getByTestId('proposal-state')).toHaveText('Applied', { timeout: 20_000 })
    await expect(again.getByTestId('proposal-confirm')).toBeDisabled()
    expect(await rowCount(state.db)).toBe(3)
    await context.close()
  })

  test('proposals: cancel withdraws the proposal and nothing is applied', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    const card = await tool(page, 'rows-delete', { resourceId: state.db, table: 'Entries', filter: { column: 'Memo', op: 'eq', value: 'two' } })
    await expect(card.getByTestId('proposal-count')).toHaveText('1')
    await card.getByTestId('proposal-cancel').click()
    await expect(card.getByTestId('proposal-state')).toHaveText('Cancelled', { timeout: 20_000 })
    await expect(card.getByTestId('proposal-confirm')).toBeDisabled()
    await shoot(card, 'proposal-cancelled', page)
    expect(await rowCount(state.db)).toBe(3)
    await context.close()
  })

  test('proposals: an expired proposal is disabled in the card and the host refuses a late confirmation', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await page.clock.install()
    await openChat(page)
    const card = await tool(page, 'rows-delete', { resourceId: state.db, table: 'Entries', filter: { column: 'Memo', op: 'eq', value: 'three' } })
    await expect(card.getByTestId('proposal-confirm')).toBeEnabled()
    const pending = (await pendingOf(member)).find(p => p.action === 'write')
    // Six minutes pass in the page; the proposal lives five.
    await page.clock.fastForward(6 * 60 * 1000)
    await expect(card.getByTestId('proposal-state')).toHaveText('Expired', { timeout: 20_000 })
    await expect(card.getByTestId('proposal-confirm')).toBeDisabled()
    await expect(card.getByTestId('proposal-cancel')).toBeDisabled()
    await expect(card.getByTestId('proposal-notice')).toBeVisible()
    await shoot(card, 'proposal-expired', page)
    await context.close()
    // The owner's own clock decides for real: nothing was applied by the card.
    expect(await rowCount(state.db)).toBe(3)
    expect(pending.id).toBeTruthy()
  })

  test('proposals: forged fields and approval names in a tool call create no proposal', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    const before = (await pendingOf(member)).length
    const base = { resourceId: state.db, table: 'Entries', rows: [{ Memo: 'x' }] }
    const calls = [
      ['rows-insert', { ...base, approved: true }],
      ['rows-insert', { ...base, userId: 'fixture-admin' }],
      ['rows-insert', { ...base, role: 'owner' }],
      ['rows-insert', { ...base, proposalId: 'x' }],
      ['confirm', { proposalId: 'x' }],
      ['approve', {}],
      ['databases-delete', { resourceId: state.db }],
    ]
    // Each call ends in the scripted final answer, which the model gives
    // only after the host put a tool result into the prompt.
    for (const [i, [name, args]] of calls.entries()) {
      await say(page, toolMessage(name, args))
      await expect(page.getByText('The change is waiting for your confirmation.')).toHaveCount(i + 1, { timeout: 40_000 })
    }
    expect(await pendingOf(member)).toHaveLength(before)
    expect(await rowCount(state.db)).toBe(3)
    await expect(cards(page)).toHaveCount(0)
    await context.close()
  })

  test('proposals: a user whose add-ons feature is withdrawn sees no data and the host answers no', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    const card = await tool(page, 'rows-insert', { resourceId: state.db, table: 'Entries', rows: [{ Memo: 'late' }] })
    await expect(card.getByTestId('proposal-database')).toHaveText(state.db)
    const p = (await pendingOf(member)).find(x => x.action === 'write')
    // An administrator withdraws the feature.
    const revoke = await admin.put(`/api/auth/admin/users/${creds.users.member.id}/permissions`, { data: { addons: false } })
    expect(revoke.status()).toBe(200)
    await page.evaluate(() => window.dispatchEvent(new Event('focus')))
    await expect(card.getByTestId('proposal-withdrawn')).toBeVisible({ timeout: 20_000 })
    await expect(card.getByTestId('proposal-database')).toHaveCount(0)
    await expect(card.getByTestId('proposal-sample')).toHaveCount(0)
    await expect(card.getByTestId('proposal-confirm')).toHaveCount(0)
    await expect(card).not.toContainText(state.db)
    await shoot(card, 'proposal-withdrawn', page)
    const denied = await confirmIn(page, p)
    expect(denied.status).toBe(403)
    expect((await member.get('/api/addons/proposals?state=pending')).status()).toBe(403)
    const read = await member.post('/api/addons/dispatch', {
      data: { operationId: `op-${randomUUID()}`, addonId: 'tables', resourceId: state.db, generation: 1, action: 'read', arguments: { tool: 'rows-query', table: 'Entries' } },
    })
    expect(read.status()).toBe(403)
    await context.close()
    // Restore the feature for the next spec.
    expect((await admin.put(`/api/auth/admin/users/${creds.users.member.id}/permissions`, { data: { addons: true } })).status()).toBe(200)
  })

  test('proposals: logging out clears the card and leaves no data in the stored conversation', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    const card = await tool(page, 'rows-insert', { resourceId: state.db, table: 'Entries', rows: [{ Memo: SECRET }] })
    await expect(card.getByTestId('proposal-sample')).toContainText(SECRET)
    const out = await pageFetch(page, '/api/auth/logout', { method: 'POST' })
    expect([200, 204]).toContain(out.status)
    await page.evaluate(() => window.dispatchEvent(new Event('focus')))
    await expect(card.getByTestId('proposal-withdrawn')).toBeVisible({ timeout: 20_000 })
    await expect(card.getByTestId('proposal-sample')).toHaveCount(0)
    await expect(card).not.toContainText(SECRET)
    const readStored = () => page.evaluate((key) => localStorage.getItem(key) || '', CHATS_KEY)
    await expect.poll(readStored, { timeout: 15_000 }).toContain('tool_result')
    const stored = await readStored()
    expect(stored).not.toContain('digest')
    expectReferenceOnly(stored)
    await shoot(card, 'proposal-logged-out', page)
    await context.close()
  })
})
