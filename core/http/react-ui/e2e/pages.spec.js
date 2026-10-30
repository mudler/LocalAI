import { test, expect } from './coverage-fixtures.js'

// The Pages screens with the pages API stubbed: the sidebar entry, the list,
// pins in ui.json, the empty state, the viewer strip, chat.send and the reload
// rules. pages-real.spec.js runs the sandbox and the bridge against the server.

const NOW = new Date().toISOString()
const TASKS = {
  name: 'tasks', path: '/pages/tasks', title: 'Tasks board', icon: 'checklist', description: 'What I do this week',
  source: 'own', author: 'assistant', updated_at: NOW, updated_by: 'assistant', rev: 7, size: 100,
}

const PAGE_HTML = (label) => `<!doctype html><body><p id="v">${label}</p><p id="ev"></p><p id="sent"></p><button id="s">s</button>
<script>
window.__mark = window.__mark || Math.random()
document.getElementById('v').dataset.mark = String(window.__mark)
localai.on('files.changed', (e) => { document.getElementById('ev').textContent = JSON.stringify(e) })
document.getElementById('s').onclick = () => localai.chat.send('summarise my tasks').then((r) => { document.getElementById('sent').textContent = JSON.stringify(r) })
</script></body>`

// Stubs the pages routes and ui.json. state.pages and state.html can change
// during a test; state.ui is the parsed ui.json; puts records the writes.
async function stub(page, state) {
  state.puts = []
  state.sends = []
  await page.route(/\/api\/chats\/pages(\?.*)?$/, (route) => route.fulfill({ json: { pages: state.pages } }))
  await page.route(/\/api\/chats\/pages\/tasks(\?.*)?$/, (route) => route.fulfill({ json: { page: state.pages[0] || TASKS, html: state.html } }))
  await page.route(/\/api\/chats\/pages\/tasks\/chat$/, (route) => {
    state.sends.push(route.request().postDataJSON())
    return route.fulfill({ json: { chat_id: 'pc1', title: 'Page: Tasks board', started: true } })
  })
  await page.route(/\/api\/chats\/page-starters\/task-board\/copy$/, (route) => {
    state.pages = [{ ...TASKS, name: 'tasks', title: 'Task board', author: 'you', updated_by: 'you' }]
    return route.fulfill({ status: 201, json: state.pages[0] })
  })
  await page.route(/\/api\/chats\/files\?path=%2F$/, (route) => route.fulfill({
    json: { path: '/', entries: state.ui ? [{ path: '/ui.json', name: 'ui.json', kind: 'file', size: 10, version: state.uiVersion || 1, rev: 1 }] : [] },
  }))
  await page.route(/\/api\/chats\/files\/content\?path=%2Fui\.json/, async (route) => {
    if (route.request().method() === 'PUT') {
      const body = JSON.parse(route.request().postData())
      state.puts.push({ url: route.request().url(), body })
      state.ui = body
      state.uiVersion = (state.uiVersion || 1) + 1
      return route.fulfill({ json: { path: '/ui.json', name: 'ui.json', kind: 'file', size: 10, version: state.uiVersion, rev: 2 } })
    }
    return route.fulfill({ body: JSON.stringify(state.ui || {}), contentType: 'application/octet-stream' })
  })
}

const nav = (page) => page.locator('#app-sidebar')

test('the Pages entry is hidden with no page and shows with one, with pinned pages under it', async ({ page }) => {
  const state = { pages: [], html: PAGE_HTML('v1') }
  await stub(page, state)
  const listed = page.waitForResponse(/\/api\/chats\/pages$/)
  await page.goto('/app/files')
  await listed
  await expect(nav(page).getByRole('link', { name: 'Files', exact: true })).toBeVisible()
  await expect(nav(page).getByRole('link', { name: 'Pages', exact: true })).toHaveCount(0)

  state.pages = [TASKS]
  state.ui = { theme: 'dark', pinnedPages: ['tasks', 'gone'] }
  await page.goto('/app/files')
  await expect(nav(page).getByRole('link', { name: 'Pages', exact: true })).toBeVisible()
  const pinned = nav(page).getByTestId('pages-pinned-link')
  await expect(pinned).toHaveCount(1)
  await expect(pinned).toHaveText('Tasks board')
  await pinned.click()
  await expect(page).toHaveURL(/\/app\/pages\/tasks$/)
})

