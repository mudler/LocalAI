import { test, expect } from './coverage-fixtures.js'
import { mockLibrary } from './library-fixtures.js'

// The Simulate a message sheet. It runs only what the API can run: a search on
// a collection, and the skills an agent has on. It never shows a model answer.

test.describe('Simulate a message', () => {
  test('a collection on its own returns real passages with scores and saves nothing', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/collections')
    await page.getByTestId('open-simulate').click()
    const sheet = page.getByTestId('simulate-sheet')
    await expect(sheet.getByRole('heading', { name: 'Simulate a message' })).toBeVisible()
    await expect(sheet).toContainText('Nothing is sent to a model')
    await expect(sheet).toContainText('no answer is simulated')
    await sheet.getByLabel('Message').fill('When can I work remotely?')
    await sheet.getByTestId('simulate-run').click()
    const groups = sheet.getByTestId('simulate-passages-handbook')
    await expect(groups).toContainText('3 passages, ranked by similarity')
    await expect(groups.locator('.lib-passage').first()).toContainText('0.74')
    expect(seen.searches[0]).toEqual({ name: 'handbook', query: 'When can I work remotely?', max_results: 5 })
    expect(seen.saves).toEqual([])
    // No agent was chosen, so there is no budget to show.
    await expect(sheet.getByTestId('simulate-total')).toHaveCount(0)
  })

  test('an agent shows its skills, an estimated budget and the passages of the collections on its list', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/collections')
    await page.getByTestId('open-simulate').click()
    const sheet = page.getByTestId('simulate-sheet')
    await sheet.getByLabel('Context').selectOption('handbook')
    await sheet.getByLabel('Message').fill('When can I work remotely?')
    await sheet.getByTestId('simulate-run').click()
    // A skill adds nothing up front: the model reads it when a task matches.
    await expect(sheet.getByTestId('simulate-skill-summarise-pdf')).toContainText('0 up front')
    await expect(sheet).toContainText('Read only when the model asks for one')
    expect(seen.searches[0]).toEqual({ name: 'handbook', query: 'When can I work remotely?', max_results: 5 })
    await expect(sheet.getByTestId('simulate-passages-handbook').locator('.lib-passage')).toHaveCount(3)
    await expect(sheet.getByTestId('simulate-total')).toContainText('About')
    await expect(sheet).toContainText('Estimates: characters divided by four')
  })

  test('an agent whose list has no collection searches nothing', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/collections')
    await page.getByTestId('open-simulate').click()
    const sheet = page.getByTestId('simulate-sheet')
    await sheet.getByLabel('Context').selectOption('support-desk')
    await sheet.getByLabel('Message').fill('Where is my order?')
    await sheet.getByTestId('simulate-run').click()
    await expect(sheet.getByTestId('simulate-skill-triage-issue')).toContainText('0 up front')
    expect(seen.searches).toEqual([])
  })

  test('an unused collection can be searched for this test inside an agent context', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/collections/release-notes')
    await page.getByTestId('collection-try').click()
    const sheet = page.getByTestId('simulate-sheet')
    await sheet.getByLabel('Context').selectOption('research-assistant')
    await sheet.getByLabel('Message').fill('What changed?')
    await sheet.getByTestId('simulate-run').click()
    await expect(sheet.getByTestId('simulate-passages-release-notes')).toContainText('only for this test')
    expect(seen.searches.map(s => s.name)).toEqual(['release-notes'])
    expect(seen.saves).toEqual([])
  })

  test('running without a message asks for one, and a failed search says why', async ({ page }) => {
    await mockLibrary(page)
    await page.route('**/api/collections/handbook/search', route => route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'index offline' }) }))
    await page.goto('/app/collections')
    await page.getByTestId('open-simulate').click()
    const sheet = page.getByTestId('simulate-sheet')
    await sheet.getByTestId('simulate-run').click()
    await expect(sheet.getByRole('alert')).toContainText('Type a message first')
    await sheet.getByLabel('Message').fill('hello')
    await sheet.getByTestId('simulate-run').click()
    await expect(sheet.getByRole('alert')).toContainText('The search failed')
  })

  test('on a phone the sheet is a bottom sheet that closes with Escape', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockLibrary(page)
    await page.goto('/app/collections')
    await page.getByTestId('open-simulate').click()
    const sheet = page.getByTestId('simulate-sheet')
    const box = await sheet.boundingBox()
    expect(box.width).toBeLessThanOrEqual(390)
    expect(box.y + box.height).toBeGreaterThan(800)
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
  })
})
