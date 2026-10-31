import { test, expect } from './coverage-fixtures.js'
import { ADMIN, app, installedAddon, sessionFor, mockAddons, ok, fail } from './addons-fixtures.js'

// What follows an add-on upgrade after it is confirmed: Check for updates
// reads every installed add-on, and an upgrade runs in the upgrade store, so it
// goes on when the page is left and shows in Activity. The confirmation and the
// "Upgrade all" dialog are covered by addons-upgrade-list.spec.js, and the
// backend dialog by backends-runtime.spec.js.

const CANDIDATE = `sha256:${'b'.repeat(64)}`
const admin = { canStart: false, canStop: true }
const pendingUpdate = (version = '1.3.0') => ({ available: true, state: 'update-available', version, digest: CANDIDATE, checkedAt: '2026-10-01T00:00:00Z' })

async function signIn(page) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN, { addons: true }) }))
}

function twoPending(over = {}) {
  return {
    apps: [
      app({ id: 'notes', title: 'Notes', update: 'update-pending', admin }),
      app({ id: 'wiki', title: 'Wiki', update: 'update-pending', admin }),
      app({ id: 'cal', title: 'Calendar', update: 'update-pending', admin }),
      app({ id: 'plain', title: 'Plain', admin }),
    ],
    installed: [
      installedAddon({ id: 'notes', title: 'Notes', update: pendingUpdate('1.3.0') }),
      installedAddon({ id: 'wiki', title: 'Wiki', version: '0.9.0', update: pendingUpdate('1.0.0') }),
      installedAddon({ id: 'cal', title: 'Calendar', update: { state: 'incompatible', version: '2.0.0', digest: CANDIDATE, reasons: ['host-version'], checkedAt: '2026-10-01T00:00:00Z' } }),
      installedAddon({ id: 'plain', title: 'Plain', update: { state: 'up-to-date', available: false } }),
    ],
    ...over,
  }
}

test.describe('Add-on updates: after the confirmation', () => {
  test('Check for updates reads every installed add-on and keeps going when one cannot be checked', async ({ page }) => {
    await signIn(page)
    const state = await mockAddons(page, twoPending({
      check: ({ id }) => (id === 'wiki' ? fail(503, 'check_failed') : null),
    }))
    await page.goto('/app/addons')
    await page.getByTestId('addons-check-all').click()
    await expect.poll(() => state.log.check.length).toBe(4)
    expect(state.log.check.every(c => c.confirm === c.operationId)).toBe(true)
    await expect(page.getByText('1 could not be checked.')).toBeVisible()
    expect(state.log.upgrade).toHaveLength(0)
  })

  test('an add-on upgrade is listed in Activity while it runs, can be canceled, and is recorded when it ends', async ({ page }) => {
    await signIn(page)
    await page.route('**/api/operations', route => route.fulfill({ json: { operations: [] } }))
    await page.route('**/api/operations/history', route => route.fulfill({ json: { operations: [] } }))
    let release
    const gate = new Promise(resolve => { release = resolve })
    const state = await mockAddons(page, twoPending())
    // The owner answers an upgrade only when the spec lets it.
    await page.route(/\/api\/addons\/wiki\/upgrade$/, async route => {
      await gate
      const body = JSON.parse(route.request().postData() || '{}')
      const a = state.installed.find(x => x.id === 'wiki')
      a.generation += 1; a.version = a.update.version; delete a.update
      return route.fulfill(ok({ OperationID: body.operationId, AddonID: 'wiki', Phase: 'completed', Generation: a.generation }))
    })
    await page.goto('/app/addons')
    await page.locator('[data-testid="addon-tile"][data-app="wiki"]').getByTestId('update-upgrade-card').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Upgrade' }).click()

    // Leave the page without a reload: the upgrade goes on.
    await page.evaluate(() => { window.history.pushState({}, '', '/app/activity'); window.dispatchEvent(new PopStateEvent('popstate')) })
    await expect(page.getByRole('heading', { name: 'Activity' })).toBeVisible()
    const card = page.locator('.operation-card').filter({ hasText: 'Wiki' })
    await expect(card).toBeVisible()
    await expect(card).toContainText('Upgrading add-on')
    await expect(card).toContainText('0.9.0 to 1.0.0')
    await expect(card.getByRole('button', { name: /cancel/i })).toBeVisible()
    await expect(card.getByRole('button', { name: /pause/i })).toHaveCount(0)

    await page.getByRole('button', { name: 'Add-ons', exact: true }).click()
    await expect(card).toBeVisible()
    await page.getByRole('button', { name: 'Backends', exact: true }).click()
    await expect(card).toHaveCount(0)
    await page.getByRole('button', { name: 'Add-ons', exact: true }).click()

    release()
    await expect(card).toHaveCount(0)
    const record = page.locator('.activity-row').filter({ hasText: 'Wiki' })
    await expect(record).toContainText('Upgraded')
    await expect(record.getByRole('link', { name: 'View in Add-ons' })).toHaveAttribute('href', '/app/addons')
  })

  test('a failed add-on upgrade stays in Activity with Retry', async ({ page }) => {
    await signIn(page)
    await page.route('**/api/operations', route => route.fulfill({ json: { operations: [] } }))
    await page.route('**/api/operations/history', route => route.fulfill({ json: { operations: [] } }))
    let attempts = 0
    const state = await mockAddons(page, twoPending({
      upgrade: () => { attempts += 1; return attempts === 1 ? fail(409, 'upgrade_failed') : null },
    }))
    await page.goto('/app/addons')
    await page.locator('[data-testid="addon-tile"][data-app="wiki"]').getByTestId('update-upgrade-card').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Upgrade' }).click()
    await expect.poll(() => state.log.upgrade.length).toBe(1)
    await page.evaluate(() => { window.history.pushState({}, '', '/app/activity'); window.dispatchEvent(new PopStateEvent('popstate')) })

    const card = page.locator('.operation-card').filter({ hasText: 'Wiki' })
    await expect(card).toContainText("Couldn't upgrade add-on")
    await expect(card).toContainText('installed version is kept')
    await card.getByRole('button', { name: /retry/i }).click()
    await expect.poll(() => state.log.upgrade.length).toBe(2)
    expect(state.log.upgrade[0].operationId).not.toBe(state.log.upgrade[1].operationId)
    await expect(card).toHaveCount(0)
    await expect(page.locator('.activity-row').filter({ hasText: 'Wiki' })).toContainText('Upgraded')
  })
})
