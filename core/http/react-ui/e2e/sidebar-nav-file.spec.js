import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'

// The order of the sidebar, kept in nav.json: written by the assistant (a plain
// file write) or by the Navigation tab of Account, validated by the server, live
// through the change feed, and never able to remove New chat, Chats, settings or
// the search. Against the real test server.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

holdHomeFiles(test)

const PAGE = 'nav-board'
const FILES = ['/LocalAI/nav.json', `/pages/${PAGE}/index.html`]

const put = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
const read = (request) => request.get('/api/chats/wellknown/nav').then(r => r.json())
const writeNav = (request, items, extra = {}) => put(request, '/LocalAI/nav.json', { items, ...extra })
const rail = (page) => page.locator('#app-sidebar')
const entries = (page) => rail(page).locator('.sidebar-nav .nav-item .nav-label')

async function clean(request) {
  for (const p of FILES) await request.delete(`/api/chats/files?path=${encodeURIComponent(p)}`)
}
async function makeChat(request, title) {
  const r = await request.post('/api/chats', { data: { model: 'mock-model' } })
  expect(r.ok()).toBeTruthy()
  const { id } = await r.json()
  expect((await request.patch(`/api/chats/${id}`, { data: { title } })).ok()).toBeTruthy()
  return id
}
async function makePage(request) {
  expect((await put(request, `/pages/${PAGE}/index.html`, '<!doctype html><p>board</p>')).ok()).toBeTruthy()
}

test.beforeEach(async ({ request }) => { await clean(request); await makePage(request) })
test.afterAll(async ({ request }) => { await clean(request) })
test.afterAll(() => unlockHomeFiles())

const fixedStay = async (page) => {
  await expect(rail(page).getByTestId('sidebar-new-chat')).toBeVisible()
  await expect(rail(page).getByTestId('sidebar-all-chats')).toBeVisible()
  await expect(rail(page).getByTestId('sidebar-your-settings')).toBeVisible()
}

test('nav.json: without nav.json the sidebar is the default one', async ({ page }) => {
  await page.goto('/app')
  await expect(rail(page).getByTestId('nav-section')).toHaveCount(0)
  await expect(entries(page).first()).toHaveText('Home')
  await fixedStay(page)
})

test('nav.json: the assistant writes the order, a name, a hidden entry, a group and links, and the sidebar follows live', async ({ page, request }) => {
  const chat = await makeChat(request, 'Gate repair nav')
  await page.goto('/app')
  await expect(rail(page).getByTestId('nav-section')).toHaveCount(0)

  await writeNav(request, [
    { id: 'home' }, { id: 'models' }, { group: 'Create' }, { id: 'agents', hidden: true }, { id: 'skills', label: 'Recipes' },
    { type: 'page', page: PAGE, label: 'Board' }, { type: 'chat', chat, label: 'Gate repair' },
    { type: 'url', url: 'https://localai.io/docs', label: 'Docs' },
  ])
  const nav = rail(page).locator('.sidebar-nav').first()
  await expect(rail(page).getByTestId('nav-group')).toHaveText('Create')
  await expect(nav.locator('.nav-label').filter({ hasText: /^Recipes$/ })).toBeVisible()
  await expect(nav.locator('.nav-label').filter({ hasText: /^Agents$/ })).toHaveCount(0)
  await expect(nav.getByTestId('nav-custom-link')).toHaveCount(3)
  // The order: Home, Models, then the group (Recipes, Board, the chat, Docs), the rest after.
  const labels = await nav.getByTestId('nav-section').first().locator('.nav-label').allTextContents()
  expect(labels.slice(0, 2)).toEqual(['Home', 'Models'])
  await fixedStay(page)

  // A built-in entry the list leaves out is not lost: Files comes after the last item.
  await expect(nav.locator('.nav-label').filter({ hasText: /^Files$/ })).toBeVisible()
  await nav.locator('[data-kind="chat"]').click()
  await expect(page).toHaveURL(new RegExp(`/app/chats/${chat}$`))
})

test('nav.json: every fixed entry stays when the file hides everything', async ({ page, request }) => {
  // Home cannot be hidden: the server refuses a file that tries.
  const refused = await request.put('/api/chats/wellknown/nav', { data: { values: { items: [{ id: 'home', hidden: true }] }, replace: true } })
  expect(refused.status()).toBe(400)
  expect((await refused.json()).errors.join(' ')).toContain('Home cannot be hidden')
  await writeNav(request, [{ id: 'home' }, ...['pages', 'files', 'agents', 'connections', 'memory', 'skills', 'models', 'addons', 'new-chat', 'chats', 'settings', 'palette'].map(id => ({ id, hidden: true }))])
  await page.goto('/app')
  await expect(rail(page).getByTestId('nav-section')).toHaveCount(1)
  await expect(entries(page).first()).toHaveText('Home')
  await fixedStay(page)
  await expect(rail(page).getByTestId('operate-updates-badge').or(rail(page).getByRole('link', { name: /administration/i }))).toBeVisible()
})

