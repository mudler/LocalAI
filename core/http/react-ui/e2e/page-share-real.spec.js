// SPDX-License-Identifier: MIT
import { test, expect, request as pwRequest } from '@playwright/test'
import { haveBinary, startRealServer } from './real-server.js'
import { b64, offerMockModel, sendFromHome } from './journey-fixtures.js'

// Sharing one page of a home, against a real local-ai binary with
// authentication on and several named accounts. Nothing on the share path is
// mocked: the owner shares in the real dialog, the others see the page under
// "Shared with you", allow it on the trust sheet, and the page saves its data
// through the real bridge into the owner's page folder. The spec follows the
// roles (view, use, edit), a change of the code, a revoke, everyone, and the
// administrator's switch.
//
//   LOCALAI_BIN=<local-ai built with -tags auth> MOCK_BACKEND_BIN=<mock-backend> \
//     PLAYWRIGHT_EXTERNAL_SERVER=1 npx playwright test -c playwright.page-share.config.js
//
// PAGE_SHARE_SHOTS_DIR names the folder of the screenshots. Skipped when the
// two binaries are not given, so the default run is unchanged.
test.skip(!haveBinary, 'needs LOCALAI_BIN and MOCK_BACKEND_BIN')
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const SHOTS = process.env.PAGE_SHARE_SHOTS_DIR || 'test-results/page-share'
const PASSWORD = 'Correct-horse-9-battery'
const ACCOUNTS = {
  admin: { email: 'admin@example.test', name: 'Admin' },
  olivia: { email: 'olivia@example.test', name: 'Olivia Owner' },
  gus: { email: 'gus@example.test', name: 'Gus Grantee' },
  vera: { email: 'vera@example.test', name: 'Vera Viewer' },
  theo: { email: 'theo@example.test', name: 'Theo Third' },
  edna: { email: 'edna@example.test', name: 'Edna Editor' },
}

// A todo list that keeps its items in todos.json and, on a click, tries to
// write one file. The buttons report what the bridge answered.
const TODO_HTML = `<!doctype html><html><body style="font-family:sans-serif;padding:16px">
<h1 id="t">Todo list</h1><ul id="list"></ul><p id="status"></p>
<button id="add">Add item</button> <button id="wi">Write index</button> <button id="wp">Write page.json</button>
<button id="wj">Write script</button> <button id="rd">Read again</button>
<script>
const out = (m) => { document.getElementById('status').textContent = m }
const draw = (items) => { document.getElementById('list').innerHTML = items.map((i) => '<li>' + String(i).replace(/</g, '&lt;') + '</li>').join('') }
const load = () => localai.files.read('todos.json').then((f) => { const v = JSON.parse(f.content); draw(v); return v }, (e) => { out('ERR ' + e.message); return null })
load()
document.getElementById('add').onclick = async () => {
  const cur = (await load()) || []
  cur.push('item ' + (cur.length + 1))
  localai.files.write('todos.json', JSON.stringify(cur)).then(() => { out('saved'); draw(cur) }, (e) => out('ERR ' + e.message))
}
const tryWrite = (id, file) => { document.getElementById(id).onclick = () => localai.files.write(file, 'x').then(() => out('wrote ' + file), (e) => out('ERR ' + e.message)) }
tryWrite('wi', 'index.html')
tryWrite('wp', 'page.json')
tryWrite('wj', 'app.js')
document.getElementById('rd').onclick = () => load().then(() => out('read'))
</script></body></html>`

let server
let ids = {}
let ctx = {} // an API context per account, with its session cookie
let states = {} // storage state per account
let adminKey

// The sign-in and register routes allow 5 calls a minute from one address, so
// the setup waits when it is told to.
async function limited(call) {
  for (let i = 0; i < 12; i++) {
    const r = await call()
    if (r.status() !== 429) return r
    await new Promise(res => setTimeout(res, 10_000))
  }
  throw new Error('still rate limited')
}

async function apiLogin(who) {
  const c = await pwRequest.newContext({ baseURL: server.url })
  const r = await limited(() => c.post('/api/auth/login', { data: { email: ACCOUNTS[who].email, password: PASSWORD } }))
  expect(r.ok(), await r.text()).toBe(true)
  return c
}

async function as(browser, who, viewport) {
  const context = await browser.newContext({ storageState: states[who], baseURL: server.url, viewport })
  const page = await context.newPage()
  if (process.env.PAGE_SHARE_DEBUG) {
    page.on('response', async (r) => { if (r.status() >= 400 && r.url().includes('/api/')) console.log(`${who} ${r.status()} ${r.request().method()} ${new URL(r.url()).pathname} ${(await r.text().catch(() => '')).slice(0, 200)}`) })
  }
  return { context, page }
}

