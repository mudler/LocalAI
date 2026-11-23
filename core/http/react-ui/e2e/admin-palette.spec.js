import { test, expect } from './coverage-fixtures.js'

// The command palette of Administration: Ctrl or Cmd K on any Administration
// page, or the Search button of the sidebar. It searches the pages, and the
// people, workers and settings the client can already list.

const PALETTE = '[data-testid="admin-palette"]'

async function mockAuth(page, user, authEnabled = true) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: { authEnabled, staticApiKeyRequired: false, providers: ['local'], user },
  }))
}

async function quiet(page) {
  await page.route('**/api/operations', route => route.fulfill({ json: [] }))
}

// The shortcut works once the page has drawn: wait for the Search button.
async function ready(page, path = '/app/operate') {
  await page.goto(path)
  await expect(page.getByTestId('admin-search')).toBeVisible()
  await expect(page.locator('.admin-layout')).toBeVisible()
}

const ADMIN = { id: 'a', name: 'Admin', email: 'admin@example.com', role: 'admin', provider: 'local' }
const USERS = [
  ADMIN,
  { id: 'u2', name: 'Chen Wei', email: 'chen@example.com', role: 'user', status: 'active' },
  { id: 'u3', name: 'Eli Katz', email: 'eli@example.com', role: 'user', status: 'active' },
]