test('nav.json: a link to a page or a chat that is gone is dimmed in the editor and skipped in the sidebar', async ({ page, request }) => {
  await writeNav(request, [{ id: 'home' }, { type: 'page', page: 'no-such-page' }, { type: 'chat', chat: 'no-such-chat' }, { type: 'page', page: PAGE }])
  await page.goto('/app')
  const nav = rail(page).locator('.sidebar-nav').first()
  await expect(nav.locator('[data-kind="page"]')).toHaveCount(1)
  await expect(nav.locator('[data-kind="chat"]')).toHaveCount(0)

  await page.goto('/app/account?tab=navigation')
  const rows = page.getByTestId('nav-row')
  await expect(rows.filter({ has: page.getByTestId('nav-row-missing') })).toHaveCount(2)
  await expect(page.getByTestId('nav-preview').getByTestId('nav-preview-item').filter({ hasText: 'no-such' })).toHaveCount(0)
})

test('nav.json: an address asks before it leaves, and Cancel stays', async ({ page, request, context }) => {
  await writeNav(request, [{ id: 'home' }, { type: 'url', url: 'https://localai.io/docs', label: 'Docs' }])
  await page.goto('/app')
  const link = rail(page).locator('[data-kind="url"]')
  await expect(link).toBeVisible()
  let opened = 0
  context.on('page', () => { opened++ })
  await link.click()
  const ask = page.getByTestId('nav-link-ask')
  await expect(ask.getByTestId('page-link-host')).toContainText('localai.io')
  expect(opened).toBe(0)
  await ask.getByTestId('page-link-cancel').click()
  await expect(ask).toHaveCount(0)
  expect(opened).toBe(0)
  await link.click()
  const popup = context.waitForEvent('page')
  await page.getByTestId('page-link-open').click()
  const p = await popup
  expect(p.url()).toContain('localai.io')
  await p.close()
})

test('nav.json: a broken file keeps the last good sidebar and the editor shows the error', async ({ page, request }) => {
  await writeNav(request, [{ id: 'home' }, { id: 'models', label: 'Engines' }])
  await page.goto('/app')
  await expect(rail(page).locator('.nav-label').filter({ hasText: /^Engines$/ })).toBeVisible()
  await writeNav(request, [{ id: 'files' }, { id: 'files' }, { type: 'url', url: 'javascript:alert(1)' }])
  const doc = await read(request)
  expect(doc.valid).toBe(false)
  expect(doc.fallback).toBe(true)
  await page.goto('/app/account?tab=navigation')
  await expect(page.getByTestId('wk-invalid-nav')).toBeVisible()
  await expect(page.getByTestId('nav-rows')).toBeVisible()
  await expect(rail(page).locator('.nav-label').filter({ hasText: /^Engines$/ })).toBeVisible()
})

