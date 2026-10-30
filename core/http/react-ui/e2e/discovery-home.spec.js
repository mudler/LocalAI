import { test, expect } from './coverage-fixtures.js'
import { mockDiscovery, newState, stubFeed, TASK_PAGE } from './discovery-fixtures.js'

// "Make LocalAI yours" on Home: the three example cards, the ready pages and
// "Hide these", with every API call stubbed (discovery-fixtures.js).

const SHOTS = process.env.DISCOVERY_SHOTS || ''
const shot = async (page, name) => { if (SHOTS) await page.screenshot({ path: `${SHOTS}/${name}.png` }) }

const section = (page) => page.getByTestId('home-examples')
const cards = (page) => page.getByTestId('home-example')

test('a first-time user sees three cards; a card puts its prompt, with their first name, in the composer and sends nothing', async ({ page }) => {
  const state = newState()
  await mockDiscovery(page, state)
  await page.goto('/app')
  await expect(section(page)).toBeVisible({ timeout: 15_000 })
  await expect(section(page).getByRole('heading', { name: 'Make LocalAI yours' })).toBeVisible()
  await expect(cards(page)).toHaveCount(3)
  await expect(cards(page).first()).toContainText('Call me Marta and keep answers short')
  // Without a mail connector the third card lists chats instead of the inbox.
  await expect(cards(page).nth(2)).toContainText("Every morning, list yesterday's chats here")
  await shot(page, 'A1-home-first-run')

  await cards(page).first().click()
  const box = page.getByTestId('home-composer').locator('textarea')
  await expect(box).toHaveValue('Call me Marta and keep answers short')
  await expect(box).toBeFocused()
  await expect(page.getByTestId('home-example-hint')).toContainText('Nothing is saved until you send it.')
  await shot(page, 'A2-home-example-in-composer')
  // Nothing was written and no chat was opened.
  expect(state.puts).toHaveLength(0)
  await expect(page).toHaveURL(/\/app$/)
})

test('with a mail connector the third card summarises the inbox', async ({ page }) => {
  const state = newState({ connectors: [{ id: 'gmail', name: 'Mail', enabled: true }] })
  await mockDiscovery(page, state)
  await page.goto('/app')
  await expect(cards(page).nth(2)).toContainText('Every morning, summarise my inbox here', { timeout: 15_000 })
})

test('a card whose result exists is gone; a result that arrives during the visit shows Done', async ({ page }) => {
  const state = newState({ files: { persona: { system_prompt: 'Keep answers short.' } } })
  await mockDiscovery(page, state)
  const change = await stubFeed(page)
  await page.goto('/app')
  await expect(cards(page)).toHaveCount(2, { timeout: 15_000 })
  await expect(page.locator('[data-card="profile"]')).toHaveCount(0)

  // The assistant makes a page in another tab: the page card says Done.
  state.pages = [TASK_PAGE]
  change(['pages/tasks/index.html'])
  const done = page.getByTestId('home-example-done')
  await expect(done).toHaveAttribute('data-card', 'page')
  await expect(done).toContainText('Change it in Account')
  await expect(cards(page)).toHaveCount(1)
})

test('Hide these hides the section, records it in user.json, and Undo brings it back', async ({ page }) => {
  const state = newState()
  await mockDiscovery(page, state)
  await page.goto('/app')
  await expect(section(page)).toBeVisible({ timeout: 15_000 })
  await page.getByTestId('home-examples-hide').click()
  await expect(section(page)).toHaveCount(0)
  expect(state.files.user.seen.home_examples.hidden).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  // The account's name was only prefilled: hiding the examples does not save it.
  expect(state.files.user.name).toBeUndefined()
  const toast = page.getByTestId('examples-hidden-toast')
  await expect(toast).toContainText('Examples hidden.')
  await shot(page, 'A4-home-examples-hidden-toast')
  await toast.getByRole('button', { name: 'Undo' }).click()
  await expect(section(page)).toBeVisible()
  expect(state.files.user.seen.home_examples).toBeUndefined()

  // Hidden for good: a reload keeps them hidden, and the library line offers Customise Home.
  await page.getByTestId('home-examples-hide').click()
  await page.reload()
  await expect(page.getByTestId('home-library')).toBeVisible({ timeout: 15_000 })
  await expect(section(page)).toHaveCount(0)
  await expect(page.getByTestId('home-customise')).toBeVisible()
})

test('a user who already changed LocalAI on their own never sees the examples', async ({ page }) => {
  const state = newState({ files: { shortcuts: { shortcuts: [] }, ui: { theme: 'dark' } } })
  await mockDiscovery(page, state)
  await page.goto('/app')
  await expect(page.getByTestId('home-library')).toBeVisible({ timeout: 15_000 })
  // Customise Home shows once every file was read: the examples stay away.
  await expect(page.getByTestId('home-customise')).toBeVisible()
  await expect(section(page)).toHaveCount(0)
})

test('a ready page is copied and opened', async ({ page }) => {
  const state = newState()
  await mockDiscovery(page, state)
  await page.goto('/app')
  await page.locator('[data-testid="home-example-ready"][data-starter="reading-list"]').click({ timeout: 15_000 })
  await expect(page).toHaveURL(/\/app\/pages\/reading-list$/)
  expect(state.copied).toBe('reading-list')
})

for (const theme of ['dark', 'light']) {
  test(`on a phone the cards stack, without their kicker (${theme})`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private window */ } }, theme)
    const state = newState({ files: { ui: undefined } })
    await mockDiscovery(page, state)
    await page.goto('/app')
    await expect(cards(page)).toHaveCount(3, { timeout: 15_000 })
    const [a, b] = [await cards(page).nth(0).boundingBox(), await cards(page).nth(1).boundingBox()]
    expect(b.y).toBeGreaterThan(a.y + a.height - 1)
    expect(Math.abs(a.x - b.x)).toBeLessThan(2)
    await expect(cards(page).first().locator('.wk-ex__kicker')).toBeHidden()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1)
    expect(overflow).toBe(false)
    await shot(page, `A5-mobile-home-first-run-${theme}`)
  })
}

test('desktop, light theme', async ({ page }) => {
  await page.addInitScript(() => { try { localStorage.setItem('localai-theme', 'light') } catch { /* private window */ } })
  const state = newState()
  await mockDiscovery(page, state)
  await page.goto('/app')
  await expect(cards(page)).toHaveCount(3, { timeout: 15_000 })
  const row = await cards(page).first().boundingBox()
  const third = await cards(page).nth(2).boundingBox()
  expect(Math.abs(row.y - third.y)).toBeLessThan(2)
  await shot(page, 'A3-home-first-run-light')
})