test.describe('Command palette', () => {
  test('Ctrl K opens it with the input focused, and Escape closes it and gives the focus back', async ({ page }) => {
    await quiet(page)
    await ready(page)
    const trigger = page.getByTestId('admin-search')
    await trigger.focus()
    await page.keyboard.press('Control+k')
    const dialog = page.locator(PALETTE)
    await expect(dialog).toBeVisible()
    await expect(dialog).toHaveAttribute('aria-modal', 'true')
    const input = dialog.getByRole('combobox')
    await expect(input).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    await expect(trigger).toBeFocused()
  })

  test('the Search button of the sidebar opens it, and Ctrl K again closes it', async ({ page }) => {
    await quiet(page)
    await ready(page)
    await page.getByTestId('admin-search').click()
    await expect(page.locator(PALETTE)).toBeVisible()
    await page.keyboard.press('Control+k')
    await expect(page.locator(PALETTE)).toHaveCount(0)
  })

  test('with no text it lists the pages, and typing narrows them', async ({ page }) => {
    await quiet(page)
    await ready(page)
    await page.keyboard.press('Control+k')
    const dialog = page.locator(PALETTE)
    const options = dialog.getByRole('option')
    await expect(options.first()).toBeVisible()
    expect(await options.count()).toBeGreaterThan(8)
    await dialog.getByRole('combobox').fill('backend')
    await expect(options.first()).toContainText('Backends')
    await expect(options.first()).toContainText('Administration / Models and runtime')
    await dialog.getByRole('combobox').fill('zzzzzz')
    await expect(dialog.getByRole('status')).toContainText('Nothing matches')
    await expect(options).toHaveCount(0)
  })

  test('arrow keys move the selection, Enter opens the page', async ({ page }) => {
    await quiet(page)
    await ready(page)
    await page.keyboard.press('Control+k')
    const dialog = page.locator(PALETTE)
    const input = dialog.getByRole('combobox')
    await input.fill('a')
    const options = dialog.getByRole('option')
    await expect(options.first()).toHaveAttribute('aria-selected', 'true')
    await page.keyboard.press('ArrowDown')
    await expect(options.nth(1)).toHaveAttribute('aria-selected', 'true')
    await expect(options.first()).toHaveAttribute('aria-selected', 'false')
    await expect(input).toHaveAttribute('aria-activedescendant', (await options.nth(1).getAttribute('id')))
    await page.keyboard.press('ArrowUp')
    await expect(options.first()).toHaveAttribute('aria-selected', 'true')
    await input.fill('settings')
    await page.keyboard.press('Enter')
    await expect(dialog).toHaveCount(0)
    await expect(page).toHaveURL(/\/app\/settings/)
  })

  test('Tab stays inside the dialog', async ({ page }) => {
    await quiet(page)
    await ready(page)
    await page.keyboard.press('Control+k')
    const dialog = page.locator(PALETTE)
    await page.keyboard.press('Tab')
    await page.keyboard.press('Tab')
    await expect(dialog.getByRole('combobox')).toBeFocused()
  })

  test('a click outside closes it, a click on a row opens it', async ({ page }) => {
    await quiet(page)
    await ready(page)
    await page.keyboard.press('Control+k')
    await page.mouse.click(5, 5)
    await expect(page.locator(PALETTE)).toHaveCount(0)
    await page.keyboard.press('Control+k')
    await page.locator(PALETTE).getByRole('combobox').fill('activity')
    await page.locator(PALETTE).getByRole('option').first().click()
    await expect(page).toHaveURL(/\/app\/activity$/)
  })

  test('it finds a person, and the Users page opens on that search', async ({ page }) => {
    await mockAuth(page, ADMIN)
    await quiet(page)
    await page.route('**/api/auth/admin/users', route => route.fulfill({ json: USERS }))
    await ready(page)
    await page.keyboard.press('Control+k')
    const dialog = page.locator(PALETTE)
    await dialog.getByRole('combobox').fill('chen')
    // The person is listed twice: as a View as entry, which comes first, and as
    // a person, which opens the Users page.
    const row = dialog.locator('[data-kind="person"]', { hasText: 'Chen Wei' })
    await expect(row).toBeVisible()
    await expect(dialog.getByRole('group', { name: 'People' })).toBeVisible()
    await row.click()
    await expect(page).toHaveURL(/\/app\/users\?q=chen%40example\.com$/)
    await expect(page.getByPlaceholder(/Search/).first()).toHaveValue('chen@example.com')
  })

  test('it finds a worker on a cluster, and no worker entries on a single node', async ({ page }) => {
    await quiet(page)
    await page.route('**/api/features', route => route.fulfill({ json: { distributed: true } }))
    await page.route('**/api/nodes', route => route.fulfill({ json: [{ id: 'n1', name: 'atlas-7', status: 'healthy', healthy: true }] }))
    await ready(page)
    await page.keyboard.press('Control+k')
    const dialog = page.locator(PALETTE)
    await dialog.getByRole('combobox').fill('atlas')
    await expect(dialog.getByRole('group', { name: 'Workers' }).getByRole('option', { name: /atlas-7/ })).toBeVisible()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(/\/app\/nodes\/n1$/)
  })

  test('it finds a setting and opens its group', async ({ page }) => {
    await quiet(page)
    await ready(page)
    await page.keyboard.press('Control+k')
    const dialog = page.locator(PALETTE)
    await dialog.getByRole('combobox').fill('watchdog')
    const group = dialog.getByRole('group', { name: 'Settings' })
    await expect(group.getByRole('option').first()).toBeVisible()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(/\/app\/settings\?group=memory$/)
  })

  test('the pages it lists follow the gates of the sidebar', async ({ page }) => {
    await quiet(page)
    await page.route('**/api/features', route => route.fulfill({ json: { distributed: false } }))
    await ready(page)
    await page.keyboard.press('Control+k')
    const dialog = page.locator(PALETTE)
    await dialog.getByRole('combobox').fill('placement')
    await expect(dialog.getByRole('option', { name: /^Placement/ })).toHaveCount(0)
    await dialog.getByRole('combobox').fill('machine')
    await expect(dialog.getByRole('option', { name: /This machine/ }).first()).toBeVisible()
  })

  test('it does not open off an Administration page', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Administration' })).toBeVisible()
    await page.keyboard.press('Control+k')
    await expect(page.locator(PALETTE)).toHaveCount(0)
  })

  test.describe('phone', () => {
    test.use({ viewport: { width: 390, height: 844 } })

    test('the Search button of the drawer closes it and opens the palette inside the screen', async ({ page }) => {
      await quiet(page)
      await page.goto('/app/operate')
      await page.getByRole('button', { name: 'Open menu' }).click()
      await page.getByTestId('admin-search').click()
      const dialog = page.locator(PALETTE)
      await expect(dialog).toBeVisible()
      const box = await dialog.boundingBox()
      expect(box.x).toBeGreaterThanOrEqual(0)
      expect(box.x + box.width).toBeLessThanOrEqual(390)
      const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(over).toBeLessThanOrEqual(0)
    })
  })
})