test('nav.json: the editor reorders, renames, hides, groups and links, and it survives a reload and shows in another tab', async ({ page, request, context }) => {
  const chat = await makeChat(request, 'Editor chat nav')
  await page.goto('/app/account?tab=navigation')
  const rows = page.getByTestId('nav-rows').getByTestId('nav-row')
  await expect(rows.first()).toBeVisible()
  const other = await context.newPage()
  await other.goto('/app')
  await expect(rail(other).getByTestId('nav-section')).toHaveCount(0)

  // Move Skills up, to just under Home, with its button, rename it, hide Agents.
  const row = (id) => page.locator(`[data-key="id:${id}"]`)
  for (let i = 0; i < 5; i++) await row('skills').getByTestId('nav-row-up').click()
  await row('skills').getByTestId('nav-row-name').fill('Recipes')
  await row('agents').getByTestId('nav-row-hide').click()
  // Home cannot be hidden: it has no eye.
  await expect(row('home').getByTestId('nav-row-hide')).toHaveCount(0)

  // A group, then a page link, a chat link and an address.
  await page.getByTestId('nav-add-group').click()
  const groupName = page.locator('[data-kind="group"]').getByTestId('nav-row-name')
  await expect(groupName).toBeFocused()
  await page.keyboard.type('Tools') // the new name is selected, so typing replaces it
  await expect(groupName).toHaveValue('Tools')
  await page.getByTestId('nav-add-link').click()
  await page.getByTestId('nav-add-page').click()
  await page.getByTestId('nav-add-select').selectOption(PAGE)
  await page.getByTestId('nav-add-confirm').click()
  await page.getByTestId('nav-add-link').click()
  await page.getByTestId('nav-add-chat').click()
  await page.getByTestId('nav-add-select').selectOption(chat)
  await page.getByTestId('nav-add-confirm').click()
  await page.getByTestId('nav-add-link').click()
  await page.getByTestId('nav-add-url').click()
  await page.getByTestId('nav-add-url-input').fill('javascript:alert(1)')
  await expect(page.getByTestId('nav-add-confirm')).toBeDisabled()
  await page.getByTestId('nav-add-url-input').fill('https://localai.io/docs')
  await page.getByTestId('nav-add-label').fill('Docs')
  await page.getByTestId('nav-add-confirm').click()

  // The live preview follows before anything is saved.
  const preview = page.getByTestId('nav-preview')
  await expect(preview.getByTestId('nav-preview-item').filter({ hasText: 'Recipes' })).toBeVisible()
  await expect(preview.getByTestId('nav-preview-item').filter({ hasText: /^Agents$/ })).toHaveCount(0)
  await expect(preview.getByTestId('nav-preview-group')).toHaveText(['Tools'])

  await page.getByTestId('wk-save-nav').click()
  await expect(page.getByText('Saved.')).toBeVisible()
  const saved = (await read(request)).data.items
  expect(saved.findIndex(i => i.id === 'skills')).toBeLessThan(saved.findIndex(i => i.id === 'files'))
  expect(saved.find(i => i.id === 'skills')).toEqual({ id: 'skills', label: 'Recipes' })
  expect(saved.find(i => i.id === 'agents')).toEqual({ id: 'agents', hidden: true })
  expect(saved.some(i => i.group === 'Tools')).toBe(true)
  expect(saved.some(i => i.type === 'page' && i.page === PAGE)).toBe(true)
  expect(saved.some(i => i.type === 'chat' && i.chat === chat)).toBe(true)
  expect(saved.some(i => i.type === 'url' && i.url === 'https://localai.io/docs' && i.label === 'Docs')).toBe(true)

  // The other tab changes without a reload (the change feed).
  await expect(rail(other).getByTestId('nav-group')).toHaveText('Tools')
  await expect(rail(other).locator('.nav-label').filter({ hasText: /^Recipes$/ })).toBeVisible()
  await expect(rail(other).locator('.nav-label').filter({ hasText: /^Agents$/ })).toHaveCount(0)
  await expect(rail(other).getByTestId('nav-custom-link')).toHaveCount(3)
  await fixedStay(other)
  await other.close()

  // A reload keeps it.
  await page.reload()
  await expect(row('skills').getByTestId('nav-row-name')).toHaveValue('Recipes')
  await expect(row('agents').getByTestId('nav-row-hide')).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByTestId('nav-rows').locator('[data-kind="link"]')).toHaveCount(3)

  // Remove a link; reset to default empties the file's list, and the sidebar is the default one again.
  await page.getByTestId('nav-reset').click()
  await page.getByTestId('wk-save-nav').click()
  await expect.poll(async () => (await read(request)).data.items).toEqual([])
  await page.goto('/app')
  await expect(rail(page).getByTestId('nav-section')).toHaveCount(0)
  await fixedStay(page)
})

test('nav.json: the keyboard moves a row, and the editor has no control for the fixed entries', async ({ page }) => {
  await page.goto('/app/account?tab=navigation')
  const first = page.getByTestId('nav-row').nth(1)
  const key = await first.getAttribute('data-key')
  await first.getByTestId('nav-row-grip').focus()
  await page.keyboard.press('ArrowUp')
  await expect(page.getByTestId('nav-row').first()).toHaveAttribute('data-key', key)
  const all = await page.getByTestId('nav-row').evaluateAll(els => els.map(e => e.dataset.key))
  for (const fixed of ['id:new-chat', 'id:chats', 'id:settings', 'id:palette']) expect(all).not.toContain(fixed)
  await expect(page.getByTestId('wk-stored-nav')).toContainText('Stored in LocalAI/nav.json')
  await expect(page.getByTestId('nav-reset')).toBeEnabled()
})

test('nav.json: a save merges with a change the assistant made while the tab was open', async ({ page, request }) => {
  await writeNav(request, [{ id: 'home' }, { id: 'files' }], { x_note: 'kept' })
  await page.goto('/app/account?tab=navigation')
  const row = (id) => page.locator(`[data-key="id:${id}"]`)
  await row('files').getByTestId('nav-row-name').fill('Documents')
  // The assistant adds a link and hides Skills in the meantime.
  await writeNav(request, [{ id: 'home' }, { id: 'files' }, { id: 'skills', hidden: true }, { type: 'url', url: 'https://localai.io/' }], { x_note: 'kept' })
  await page.getByTestId('wk-save-nav').click()
  await expect(page.getByText('Saved.')).toBeVisible()
  const saved = (await read(request)).data
  expect(saved.x_note).toBe('kept')
  expect(saved.items.find(i => i.id === 'files')).toEqual({ id: 'files', label: 'Documents' })
  expect(saved.items.find(i => i.id === 'skills')).toEqual({ id: 'skills', hidden: true })
  expect(saved.items.some(i => i.url === 'https://localai.io/')).toBe(true)
})
