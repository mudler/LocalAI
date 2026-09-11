import { test, expect } from './coverage-fixtures.js'

// Searching triggers a refetch. The search box lives in the rail column, so if
// a refetch unmounts the view it takes the field you are typing into with it,
// dropping focus and the caret. That is what this guards.
const MOCK = {
  models: [
    { name: 'alpha-model', description: 'a', backend: 'llama-cpp', installed: false, tags: ['llm'] },
    { name: 'beta-model', description: 'b', backend: 'llama-cpp', installed: false, tags: ['llm'] },
  ],
  allBackends: ['llama-cpp'], allTags: ['llm'],
  availableModels: 2, installedModels: 0, totalPages: 1, currentPage: 1,
}

test.describe('Models Explore - searching keeps the view', () => {
  test('a refetch keeps the search box, its focus and its value', async ({ page }) => {
    let calls = 0
    await page.route('**/api/models*', async (route) => {
      calls += 1
      // Slow the refetch so the loading window is real and observable.
      if (calls > 1) await new Promise((r) => setTimeout(r, 600))
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(MOCK) })
    })

    await page.goto('/app/models')
    const search = page.locator('.filter-bar-group__search input')
    await expect(search).toBeVisible({ timeout: 10_000 })

    await search.click()
    await search.fill('alpha')

    // Mid-refetch: the field is still mounted, still focused, still holding
    // what was typed, and the rail is marked busy rather than replaced.
    await expect(search).toBeFocused()
    await expect(search).toHaveValue('alpha')
    await expect(page.locator('.entity-rail')).toBeVisible()

    await page.waitForTimeout(900)
    await expect(search).toBeFocused()
    await expect(search).toHaveValue('alpha')
  })

  test('the first load still shows a skeleton, not an empty shell', async ({ page }) => {
    // Nothing to keep on a cold start, so the skeleton is still right there.
    await page.route('**/api/models*', async (route) => {
      await new Promise((r) => setTimeout(r, 800))
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(MOCK) })
    })
    await page.goto('/app/models')
    await expect(page.getByTestId('gallery-loader')).toBeVisible({ timeout: 5_000 })
  })
})
