import { test, expect } from './coverage-fixtures.js'
import { installCoordinators, ADMIN_LIST, auditRows } from './coordinator-fixtures.js'
import { statusFixture } from './access-fixtures.js'

// The administrator's section of Chats of all users: every coordinator, its
// state and usage, pause with a confirm, resume, and the audit log across all
// of them. The API is stubbed.

const rows = (page) => page.getByTestId('admin-coordinator-row')
const adminAudit = (n = 60) => auditRows(n).map((r, i) => ({ ...r, coordinator_id: i % 2 ? 'k2' : 'k1', owner: i % 2 ? 'bob' : 'alice' }))

test.describe('Admin: coordinators', () => {
  test('lists the coordinators of several owners with state and usage', async ({ page }) => {
    await installCoordinators(page, { admin: { list: ADMIN_LIST } })
    await page.goto('/app/chats-admin')
    const section = page.getByTestId('admin-coordinators')
    await expect(section.getByRole('heading', { name: 'Coordinators (3)' })).toBeVisible()
    await expect(rows(page)).toHaveCount(3)
    await expect(rows(page).nth(0)).toContainText('alice')
    await expect(rows(page).nth(0)).toContainText('daily-ops')
    await expect(rows(page).nth(0).getByTestId('admin-coordinator-state')).toHaveText('Scheduled')
    await expect(rows(page).nth(0)).toContainText('3 of 48 runs')
    await expect(rows(page).nth(0)).toContainText('5 of 100 messages')
    await expect(rows(page).nth(1).getByTestId('admin-coordinator-state')).toHaveText('Paused')
    await expect(rows(page).nth(1)).toContainText('by alice')
    await expect(rows(page).nth(2).getByTestId('admin-coordinator-state')).toHaveText('Idle')
    // The other sections still load.
    await expect(page.getByTestId('admin-agents')).toBeVisible()
  })

  test('pauses one after a confirm that says what it does, and cancel changes nothing', async ({ page }) => {
    const s = await installCoordinators(page, { admin: { list: ADMIN_LIST } })
    await page.goto('/app/chats-admin')
    await rows(page).nth(0).getByRole('button', { name: 'Pause daily-ops' }).click()
    const dialog = page.getByTestId('coordinator-pause-dialog')
    await expect(dialog).toContainText('Pause "daily-ops"?')
    await expect(dialog).toContainText('stops everything this coordinator does')
    await expect(dialog).toContainText('wait and are not lost')
    // Focus starts on Cancel, so Enter never pauses by accident.
    await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    expect(s.calls.filter(c => c.url.includes('/pause'))).toHaveLength(0)
    await expect(rows(page).nth(0).getByRole('button', { name: 'Pause daily-ops' })).toBeFocused()

    await rows(page).nth(0).getByRole('button', { name: 'Pause daily-ops' }).click()
    await page.getByTestId('coordinator-pause-confirm').click()
    await expect(dialog).toHaveCount(0)
    expect(s.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/admin/coordinators/k1/pause')).toHaveLength(1)
    await expect(rows(page).nth(0).getByTestId('admin-coordinator-state')).toHaveText('Paused')
    await expect(rows(page).nth(0)).toContainText('by root')
  })

  test('resumes at once, without a confirm', async ({ page }) => {
    const s = await installCoordinators(page, { admin: { list: ADMIN_LIST } })
    await page.goto('/app/chats-admin')
    await rows(page).nth(1).getByRole('button', { name: 'Resume release-watch' }).click()
    await expect(rows(page).nth(1).getByTestId('admin-coordinator-state')).not.toHaveText('Paused')
    expect(s.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/admin/coordinators/k2/resume')).toHaveLength(1)
  })

  test('shows the audit log across coordinators, filters it and pages', async ({ page }) => {
    const s = await installCoordinators(page, { admin: { list: ADMIN_LIST }, audit: adminAudit() })
    await page.goto('/app/chats-admin')
    // Loaded on demand.
    expect(s.calls.filter(c => c.url.endsWith('/admin/coordinators/audit'))).toHaveLength(0)
    const toggle = page.getByTestId('admin-audit-toggle')
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const audit = page.getByTestId('admin-coordinator-audit')
    const arows = audit.getByTestId('coordinator-audit-row')
    await expect(arows).toHaveCount(25)
    await expect(arows.first()).toContainText('alice')
    await audit.getByTestId('crd-f-outcome').selectOption('denied')
    await expect(arows.first()).toContainText('Not a listed contact')
    await expect(arows.first()).toContainText('Denied')
    await audit.getByTestId('crd-f-clear').click()
    await audit.getByTestId('crd-f-coordinator').selectOption('k2')
    await expect.poll(() => s.calls.some(c => c.search.includes('coordinator=k2'))).toBe(true)
    await expect(arows.first()).toContainText('bob')
    expect(await arows.evaluateAll(els => els.every(e => e.textContent.includes('bob')))).toBe(true)
    await audit.getByTestId('crd-f-clear').click()
    await audit.getByTestId('coordinator-audit-more').click()
    await expect(arows).toHaveCount(50)
    await toggle.click()
    await expect(audit).toHaveCount(0)
  })

  test('leaves the section out when the server has no coordinators', async ({ page }) => {
    await installCoordinators(page, { admin: { list: [], status: 404 } })
    await page.goto('/app/chats-admin')
    await expect(page.getByTestId('admin-agents')).toBeVisible()
    await expect(page.getByTestId('admin-triggers')).toBeVisible()
    await expect(page.getByTestId('admin-connectors')).toBeVisible()
    await expect(page.getByTestId('admin-coordinators')).toHaveCount(0)
  })

  test('says a failed read in the section, with Try again, and the others still render', async ({ page }) => {
    await installCoordinators(page, { admin: { list: [], status: 500 } })
    await page.goto('/app/chats-admin')
    await expect(page.getByTestId('admin-coordinators').getByTestId('load-error')).toContainText('Could not read the coordinators.')
    await expect(page.getByTestId('admin-agents')).toBeVisible()
  })

  test('says so when no user has a coordinator', async ({ page }) => {
    await installCoordinators(page, { admin: { list: [] } })
    await page.goto('/app/chats-admin')
    await expect(page.getByTestId('admin-coordinators-empty')).toHaveText('No user has a coordinator.')
  })

  test('a user who is not an administrator is sent away and the admin calls are never made', async ({ page }) => {
    const s = await installCoordinators(page, { admin: { list: ADMIN_LIST } })
    await page.route('**/api/auth/status', route => route.fulfill({ json: statusFixture('member') }))
    await page.goto('/app/chats-admin')
    await expect(page).not.toHaveURL(/chats-admin/)
    await expect(page.getByTestId('admin-coordinators')).toHaveCount(0)
    expect(s.calls.filter(c => c.url.includes('/admin/'))).toHaveLength(0)
  })

  test('fits a phone and renders in the dark theme', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('localai-theme', 'dark'))
    await page.setViewportSize({ width: 390, height: 800 })
    await installCoordinators(page, { admin: { list: ADMIN_LIST }, audit: adminAudit() })
    await page.goto('/app/chats-admin')
    await page.getByTestId('admin-audit-toggle').click()
    await expect(page.getByTestId('admin-coordinator-audit').getByTestId('coordinator-audit-row').first()).toBeVisible()
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(over).toBeLessThanOrEqual(0)
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  })
})
