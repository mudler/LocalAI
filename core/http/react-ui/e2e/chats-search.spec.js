import { test, expect } from './coverage-fixtures.js'
import { RECENT, TRIG, TWO_HITS, hit, mockSearch } from './chats-search.js'

// Search across all chats: the Ctrl or Cmd K dialog of a transcript and the All
// chats page show the hits of GET /api/chats/search best first, each with the
// words that matched marked in a snippet, filters for agent, origin and date,
// keyboard control, and a link that opens the chat at the matching message.
// The API is stubbed.

async function openDialog(page) {
  await page.goto('/app/chats/c1')
  await page.getByTestId('chats-trigger').click()
  return page.getByTestId('chats-menu')
}

test.describe('Chat search in the Ctrl K dialog', () => {
  test('shows the hits with the matched words marked, and never reads message text as HTML', async ({ page }) => {
    await mockSearch(page, { answer: TWO_HITS })
    const menu = await openDialog(page)
    await menu.getByRole('combobox', { name: 'Search conversations...' }).fill('qdrant port')
    const rows = menu.getByTestId('chats-row')
    await expect(rows).toHaveCount(2)
    await expect(menu.getByTestId('chats-search-status')).toContainText('2 results')
    // The chat that only the search can find is in the list.
    await expect(rows.nth(1)).toContainText('Backup notes')
    const first = rows.nth(0).getByTestId('chats-snippet')
    await expect(first).toContainText('Assistant')
    await expect(first.locator('mark')).toHaveText(['Qdrant', 'port'])
    await expect(rows.nth(0)).toContainText('2 more matches in this chat')
    // The second snippet holds tags: they show as text, and nothing runs.
    const second = rows.nth(1).getByTestId('chats-snippet')
    await expect(second).toContainText('<img src=x onerror="window.__pwned=1">')
    await expect(second.locator('img, b')).toHaveCount(0)
    await expect(second.locator('mark')).toHaveText(['port'])
    expect(await page.evaluate(() => window.__pwned)).toBeUndefined()
  })

  test('arrows move through the hits, Enter opens the hit at its message', async ({ page }) => {
    await mockSearch(page, { answer: TWO_HITS })
    const menu = await openDialog(page)
    const box = menu.getByRole('combobox', { name: 'Search conversations...' })
    await box.fill('qdrant')
    const rows = menu.getByTestId('chats-row')
    await expect(rows).toHaveCount(2)
    await expect(rows.nth(0)).toHaveAttribute('aria-selected', 'true')
    await box.press('ArrowDown')
    await expect(rows.nth(1)).toHaveAttribute('aria-selected', 'true')
    await expect(box).toHaveAttribute('aria-activedescendant', /old1:7$/)
    await box.press('ArrowUp')
    await expect(rows.nth(0)).toHaveAttribute('aria-selected', 'true')
    await box.press('Enter')
    await expect(page).toHaveURL(/\/app\/chats\/c1\?seq=42$/)
    await expect(menu).toHaveCount(0)
  })

  test('the first Escape clears the search, the second closes the dialog', async ({ page }) => {
    await mockSearch(page, { answer: TWO_HITS })
    const menu = await openDialog(page)
    const box = menu.getByRole('combobox', { name: 'Search conversations...' })
    await box.fill('qdrant')
    await expect(menu.getByTestId('chats-row')).toHaveCount(2)
    await box.press('Escape')
    await expect(box).toHaveValue('')
    await expect(menu).toBeVisible()
    await expect(menu.getByTestId('chats-day-today')).toBeVisible()
    await box.press('Escape')
    await expect(menu).toHaveCount(0)
  })

  test('filters narrow the search and Reset takes them off', async ({ page }) => {
    const { requests } = await mockSearch(page, { answer: TWO_HITS })
    const menu = await openDialog(page)
    await menu.getByRole('combobox', { name: 'Search conversations...' }).fill('qdrant')
    await expect(menu.getByTestId('chats-row')).toHaveCount(2)
    await menu.getByTestId('chats-filter-agent').selectOption('helper')
    await expect.poll(() => requests.at(-1)?.agent).toBe('helper')
    await menu.getByTestId('chats-filter-origin').selectOption('trigger')
    await expect.poll(() => requests.at(-1)?.origin).toBe('trigger')
    await menu.getByTestId('chats-filter-range').selectOption('week')
    await expect.poll(() => Date.now() - Date.parse(requests.at(-1)?.since)).toBeGreaterThan(6.9 * 86400000)
    await menu.getByTestId('chats-filter-range').selectOption('custom')
    await menu.getByTestId('chats-filter-from').fill('2025-01-01')
    await menu.getByTestId('chats-filter-to').fill('2025-01-31')
    await expect.poll(() => requests.at(-1)).toMatchObject({ agent: 'helper', origin: 'trigger', since: '2025-01-01', until: '2025-01-31', q: 'qdrant' })
    await menu.getByTestId('chats-filter-reset').click()
    await expect.poll(() => Object.keys(requests.at(-1)).sort()).toEqual(['limit', 'q'])
    await expect(menu.getByTestId('chats-filter-reset')).toHaveCount(0)
  })

  test('says so when nothing matches, and names the filters when they are on', async ({ page }) => {
    await mockSearch(page, { answer: { hits: [] } })
    const menu = await openDialog(page)
    await menu.getByRole('combobox', { name: 'Search conversations...' }).fill('zzzz')
    await expect(menu.getByTestId('chats-empty')).toHaveText('No chats match “zzzz”.')
    await menu.getByTestId('chats-filter-origin').selectOption('job')
    await expect(menu.getByTestId('chats-empty')).toHaveText('No chats match “zzzz” with these filters.')
  })

  test('shows a loading state, keeps the old hits dimmed, and says when older chats are still being indexed', async ({ page }) => {
    await mockSearch(page, { answer: (p) => ({ ...TWO_HITS, indexing: p.q === 'qdrant', partial: p.q === 'port', searched_chats: 40, total_chats: 120 }), delay: 400 })
    const menu = await openDialog(page)
    const box = menu.getByRole('combobox', { name: 'Search conversations...' })
    await box.fill('qdrant')
    await expect(menu.getByTestId('chats-search-status')).toHaveText('Searching…')
    await expect(menu.getByTestId('chats-loading')).toBeVisible()
    await expect(menu.getByTestId('chats-row')).toHaveCount(2)
    await expect(menu.getByTestId('chats-search-indexing')).toContainText('still being indexed')
    await box.fill('port')
    await expect(menu.getByTestId('chats-row')).toHaveCount(2)
    await expect(menu.locator('.cx-find-list[data-dim]')).toBeVisible()
    await expect(menu.getByTestId('chats-search-partial')).toHaveText('Searched the newest 40 of 120 chats.')
  })

  test('shows an error with Try again', async ({ page }) => {
    let fail = true
    await mockSearch(page, { answer: () => TWO_HITS })
    await page.route(/\/api\/chats\/search(\?.*)?$/, route => (fail ? route.fulfill({ status: 500, json: { error: { message: 'boom' } } }) : route.fallback()), { times: 1 })
    const menu = await openDialog(page)
    await menu.getByRole('combobox', { name: 'Search conversations...' }).fill('qdrant')
    await expect(menu.getByTestId('chats-search-error')).toContainText('The search failed.')
    fail = false
    await menu.getByRole('button', { name: 'Try again' }).click()
    await expect(menu.getByTestId('chats-row')).toHaveCount(2)
  })
})

