import { test, expect } from './coverage-fixtures.js'

// Memory page (src/pages/Collections.jsx), against the real test server: no
// collections exist there, so the page shows its empty state.
test.describe('Collections page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/collections')
  })

  test('renders memory with an empty state and create control', async ({ page }) => {
    await expect(page).toHaveURL(/\/app\/collections$/)
    await expect(page.getByRole('heading', { name: 'Memory', exact: true })).toBeVisible()
    await expect(page.getByText(/Memory holds documents an agent can search/i)).toBeVisible()
    await expect(page.getByRole('button', { name: 'New collection' }).first()).toBeVisible()
  })

  test('new-collection name field accepts input', async ({ page }) => {
    await page.getByRole('button', { name: 'New collection' }).first().click()
    const input = page.getByLabel('New collection name')
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
    await page.getByTestId('add-source-toggle').click()
    await page.locator('#source-url').fill('https://example.com/feed')
    await page.locator('#source-interval').fill('3600')
    await page.getByRole('button', { name: 'Add URL' }).click()

    await expect.poll(() => postedBody).toEqual({
      url: 'https://example.com/feed',
      update_interval: 3600,
    })
    expect(typeof postedBody.update_interval).toBe('number')
  })
})
