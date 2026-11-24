import { test, expect } from './coverage-fixtures.js'
import { mockDiscovery, newState, TASK_PAGE } from './discovery-fixtures.js'

// The Pages entry's one-time "New" label and the Pages screen, with every API
// call stubbed (discovery-fixtures.js).

const SHOTS = process.env.DISCOVERY_SHOTS || ''
// Screenshots wait out the entry fade, so they show the settled screen.
const shot = async (page, name) => { if (SHOTS) { await page.waitForTimeout(600); await page.screenshot({ path: `${SHOTS}/${name}.png` }) } }

const nav = (page) => page.locator('#app-sidebar')
const READING = { ...TASK_PAGE, name: 'reading', title: 'Reading list', icon: 'book', description: 'Books and articles to read', author: 'you', updated_by: 'you' }

async function stubFiles(page, state) {
  state.moves = []
  state.removed = []
  await page.route('**/api/chats/files/move', (route) => {
    const { from, to } = route.request().postDataJSON()
    state.moves.push([from, to])
    state.pages = state.pages.map(p => (`/pages/${p.name}` === from ? { ...p, name: to.split('/').pop() } : p))
    return route.fulfill({ json: { path: to } })
  })
  await page.route(/\/api\/chats\/files\?path=/, (route) => {
    if (route.request().method() !== 'DELETE') return route.fulfill({ json: { entries: [] } })
    const path = new URL(route.request().url()).searchParams.get('path')
    state.removed.push(path)
    state.pages = state.pages.filter(p => `/pages/${p.name}` !== path)
    return route.fulfill({ json: { rev: 5 } })
  })
}

test('the Pages entry says New once, keeps the date in user.json, and drops the label once the user opens it', async ({ page }) => {
  const state = newState({ pages: [TASK_PAGE] })
  await mockDiscovery(page, state)
  await page.goto('/app')
  const label = nav(page).getByTestId('nav-pages-new')
  await expect(label).toHaveText('New', { timeout: 15_000 })
  // The label is text inside the link: "Pages, New" for a screen reader.
  await expect(nav(page).getByRole('link', { name: /Pages\s*New/ })).toBeVisible()
  await expect.poll(() => state.files.user?.seen?.new?.pages).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  await shot(page, 'D1-sidebar-pages-new')

  await nav(page).getByRole('link', { name: /Pages/ }).first().click()
  await expect(page).toHaveURL(/\/app\/pages$/)
  await expect(label).toHaveCount(0)
  await expect.poll(() => state.files.user?.seen?.new?.pages).toBe('opened')
  await page.goto('/app')
  await expect(nav(page).getByRole('link', { name: /^Pages( New)?$/ })).toBeVisible({ timeout: 15_000 })
  await expect(label).toHaveCount(0)
})

test('no New label after a week, or with "Notes about new things" off', async ({ page }) => {
  const state = newState({ pages: [TASK_PAGE], files: { user: { seen: { new: { pages: '2020-01-01' } } } } })
  await mockDiscovery(page, state)
  await page.goto('/app')
  await expect(nav(page).getByRole('link', { name: /^Pages( New)?$/ })).toBeVisible({ timeout: 15_000 })
  await expect(nav(page).getByTestId('nav-pages-new')).toHaveCount(0)

  state.files.user = { seen: { new_off: true } }
  await page.reload()
  await expect(nav(page).getByRole('link', { name: /^Pages( New)?$/ })).toBeVisible({ timeout: 15_000 })
  await expect(nav(page).getByTestId('nav-pages-new')).toHaveCount(0)
  expect(state.files.user.seen.new).toBeUndefined()
})

test('the Pages screen lists yours with a pin toggle and a menu to ask, show files, rename and delete', async ({ page }) => {
  const state = newState({ pages: [TASK_PAGE, READING] })
  await mockDiscovery(page, state)
  await stubFiles(page, state)
  await page.goto('/app/pages')
  await expect(page.getByRole('heading', { name: /Yours/ })).toContainText('2', { timeout: 15_000 })
  await expect(page.getByTestId('pages-ask-new')).toBeVisible()
  const row = page.locator('[data-testid="page-card"][data-page="tasks"]')
  await expect(row).toContainText('pages/tasks')
  await expect(row.getByTestId('page-author')).toHaveText('Changed just now by the assistant')
  await expect(row.getByTestId('page-pin')).toHaveAttribute('aria-pressed', 'false')
  await row.getByRole('button', { name: 'More for Task board' }).click()
  for (const item of ['Ask the assistant to change this', 'Show its files', 'Rename', 'Delete page']) {
    await expect(page.getByRole('menuitem', { name: item })).toBeVisible()
  }
  await shot(page, 'D2-pages-screen-menu')
  await page.getByRole('menuitem', { name: 'Rename' }).click()
  await row.getByTestId('page-rename-input').fill('board')
  await row.getByRole('button', { name: 'Save' }).click()
  await expect.poll(() => state.moves).toEqual([['/pages/tasks', '/pages/board']])
  await expect(page.locator('[data-testid="page-card"][data-page="board"]')).toBeVisible()

  const reading = page.locator('[data-testid="page-card"][data-page="reading"]')
  await reading.getByRole('button', { name: 'More for Reading list' }).click()
  await page.getByRole('menuitem', { name: 'Delete page' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete page' }).click()
  await expect.poll(() => state.removed).toEqual(['/pages/reading'])
  await expect(reading).toHaveCount(0)
})

test('Show its files opens the page folder in Files', async ({ page }) => {
  const state = newState({ pages: [TASK_PAGE] })
  await mockDiscovery(page, state)
  await stubFiles(page, state)
  await page.goto('/app/pages')
  await page.getByRole('button', { name: 'More for Task board' }).click({ timeout: 15_000 })
  await page.getByRole('menuitem', { name: 'Show its files' }).click()
  await expect(page).toHaveURL(/\/app\/files\?path=%2Fpages%2Ftasks$/)
})

for (const theme of ['dark', 'light']) {
  test(`the empty Pages screen asks for a page and offers both ready pages (${theme})`, async ({ page }) => {
    await page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private window */ } }, theme)
    const state = newState({ pages: [] })
    await mockDiscovery(page, state)
    await page.goto('/app/pages')
    const empty = page.getByTestId('pages-empty')
    await expect(empty.getByRole('heading', { name: 'No pages yet' })).toBeVisible({ timeout: 15_000 })
    await expect(empty.getByTestId('pages-ask')).toHaveText(/Ask for a page/)
    await expect(empty.getByTestId('pages-starter')).toHaveText(/Start from a task board/)
    await expect(empty.getByTestId('pages-starter-reading')).toHaveText(/Start from a reading list/)
    await expect(empty).toContainText('This entry leaves the sidebar when you have no pages.')
    await shot(page, `D3-pages-empty-${theme}`)
    await empty.getByTestId('pages-starter').click()
    await expect(page).toHaveURL(/\/app\/pages\/task-board$/)
  })
}

test('on a phone the rows keep their actions without a horizontal scroll', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const state = newState({ pages: [TASK_PAGE, READING] })
  await mockDiscovery(page, state)
  await page.goto('/app/pages')
  await expect(page.getByTestId('page-card')).toHaveCount(2, { timeout: 15_000 })
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1)
  expect(overflow).toBe(false)
  await shot(page, 'D4-mobile-pages-screen')
})
