import { test, expect } from '@playwright/test'
import { ADMIN, MEMBER, app, installedAddon, sessionFor, mockAddons } from './addons-fixtures.js'

// The upgrade action where the "update available" notice is seen: the list.
// The routes are stand-ins (addons-fixtures.js); the owner's own decisions are
// covered by addons-upgrade.spec.js against the real owner.

const shots = process.env.UPGRADE_SHOTS
const theme = process.env.UPGRADE_THEME
async function shoot(page, name) {
  if (!shots) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${shots}/${name}${process.env.UPGRADE_SUFFIX || ''}.png`, fullPage: false })
}

const CANDIDATE = 'sha256:' + 'b'.repeat(64)
const adminOf = (id) => ({ canStart: false, canStop: true, managementUrl: `/app/addons?manage=${id}` })
const pending = (id, title) => app({ id, name: id, title, update: 'update-pending', admin: adminOf(id), icon: undefined, mainPage: undefined })
const avail = (over = {}) => ({ available: true, state: 'update-available', version: '1.3.0', digest: CANDIDATE, checkedAt: '2026-10-01T00:00:00Z', ...over })
const rec = (id, title, update) => installedAddon({ id, title, icon: undefined, mainPage: undefined, digest: 'sha256:' + id.padEnd(32, '0'), update })
const tile = (page, id) => page.locator(`[data-testid="addon-tile"][data-app="${id}"]`)

async function signIn(page, user, permissions = {}) {
  if (theme) await page.addInitScript((value) => { try { localStorage.setItem('localai-theme', value) } catch { /* private window */ } }, theme)
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
    await page.getByRole('alertdialog').getByRole('button', { name: 'Update' }).click()
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
    await page.getByRole('alertdialog').getByRole('button', { name: 'Update' }).click()
    await expect.poll(() => state.log.upgrade.length).toBe(1)
  })
})

test.describe('Add-ons: the Apps page leaves the bulk update to the Updates page', () => {
  test('an administrator sees a link to the Updates page, and no bulk button or check of its own', async ({ page }) => {
    await signIn(page, ADMIN)
    const state = await mockAddons(page, { apps: [pending('notes', 'Notes'), pending('wiki', 'Wiki')], installed: [rec('notes', 'Notes', avail()), rec('wiki', 'Wiki', avail())] })
    await page.goto('/app/addons')
    await expect(page.getByTestId('bulk-upgrade-open')).toHaveCount(0)
    await expect(page.getByTestId('addons-check-all')).toHaveCount(0)
    await page.getByTestId('addons-open-updates').click()
    await expect(page).toHaveURL(/\/app\/updates$/)
    expect(state.log.upgrade).toHaveLength(0)
    expect(state.log.check).toHaveLength(0)
  })
})
