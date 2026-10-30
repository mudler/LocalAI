import { test, expect } from '@playwright/test'
import { ADMIN, MEMBER, app, installedAddon, sessionFor, mockAddons, fail } from './addons-fixtures.js'

// The upgrade action where the "update available" notice is seen: the list.
// The routes are stand-ins (addons-fixtures.js); the owner's own decisions are
// covered by addons-upgrade.spec.js against the real owner.

const shots = process.env.UPGRADE_SHOTS
async function shoot(page, name) {
  if (!shots) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${shots}/${name}.png`, fullPage: false })
}

const CANDIDATE = 'sha256:' + 'b'.repeat(64)
const adminOf = (id) => ({ canStart: false, canStop: true, managementUrl: `/app/addons?manage=${id}` })
const pending = (id, title) => app({ id, name: id, title, update: 'update-pending', admin: adminOf(id), icon: undefined, mainPage: undefined })
const avail = (over = {}) => ({ available: true, state: 'update-available', version: '1.3.0', digest: CANDIDATE, checkedAt: '2026-10-01T00:00:00Z', ...over })
const rec = (id, title, update) => installedAddon({ id, title, icon: undefined, mainPage: undefined, digest: 'sha256:' + id.padEnd(32, '0'), update })
const tile = (page, id) => page.locator(`[data-testid="addon-tile"][data-app="${id}"]`)

async function signIn(page, user, permissions = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(user, permissions) }))
}

test.describe('Add-ons: upgrade from the list', () => {
  test('the Upgrade action is on the card of an add-on with an update, and opens the confirmation', async ({ page }) => {
    await signIn(page, ADMIN)
    const state = await mockAddons(page, {
      apps: [pending('notes', 'Notes'), app({ id: 'plain', title: 'Plain', admin: adminOf('plain'), icon: undefined, mainPage: undefined })],
      installed: [rec('notes', 'Notes', avail()), rec('plain', 'Plain', { state: 'up-to-date', available: false })],
    })
    await page.goto('/app/addons')
    await expect(tile(page, 'notes').getByTestId('update-chip')).toBeVisible()
    await expect(tile(page, 'plain').getByTestId('update-upgrade-card')).toHaveCount(0)
    await expect(tile(page, 'notes').getByTestId('update-upgrade-card')).toBeVisible()
    await shoot(page, 'list-action')
    await tile(page, 'notes').getByTestId('update-upgrade-card').click()
    await expect(page.getByTestId('upgrade-confirm')).toContainText('1.3.0')
    expect(state.log.upgrade).toHaveLength(0)
    await page.getByRole('alertdialog').getByRole('button', { name: 'Upgrade' }).click()
    await expect.poll(() => state.log.upgrade.length).toBe(1)
    expect(state.log.upgrade[0]).toMatchObject({ id: 'notes', candidateDigest: CANDIDATE })
  })

  test('when the upgrade cannot start the card says why, and Check again can make it startable', async ({ page }) => {
    await signIn(page, ADMIN)
    let installedRef = null
    const state = await mockAddons(page, {
      check: ({ id }) => { if (id === 'tables') installedRef.update = { ...installedRef.update, digest: CANDIDATE }; return null },
      apps: [pending('tables', 'Tables'), pending('docs', 'Docs'), pending('slack', 'Slack')],
      installed: [
        rec('tables', 'Tables', { available: true, state: 'update-available', version: '2.0.0', checkedAt: '2026-10-01T00:00:00Z' }),
        rec('docs', 'Docs', { available: false, state: 'incompatible', version: '3.0.0', reasons: ['host-version'] }),
        rec('slack', 'Slack', { state: 'check-failed', available: false, error: 'refresh-failed' }),
      ],
    })
    installedRef = state.installed.find(a => a.id === 'tables')
    await page.goto('/app/addons')
    await expect(tile(page, 'tables').getByTestId('upgrade-reason')).toContainText('not confirmed yet')
    await expect(tile(page, 'tables').getByTestId('update-upgrade-card')).toHaveCount(0)
    await expect(tile(page, 'docs').getByTestId('upgrade-reason')).toContainText('cannot run here')
    await expect(tile(page, 'docs').getByTestId('upgrade-reason')).not.toContainText('Check again')
    await expect(tile(page, 'slack').getByTestId('upgrade-reason')).toContainText('last check failed')
    await shoot(page, 'list-reasons')
    // The check resolves the candidate: the owner now has a digest.
    await tile(page, 'tables').getByTestId('upgrade-reason-check').click()
    await expect.poll(() => state.log.check.length).toBe(1)
    expect(state.log.check[0].id).toBe('tables')
    await expect(tile(page, 'tables').getByTestId('update-upgrade-card')).toBeVisible({ timeout: 15_000 })
    await expect(tile(page, 'tables').getByTestId('upgrade-reason')).toHaveCount(0)
  })

  test('a user who is not an administrator sees the notice with the reason and no action', async ({ page }) => {
    await signIn(page, MEMBER, { addons: true })
    await mockAddons(page, { apps: [app({ id: 'notes', title: 'Notes', update: 'update-pending', icon: undefined, mainPage: undefined })], installed: [] })
    await page.goto('/app/addons')
    await expect(tile(page, 'notes').getByTestId('update-chip')).toBeVisible()
    await expect(tile(page, 'notes').getByTestId('upgrade-reason')).toContainText('Only an administrator')
    await expect(tile(page, 'notes').getByTestId('update-upgrade-card')).toHaveCount(0)
    await expect(page.getByTestId('bulk-upgrade-open')).toHaveCount(0)
  })

  test('the Browse entry of an installed add-on with an update carries the same action', async ({ page }) => {
    await signIn(page, ADMIN)
    const state = await mockAddons(page, { apps: [pending('notes', 'Notes')], installed: [rec('notes', 'Notes', avail())] })
    await page.goto('/app/addons?tab=browse')
    await expect(page.getByTestId('entry-upgrade').getByTestId('update-upgrade-card')).toBeVisible()
    await page.getByTestId('entry-upgrade').getByTestId('update-upgrade-card').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Upgrade' }).click()
    await expect.poll(() => state.log.upgrade.length).toBe(1)
  })
})

test.describe('Add-ons: upgrade all', () => {
  const ids = ['alpha', 'bravo', 'charlie', 'delta']
  const setup = async (page, upgrade) => {
    await signIn(page, ADMIN)
    return mockAddons(page, {
      apps: [...ids.map(id => pending(id, id[0].toUpperCase() + id.slice(1))), pending('echo', 'Echo')],
      installed: [...ids.map(id => rec(id, id[0].toUpperCase() + id.slice(1), avail())), rec('echo', 'Echo', { available: true, state: 'update-available', version: '9.0.0' })],
      upgrade,
    })
  }

  test('runs the selected add-ons one after another, keeps going after a failure and retries the failed one', async ({ page }) => {
    let bravo = 0
    const state = await setup(page, ({ id }) => {
      if (id !== 'bravo') return null
      bravo += 1
      return bravo === 1 ? fail(409, 'candidate_changed') : null
    })
    await page.goto('/app/addons')
    await expect(page.getByTestId('bulk-upgrade-open')).toContainText('Upgrade all (4)')
    await page.getByTestId('bulk-upgrade-open').click()
    const dialog = page.getByTestId('bulk-upgrade-dialog')
    await expect(dialog.getByTestId('bulk-upgrade-item')).toHaveCount(5)
    // The add-on that cannot start is listed with its reason and cannot be picked.
    const echo = dialog.locator('[data-item="echo"]')
    await expect(echo.getByRole('checkbox')).toBeDisabled()
    await expect(echo.getByTestId('bulk-upgrade-why')).toContainText('not confirmed')
    await dialog.getByTestId('bulk-upgrade-none').click()
    await expect(dialog.getByTestId('bulk-upgrade-start')).toBeDisabled()
    await dialog.getByTestId('bulk-upgrade-all').click()
    await dialog.locator('[data-item="delta"]').getByRole('checkbox').uncheck()
    await shoot(page, 'bulk-select')
    await dialog.getByTestId('bulk-upgrade-start').click()
    await expect(dialog.locator('[data-item="bravo"]')).toHaveAttribute('data-phase', 'failed')
    await expect(dialog.locator('[data-item="charlie"]')).toHaveAttribute('data-phase', 'done')
    await expect(dialog.getByTestId('bulk-upgrade-progress')).toContainText('2 upgraded, 1 failed, 0 skipped')
    expect(state.log.upgrade.map(u => u.id)).toEqual(['alpha', 'bravo', 'charlie'])
    expect(new Set(state.log.upgrade.map(u => u.operationId)).size).toBe(3)
    await expect(dialog.getByTestId('bulk-upgrade-error')).toHaveCount(1)
    await shoot(page, 'bulk-result')
    await dialog.getByTestId('bulk-upgrade-retry').click()
    await expect(dialog.getByTestId('bulk-upgrade-progress')).toContainText('1 upgraded, 0 failed')
    expect(state.log.upgrade.map(u => u.id)).toEqual(['alpha', 'bravo', 'charlie', 'bravo'])
    expect(state.log.upgrade[3].operationId).not.toBe(state.log.upgrade[1].operationId)
    await dialog.getByTestId('bulk-upgrade-close').click()
    await expect(page.getByTestId('bulk-upgrade-summary')).toContainText('1 upgraded')
  })
})