test('the list shows each page with who changed it, and Pin keeps the other keys of ui.json', async ({ page }) => {
  const state = { pages: [TASKS], html: PAGE_HTML('v1'), ui: { theme: 'light', accent: '#112233', later: { x: 1 } } }
  await stub(page, state)
  await page.goto('/app/pages')
  const card = page.getByTestId('page-card')
  await expect(card).toHaveCount(1)
  await expect(card).toContainText('Tasks board')
  await expect(card).toContainText('pages/tasks/index.html')
  await expect(card.getByTestId('page-author')).toHaveText('by the assistant')
  await expect(card).toContainText('Changed just now')

  await card.getByTestId('page-pin').click()
  await expect(card.getByTestId('page-pin')).toHaveText(/Unpin/)
  expect(state.puts).toHaveLength(1)
  expect(state.puts[0].url).toContain('base=1')
  expect(state.puts[0].body).toEqual({ theme: 'light', accent: '#112233', later: { x: 1 }, pinnedPages: ['tasks'] })
  await expect(nav(page).getByTestId('pages-pinned-link')).toHaveText('Tasks board')

  await card.getByTestId('page-pin').click()
  await expect(card.getByTestId('page-pin')).toHaveText(/^\s*Pin/)
  expect(state.puts[1].body.pinnedPages).toEqual([])
  await expect(nav(page).getByTestId('pages-pinned-link')).toHaveCount(0)
})

test('the empty state copies the task board and opens it', async ({ page }) => {
  const state = { pages: [], html: PAGE_HTML('board') }
  await stub(page, state)
  await page.goto('/app/pages')
  await expect(page.getByTestId('pages-empty')).toBeVisible()
  await page.getByTestId('pages-starter').click()
  await expect(page).toHaveURL(/\/app\/pages\/tasks$/)
  await expect(page.frameLocator('[data-testid="page-frame"]').locator('#v')).toHaveText('board')
})

test('Ask the assistant opens a chat with the page path in the box, not sent', async ({ page }) => {
  const state = { pages: [TASKS], html: PAGE_HTML('v1') }
  await stub(page, state)
  await page.goto('/app/pages/tasks')
  await expect(page.getByTestId('page-updated')).toHaveText(/updated just now by the assistant/)
  await page.getByTestId('page-ask').click()
  await expect(page).toHaveURL(/\/app\/chats$/)
  await expect(page.locator('textarea').first()).toHaveValue('Change the page in pages/tasks/ so that ')
})

test('chat.send waits for the person: Dismiss answers sent false, Send posts to the chosen chat', async ({ page }) => {
  const state = { pages: [TASKS], html: PAGE_HTML('v1') }
  await stub(page, state)
  await page.goto('/app/pages/tasks')
  const f = page.frameLocator('[data-testid="page-frame"]')
  await f.locator('#s').click()
  await expect(page.getByTestId('page-send-ask')).toContainText('summarise my tasks')
  await page.getByTestId('page-send-ask').getByRole('button', { name: 'Dismiss' }).click()
  await expect(f.locator('#sent')).toHaveText('{"sent":false}')
  expect(state.sends).toHaveLength(0)

  await f.locator('#s').click()
  await page.getByTestId('page-send-ask').getByRole('button', { name: 'Send' }).click()
  await expect(page.getByTestId('page-send-done')).toContainText('Sent to Page: Tasks board')
  await expect(f.locator('#sent')).toHaveText('{"sent":true,"chat_id":"pc1"}')
  expect(state.sends).toEqual([expect.objectContaining({ text: 'summarise my tasks' })])
  expect(state.sends[0].chat_id).toBeUndefined()
})

test('a change of index.html reloads the page; anything else reaches it as files.changed', async ({ page }) => {
  const state = { pages: [TASKS], html: PAGE_HTML('v1') }
  await stub(page, state)
  await page.goto('/app/pages/tasks')
  const f = page.frameLocator('[data-testid="page-frame"]')
  await expect(f.locator('#v')).toHaveText('v1')
  const mark = await f.locator('#v').getAttribute('data-mark')

  // Same HTML: the frame stays and hears about the change.
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await expect(f.locator('#ev')).toHaveText('{"paths":[],"resync":true}')
  expect(await f.locator('#v').getAttribute('data-mark')).toBe(mark)

  // New HTML: the frame loads again.
  state.html = PAGE_HTML('v2')
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await expect(f.locator('#v')).toHaveText('v2')
})

test('a page that does not exist says so, with a way back', async ({ page }) => {
  await page.route(/\/api\/chats\/pages\/nope(\?.*)?$/, (route) => route.fulfill({ status: 404, json: { error: { message: 'page or file not found', code: 404, type: 'not_found' } } }))
  await page.goto('/app/pages/nope')
  await expect(page.getByTestId('page-not-found')).toContainText('There is no page called nope.')
  await page.getByRole('link', { name: 'All pages' }).click()
  await expect(page).toHaveURL(/\/app\/pages$/)
})

test('the viewer fits a phone screen without horizontal scroll', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const state = { pages: [TASKS], html: PAGE_HTML('v1') }
  await stub(page, state)
  await page.goto('/app/pages/tasks')
  await expect(page.getByTestId('page-frame')).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
  await page.goto('/app/pages')
  await expect(page.getByTestId('page-card')).toBeVisible()
  const listOverflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(listOverflow).toBeLessThanOrEqual(0)
})
