import { test, expect } from './coverage-fixtures.js'
import { ADMIN, app, installedAddon, sessionFor, mockAddons, ok, fail } from './addons-fixtures.js'

// The review step for add-on and backend upgrades: nothing runs until the
// administrator confirms in the sheet, one failure does not stop the others,
// and an add-on upgrade shows in Activity.

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

test.describe('Add-on updates: review sheet', () => {
  test('the badge opens a list of pending updates; an incompatible one cannot be selected and nothing is sent until confirmed', async ({ page }) => {
    await signIn(page)
    const state = await mockAddons(page, twoPending())
    await page.goto('/app/addons')

    const badge = page.getByTestId('addons-updates')
    await expect(badge).toContainText('Updates (3)')
    await badge.click()

    const sheet = page.getByTestId('update-review')
    const rows = sheet.getByTestId('update-review-row')
    await expect(rows).toHaveCount(3)
    const notes = rows.filter({ hasText: 'Notes' })
    await expect(notes.getByTestId('update-review-versions')).toContainText('1.2.0')
    await expect(notes.getByTestId('update-review-versions')).toContainText('1.3.0')
    const cal = rows.filter({ hasText: 'Calendar' })
    await expect(cal.getByRole('checkbox')).toBeDisabled()
    await expect(cal.getByTestId('update-review-blocked')).toContainText('The host version does not fit')

    // Nothing is selected: "Upgrade selected" is off, and "Upgrade all" leaves the blocked one out.
    await expect(sheet.getByTestId('update-review-selected')).toBeDisabled()
    await expect(sheet.getByTestId('update-review-all')).toContainText('Upgrade all (2)')
    expect(state.log.upgrade).toHaveLength(0)

    await notes.getByRole('checkbox').check()
    await expect(sheet.getByTestId('update-review-selected')).toContainText('Upgrade selected (1)')
    await sheet.getByTestId('update-review-selected').click()
    await expect.poll(() => state.log.upgrade.length).toBe(1)
    expect(state.log.upgrade[0].id).toBe('notes')
    expect(state.log.upgrade[0].confirm).toBe(state.log.upgrade[0].operationId)
    await expect(notes).toHaveAttribute('data-state', 'done')
    await expect(sheet.getByTestId('update-review-summary')).toContainText('1 upgraded, 0 failed')
  })

  test('Upgrade all runs every upgradable add-on and continues past one that fails, then reports each', async ({ page }) => {
    await signIn(page)
    const state = await mockAddons(page, twoPending({
      upgrade: ({ id }) => (id === 'notes' ? fail(409, 'upgrade_failed') : null),
    }))
    await page.goto('/app/addons')
    await page.getByTestId('addons-updates').click()
    const sheet = page.getByTestId('update-review')
    await sheet.getByTestId('update-review-all').click()

    await expect(sheet.getByTestId('update-review-summary')).toContainText('1 upgraded, 1 failed')
    expect(state.log.upgrade.map(u => u.id)).toEqual(['notes', 'wiki'])
    const rows = sheet.getByTestId('update-review-row')
    await expect(rows.filter({ hasText: 'Notes' })).toHaveAttribute('data-state', 'failed')
    await expect(rows.filter({ hasText: 'Notes' }).getByRole('alert')).toContainText('installed version is kept')
    await expect(rows.filter({ hasText: 'Wiki' })).toHaveAttribute('data-state', 'done')
    await expect(rows.filter({ hasText: 'Calendar' })).toHaveAttribute('data-state', 'idle')
    // One toast per item.
    await expect(page.getByText('Wiki upgraded to 1.0.0.')).toBeVisible()
    await expect(page.getByText(/Notes was not upgraded/)).toBeVisible()
  })

  test('a card Upgrade action opens the sheet with that add-on checked', async ({ page }) => {
    await signIn(page)
    const state = await mockAddons(page, twoPending())
    await page.goto('/app/addons')
    await page.locator('[data-testid="addon-tile"][data-app="wiki"]').getByTestId('tile-upgrade').click()
    const sheet = page.getByTestId('update-review')
    await expect(sheet.getByTestId('update-review-row').filter({ hasText: 'Wiki' }).getByRole('checkbox')).toBeChecked()
    await expect(sheet.getByTestId('update-review-row').filter({ hasText: 'Notes' }).getByRole('checkbox')).not.toBeChecked()
    expect(state.log.upgrade).toHaveLength(0)
    await page.locator('[data-testid="addon-tile"][data-app="plain"]').getByTestId('tile-upgrade').waitFor({ state: 'detached' })
  })

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
    await page.locator('[data-testid="addon-tile"][data-app="wiki"]').getByTestId('tile-upgrade').click()
    await page.getByTestId('update-review-selected').click()
    await page.getByTestId('update-review-close').click()

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
    await page.locator('[data-testid="addon-tile"][data-app="wiki"]').getByTestId('tile-upgrade').click()
    await page.getByTestId('update-review-selected').click()
    await expect(page.getByTestId('update-review-summary')).toContainText('0 upgraded, 1 failed')
    await page.getByTestId('update-review-close').click()
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

test.describe('Backend updates: review sheet', () => {
  const installedBackends = ['llama-cpp', 'whisper'].map(Name => ({ Name, IsSystem: false, Version: '1.0.0', Metadata: { version: '1.0.0' } }))
  const catalog = installedBackends.map(b => ({ id: b.Name, name: b.Name, description: b.Name, installed: true, version: '1.0.0', isMeta: true, isAlias: false, isDevelopment: false, tags: [] }))
  const upgrades = Object.fromEntries(installedBackends.map(b => [b.Name, { backend_name: b.Name, installed_version: '1.0.0', available_version: '1.1.0' }]))

  async function mock(page) {
    await page.route('**/api/backends/upgrades', route => route.fulfill({ json: upgrades }))
    await page.route('**/api/backends?*', route => route.fulfill({ json: { backends: catalog } }))
    await page.route('**/backends', route => (new URL(route.request().url()).pathname === '/backends' ? route.fulfill({ json: installedBackends }) : route.continue()))
    await page.route('**/api/nodes', route => route.fulfill({ json: [] }))
  }

  test('Updates opens the sheet; nothing is upgraded until confirmed, and one failure does not stop the next', async ({ page }) => {
    await mock(page)
    const sent = []
    await page.route('**/api/backends/upgrade/*', route => {
      const name = new URL(route.request().url()).pathname.split('/').at(-1)
      sent.push(name)
      return name === 'llama-cpp'
        ? route.fulfill({ status: 500, json: { error: 'registry unavailable' } })
        : route.fulfill({ json: { jobID: 'j1', uuid: 'j1', message: 'started' } })
    })
    await page.goto('/app/backends?view=installed')
    await page.getByTestId('backends-updates').click()
    const sheet = page.getByTestId('update-review')
    await expect(sheet.getByTestId('update-review-row')).toHaveCount(2)
    await expect(sheet.getByTestId('update-review-row').first().getByTestId('update-review-versions')).toContainText('v1.0.0')
    await expect(sheet.getByTestId('update-review-row').first().getByTestId('update-review-versions')).toContainText('v1.1.0')
    expect(sent).toEqual([])
    await sheet.getByTestId('update-review-all').click()
    await expect(sheet.getByTestId('update-review-summary')).toContainText('1 started, 1 failed')
    expect(sent).toEqual(['llama-cpp', 'whisper'])
    await expect(sheet.getByTestId('update-review-row').filter({ hasText: 'llama-cpp' }).getByRole('alert')).toContainText('registry unavailable')
  })

  test('a row Upgrade button opens the sheet with that backend checked instead of upgrading at once', async ({ page }) => {
    await mock(page)
    let calls = 0
    await page.route('**/api/backends/upgrade/*', route => { calls += 1; return route.fulfill({ json: { jobID: 'j1' } }) })
    await page.goto('/app/backends?view=installed&backend=whisper')
    await page.getByRole('button', { name: /update to v1\.1\.0/i }).first().click()
    const sheet = page.getByTestId('update-review')
    await expect(sheet.getByTestId('update-review-row').filter({ hasText: 'whisper' }).getByRole('checkbox')).toBeChecked()
    await expect(sheet.getByTestId('update-review-row').filter({ hasText: 'llama-cpp' }).getByRole('checkbox')).not.toBeChecked()
    expect(calls).toBe(0)
    await sheet.getByTestId('update-review-selected').click()
    await expect.poll(() => calls).toBe(1)
  })
})
