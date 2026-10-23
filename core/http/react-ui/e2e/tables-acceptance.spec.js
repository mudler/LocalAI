import { test, expect, request as playwrightRequest } from '@playwright/test'
import fs from 'node:fs'
import { randomUUID } from 'node:crypto'
import { chatOf, mockChat } from './chat-fixtures.js'
import { seedChats, CHATS_KEY } from './home-fixtures.js'

// Tables acceptance in the browser. The runner (scripts/addons/tables_acceptance.sh)
// starts the owner and two frontends as separate server processes, one shared
// account store, the real mTLS carrier and the packaged Tables installed from a
// catalog on loopback; ADDONS_TABLES_CLUSTER names its ready file. Every call of
// the pages goes through a real frontend, the real owner and a real SQLite
// database. Only the chat model list and the system probes of the chat page are
// stubbed. The specs stop and start the owner and run in order on one worker.

const ready = JSON.parse(fs.readFileSync(process.env.ADDONS_TABLES_CLUSTER, 'utf8'))
const URLS = ready.urls
const users = ready.users
const ROUTE = '/app/addons/tables/databases'
const SECRET = 'ACCEPTANCE-SECRET-7731'

async function browserAs(browser, node, who, extra = {}) {
  const base = URLS[node]
  const context = await browser.newContext({ baseURL: base, ...extra })
  await context.addCookies([{ name: 'session', value: users[who].session, url: base }])
  const page = await context.newPage()
  return { context, page, base }
}

async function apiAs(node, who) {
  return playwrightRequest.newContext({ baseURL: URLS[node], extraHTTPHeaders: { Authorization: `Bearer ${users[who].token}` } })
}