const settle = (page) => page.evaluate(() => Promise.all(document.getAnimations().filter(a => a.effect?.getTiming().iterations !== Infinity).map(a => a.finished.catch(() => {}))))
const shot = async (page, name) => { await settle(page); await page.screenshot({ path: `${SHOTS}/${name}.png` }) }
const theme = (page, t) => page.addInitScript((m) => localStorage.setItem('localai-theme', m), t)

// A call from the owner's browser: a same-origin fetch with the confirmation
// header, which is all the share routes take.
async function fetchAs(page, method, path, body, confirm) {
  return page.evaluate(async ([m, p, b, c]) => {
    const headers = { 'Content-Type': 'application/json' }
    if (c) headers['X-LocalAI-Confirm'] = c
    const r = await fetch(p, { method: m, headers, body: b ? JSON.stringify(b) : undefined })
    let json = null
    try { json = await r.json() } catch { /* no body */ }
    return { status: r.status, json }
  }, [method, path, body ?? null, confirm ?? ''])
}

const put = (c, p, body) => c.put(`/api/chats/files/content?path=${encodeURIComponent(p)}&overwrite=1`, { data: body, headers: { 'Content-Type': 'application/octet-stream' } })
const frame = (page) => page.frameLocator('[data-testid="page-frame"]')
const card = (page, name) => page.locator(`[data-testid="page-card"][data-page="${name}"]`)
const sharedCard = (page, name) => page.locator(`[data-testid="shared-card"][data-page="${name}"]`)
const sharedList = async (c) => (await (await c.get('/api/chats/shared-pages')).json())
const sharedRef = async (who) => (await sharedList(ctx[who])).pages.find(p => p.name === 'todo')
const sharedPath = (p) => `/app/pages/shared/${p.folder_id}/todo`
const ownerRead = async (path = 'todos.json') => (await (await ctx.olivia.get(`/api/chats/pages/todo/file?path=${path}`)).json()).content

async function openShare(page) {
  await card(page, 'todo').getByRole('button', { name: /^More for / }).click()
  await page.getByRole('menuitem', { name: 'Share' }).click()
  const dialog = page.getByTestId('page-share-dialog')
  await expect(dialog).toBeVisible()
  return dialog
}
async function shareWith(dialog, who, role) {
  const first = ACCOUNTS[who].name.split(' ')[0].slice(0, 4)
  await dialog.getByTestId('share-search').fill(first)
  await expect(dialog.getByTestId('share-user')).toHaveCount(1, { timeout: 10_000 })
  await dialog.getByTestId('share-new-role').selectOption(role)
  await dialog.getByTestId('share-user').getByRole('button', { name: new RegExp(ACCOUNTS[who].name) }).click()
  await expect(dialog.locator(`[data-testid="share-grant"][data-user="${ids[who]}"]`)).toHaveAttribute('data-role', role)
}
const grantsOf = async () => (await fetchAsOwner('GET', '/api/chats/pages/todo/shares')).json
let ownerPage
const fetchAsOwner = (method, path, body) => fetchAs(ownerPage, method, path, body, path.includes('/pages/shares/search') ? 'pages' : 'todo')

// The bridge call that a click makes, with its status.
async function clickAndStatus(page, button, urlPart = '/file') {
  const [resp] = await Promise.all([
    page.waitForResponse((r) => r.url().includes(urlPart) && r.request().method() === 'PUT', { timeout: 15_000 }),
    frame(page).locator(button).click(),
  ])
  return resp.status()
}

test.beforeAll(async () => {
  test.setTimeout(300_000)
  // A chat needs a model with room for its tools, so the mock model gets a context.
  server = await startRealServer({ auth: true, contextSize: 32768 })
  const anon = await pwRequest.newContext({ baseURL: server.url })
  for (const who of Object.keys(ACCOUNTS)) {
    const r = await limited(() => anon.post('/api/auth/register', { data: { ...ACCOUNTS[who], password: PASSWORD } }))
    expect(r.ok(), await r.text()).toBe(true)
  }
  for (const who of Object.keys(ACCOUNTS)) {
    ctx[who] = await apiLogin(who)
    states[who] = await ctx[who].storageState()
  }
  const users = await (await ctx.admin.get('/api/auth/admin/users')).json()
  for (const who of Object.keys(ACCOUNTS)) ids[who] = (users.users || users).find(u => u.email === ACCOUNTS[who].email).id
  adminKey = (await (await ctx.admin.post('/api/auth/api-keys', { data: { name: 'setup' } })).json()).key
  // The pages feature is off for a new account: the administrator turns it on.
  for (const who of ['olivia', 'gus', 'vera', 'theo', 'edna']) {
    const r = await ctx.admin.put(`/api/auth/admin/users/${ids[who]}/permissions`, { data: { chats: true, pages: true } })
    expect(r.ok(), await r.text()).toBe(true)
  }
  // The owner's page, with its data file.
  expect((await put(ctx.olivia, '/pages/todo/index.html', TODO_HTML)).ok()).toBe(true)
  expect((await put(ctx.olivia, '/pages/todo/todos.json', '[]')).ok()).toBe(true)
})
test.afterAll(async () => { await server?.close() })