test.describe('Chat search on the All chats page', () => {
  test('finds a chat that the list does not hold and opens it at the message', async ({ page }) => {
    await mockSearch(page, { answer: TWO_HITS })
    await page.goto('/app/chats/all')
    const list = page.getByTestId('all-chats-list')
    await list.getByRole('combobox', { name: 'Search conversations...' }).fill('qdrant')
    const rows = list.getByTestId('chats-row')
    await expect(rows).toHaveCount(2)
    await rows.nth(1).click()
    await expect(page).toHaveURL(/\/app\/chats\/old1\?seq=7$/)
  })

  test('a title-only hit opens the chat without a message', async ({ page }) => {
    await mockSearch(page, { answer: { hits: [{ chat: RECENT[1], in_title: true }] } })
    await page.goto('/app/chats/all')
    const list = page.getByTestId('all-chats-list')
    await list.getByRole('combobox', { name: 'Search conversations...' }).fill('plan')
    await expect(list.getByTestId('chats-row')).toContainText('Matches the title')
    await list.getByTestId('chats-row').click()
    await expect(page).toHaveURL(/\/app\/chats\/c2$/)
  })

  test('a chat started by a trigger carries the name of the trigger', async ({ page }) => {
    await mockSearch(page, { answer: { hits: [hit(TRIG, 1, 'assistant', 'The digest has the Qdrant news.', [{ start: 18, end: 24 }])] } })
    await page.goto('/app/chats/all')
    const list = page.getByTestId('all-chats-list')
    await list.getByRole('combobox', { name: 'Search conversations...' }).fill('qdrant')
    await expect(list.getByTestId('chats-origin-badge')).toHaveText('Morning digest')
  })
})

