import { test, expect } from './coverage-fixtures.js'

// The split view is meant to scroll inside itself. It is easy to regress into
// scrolling the document instead, because the shell's height rules are floors
// (min-height: 100dvh) rather than ceilings, so any tall pane silently grows
// the whole column and takes the rail with it.
// A description long enough that the detail pane must overflow, which is the
// only condition under which the bug shows.
const LONG = Array.from({ length: 60 }, (_, i) =>
  `Paragraph ${i + 1}. This entry carries a long description so the detail pane has more content than the viewport can hold.`,
).join('\n\n')

const MOCK = {
  models: [
    { name: 'long-model', description: LONG, backend: 'llama-cpp', installed: false, tags: ['llm'] },
    { name: 'short-model', description: 'Short.', backend: 'llama-cpp', installed: false, tags: ['llm'] },
  ],
  allBackends: ['llama-cpp'], allTags: ['llm'],
  availableModels: 2, installedModels: 0, totalPages: 1, currentPage: 1,
}

test.describe('Models Explore - the view scrolls, not the page', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/models*', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify(MOCK) }))
  })

  test('a long detail scrolls the pane and leaves the page height alone', async ({ page }) => {
    await page.setViewportSize({ width: 1400, height: 900 })
    await page.goto('/app/models')
    await expect(page.locator('[data-testid="discover-rail-item"]').first()).toBeVisible({ timeout: 10_000 })

    const pageHeight = () => page.evaluate(() => document.documentElement.scrollHeight)
    const railHeight = () => page.evaluate(
      () => document.querySelector('.entity-rail')?.getBoundingClientRect().height,
    )

    const beforePage = await pageHeight()
    const beforeRail = await railHeight()

    await page.locator('[data-testid="discover-rail-item"]').first().click()
    await expect(page.locator('[data-testid="discover-back"]')).toBeVisible()

    // Selecting something must not make the document taller, and must not
    // stretch the rail to match the pane.
    expect(await pageHeight()).toBe(beforePage)
    // Sub-pixel: layout can settle a fraction differently without the rail
    // having grown. A pixel of tolerance keeps this about the bug it guards.
    expect(Math.abs((await railHeight()) - beforeRail)).toBeLessThan(1)

    // The pane is the thing that scrolls.
    const paneOverflows = await page.evaluate(() => {
      const el = document.querySelector('.split-view__pane')
      return el ? getComputedStyle(el).overflowY : null
    })
    expect(paneOverflows).toBe('auto')
  })

  test('stacked below the breakpoint it scrolls with the document again', async ({ page }) => {
    // Pinning the height when the columns stack would trap both halves in short
    // scrollers, so the constraint is lifted there on purpose.
    await page.setViewportSize({ width: 700, height: 800 })
    await page.goto('/app/models')
    await expect(page.locator('[data-testid="discover-rail-item"]').first()).toBeVisible({ timeout: 10_000 })

    const overflow = await page.evaluate(() => {
      const el = document.querySelector('.split-view__pane')
      return el ? getComputedStyle(el).overflowY : null
    })
    expect(overflow).toBe('visible')
  })
})
