import { test, expect } from './coverage-fixtures.js'

// Collections (Knowledge Base) feature page (src/pages/Collections.jsx).
test.describe('Collections page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/collections')
  })

  test('renders the knowledge base with an empty state and create control', async ({ page }) => {
    await expect(page).toHaveURL(/\/app\/collections$/)
    await expect(page.getByRole('heading', { name: 'Knowledge Base' })).toBeVisible()
    await expect(page.getByText(/No collections yet/i)).toBeVisible()
    await expect(page.locator('button.btn-primary').filter({ hasText: 'Create' })).toBeVisible()
  })

  test('new-collection name field accepts input', async ({ page }) => {
    const input = page.locator('input, textarea').first()
    await expect(input).toBeVisible()
    await input.fill('my-kb')
    await expect(input).toHaveValue('my-kb')
  })

  test('posts the source update interval as a JSON number', async ({ page }) => {
    const collectionName = 'interval-regression'
    const collectionPath = encodeURIComponent(collectionName)
    let postedBody

    await page.route(`**/api/agents/collections/${collectionPath}/entries`, route =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ entries: [] }) }))
    await page.route(`**/api/agents/collections/${collectionPath}/sources`, async route => {
      if (route.request().method() === 'POST') {
        postedBody = route.request().postDataJSON()
        await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ status: 'ok' }) })
      } else {
        await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ sources: [] }) })
      }
    })

    await page.goto(`/app/collections/${collectionPath}`)
    await page.getByRole('button', { name: 'Sources' }).click()
    await page.locator('#source-url').fill('https://example.com/feed')
    await page.locator('#source-interval').fill('3600')
    await page.getByRole('button', { name: 'Add Source' }).click()

    await expect.poll(() => postedBody).toEqual({
      url: 'https://example.com/feed',
      update_interval: 3600,
    })
    expect(typeof postedBody.update_interval).toBe('number')
  })
})
