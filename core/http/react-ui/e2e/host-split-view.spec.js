import { test, expect } from './coverage-fixtures.js'

// Host is an inventory, not a catalog, so its split view differs from the two
// galleries in exactly one place: the pane with nothing selected reports what
// is happening rather than offering something to install.

const PANE = '[data-testid="host-pane"]'
const railItems = (page) => page.locator('[data-testid="host-rail-item"]')
const railItem = (page, id) => page.locator(`[data-entity="${id}"]`)

test.describe('Host - split view', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/manage')
    await expect(railItems(page).first()).toBeVisible({ timeout: 10_000 })
  })

  test('the inventory renders no table', async ({ page }) => {
    await expect(page.locator('[data-testid="host"]')).toBeVisible()
    await expect(page.locator('table thead th')).toHaveCount(0)
  })

  test('with nothing selected the pane reports the current state', async ({ page }) => {
    await expect(page.locator(PANE)).toContainText('Right now')
    await expect(page.locator(PANE)).toContainText('Loaded')
    await expect(page.locator('[data-testid="host-back"]')).toHaveCount(0)
  })

  test('choosing a model turns the pane into its detail, and back returns', async ({ page }) => {
    const first = railItems(page).first()
    const name = await first.getAttribute('data-entity')
    await first.click()

    await expect(page.locator(PANE)).toContainText(name)
    await expect(page.locator(PANE)).toContainText('State')
    await expect(page.locator(PANE)).not.toContainText('Right now')

    await page.locator('[data-testid="host-back"]').click()
    await expect(page.locator(PANE)).toContainText('Right now')
  })

  test('the selection lives in the URL', async ({ page }) => {
    const first = railItems(page).first()
    const name = await first.getAttribute('data-entity')
    await first.click()
    await expect(page).toHaveURL(new RegExp(`[?&]sel=${encodeURIComponent(name)}`))
  })

  test('the rail buckets by state rather than by capability', async ({ page }) => {
    // The opposite of the galleries, and deliberately so: nobody opens Host
    // wondering which of their models does vision.
    const groups = page.locator('[data-testid^="host-rail-group-"]')
    await expect(groups.first()).toBeVisible()
    const ids = await groups.evaluateAll(els => els.map(e => e.dataset.testid))
    for (const id of ids) {
      expect(['host-rail-group-running', 'host-rail-group-idle', 'host-rail-group-disabled']).toContain(id)
    }
  })

  test('switching tabs drops a selection that belonged to the other tab', async ({ page }) => {
    await railItems(page).first().click()
    await expect(page.locator('[data-testid="host-back"]')).toBeVisible()

    // The other tab may legitimately be empty on a fresh host, so the contract
    // is that the stale selection is gone, not that a pane appears.
    await page.locator('.tab', { hasText: 'Backends' }).click()
    await expect(page.locator('[data-testid="host-back"]')).toHaveCount(0)
    await expect(page).not.toHaveURL(/[?&]sel=/)
  })
})