test('(a) the owner shares with Use, the grantee allows once and adds an item, the owner sees it', async ({ browser }) => {
  const { context: oc, page: owner } = await as(browser, 'olivia')
  ownerPage = owner
  await owner.goto('/app/pages')
  await expect(card(owner, 'todo')).toBeVisible()
  const dialog = await openShare(owner)
  await expect(dialog.getByTestId('share-warning')).toBeVisible()
  await shareWith(dialog, 'gus', 'use')
  await shot(owner, 'a-share-dialog-desktop-light')
  const g = await grantsOf()
  expect(g.grants).toEqual([{ userId: ids.gus, displayName: 'Gus Grantee', role: 'use', consent: 'none' }])
  expect(g.enabled).toBe(true)
  expect(g.hash).toMatch(/\S/)
  expect(g.allowedCount).toBe(0)
  await dialog.getByRole('button', { name: 'Close' }).last().click()
  await expect(card(owner, 'todo').getByTestId('page-shared-badge')).toBeVisible()

  const { context: gc, page: gus } = await as(browser, 'gus')
  await gus.goto('/app/pages')
  const row = sharedCard(gus, 'todo')
  await expect(row).toBeVisible()
  await expect(row.getByTestId('shared-from')).toHaveText('From Olivia Owner')
  await expect(row.getByTestId('shared-role')).toHaveText('Use')
  await expect(row.getByTestId('shared-badge')).toContainText('Not allowed yet')
  const words = await gus.evaluate(() => document.body.innerText)
  expect(words).not.toContain('users/')
  expect(words).not.toContain(ids.olivia)
  expect(words).not.toMatch(/pages\/todo/)
  await shot(gus, 'a-shared-list-desktop-light')

  const ref = await sharedRef('gus')
  await gus.goto(sharedPath(ref))
  const sheet = gus.getByTestId('trust-sheet')
  await expect(sheet).toBeVisible()
  await expect(sheet.getByTestId('trust-who')).toHaveText('Written by Olivia Owner. Not checked by an administrator.')
  await expect(sheet.getByTestId('trust-allow-embed')).toHaveCount(0)
  await expect(sheet).not.toContainText('You can only look at this page', { timeout: 1000 }) // Use may save data
  await expect(gus.getByTestId('page-frame')).toHaveCount(0)
  await shot(gus, 'a-trust-first-desktop-light')
  await sheet.getByTestId('trust-allow-once').click()
  await expect(gus.getByTestId('shared-pill')).toHaveText(/Allowed for this visit/)
  await expect(frame(gus).locator('#t')).toHaveText('Todo list', { timeout: 15_000 })
  expect(await clickAndStatus(gus, '#add')).toBe(200)
  await expect(frame(gus).locator('#status')).toHaveText('saved')
  await shot(gus, 'a-page-running-desktop-light')

  expect(JSON.parse(await ownerRead())).toEqual(['item 1'])
  await owner.goto('/app/pages/todo')
  await expect(frame(owner).locator('#list')).toContainText('item 1', { timeout: 15_000 })
  await shot(owner, 'a-owner-sees-item-desktop-light')
  await gc.close()
  await oc.close()
})

test('the shared JSON of a grantee has no path and no account ID of the owner', async () => {
  const raw = JSON.stringify(await sharedList(ctx.gus))
  expect(raw).not.toContain(ids.olivia)
  expect(raw).not.toContain('/users/')
  expect(raw).not.toContain('"path":"/')
  const ref = await sharedRef('gus')
  expect(ref).toMatchObject({ user_shared: true, role: 'use', owner_name: 'Olivia Owner' })
  expect(ref.folder ?? '').toBe('')
  expect(ref.path ?? '').toBe('')
  const one = JSON.stringify(await (await ctx.gus.get(`/api/chats/shared-pages/${ref.folder_id}/todo`)).json())
  expect(one).not.toContain(ids.olivia)
  expect(one).not.toContain('/users/')
  const folders = JSON.stringify(await (await ctx.gus.get('/api/chats/shared-folders')).json())
  expect(folders).not.toContain(ids.olivia)
  expect(folders).not.toContain('/users/')
})

