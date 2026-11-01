import { test, expect } from '@playwright/test'
import { ADMIN, app, installedAddon, sessionFor, mockAddons } from './addons-fixtures.js'

// "Check again" on the card of an add-on whose update notice cannot be acted
// on yet. The check route answers the owner's status, whose candidate fields
// are candidateVersion and candidateDigest; the installed records carry the
// update object with version and digest. The routes are stand-ins
// (addons-fixtures.js); the owner's own decisions are covered by the Go specs
// of the update checker.

const CANDIDATE = 'sha256:' + 'b'.repeat(64)
const adminOf = (id) => ({ canStart: false, canStop: true, managementUrl: `/app/addons?manage=${id}` })
const pending = (id, title) => app({ id, name: id, title, update: 'update-pending', admin: adminOf(id), icon: undefined, mainPage: undefined })
const rec = (id, title, update) => installedAddon({ id, title, icon: undefined, mainPage: undefined, version: '0.0.0-dev.2cadf319a0e1', channel: 'development', digest: 'sha256:' + 'a'.repeat(64), update })
const tile = (page, id) => page.locator(`[data-testid="addon-tile"][data-app="${id}"]`)
// A development build the owner reported as an update with no build to pin.
const unpinned = () => ({ available: true, state: 'update-available', version: '0.0.0-development', checkedAt: '2026-10-30T16:00:00Z' })
const status = (over) => ({ status: 200, contentType: 'application/json', body: JSON.stringify({ addonId: 'talk', generation: 1, installedVersion: '0.0.0-dev.2cadf319a0e1', channel: 'development', selection: '0.0.0-development', checkedAt: new Date().toISOString(), ...over }) })

async function signIn(page) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN) }))
}

test.describe('Add-ons: Check again on the card', () => {
  test('a check that still names no build to pin says so, and when it checked', async ({ page }) => {
    await signIn(page)
    const state = await mockAddons(page, {
      apps: [pending('talk', 'Talk')],
      installed: [rec('talk', 'Talk', unpinned())],
      check: () => status({ state: 'update-available', available: true, candidateVersion: '0.0.0-development' }),
    })
    await page.goto('/app/addons')
    const card = tile(page, 'talk')
    await expect(card.getByTestId('upgrade-reason')).toContainText('An update is reported, but its details are not confirmed yet.')
    await expect(card.getByTestId('upgrade-check-result')).toHaveCount(0)
    await card.getByTestId('upgrade-reason-check').click()
    await expect.poll(() => state.log.check.length).toBe(1)
    const result = card.getByTestId('upgrade-check-result')
    await expect(result).toHaveText('Checked just now.')
    // The same answer again is explained, not sent back to another check.
    await expect(card.getByTestId('upgrade-reason')).toContainText('The library reports an update but names no build to install, so it cannot be upgraded from here.')
    await expect(card.getByTestId('upgrade-reason')).not.toContainText('not confirmed yet')
    await expect(card.getByTestId('update-upgrade-card')).toHaveCount(0)
    await expect(card.getByTestId('upgrade-reason-check')).toBeEnabled()
  })

  test('a check that finds the tag moved offers Upgrade pinned to the build it names', async ({ page }) => {
    await signIn(page)
    let talk = null
    const state = await mockAddons(page, {
      apps: [pending('talk', 'Talk')],
      installed: [rec('talk', 'Talk', unpinned())],
      check: () => {
        talk.update = { available: true, state: 'update-available', version: '0.0.0-development.bbbbbbbbbbbb', digest: CANDIDATE, checkedAt: new Date().toISOString() }
        return status({ state: 'update-available', available: true, candidateVersion: '0.0.0-development.bbbbbbbbbbbb', candidateDigest: CANDIDATE })
      },
    })
    talk = state.installed.find(a => a.id === 'talk')
    await page.goto('/app/addons')
    const card = tile(page, 'talk')
    await card.getByTestId('upgrade-reason-check').click()
    await expect(card.getByTestId('update-upgrade-card')).toBeVisible({ timeout: 15_000 })
    await card.getByTestId('update-upgrade-card').click()
    await expect(page.getByTestId('upgrade-confirm')).toContainText('0.0.0-development.bbbbbbbbbbbb')
    await page.getByRole('alertdialog').getByRole('button', { name: 'Upgrade' }).click()
    await expect.poll(() => state.log.upgrade.length).toBe(1)
    expect(state.log.upgrade[0]).toMatchObject({ id: 'talk', candidateDigest: CANDIDATE, expectedDigest: 'sha256:' + 'a'.repeat(64) })
  })

  test('a check that finds the add-on current says so after the notice is gone', async ({ page }) => {
    await signIn(page)
    let talk = null
    let listed = null
    const state = await mockAddons(page, {
      apps: [pending('talk', 'Talk')],
      installed: [rec('talk', 'Talk', unpinned())],
      check: () => {
        talk.update = { available: false, state: 'up-to-date', checkedAt: new Date().toISOString() }
        delete listed.update
        return status({ state: 'up-to-date', available: false, candidateVersion: '0.0.0-development.aaaaaaaaaaaa', candidateDigest: 'sha256:' + 'a'.repeat(64) })
      },
    })
    talk = state.installed.find(a => a.id === 'talk')
    listed = state.apps.find(a => a.id === 'talk')
    await page.goto('/app/addons')
    const card = tile(page, 'talk')
    await card.getByTestId('upgrade-reason-check').click()
    await expect(card.getByTestId('upgrade-check-result')).toHaveText('Checked just now. This add-on is up to date.')
    await expect(card.getByTestId('update-chip')).toHaveCount(0)
    await expect(card.getByTestId('update-upgrade-card')).toHaveCount(0)
  })

  test('a failed check shows why, and the check can be run again', async ({ page }) => {
    await signIn(page)
    const state = await mockAddons(page, {
      apps: [pending('talk', 'Talk')],
      installed: [rec('talk', 'Talk', unpinned())],
      check: () => ({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { type: 'check_failed', message: 'check_failed', code: 503 } }) }),
    })
    await page.goto('/app/addons')
    const card = tile(page, 'talk')
    await card.getByTestId('upgrade-reason-check').click()
    const result = card.getByTestId('upgrade-check-result')
    await expect(result).toHaveText('The check did not complete. The library could not be read, so the upgrade was not started. Nothing was changed.')
    await expect(result).toHaveAttribute('data-tone', 'error')
    await expect(card.getByTestId('upgrade-reason-check')).toBeEnabled()
    await card.getByTestId('upgrade-reason-check').click()
    await expect.poll(() => state.log.check.length).toBe(2)
  })
})
