import { test, expect } from '@playwright/test'
import { ADMIN, app, installedAddon, sessionFor, mockAddons, fail } from './addons-fixtures.js'

// The owner is busy with an upgrade. The routes are stand-ins
// (addons-fixtures.js); the owner's own behaviour is covered by the Go specs
// "reads during a long upgrade" and "apps list while the owner is busy". The server fails closed (a typed 503 on a timeout, never an older list); the page keeps its own last list..

const shots = process.env.BUSY_SHOTS
const theme = process.env.BUSY_THEME
async function shoot(page, name) {
  if (!shots) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${shots}/${name}${process.env.BUSY_SUFFIX || ''}.png`, fullPage: false })
}

const adminOf = (id, running = true) => ({ canStart: !running, canStop: running, managementUrl: `/app/addons?manage=${id}` })
const notes = (over = {}) => app({ id: 'notes', name: 'notes', title: 'Notes', icon: undefined, mainPage: undefined, admin: adminOf('notes'), ...over })
const sandbox = (over = {}) => app({ id: 'sandbox', name: 'sandbox', title: 'Sandbox', description: 'Run commands in a guest.', icon: undefined, mainPage: undefined, admin: adminOf('sandbox'), generation: 5, version: '2.1.0', ...over })
const tables = (over = {}) => app({ id: 'tables', name: 'tables', title: 'Tables', description: 'Spreadsheets.', icon: undefined, mainPage: undefined, admin: adminOf('tables'), generation: 2, version: '0.9.0', ...over })
const upgrading = (phase, since = '2026-11-04T17:18:00Z') => ({ kind: 'upgrade', phase, since })
const tile = (page, id) => page.locator(`[data-testid="addon-tile"][data-app="${id}"]`)
const body = (payload) => ({ status: 200, contentType: 'application/json', body: JSON.stringify(payload) })

async function signIn(page) {
  if (theme) await page.addInitScript((value) => { try { localStorage.setItem('localai-theme', value) } catch { /* private window */ } }, theme)
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN) }))
}

test.describe('Add-ons: the owner is busy', () => {
  test('an error on the first read is not "no add-ons": the page says the server is busy and a retry loads the list', async ({ page }) => {
    await signIn(page)
    let busy = true
    await mockAddons(page, {
      apps: [notes(), tables()],
      installed: [installedAddon({ id: 'notes' }), installedAddon({ id: 'tables', title: 'Tables' })],
      appsList: () => (busy ? fail(503, 'owner_busy', { message: 'busy', code: 503 }) : null),
    })
    await page.goto('/app/addons')
    const error = page.getByTestId('addons-load-error')
    await expect(error).toBeVisible()
    await expect(error).toContainText('did not answer in time')
    await expect(page.getByText('No add-ons yet')).toHaveCount(0)
    await expect(page.getByTestId('addon-tile')).toHaveCount(0)
    await shoot(page, 'busy-first-load')
    busy = false
    await error.getByRole('button').click()
    await expect(tile(page, 'notes')).toBeVisible()
    await expect(tile(page, 'tables')).toBeVisible()
    await expect(page.getByTestId('addons-load-error')).toHaveCount(0)
  })

  test('a plain 500 is an error too, never the empty state', async ({ page }) => {
    await signIn(page)
    await mockAddons(page, { apps: [notes()], appsList: () => fail(500, 'server_error') })
    await page.goto('/app/addons')
    await expect(page.getByTestId('addons-load-error')).toBeVisible()
    await expect(page.getByText('No add-ons yet')).toHaveCount(0)
  })

  test('a timeout after a list with upgrades in flight keeps the rows, names what is upgrading and shows the phase of each row', async ({ page }) => {
    await signIn(page)
    let busy = false
    await mockAddons(page, {
      apps: [notes(), sandbox(), tables()],
      installed: [installedAddon({ id: 'notes' })],
      appsList: () => (busy
        ? fail(503, 'owner_busy', { message: 'busy', code: 503 })
        : body({ apps: [notes({ operation: upgrading('downloading') }), sandbox({ operation: upgrading('starting') }), tables()] })),
    })
    await page.goto('/app/addons')
    await expect(tile(page, 'notes').getByTestId('operation-chip')).toBeVisible()
    busy = true
    await page.reload()
    const banner = page.getByTestId('addons-stale')
    await expect(banner).toBeVisible()
    await expect(banner).toContainText('The add-on server is busy (upgrading Notes, Sandbox)')
    await expect(banner).toContainText('Showing the last known state.')
    await expect(tile(page, 'tables')).toBeVisible()
    await expect(tile(page, 'notes').getByTestId('operation-chip')).toContainText('Upgrading… · downloading and checking')
    await expect(tile(page, 'sandbox').getByTestId('operation-chip')).toContainText('Upgrading… · starting')
    await expect(tile(page, 'tables').getByTestId('operation-chip')).toHaveCount(0)
    await expect(page.getByText('No add-ons yet')).toHaveCount(0)
    await shoot(page, 'busy-stale-list')
  })

  test('a live answer with an upgrade in flight shows the rows as upgrading and no banner', async ({ page }) => {
    await signIn(page)
    await mockAddons(page, { apps: [notes({ operation: upgrading('backing-up') }), tables()] })
    await page.goto('/app/addons')
    await expect(tile(page, 'notes').getByTestId('operation-chip')).toContainText('backing up data')
    await expect(page.getByTestId('addons-stale')).toHaveCount(0)
  })

  test('a refresh while the server is busy shows the last list this tab got, marked as last known, and recovers by itself', async ({ page }) => {
    await signIn(page)
    let busy = false
    await mockAddons(page, {
      apps: [notes(), tables()],
      appsList: () => (busy ? fail(503, 'owner_busy', { message: 'busy', code: 503 }) : null),
    })
    await page.goto('/app/addons')
    await expect(tile(page, 'notes')).toBeVisible()
    busy = true
    await page.reload()
    const banner = page.getByTestId('addons-stale')
    await expect(banner).toBeVisible()
    await expect(banner).toContainText('Showing the last known state.')
    await expect(tile(page, 'notes')).toBeVisible()
    await expect(tile(page, 'tables')).toBeVisible()
    await expect(page.getByTestId('addons-load-error')).toHaveCount(0)
    await expect(page.getByText('No add-ons yet')).toHaveCount(0)
    await shoot(page, 'busy-last-known')
    busy = false
    await banner.getByTestId('addons-stale-retry').click()
    await expect(page.getByTestId('addons-stale')).toHaveCount(0)
    await expect(tile(page, 'notes')).toBeVisible()
  })

  test('an empty list the owner answered is the only empty state', async ({ page }) => {
    await signIn(page)
    await mockAddons(page, { apps: [] })
    await page.goto('/app/addons')
    await expect(page.getByText('No add-ons yet').first()).toBeVisible()
    await expect(page.getByTestId('addons-load-error')).toHaveCount(0)
  })

  test('a remembered empty list shown during a timeout is not the empty state either', async ({ page }) => {
    await signIn(page)
    let busy = false
    await mockAddons(page, { apps: [], appsList: () => (busy ? fail(503, 'owner_busy', { message: 'busy', code: 503 }) : null) })
    await page.goto('/app/addons')
    await expect(page.getByText('No add-ons yet').first()).toBeVisible()
    busy = true
    await page.reload()
    await expect(page.getByTestId('addons-load-error')).toBeVisible()
    await expect(page.getByText('No add-ons yet')).toHaveCount(0)
  })
})