async function pageFetch(page, path, { method = 'GET', body, headers = {} } = {}) {
  return page.evaluate(async ({ path, method, body, headers }) => {
    const r = await fetch(path, {
      method, credentials: 'same-origin', cache: 'no-store',
      headers: { Accept: 'application/json', ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    let json = null
    try { json = await r.json() } catch { json = null }
    return { status: r.status, json }
  }, { path, method, body, headers })
}

const control = async (path) => {
  const r = await fetch(`${ready.control.url}${path}`, { method: 'POST', headers: { 'X-Control': ready.control.token } })
  if (!r.ok) throw new Error(`${path}: ${r.status} ${await r.text()}`)
}

const isAsset = (url) => /\/api\/addons\/tables\/assets\//.test(url)
const noOverflow = (page) => page.evaluate(() => document.scrollingElement.scrollWidth <= window.innerWidth)
const b64url = (s) => Buffer.from(s).toString('base64url')
const toolMessage = (tool, args) => `Please run this ADDON_TOOL_CALL:addon__tables__${tool}:${b64url(JSON.stringify(args))}`

async function generation(api) {
  const r = await api.get('/api/addons/apps')
  expect(r.status()).toBe(200)
  return (await r.json()).apps.find(a => a.id === 'tables').generation
}

// A database with a table Entries(Item text) and two rows, made through the
// person's own page on the node.
async function seed(page, api, name) {
  const gen = await generation(api)
  const op = `op-${randomUUID()}`
  const made = await pageFetch(page, '/api/addons/resources', { method: 'POST', body: { addonId: 'tables', operationId: op, generation: gen, arguments: { name } }, headers: { 'X-LocalAI-Confirm': op } })
  expect(made.status, JSON.stringify(made.json)).toBe(200)
  let rev = made.json.revision
  const step = async (tool, args) => {
    const r = await pageFetch(page, `/api/addons/resources/${made.json.id}/apply`, {
      method: 'POST', body: { operationId: `op-${randomUUID()}`, generation: gen, revision: rev, tool, arguments: args }, headers: { 'X-LocalAI-Confirm': made.json.id },
    })
    expect(r.status, `${tool} ${JSON.stringify(r.json)}`).toBe(200)
    rev = r.json.revision
  }
  await step('tables-create', { table: 'Entries' })
  await step('columns-add', { table: 'Entries', column: 'Item', type: 'text' })
  await step('rows-insert', { table: 'Entries', rows: [{ Item: SECRET }, { Item: 'second' }] })
  return { id: made.json.id, generation: gen }
}

async function openDb(page, id, prefix = '') {
  await page.goto(`${prefix}${ROUTE}/${id}?table=Entries`)
  await expect(page.getByTestId('tbl-database-page')).toBeVisible({ timeout: 30_000 })
  await expect(page.getByTestId('tbl-row').first()).toBeVisible({ timeout: 30_000 })
}

test.describe.configure({ mode: 'serial' })

let member
const state = {}
test.beforeAll(async () => {
  member = await apiAs('front-a', 'member')
})
test.afterAll(async () => {
  await member.dispose()
})

test.describe('Tables acceptance through real servers', () => {
  test('tables-acceptance: a member opens the databases and a grid through each non-owner frontend and the module comes from the owner by digest', async ({ browser }) => {
    const first = await browserAs(browser, 'front-a', 'member')
    await first.page.goto(ROUTE)
    await expect(first.page.getByTestId('tbl-list-page')).toBeVisible({ timeout: 30_000 })
    state.db = await seed(first.page, member, 'Acceptance')
    await first.context.close()
    for (const node of ['front-a', 'front-b']) {
      const { context, page, base } = await browserAs(browser, node, 'member')
      const assets = []
      page.on('response', r => { if (isAsset(r.url())) assets.push({ url: r.url(), status: r.status() }) })
      await openDb(page, state.db.id)
      await expect(page.getByTestId('tbl-grid')).toContainText(SECRET)
      expect(assets.length).toBeGreaterThanOrEqual(1)
      for (const a of assets) {
        expect(a.status).toBe(200)
        expect(new URL(a.url).origin).toBe(new URL(base).origin)
        expect(new URL(a.url).pathname).toMatch(/^\/api\/addons\/tables\/assets\/[0-9a-f]{64}\//)
      }
      await context.close()
    }
  })

  test('tables-acceptance: an approval card from chat on one frontend applies once and the row shows on the other frontend', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'front-a', 'member')
    await mockChat(page, { models: [{ id: 'mock-model', capabilities: ['FLAG_CHAT'] }], loaded: ['mock-model'] })
    await seedChats(page, [chatOf('acc', 'Tables', 'mock-model', [], { addonTools: true })], 'acc')
    await page.goto('/app/chat')
    await page.getByTestId('chat-composer').waitFor()
    await page.getByTestId('chat-input').fill(toolMessage('rows-insert', { resourceId: state.db.id, table: 'Entries', rows: [{ Item: 'from-chat' }] }))
    await page.getByTestId('chat-send').click()
    const card = page.getByTestId('proposal-card')
    await expect(card).toHaveCount(1, { timeout: 40_000 })
    await expect(card.getByTestId('proposal-operation')).toHaveText('rows-insert')
    await expect(card.getByTestId('proposal-count')).toHaveText('1')
    await expect(card.getByTestId('proposal-state')).toHaveText('Waiting for your decision')
    // Nothing is applied before the person decides: the other frontend still
    // shows the two rows.
    const other = await browserAs(browser, 'front-b', 'member')
    await openDb(other.page, state.db.id)
    await expect(other.page.getByTestId('tbl-row')).toHaveCount(2)
    await card.getByTestId('proposal-confirm').click()
    await expect(card.getByTestId('proposal-state')).toHaveText('Applied', { timeout: 30_000 })
    await expect(card.getByTestId('proposal-confirm')).toBeDisabled()
    await other.page.reload()
    await expect(other.page.getByTestId('tbl-row')).toHaveCount(3, { timeout: 30_000 })
    await expect(other.page.getByTestId('tbl-grid')).toContainText('from-chat')
    // A reload of the chat draws the same applied card from the host.
    await page.reload()
    await expect(page.getByTestId('proposal-card').last().getByTestId('proposal-state')).toHaveText('Applied', { timeout: 30_000 })
    await other.context.close()
    await context.close()
  })

  test('tables-acceptance: deep links keep the proxy path prefix on a non-owner frontend and survive a reload', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'front-b', 'member', { extraHTTPHeaders: { 'X-Forwarded-Prefix': '/llm' } })
    const assets = []
    page.on('request', r => { if (isAsset(r.url())) assets.push(new URL(r.url()).pathname) })
    await openDb(page, state.db.id, '/llm')
    expect(assets.length).toBeGreaterThanOrEqual(1)
    for (const p of assets) expect(p).toMatch(/^\/llm\/api\/addons\/tables\/assets\/[0-9a-f]{64}\//)
    await page.reload()
    await expect(page.getByTestId('tbl-database-page')).toBeVisible({ timeout: 30_000 })
    await expect(page).toHaveURL(new RegExp(`/llm/app/addons/tables/databases/${state.db.id}`))
    await context.close()
  })

  test('tables-acceptance: the grid works by keyboard, at 400 pixels and in both themes on a non-owner frontend', async ({ browser }) => {
    const small = await browserAs(browser, 'front-a', 'member', { viewport: { width: 400, height: 800 } })
    await openDb(small.page, state.db.id)
    expect(await noOverflow(small.page)).toBe(true)
    await small.page.getByRole('button', { name: 'Sort by Item' }).focus()
    await small.page.keyboard.press('Enter')
    await expect(small.page.locator('th[aria-sort="ascending"]')).toContainText('Item')
    await small.page.getByRole('button', { name: /^Edit row/ }).first().focus()
    await small.page.keyboard.press('Enter')
    await expect(small.page.getByTestId('tbl-edit-dialog')).toBeVisible()
    await small.page.keyboard.press('Escape')
    await expect(small.page.getByTestId('tbl-edit-dialog')).toHaveCount(0)
    expect(await noOverflow(small.page)).toBe(true)
    await small.context.close()
    for (const mode of ['light', 'dark']) {
      const ctx = await browser.newContext({ baseURL: URLS['front-b'], viewport: { width: 1280, height: 800 } })
      await ctx.addCookies([{ name: 'session', value: users.member.session, url: URLS['front-b'] }])
      await ctx.addInitScript((m) => localStorage.setItem('localai-theme', m), mode)
      const page = await ctx.newPage()
      await openDb(page, state.db.id)
      await expect(page.locator('html')).toHaveAttribute('data-theme', mode)
      const colors = await page.getByTestId('tbl-grid').evaluate((el) => ({
        text: getComputedStyle(el.querySelector('tbody td[data-column="Item"]')).color,
        head: getComputedStyle(el.querySelector('th')).backgroundColor,
      }))
      expect(colors.text).not.toBe('')
      expect(colors.head).not.toBe('rgba(0, 0, 0, 0)')
      await ctx.close()
    }
  })

  test('tables-acceptance: the page says it is unavailable while the owner is gone and recovers on reload when it is back', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'front-b', 'member')
    await openDb(page, state.db.id)
    await control('/owner/stop')
    try {
      await page.reload()
      const unavailable = page.getByTestId('tbl-unavailable').or(page.getByTestId('addon-unavailable')).or(page.getByTestId('addon-stale'))
      await expect(unavailable.first()).toBeVisible({ timeout: 40_000 })
      await expect(page.getByTestId('tbl-grid')).toHaveCount(0)
      await expect(page.locator('body')).not.toContainText(SECRET)
    } finally {
      await control('/owner/start')
    }
    await expect.poll(async () => (await member.get('/api/addons/apps')).status(), { timeout: 60_000 }).toBe(200)
    await expect.poll(async () => {
      const r = await member.get('/api/addons/apps')
      const a = (await r.json()).apps.find(x => x.id === 'tables')
      return a && a.state
    }, { timeout: 60_000 }).toBe('running')
    await page.reload()
    await expect(page.getByTestId('tbl-row').first()).toBeVisible({ timeout: 40_000 })
    await expect(page.getByTestId('tbl-grid')).toContainText(SECRET)
    await context.close()
  })

  test('tables-acceptance: after logout nothing of the database is left in the page or the stored chat', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'front-a', 'member')
    await mockChat(page, { models: [{ id: 'mock-model', capabilities: ['FLAG_CHAT'] }], loaded: ['mock-model'] })
    await seedChats(page, [chatOf('acc2', 'Tables', 'mock-model', [], { addonTools: true })], 'acc2')
    await openDb(page, state.db.id)
    await expect(page.getByTestId('tbl-grid')).toContainText(SECRET)
    const out = await pageFetch(page, '/api/auth/logout', { method: 'POST' })
    expect([200, 204]).toContain(out.status)
    // The same session is refused by the other frontend too.
    const other = await browserAs(browser, 'front-b', 'member')
    await other.page.goto('/app')
    const apps = await pageFetch(other.page, '/api/addons/apps')
    expect(apps.status).toBe(401)
    await other.context.close()
    await page.reload()
    await expect(page.getByTestId('tbl-grid')).toHaveCount(0)
    await expect(page.locator('body')).not.toContainText(SECRET)
    const stored = await page.evaluate((key) => localStorage.getItem(key) || '', CHATS_KEY)
    expect(stored).not.toContain(SECRET)
    expect(stored).not.toContain('digest')
    await context.close()
  })
})
