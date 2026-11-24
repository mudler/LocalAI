import { test, expect } from './coverage-fixtures.js'
import { adminItem } from './admin-nav.js'
import { UPGRADE_LLAMA, mockOperate } from './operate-fixtures.js'

// An update is said where it can be acted on: in the row's state, in the
// update line above the list and on the tab. It is not a banner. A filled
// panel makes every notice shout at the weight of an error, and a coloured
// rail on its edge is decoration that repeats on every item.

test('an update is a row state and a line above the list, not a banner or a rail', async ({ page }) => {
  await mockOperate(page, { upgrades: UPGRADE_LLAMA })
  await page.goto('/app/backends?view=installed')

  const row = page.locator('[data-entity="llama-cpp"]')
  await expect(row).toContainText('Update 0.9.7')
  const updates = page.getByTestId('backends-updates')
  await expect(updates).toContainText('1 update available')
  await expect(updates.getByRole('link', { name: 'Review' })).toBeVisible()
  await expect(adminItem(page, '/app/updates').getByTestId('admin-signal-updates')).toContainText('1')

  // Nothing here is a filled card with an edge.
  await expect(page.locator('.bk-notice')).toHaveCount(0)
  const edges = await page.evaluate(() => [...document.querySelectorAll('.bk-table tr, .op-notice, .bk-recommend, .bk-manual')]
    .map(el => parseFloat(getComputedStyle(el).borderLeftWidth)))
  expect(Math.max(0, ...edges)).toBeLessThanOrEqual(1)
})

test('a failed read of the updates is said on the page, not shown as no updates', async ({ page }) => {
  await mockOperate(page, { upgrades: UPGRADE_LLAMA })
  // Registered after the fixture: the last route wins.
  let refused = true
  await page.route('**/api/backends/updates', route => (refused
    ? route.fulfill({ status: 401, json: { error: { message: 'Authentication required' } } })
    : route.fallback()))
  await page.goto('/app/backends?view=installed')

  const notice = page.getByTestId('backends-updates-unreadable')
  await expect(notice).toContainText('Could not load the list of updates')
  await expect(page.getByTestId('backends-updates')).toHaveCount(0)

  refused = false
  await notice.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByTestId('backends-updates')).toContainText('1 update available')
  await expect(notice).toHaveCount(0)
})