test.describe('The link to a message', () => {
  test('scrolls to the message of ?seq and marks it for a moment', async ({ page }) => {
    await mockSearch(page, { answer: { hits: [] } })
    await page.goto('/app/chats/c1?seq=42')
    const msg = page.locator('[data-testid="chat-message"][data-index="42"]')
    await expect(msg).toBeVisible()
    await expect(msg).toHaveAttribute('data-flash', 'on')
    await expect(msg).toBeInViewport()
    await expect(msg).not.toHaveAttribute('data-flash', /.*/, { timeout: 6000 })
    // The first lines are above the fold.
    await expect(page.locator('[data-testid="chat-message"][data-index="1"]')).not.toBeInViewport()
  })

  test('ignores a seq that is not in the chat', async ({ page }) => {
    await mockSearch(page, { answer: { hits: [] } })
    await page.goto('/app/chats/c1?seq=999')
    await expect(page.locator('[data-testid="chat-message"]').first()).toBeVisible()
    await expect(page.locator('[data-flash]')).toHaveCount(0)
  })

  test('a hit in the open chat jumps again on a second click', async ({ page }) => {
    await mockSearch(page, { answer: { hits: [hit(RECENT[0], 42, 'assistant', 'The Qdrant port is 6333.', [{ start: 4, end: 10 }])] } })
    await page.goto('/app/chats/c1?seq=42')
    await expect(page.locator('[data-index="42"]')).toBeInViewport()
    await page.evaluate(() => document.querySelector('[data-testid="chat-message"][data-index="1"]').scrollIntoView())
    await expect(page.locator('[data-index="42"]')).not.toBeInViewport()
    await page.getByTestId('chats-trigger').click()
    await page.getByTestId('chats-menu').getByRole('combobox', { name: 'Search conversations...' }).fill('qdrant')
    await page.getByTestId('chats-menu').getByTestId('chats-row').click()
    await expect(page.locator('[data-index="42"]')).toBeInViewport()
  })
})

test.describe('Chat search on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('fits the screen: filters wrap, rows do not overflow', async ({ page }) => {
    await mockSearch(page, { answer: TWO_HITS })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chats-trigger').click()
    const menu = page.getByTestId('chats-menu')
    await menu.getByRole('combobox', { name: 'Search conversations...' }).fill('qdrant port')
    await expect(menu.getByTestId('chats-row')).toHaveCount(2)
    const box = await menu.boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    const overflow = await menu.evaluate(el => el.scrollWidth - el.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    for (const sel of ['chats-filter-origin', 'chats-filter-range']) {
      const b = await menu.getByTestId(sel).boundingBox()
      expect(b.x + b.width).toBeLessThanOrEqual(390)
    }
  })
})

test.describe('Past chats in the Tools menu', () => {
  const groups = (on) => [
    { group: 'web', tools: ['web_search', 'web_fetch'], off: false, usable: true, provider: 'builtin' },
    { group: 'history', tools: ['search_chats', 'read_chat'], off: !on, opt_in: true, usable: true, provider: 'builtin',
      title: 'Past chats', description: 'The model can search and read your earlier chats.', when_off: 'The model cannot see your earlier chats.' },
  ]

  test('is an opt-in switch that is off until the chat switches it on', async ({ page }) => {
    await mockSearch(page, { answer: { hits: [] } })
    const calls = []
    await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [] } }))
    await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: { addons: [], servers: [], connectors: [], off: [], builtins: groups(false) } }))
    await page.route('**/api/chats/c1/builtins', (route) => {
      const body = route.request().postDataJSON()
      calls.push(body)
      return route.fulfill({ json: { addons: [], servers: [], connectors: [], off: [], builtins: groups((body.on || []).includes('history')) } })
    })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const row = page.getByRole('switch', { name: 'Past chats' })
    await expect(row).not.toBeChecked()
    await expect(page.getByText('Lets the model search and read your earlier chats.')).toBeVisible()
    await row.check()
    await expect.poll(() => calls.at(-1)).toEqual({ off: [], on: ['history'] })
    await expect(row).toBeChecked()
    await row.uncheck()
    await expect.poll(() => calls.at(-1)).toEqual({ off: [], on: [] })
  })
})
