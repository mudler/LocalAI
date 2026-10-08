import { test, expect } from './coverage-fixtures.js'
import { mockLedger, mockPlacement } from './ledger-fixtures.js'
import { mockLibrary } from './library-fixtures.js'

// Selected rows, status strips and empty states keep to the taste guide:
// no coloured left rail on a selectable or card-like element, and an empty
// state is a stacked column, not a row of three things run together.

const leftBorder = (locator) => locator.evaluate(el => parseFloat(getComputedStyle(el).borderLeftWidth))

test.describe('Model editor', () => {
  test('opens on the real config shapes with no error toast, and the current section is not marked by a rail', async ({ page }) => {
    await mockLedger(page)
    await mockPlacement(page)
    await page.goto('/app/model-editor/qwen3-8b-instruct')
    const current = page.locator('.set-rail__item--on')
    await expect(current).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('editor-placement')).toBeVisible()
    expect(await leftBorder(current)).toBe(0)
    // Every section icon is the same quiet colour, whatever the section.
    const colours = await page.locator('.me-section-icon').evaluateAll(els => els.map(el => getComputedStyle(el).color))
    expect(colours.length).toBeGreaterThan(1)
    expect(new Set(colours).size).toBe(1)
    await expect(page.locator('.toast')).toHaveCount(0)
  })

  test('"No fields configured" stacks the icon, the title and the text', async ({ page }) => {
    await mockLedger(page)
    await mockPlacement(page, { configs: { bare: '{}\n' } })
    await page.goto('/app/model-editor/bare')
    const empty = page.getByTestId('editor-no-fields')
    await expect(empty).toBeVisible({ timeout: 15_000 })
    await expect(empty.getByRole('heading', { name: 'No fields configured' })).toBeVisible()
    const icon = await empty.locator('.dk-empty-icon').boundingBox()
    const title = await empty.locator('.dk-empty-title').boundingBox()
    const text = await empty.locator('.dk-empty-text').boundingBox()
    expect(icon.y + icon.height).toBeLessThanOrEqual(title.y + 1)
    expect(title.y + title.height).toBeLessThanOrEqual(text.y + 1)
    await expect(page.locator('.toast')).toHaveCount(0)
  })
})

test('an install in progress is a plain strip with no status rail', async ({ page }) => {
  await mockLedger(page, {
    operations: [{
      id: 'model-a', name: 'model-a', fullName: 'model-a', jobID: 'job-a', progress: 40, taskType: 'installation',
      isDeletion: false, isBackend: false, isQueued: false, cancellable: true,
    }],
  })
  await page.goto('/app/models')
  const strip = page.locator('.operations-strip')
  await expect(strip).toBeVisible({ timeout: 15_000 })
  expect(await leftBorder(strip)).toBe(0)
})

test('the skill editor marks its current section without a left rail', async ({ page }) => {
  await mockLibrary(page)
  await page.goto('/app/skills/new')
  const current = page.locator('.skilledit-sidebar-item.active').first()
  await expect(current).toBeVisible({ timeout: 15_000 })
  expect(await leftBorder(current)).toBe(0)
})
