import { test, expect } from './coverage-fixtures.js'
import { mockOperate } from './operate-fixtures.js'
import { mockTraffic } from './traffic-fixtures.js'

// The one template of an Administration page (breadcrumb, title, one primary
// action, content) and the one filter bar (search, chips, a Filters popover,
// a sort at the right), as the Activity, Users and Usage pages use them.

const ADMIN = { id: 'a', name: 'Admin', email: 'admin@example.com', role: 'admin', provider: 'local', status: 'active' }
const USERS = [
  ADMIN,
  { id: 'u2', name: 'Chen Wei', email: 'chen@example.com', role: 'user', status: 'active' },
  { id: 'u3', name: 'Eli Katz', email: 'eli@example.com', role: 'user', status: 'pending' },
  { id: 'u4', name: 'Ivo Marin', email: 'ivo@example.com', role: 'user', status: 'disabled' },
]

async function signIn(page) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user: ADMIN, registrationMode: 'approval' },
  }))
  await page.route('**/api/auth/admin/users', route => route.fulfill({ json: USERS }))
}

test.describe('Admin page template', () => {
  test('Activity: one h1, the breadcrumb in the header, the filter chips in the bar', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/activity')
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
    const header = page.locator('.page-header')
    await expect(header.getByTestId('admin-crumbs')).toContainText('Models and runtime')
    // The layout draws no second breadcrumb.
    await expect(page.getByTestId('admin-crumbs')).toHaveCount(1)
    await expect(page.getByTestId('admin-filters')).toBeVisible()
    const chips = page.getByRole('group', { name: 'Filter operations' })
    await expect(chips.getByRole('button', { name: 'All' })).toHaveAttribute('aria-pressed', 'true')
    await chips.getByRole('button', { name: 'Backends' }).click()
    await expect(chips.getByRole('button', { name: 'Backends' })).toHaveAttribute('aria-pressed', 'true')
  })

  test('Users: Invite someone is the one primary action of the header', async ({ page }) => {
    await signIn(page)
    await mockOperate(page)
    await page.goto('/app/users')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Users')
    const header = page.locator('.page-header')
    await expect(header.getByTestId('admin-crumbs')).toContainText('Access')
    await expect(page.locator('.page-header .dk-btn--primary')).toHaveCount(1)
    await header.getByRole('button', { name: 'Invite someone' }).click()
    await expect(page.getByTestId('invite-sheet')).toBeVisible()
    await expect(page).toHaveURL(/\/app\/users$/)
  })

  test('Users: search, chips with counts and the sort sit in the filter bar', async ({ page }) => {
    await signIn(page)
    await mockOperate(page)
    await page.goto('/app/users')
    const bar = page.getByTestId('admin-filters')
    await expect(bar).toBeVisible()
    const rows = page.locator('[data-testid="users-table"] tbody tr[data-row]')
    await expect(rows).toHaveCount(4)
    await bar.getByRole('button', { name: /Pending/ }).click()
    await expect(rows).toHaveCount(1)
    await expect(rows.first()).toContainText('Eli Katz')
    await bar.getByRole('button', { name: /^All/ }).click()
    await bar.getByRole('searchbox', { name: 'Search by name or email' }).fill('chen')
    await expect(rows).toHaveCount(1)
    await bar.getByRole('searchbox').fill('')
    await bar.getByLabel('Sort').selectOption('role')
    await expect(rows.first()).toContainText('Admin')
  })

  test('Usage: the group-by chips, the Filters popover for the model and the title in the header', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/usage')
    await expect(page.getByRole('heading', { level: 1 })).toContainText('Usage')
    await expect(page.locator('.page-header').getByTestId('admin-crumbs')).toContainText('Traffic')
    const bar = page.getByTestId('admin-filters')
    await expect(bar.getByRole('radio', { name: 'Model' })).toHaveAttribute('aria-checked', 'true')
    const button = bar.getByTestId('admin-filters-button')
    await expect(button).toHaveAttribute('aria-expanded', 'false')
    await button.click()
    await expect(button).toHaveAttribute('aria-expanded', 'true')
    const panel = page.getByTestId('admin-filters-panel')
    await expect(panel.getByRole('combobox')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(panel).toHaveCount(0)
    await expect(button).toBeFocused()
  })

  test('Usage: choosing a model in the popover counts on the Filters button', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/usage')
    const button = page.getByTestId('admin-filters-button')
    await button.click()
    const select = page.getByTestId('admin-filters-panel').getByRole('combobox')
    const option = await select.locator('option').nth(1).getAttribute('value')
    await select.selectOption(option)
    await expect(button).toContainText('1')
  })

  test.describe('phone', () => {
    test.use({ viewport: { width: 390, height: 844 } })

    for (const path of ['/app/activity', '/app/usage', '/app/users']) {
      test(`${path} keeps the filter bar and the header inside the screen`, async ({ page }) => {
        await mockTraffic(page)
        await mockOperate(page)
        await signIn(page)
        await page.goto(path)
        await expect(page.getByTestId('admin-filters')).toBeVisible()
        const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
        expect(over).toBeLessThanOrEqual(0)
        const box = await page.getByTestId('admin-filters').boundingBox()
        expect(box.x + box.width).toBeLessThanOrEqual(390)
      })
    }
  })
})
