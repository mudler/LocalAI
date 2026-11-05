import { test, expect } from './coverage-fixtures.js'

// Knowledge page (src/pages/Collections.jsx) on the real test server. The collections
// service of the test server never finishes starting, so the list answers 503;
// the empty-state cases stub an empty list, and one case checks that a 503 is
// shown as "starting", not as an empty library.
test.describe('Collections page', () => {
  test.beforeEach(async ({ page }, info) => {
    if (!info.title.includes('starting')) {
      await page.route(/\/api\/collections(\?.*)?$/, route => route.fulfill({ contentType: 'application/json', body: JSON.stringify({ collections: [] }) }))
    }
    await page.goto('/app/collections')
  })

  test('a knowledge service that is still starting is not shown as an empty library', async ({ page }) => {
    await page.route(/\/api\/collections(\?.*)?$/, route => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'collections are starting, please retry shortly' }) }))
    await page.goto('/app/collections')
    await expect(page.getByTestId('memory-starting')).toContainText('Knowledge is starting.')
    await expect(page.getByTestId('memory-empty')).toHaveCount(0)
    await expect(page.getByTestId('memory-starting').getByRole('button', { name: 'Try again' })).toBeVisible()
  })

  test('renders knowledge with an empty state and create control', async ({ page }) => {
    await expect(page).toHaveURL(/\/app\/collections$/)
    await expect(page.getByRole('heading', { name: 'Knowledge', exact: true })).toBeVisible()
    await expect(page.getByText(/Knowledge holds documents an agent can search/i)).toBeVisible()
    await expect(page.getByRole('button', { name: 'New collection' }).first()).toBeVisible()
  })

  test('says how a search finds passages, in plain words', async ({ page }) => {
    const list = mode => route => route.fulfill({ contentType: 'application/json', body: JSON.stringify({ collections: ['docs'], search_mode: mode, embedding_model_installed: true }) })
    await page.route(/\/api\/collections(\?.*)?$/, list('semantic'))
    await page.goto('/app/collections')
    await expect(page.getByTestId('search-mode')).toContainText('Search: semantic.')
    await expect(page.getByTestId('search-mode')).toContainText('by meaning, not by exact words or codes')

    await page.unroute(/\/api\/collections(\?.*)?$/)
    await page.route(/\/api\/collections(\?.*)?$/, list('hybrid'))
    await page.goto('/app/collections')
    await expect(page.getByTestId('search-mode')).toContainText('Search: hybrid.')
    await expect(page.getByTestId('search-mode')).toContainText('by meaning and by exact words')

    await page.setViewportSize({ width: 360, height: 740 })
    await expect(page.getByTestId('search-mode')).toBeVisible()
    // The page animates in; wait for the layout to settle before measuring.
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0)
  })

  test('says nothing about the search mode when the server does not report it', async ({ page }) => {
    await expect(page.getByTestId('memory-empty')).toBeVisible()
    await expect(page.getByTestId('search-mode')).toHaveCount(0)
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

    await page.route(`**/api/collections/${collectionPath}/entries`, route =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ entries: [] }) }))
    await page.route(`**/api/collections/${collectionPath}/sources`, async route => {
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
