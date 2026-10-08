import { test, expect } from './coverage-fixtures.js'
import { mockAccess } from './access-fixtures.js'

test.describe('Account', () => {
  let state
  test.beforeEach(async ({ page }) => {
    state = await mockAccess(page)
    await page.goto('/app/account')
    await expect(page.getByTestId('account-profile')).toBeVisible()
  })

  test('has four tabs, opens on the profile and keeps the tab in the address', async ({ page }) => {
    const tabs = await page.getByRole('tab').allTextContents()
    expect(tabs).toEqual(['Profile', 'Security', 'API keys', 'Usage'])
    await expect(page.getByRole('tab', { name: 'Profile' })).toHaveAttribute('aria-selected', 'true')
    await page.getByRole('tab', { name: 'Usage' }).click()
    await expect(page).toHaveURL(/tab=usage/)
    await page.goBack().catch(() => {})
    await page.goto('/app/account?tab=keys')
    await expect(page.getByTestId('api-keys-panel')).toBeVisible()
  })

  test('profile saves the name and the picture address', async ({ page }) => {
    const save = page.getByRole('button', { name: 'Save' })
    await expect(save).toBeDisabled()
    await page.getByLabel('Display name').fill('Alice L')
    await page.getByLabel('Avatar URL').fill('https://example.org/a.png')
    await expect(save).toBeEnabled()
    await save.click()
    await expect(page.getByText('Profile updated')).toBeVisible()
    expect(state.writes[0]).toMatchObject({ method: 'PUT', path: '/api/auth/profile', body: { name: 'Alice L', avatar_url: 'https://example.org/a.png' } })
    await expect(page.getByTestId('account-profile')).toContainText('alice@lab.example')
    await expect(page.getByTestId('account-profile')).toContainText('admin · local sign-in')
  })

  test('security changes a local password and checks the confirmation', async ({ page }) => {
    await page.getByRole('tab', { name: 'Security' }).click()
    const form = page.getByTestId('account-security')
    await form.getByLabel('Current password').fill('old-password-123')
    await form.getByLabel('New password').fill('new-password-1234')
    await form.getByLabel('Confirm password').fill('different-1234')
    await form.getByRole('button', { name: 'Change password' }).click()
    await expect(page.getByText('Passwords do not match')).toBeVisible()
    expect(state.writes).toEqual([])
    await form.getByLabel('Confirm password').fill('new-password-1234')
    await form.getByRole('button', { name: 'Change password' }).click()
    await expect(page.getByText('Password changed')).toBeVisible()
    expect(state.writes[0].body).toEqual({ current_password: 'old-password-123', new_password: 'new-password-1234', acknowledge_weak_password: false })
    await expect(form.getByLabel('Current password')).toHaveValue('')
  })

  test('a weak password can be sent anyway after the person says so', async ({ page }) => {
    state.failNext = { error: 'Password is weak', overridable: true }
    await page.getByRole('tab', { name: 'Security' }).click()
    const form = page.getByTestId('account-security')
    await form.getByLabel('Current password').fill('old-password-123')
    await form.getByLabel('New password').fill('password1234')
    await form.getByLabel('Confirm password').fill('password1234')
    await form.getByRole('button', { name: 'Change password' }).click()
    await expect(form.getByRole('alert')).toContainText('Password is weak')
    await form.getByLabel('Use this password anyway').check()
    await form.getByRole('button', { name: 'Change password' }).click()
    await expect(page.getByText('Password changed')).toBeVisible()
    expect(state.writes[1].body.acknowledge_weak_password).toBe(true)
  })

  test('usage shows the last 30 days, tokens by model and the limits an admin set', async ({ page }) => {
    await page.getByRole('tab', { name: 'Usage' }).click()
    const usage = page.getByTestId('account-usage')
    await expect(usage.getByTestId('usage-requests')).toHaveText('275')
    await expect(usage).toContainText('Tokens in')
    await expect(usage.getByRole('list', { name: 'Tokens by model' }).locator('li')).toHaveCount(4)
    await expect(usage.getByRole('list', { name: 'Tokens by model' }).locator('li').first()).toContainText('qwen3-8b-instruct')
    const limits = page.getByTestId('account-limits').locator('li')
    await expect(limits).toHaveCount(3)
    await expect(limits.first()).toContainText('All models, tokens per day')
    await expect(limits.first()).toContainText('1.2M of 2M')
    await expect(limits.nth(1)).toContainText('requests per hour')
    await expect(limits.nth(2).getByRole('progressbar')).toHaveAttribute('aria-valuenow', '94')
    await expect(usage.getByRole('link', { name: 'Open full usage' })).toHaveAttribute('href', '/app/usage')
  })
})

test.describe('Account: other people and states', () => {
  test('a person who signs in with GitHub is told the password is not theirs to change', async ({ page }) => {
    await mockAccess(page, { status: 'oauthMember' })
    await page.goto('/app/account?tab=security')
    await expect(page.getByTestId('account-oauth-only')).toContainText('not available for github accounts')
    await expect(page.getByLabel('Current password')).toHaveCount(0)
  })

  test('a person who is not an admin sees their own account, usage and limits', async ({ page }) => {
    await mockAccess(page, { status: 'member' })
    await page.goto('/app/account?tab=usage')
    await expect(page.getByTestId('account-usage')).toBeVisible()
    await expect(page.getByTestId('account-page')).toContainText('bob · user')
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' })).toHaveCount(0)
  })

  test('without authentication there is no account', async ({ page }) => {
    await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: false, staticApiKeyRequired: false, providers: [] } }))
    await page.goto('/app/account')
    await expect(page.getByText('Account unavailable')).toBeVisible()
  })

  test('the usage tab says so when the call fails', async ({ page }) => {
    await mockAccess(page)
    await page.route('**/api/auth/usage?*', route => route.fulfill({ status: 500, json: { error: 'boom' } }))
    await page.goto('/app/account?tab=usage')
    await expect(page.getByRole('alert')).toContainText('Failed to load your usage')
  })

  test('a person with no limits and no requests sees calm empty states', async ({ page }) => {
    await mockAccess(page, { quotas: [] })
    await page.route('**/api/auth/usage?*', route => route.fulfill({ json: { usage: [], totals: {} } }))
    await page.goto('/app/account?tab=usage')
    await expect(page.getByText('No requests from you in the last 30 days.')).toBeVisible()
    await expect(page.getByText('No limits are set on your account.')).toBeVisible()
  })
})

test.describe('Account: phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('every tab fits the screen', async ({ page }) => {
    await mockAccess(page)
    for (const tab of ['profile', 'security', 'keys', 'usage']) {
      await page.goto(`/app/account?tab=${tab}`)
      await expect(page.getByRole('tabpanel')).toBeVisible()
      await page.waitForTimeout(150)
      expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), tab).toBe(false)
    }
  })
})