test('(b) a View grantee can read the page and gets 403 when it saves', async ({ browser }) => {
  const { context: oc, page: owner } = await as(browser, 'olivia')
  ownerPage = owner
  await owner.goto('/app/pages')
  expect((await fetchAsOwner('PUT', '/api/chats/pages/todo/shares', { principal: ids.vera, role: 'view' })).status).toBe(200)
  const { context: vc, page: vera } = await as(browser, 'vera')
  const ref = await sharedRef('vera')
  expect(ref.role).toBe('view')
  await vera.goto(sharedPath(ref))
  await expect(vera.getByTestId('trust-sheet')).toContainText('You can only look at this page, so it cannot save its changes.')
  await vera.getByTestId('trust-allow-once').click()
  await expect(frame(vera).locator('#list')).toContainText('item 1', { timeout: 15_000 })
  await expect(vera.getByTestId('shared-view-only')).toBeVisible()
  await shot(vera, 'b-view-only-desktop-light')
  expect(await clickAndStatus(vera, '#add')).toBe(403)
  await expect(frame(vera).locator('#status')).toContainText('ERR')
  expect(JSON.parse(await ownerRead())).toEqual(['item 1'])
  await vc.close()
  await oc.close()
})

test('(c) a Use grantee cannot write code through the bridge, Files, the folder routes or a chat', async ({ browser }) => {
  const { context: gc, page: gus } = await as(browser, 'gus')
  const ref = await sharedRef('gus')
  const { context: oc0, page: owner0 } = await as(browser, 'olivia')
  ownerPage = owner0
  await owner0.goto('/app/pages')
  await gus.goto(sharedPath(ref))
  await expect(gus.getByTestId('trust-sheet')).toHaveCount(0) // the consent of (a) is "once"
  await gus.getByTestId('trust-allow').click()
  await expect(frame(gus).locator('#list')).toContainText('item 1', { timeout: 15_000 })
  // The host refuses a code file before it sends anything; the server refuses
  // it too, which a direct call with the allowed version shows.
  for (const [b, file] of [['#wi', 'index.html'], ['#wp', 'page.json'], ['#wj', 'app.js']]) {
    await frame(gus).locator('#status').evaluate((el) => { el.textContent = '' })
    await frame(gus).locator(b).click()
    await expect(frame(gus).locator('#status')).toContainText('ERR', { timeout: 10_000 })
    const direct = await gus.evaluate(async ([folder, hash, rel]) => {
      const r = await fetch(`/api/chats/shared-pages/${folder}/todo/file`, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-LocalAI-Page-Hash': hash }, body: JSON.stringify({ path: rel, content: 'x' }) })
      return r.status
    }, [ref.folder_id, (await sharedRef('gus')).hash, file])
    expect(direct, file).toBe(403)
  }
  expect(await clickAndStatus(gus, '#add')).toBe(200)
  const html = await (await ctx.olivia.get('/api/chats/pages/todo')).json()
  expect(html.html).toBe(TODO_HTML)

  // A grantee cannot list, change or take back the shares of the owner's page
  // (the routes take the caller's own pages), and cannot share it onward. The
  // answer is the one for a page that does not exist.
  const none = await gus.evaluate(async () => (await fetch('/api/chats/pages/nosuchpage/shares', { headers: { 'X-LocalAI-Confirm': 'nosuchpage' } })).status)
  expect(none).toBe(403)
  for (const [method, path, body] of [
    ['GET', '/api/chats/pages/todo/shares'],
    ['PUT', '/api/chats/pages/todo/shares', { principal: ids.theo, role: 'view' }],
    ['DELETE', `/api/chats/pages/todo/shares/${ids.vera}`],
  ]) {
    const r = await fetchAs(gus, method, path, body, 'todo')
    expect(r.status, `${method} ${path}`).toBe(none)
  }
  expect((await ctx.vera.get('/api/chats/pages/todo/shares')).status()).toBeGreaterThanOrEqual(400)
  expect((await grantsOf()).grants.some(g => g.userId === ids.theo)).toBe(false)

  // Files reaches the caller's own home only: the owner's page is not there.
  const tree = JSON.stringify(await (await ctx.gus.get('/api/chats/files?path=/')).json())
  expect(tree).not.toContain('todo')
  const evil = await ctx.gus.put(`/api/chats/files/content?path=${encodeURIComponent(`/users/${ids.olivia}/pages/todo/index.html`)}&overwrite=1`, { data: 'x', headers: { 'Content-Type': 'application/octet-stream' } })
  expect(await ownerRead('index.html')).toContain('Todo list')
  expect([evil.status() < 300 ? 'created in his own home' : 'refused']).toBeTruthy()
  expect((await ctx.gus.get('/api/chats/pages')).status()).toBe(200)
  expect(JSON.stringify(await (await ctx.gus.get('/api/chats/pages')).json())).not.toContain('"todo"')

  // The owner cannot share pages/ or a folder of a page through the generic
  // folder routes either.
  const { context: oc, page: owner } = await as(browser, 'olivia')
  ownerPage = owner
  await owner.goto('/app/pages')
  const list = await fetchAs(owner, 'GET', '/api/chats/files/shares', null, 'files')
  expect(list.status).toBe(200)
  for (const p of ['/pages', '/pages/todo/sub']) {
    await put(ctx.olivia, `${p}/x.json`, '{}')
    const found = await fetchAs(owner, 'POST', '/api/chats/files/shares', { path: p }, p)
    expect(found.status, `${p}: ${JSON.stringify(found.json)}`).toBeGreaterThanOrEqual(400)
  }
  // A folder that already has grants is found, so the owner can see and take
  // them back (a page included); no grant can be made or changed through it.
  expect((await fetchAsOwner('PUT', '/api/chats/pages/todo/shares', { principal: ids.gus, role: 'use' })).status).toBe(200)
  const found = await fetchAs(owner, 'POST', '/api/chats/files/shares', { path: '/pages/todo' }, '/pages/todo')
  if (found.status === 200) {
    for (const role of ['read', 'write', 'data']) {
      const set = await fetchAs(owner, 'PUT', `/api/chats/files/shares/${found.json.id}/grants/${ids.theo}`, { role }, String(found.json.id))
      expect(set.status, role).toBeGreaterThanOrEqual(400)
    }
    const grants = await grantsOf()
    expect(grants.grants.some(g => g.userId === ids.theo)).toBe(false)
    expect(grants.grants.find(g => g.userId === ids.gus).role).toBe('use')
  }
  await oc.close()

  // A chat of the grantee mounts the page folder read-only: the model cannot
  // write code there, and not data either.
  await offerMockModel(gus)
  await sendFromHome(gus, `change the page ADDON_TOOL_CALL:write_file:${b64({ path: '/workspace/shared-todo/index.html', content: '<p>owned</p>' })}`)
  await gus.waitForURL(/\/app\/chats\/[0-9a-f]+$/)
  const chatId = gus.url().split('/').pop()
  await expect.poll(async () => (await (await ctx.gus.get(`/api/chats/${chatId}`)).json()).running, { timeout: 60_000 }).toBe(false)
  expect(await ownerRead('index.html')).toBe(TODO_HTML)
  await oc0.close()
  await gc.close()
})

test('(d) the owner changes the code: the grantee is asked again and the open frame stops', async ({ browser }) => {
  const { context: oc, page: owner } = await as(browser, 'olivia')
  ownerPage = owner
  const { context: gc, page: gus } = await as(browser, 'gus')
  const ref = await sharedRef('gus')
  await gus.goto(sharedPath(ref))
  await expect(frame(gus).locator('#list')).toContainText('item', { timeout: 15_000 })
  await expect(gus.getByTestId('shared-pill')).toHaveText(/Allowed/)

  await owner.goto('/app/pages/todo')
  await expect(frame(owner).locator('#t')).toBeVisible({ timeout: 15_000 })
  expect((await put(ctx.olivia, '/pages/todo/index.html', TODO_HTML.replace('Todo list', 'Todo list v2'))).ok()).toBe(true)
  const shares = (await grantsOf())
  expect(shares.grants.find(x => x.userId === ids.gus).consent).toBe('changed')
  expect(shares.allowedCount).toBe(0)
  await owner.getByTestId('page-reload').click()
  await expect(owner.getByTestId('page-ask-again')).toContainText('will be asked to allow it again')
  await shot(owner, 'd-owner-asks-again-desktop-light')

  // The open frame still holds the version that Gus allowed. It reads no new
  // code: a read of index.html with the old version is refused with 409. Its
  // calls for data files go on, under the consent to the old version.
  const call = (path) => gus.evaluate(async ([folder, hash, rel]) => {
    const r = await fetch(`/api/chats/shared-pages/${folder}/todo/file?path=${rel}`, { headers: { 'X-LocalAI-Page-Hash': hash } })
    return r.status
  }, [ref.folder_id, ref.hash, path])
  const path = 'index.html'
  expect(await call(path)).toBe(409)
  expect(await call('todos.json')).toBe(200)
  await frame(gus).locator('#rd').click()
  await expect(frame(gus).locator('#status')).toHaveText('read')
  await shot(gus, 'd-frame-after-change-desktop-light')
  await gus.reload()
  const sheet = gus.getByTestId('trust-sheet')
  await expect(sheet).toHaveAttribute('data-variant', 'changed')
  await shot(gus, 'd-trust-changed-desktop-light')
  await sheet.getByTestId('trust-allow').click()
  await expect(frame(gus).locator('#t')).toHaveText('Todo list v2', { timeout: 15_000 })
  await gc.close()
  await oc.close()
})

test('(e) the owner revokes: the open frame stops at its next call', async ({ browser }) => {
  const { context: oc, page: owner } = await as(browser, 'olivia')
  ownerPage = owner
  const { context: gc, page: gus } = await as(browser, 'gus')
  const ref = await sharedRef('gus')
  await gus.goto(sharedPath(ref))
  await expect(frame(gus).locator('#t')).toHaveText('Todo list v2', { timeout: 15_000 })

  await owner.goto('/app/pages')
  const dialog = await openShare(owner)
  await dialog.getByRole('button', { name: 'Stop sharing with Gus Grantee' }).click()
  await dialog.getByRole('button', { name: 'Stop sharing', exact: true }).click()
  await expect(dialog.locator(`[data-testid="share-grant"][data-user="${ids.gus}"]`)).toHaveCount(0)
  await frame(gus).locator('#rd').click()
  await expect(gus.getByTestId('page-frame')).toHaveCount(0, { timeout: 15_000 })
  await expect(gus.getByTestId('shared-blocked')).toBeVisible()
  await shot(gus, 'e-revoked-desktop-light')
  expect((await ctx.gus.get(`/api/chats/shared-pages/${ref.folder_id}/todo`)).status()).toBe(404)
  expect((await sharedList(ctx.gus)).pages.find(p => p.name === 'todo')).toBeUndefined()
  // A later grant asks again: the consent went with the grant.
  expect((await fetchAsOwner('PUT', '/api/chats/pages/todo/shares', { principal: ids.gus, role: 'use' })).status).toBe(200)
  expect((await (await ctx.gus.get(`/api/chats/shared-pages/${(await sharedRef('gus')).folder_id}/todo`)).json()).consent.state).toBe('none')
  expect((await fetchAsOwner('DELETE', `/api/chats/pages/todo/shares/${ids.gus}`)).status).toBe(200)
  await gc.close()
  await oc.close()
})

test('(f) Everyone with View works for a third account, Everyone with Edit is refused', async ({ browser }) => {
  const { context: oc, page: owner } = await as(browser, 'olivia')
  ownerPage = owner
  await owner.goto('/app/pages')
  const dialog = await openShare(owner)
  const everyoneRole = dialog.getByTestId('share-everyone-add').getByTestId('share-everyone-role')
  await expect(everyoneRole.locator('option')).toHaveText(['View', 'Use'])
  await everyoneRole.selectOption('view')
  await dialog.getByTestId('share-everyone-add').getByRole('button', { name: 'Share with everyone' }).click()
  await expect(dialog.locator('[data-testid="share-grant"][data-user="@everyone"]')).toHaveAttribute('data-role', 'view')
  await shot(owner, 'f-everyone-dialog-desktop-light')
  const refused = await fetchAsOwner('PUT', '/api/chats/pages/todo/shares', { principal: '@everyone', role: 'edit' })
  expect(refused.status).toBe(400)
  expect((await grantsOf()).grants.find(x => x.userId === '@everyone').role).toBe('view')

  const { context: tc, page: theo } = await as(browser, 'theo')
  const ref = await sharedRef('theo')
  expect(ref).toMatchObject({ user_shared: true, role: 'view', owner_name: 'Olivia Owner' })
  await theo.goto('/app/pages')
  await expect(sharedCard(theo, 'todo').getByTestId('shared-from')).toHaveText('From Olivia Owner')
  await theo.goto(sharedPath(ref))
  await theo.getByTestId('trust-allow-once').click()
  await expect(frame(theo).locator('#list')).toContainText('item', { timeout: 15_000 })
  expect(await clickAndStatus(theo, '#add')).toBe(403)
  await tc.close()
  await oc.close()
})

test('(g) the administrator switch: a new share is refused, an existing one does not open, the owner can still revoke', async ({ browser }) => {
  const { context: oc, page: owner } = await as(browser, 'olivia')
  ownerPage = owner
  await owner.goto('/app/pages')
  const set = (on) => ctx.admin.post('/api/settings', { data: { user_page_shares_enabled: on }, headers: { Authorization: `Bearer ${adminKey}` } })
  expect((await set(false)).ok()).toBe(true)
  try {
    const refused = await fetchAsOwner('PUT', '/api/chats/pages/todo/shares', { principal: ids.gus, role: 'use' })
    expect(refused.status).toBe(409)
    expect(refused.json.error.type).toBe('sharing_disabled')
    expect((await fetchAsOwner('GET', '/api/chats/pages/shares/search?q=Gus')).status).toBe(409)
    const listing = await grantsOf()
    expect(listing.enabled).toBe(false)

    const { context: tc, page: theo } = await as(browser, 'theo')
    const body = await sharedList(ctx.theo)
    expect(body.user_shares_enabled).toBe(false)
    const ref = body.pages.find(p => p.name === 'todo')
    expect(ref.turned_off).toBe(true)
    expect((await ctx.theo.get(`/api/chats/shared-pages/${ref.folder_id}/todo`)).status()).toBe(403)
    await theo.goto('/app/pages')
    await expect(sharedCard(theo, 'todo').getByTestId('shared-user-off')).toBeVisible()
    await expect(sharedCard(theo, 'todo').getByTestId('shared-open')).toHaveCount(0)
    await expect(sharedCard(theo, 'todo')).not.toContainText('years ago') // it has no time, since its code is not read
    await shot(theo, 'g-turned-off-desktop-light')
    await theo.goto(sharedPath(ref))
    await expect(theo.getByTestId('page-frame')).toHaveCount(0)
    await shot(theo, 'g-turned-off-page-desktop-light')

    await owner.goto('/app/pages')
    const dialog = await openShare(owner)
    await expect(dialog.getByTestId('share-add-off')).toBeVisible()
    await shot(owner, 'g-dialog-off-desktop-light')
    await dialog.getByRole('button', { name: 'Stop sharing with everyone' }).click().catch(async () => {
      await dialog.locator('[data-testid="share-grant"][data-user="@everyone"]').getByRole('button').last().click()
    })
    await dialog.getByRole('button', { name: 'Stop sharing', exact: true }).click()
    await expect(dialog.locator('[data-testid="share-grant"][data-user="@everyone"]')).toHaveCount(0)
    expect((await sharedList(ctx.theo)).pages.find(p => p.name === 'todo')).toBeUndefined()
    await tc.close()
  } finally {
    await set(true)
  }
  await oc.close()
})

test('(h) an Edit grantee changes the code: the owner is asked through the trust sheet and the page does not run on Home before', async ({ browser }) => {
  const { context: oc, page: owner } = await as(browser, 'olivia')
  ownerPage = owner
  await owner.goto('/app/pages')
  expect((await fetchAsOwner('PUT', '/api/chats/pages/todo/shares', { principal: ids.edna, role: 'edit' })).status).toBe(200)
  const list = (await (await ctx.olivia.get('/api/chats/pages')).json()).pages.find(p => p.name === 'todo')
  expect(list).toMatchObject({ others_can_edit: true, consent: 'none', shared_with: 2 })
  expect(list.hash).toMatch(/\S/)

  // The page stays in the owner's list and opens through the allow step.
  await owner.reload()
  await expect(card(owner, 'todo')).toBeVisible()
  await owner.goto('/app/pages/todo')
  const sheet = owner.getByTestId('trust-sheet')
  await expect(sheet).toBeVisible()
  await expect(owner.getByTestId('page-frame')).toHaveCount(0)
  await expect(sheet.getByTestId('trust-allow-embed')).toHaveCount(0)
  await shot(owner, 'h-owner-trust-desktop-light')
  // The consent comes only from the owner's own browser session: a call from
  // an API client, even with the session cookie, is refused.
  const direct = await ctx.olivia.put('/api/chats/pages/todo/consent', { data: { hash: list.hash } })
  expect(direct.status()).toBe(403)
  expect((await direct.json()).error.type).toBe('not_human_surface')
  expect((await ctx.olivia.delete('/api/chats/pages/todo/consent')).status()).toBe(403)
  expect((await fetchAs(owner, 'PUT', '/api/chats/pages/todo/consent', { hash: list.hash, embed: true }, 'todo')).status).toBe(400)

  // Home does not run it either, with it as the page area.
  expect((await put(ctx.olivia, '/LocalAI/home.json', JSON.stringify({ pageArea: 'todo' }))).ok()).toBe(true)
  await owner.goto('/app')
  await expect(owner.getByTestId('page-area-open-only')).toBeVisible({ timeout: 15_000 })
  await expect(owner.getByTestId('page-frame')).toHaveCount(0)
  await shot(owner, 'h-home-open-only-desktop-light')
  await ctx.olivia.delete(`/api/chats/files?path=${encodeURIComponent('/LocalAI/home.json')}`)

  // The owner allows this version, once.
  await owner.goto('/app/pages/todo')
  await sheet.getByTestId('trust-allow').click()
  await expect(frame(owner).locator('#t')).toBeVisible({ timeout: 15_000 })

  // Edna changes the code in a chat: her mount of the page folder is writable.
  const { context: ec, page: edna } = await as(browser, 'edna')
  await offerMockModel(edna)
  await sendFromHome(edna, `edit the page ADDON_TOOL_CALL:write_file:${b64({ path: '/workspace/shared-todo/index.html', content: TODO_HTML.replace('Todo list', 'Todo list by Edna') })}`)
  await edna.waitForURL(/\/app\/chats\/[0-9a-f]+$/)
  const chatId = edna.url().split('/').pop()
  await expect.poll(async () => (await (await ctx.edna.get(`/api/chats/${chatId}`)).json()).running, { timeout: 60_000 }).toBe(false)
  if (process.env.PAGE_SHARE_DEBUG) console.log('EDNA CHAT', JSON.stringify(await (await ctx.edna.get(`/api/chats/${chatId}`)).json()).slice(0, 3000))
  await expect.poll(async () => (await (await ctx.olivia.get('/api/chats/pages/todo')).json()).html, { timeout: 15_000 }).toContain('Todo list by Edna')

  const after = (await (await ctx.olivia.get('/api/chats/pages/todo')).json())
  expect(after.consent.state).toBe('changed')
  // The owner's open frame holds the old version: the page sees the change and
  // stops it, without waiting for a call.
  await expect(owner.getByTestId('page-frame')).toHaveCount(0, { timeout: 15_000 })
  await owner.reload()
  await expect(owner.getByTestId('trust-sheet')).toHaveAttribute('data-variant', 'changed')
  await expect(owner.getByTestId('page-frame')).toHaveCount(0)
  await shot(owner, 'h-owner-trust-changed-desktop-light')
  await ec.close()
  await oc.close()
})

for (const v of [{ name: 'desktop', width: 1440, height: 900 }, { name: 'phone', width: 390, height: 844 }]) {
  for (const t of ['dark', 'light']) {
    test(`screenshots of the dialog, the trust sheet and the list, ${v.name} ${t}`, async ({ browser }) => {
      const owner = await as(browser, 'olivia', { width: v.width, height: v.height })
      ownerPage = owner.page
      await theme(owner.page, t)
      await owner.page.goto('/app/pages')
      await expect(card(owner.page, 'todo')).toBeVisible()
      await shot(owner.page, `z-owner-list-${v.name}-${t}`)
      await openShare(owner.page)
      await shot(owner.page, `z-dialog-${v.name}-${t}`)
      await owner.context.close()
      const gus = await as(browser, 'gus', { width: v.width, height: v.height })
      await theme(gus.page, t)
      expect((await fetchAs((await (async () => { await gus.page.goto('/app'); return gus.page })()), 'PUT', '/api/chats/pages/todo/shares', {}, 'todo')).status).toBeGreaterThanOrEqual(400)
      await gus.context.close()
      // The owner shares again, for a grantee who has not allowed this version.
      const o2 = await as(browser, 'olivia', { width: v.width, height: v.height })
      ownerPage = o2.page
      await o2.page.goto('/app/pages')
      expect((await fetchAsOwner('PUT', '/api/chats/pages/todo/shares', { principal: ids.gus, role: 'use' })).status).toBe(200)
      await o2.context.close()
      const g2 = await as(browser, 'gus', { width: v.width, height: v.height })
      await theme(g2.page, t)
      await g2.page.goto('/app/pages')
      await expect(sharedCard(g2.page, 'todo')).toBeVisible()
      await shot(g2.page, `z-shared-list-${v.name}-${t}`)
      await g2.page.goto(sharedPath(await sharedRef('gus')))
      await expect(g2.page.getByTestId('trust-sheet')).toBeVisible()
      await shot(g2.page, `z-trust-${v.name}-${t}`)
      await g2.context.close()
      const o3 = await as(browser, 'olivia')
      ownerPage = o3.page
      await o3.page.goto('/app/pages')
      expect((await fetchAsOwner('DELETE', `/api/chats/pages/todo/shares/${ids.gus}`)).status).toBe(200)
      await o3.context.close()
    })
  }
}
