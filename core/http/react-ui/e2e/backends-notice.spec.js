import { test, expect } from './coverage-fixtures.js'
import { UPGRADE_LLAMA, mockOperate } from './operate-fixtures.js'

// An update is said where it can be acted on: in the row's state, in the one
// primary button above the list and on the tab. It is not a banner. A filled
// panel makes every notice shout at the weight of an error, and a coloured
// rail on its edge is decoration that repeats on every item.

test('an update is a row state and a header button, not a banner or a rail', async ({ page }) => {
  await mockOperate(page, { upgrades: UPGRADE_LLAMA })
  await page.goto('/app/backends?view=installed')

  const row = page.locator('[data-entity="llama-cpp"]')
  await expect(row).toContainText('Update 0.9.7')
  await expect(page.getByRole('button', { name: 'Update all (1)' })).toBeVisible()
  await expect(page.locator('.dk-hubtabs [data-hub-tab="runtime"] .dk-hubtab-attn')).toContainText('1')

  // Nothing here is a filled card with an edge.
  await expect(page.locator('.bk-notice')).toHaveCount(0)
  const edges = await page.evaluate(() => [...document.querySelectorAll('.bk-table tr, .op-notice, .bk-recommend, .bk-manual')]
    .map(el => parseFloat(getComputedStyle(el).borderLeftWidth)))
  expect(Math.max(0, ...edges)).toBeLessThanOrEqual(1)
})
