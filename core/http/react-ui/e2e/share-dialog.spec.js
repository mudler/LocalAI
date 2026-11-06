// SPDX-License-Identifier: MIT
import { test, expect } from './coverage-fixtures.js'

// The shared Share dialog over the generic sharing routes, on the UI catalog
// (/app/ui-catalog), with /api/shares answered here. Set SHARE_SHOTS_DIR to
// also save screenshots of each state in both themes, at desktop and phone
// widths.

const CATALOG = '/app/ui-catalog'
const BASE = '**/api/shares/example:note/r0123456789abcdef0123456789abcde'
const SHOTS = process.env.SHARE_SHOTS_DIR
const PEOPLE = [
  { id: 'u-alice', displayName: 'Alice Martin' },
  { id: 'u-bob', displayName: 'Bob Okafor' },
  { id: 'u-carla', displayName: 'Carla Rossi' },
]

// A small sharing server in the page: grants keyed by principal, the user
// search by name prefix, and every request recorded.
async function stubShares(page, { grants = [], searchDelay = 0, fail = {} } = {}) {
  const state = { grants: grants.map(g => ({ ...g })), requests: [] }
  const list = () => ({ kind: 'example:note', resourceId: 'r0123456789abcdef0123456789abcde', roles: ['use', 'manage'], everyone: true, grants: state.grants })
  await page.route(`${BASE}/**`, async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const rest = url.pathname.split('/r0123456789abcdef0123456789abcde/')[1]
    state.requests.push({ method: req.method(), path: url.pathname + url.search, confirm: req.headers()['x-localai-confirm'] || '', body: req.postDataJSON?.() ?? null })
    if (fail[rest]) return route.fulfill({ status: fail[rest].status, json: { error: { type: fail[rest].type || '', message: 'refused' } } })
    if (rest === 'grants' && req.method() === 'GET') return route.fulfill({ json: list() })
    if (rest.startsWith('users')) {
      const q = (url.searchParams.get('q') || '').toLowerCase()
      if (searchDelay) await new Promise(r => setTimeout(r, searchDelay))
      return route.fulfill({ json: { users: PEOPLE.filter(p => p.displayName.toLowerCase().startsWith(q)) } })
    }
    const principal = decodeURIComponent(rest.replace(/^grants\//, ''))
    if (req.method() === 'PUT') {
      const role = req.postDataJSON().role
      const p = PEOPLE.find(x => x.id === principal)
      const existing = state.grants.find(g => g.userId === principal)
      if (existing) existing.role = role
      else state.grants.push(principal === '@everyone' ? { userId: '@everyone', role } : { userId: principal, displayName: p?.displayName || '', role })
      return route.fulfill({ json: list() })
    }
    if (req.method() === 'DELETE') {
      state.grants = state.grants.filter(g => g.userId !== principal)
      return route.fulfill({ json: list() })
    }
    return route.fulfill({ status: 404, json: {} })
  })
  return state
}

async function openCatalog(page, { theme = 'light' } = {}) {
  await page.addInitScript((t) => localStorage.setItem('localai-theme', t), theme)
  await page.goto(CATALOG)
  await expect(page.getByTestId('cat-section-share')).toBeVisible({ timeout: 15_000 })
}

async function openShare(page, which = 'open') {
  await page.getByTestId(`cat-share-${which}`).click()
  const dialog = page.getByTestId('share-dialog')
  await expect(dialog).toBeVisible()
  return dialog
}

test.describe('Share dialog', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
  })

  test('an empty resource: the owner finds a person by name and adds them with the chosen role', async ({ page }) => {
    const server = await stubShares(page)
    await openCatalog(page)
    const dialog = await openShare(page)
    await expect(dialog.getByRole('heading', { name: 'Share "Release notes"' })).toBeVisible()
    await expect(dialog.getByTestId('share-no-grants')).toHaveText('Only you have access.')
    // The search field has the focus when the dialog opens.
    await expect(dialog.getByLabel('Find a person by name')).toBeFocused()
    // Two letters are not a search.
    await dialog.getByLabel('Find a person by name').fill('bo')
    await page.waitForTimeout(400)
    expect(server.requests.filter(r => r.path.includes('/users'))).toHaveLength(0)
    await dialog.getByLabel('Role for people you add').selectOption('manage')
    await dialog.getByLabel('Find a person by name').fill('bob')
    const bob = dialog.getByTestId('share-user').filter({ hasText: 'Bob Okafor' })
    await expect(bob).toHaveCount(1)
    // Only an identifier and a name are shown, never an address.
    await expect(dialog.getByTestId('share-users')).not.toContainText('@')
    await bob.getByRole('button', { name: 'Add Bob Okafor as Can manage' }).click()
    await expect(dialog.getByTestId('share-notice')).toHaveText('Bob Okafor now has access.')
    await expect(dialog.locator('[data-testid="share-grant"][data-user="u-bob"]')).toHaveAttribute('data-role', 'manage')
    await expect(dialog.getByLabel('Find a person by name')).toHaveValue('')
    const put = server.requests.find(r => r.method === 'PUT')
    expect(put.path).toBe('/api/shares/example:note/r0123456789abcdef0123456789abcde/grants/u-bob')
    expect(put.confirm).toBe('r0123456789abcdef0123456789abcde')
    expect(put.body.role).toBe('manage')
    expect(put.body.operationId).toMatch(/^share-/)
    // Someone who already has access is not offered again.
    await dialog.getByLabel('Find a person by name').fill('bob')
    await expect(dialog.getByTestId('share-no-users')).toHaveText('Everybody who matches already has access.')
    await dialog.getByLabel('Find a person by name').fill('zzz')
    await expect(dialog.getByTestId('share-no-users')).toHaveText('Nobody matches.')
  })

  test('the owner changes a role, and a revoke asks first and can be cancelled', async ({ page }) => {
    const server = await stubShares(page, { grants: [{ userId: 'u-alice', displayName: 'Alice Martin', role: 'use' }, { userId: 'u-carla', displayName: 'Carla Rossi', role: 'manage' }] })
    await openCatalog(page)
    const dialog = await openShare(page)
    await expect(dialog.getByTestId('share-grant')).toHaveCount(2)
    await dialog.getByLabel('Role of Alice Martin').selectOption('manage')
    await expect(dialog.getByTestId('share-notice')).toHaveText('Alice Martin is now Can manage.')
    await expect(dialog.locator('[data-testid="share-grant"][data-user="u-alice"]')).toHaveAttribute('data-role', 'manage')

    await dialog.getByRole('button', { name: 'Stop sharing with Carla Rossi' }).click()
    await expect(dialog.getByText('Stop sharing with Carla Rossi? Their running work on it stops.')).toBeVisible()
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    expect(server.requests.filter(r => r.method === 'DELETE')).toHaveLength(0)
    await dialog.getByRole('button', { name: 'Stop sharing with Carla Rossi' }).click()
    await dialog.getByRole('group', { name: 'Stop sharing with Carla Rossi' }).getByRole('button', { name: 'Stop sharing' }).click()
    await expect(dialog.getByTestId('share-notice')).toHaveText('Stopped sharing with Carla Rossi.')
    await expect(dialog.getByTestId('share-grant')).toHaveCount(1)
    const del = server.requests.find(r => r.method === 'DELETE')
    expect(del.path).toMatch(/\/grants\/u-carla\?operationId=share-/)
    expect(del.confirm).toBe('r0123456789abcdef0123456789abcde')
  })

  test('a kind that allows it can be shared with everyone, and that grant is changed apart', async ({ page }) => {
    const server = await stubShares(page)
    await openCatalog(page)
    const dialog = await openShare(page)
    const add = dialog.getByTestId('share-everyone-add')
    await expect(add).toContainText('Every account on this LocalAI, including new ones.')
    await add.getByRole('button', { name: 'Share with everyone' }).click()
    const row = dialog.locator('[data-testid="share-grant"][data-user="@everyone"]')
    await expect(row).toHaveAttribute('data-role', 'use')
    await expect(row).toContainText('Everyone')
    await expect(dialog.getByTestId('share-everyone-add')).toHaveCount(0)
    expect(server.requests.find(r => r.method === 'PUT').path).toBe('/api/shares/example:note/r0123456789abcdef0123456789abcde/grants/@everyone')
    await dialog.getByRole('button', { name: 'Stop sharing with Everyone' }).click()
    await dialog.getByRole('group', { name: 'Stop sharing with Everyone' }).getByRole('button', { name: 'Stop sharing' }).click()
    await expect(dialog.getByTestId('share-everyone-add')).toBeVisible()
  })

  test('a search shows that it is running, and a refused change says why and keeps the dialog', async ({ page }) => {
    await stubShares(page, { searchDelay: 800, fail: { 'grants/u-alice': { status: 503, type: 'outcome_unknown' } } })
    await openCatalog(page)
    const dialog = await openShare(page)
    await dialog.getByLabel('Find a person by name').fill('ali')
    await expect(dialog.getByTestId('share-searching')).toBeVisible()
    await dialog.getByTestId('share-user').filter({ hasText: 'Alice Martin' }).getByRole('button').click()
    await expect(dialog.getByTestId('share-error')).toHaveText('The change may not have been saved. Try again to be sure.')
    await expect(dialog.getByTestId('share-no-grants')).toBeVisible()
  })

  test('the grants that fail to load offer a retry; a denial is not retried', async ({ page }) => {
    // Down until the owner comes back, whatever the number of reads.
    let up = false
    await page.route(`${BASE}/grants`, route => {
      return !up
        ? route.fulfill({ status: 503, json: { error: { message: 'down' } } })
        : route.fulfill({ json: { grants: [{ userId: 'u-alice', displayName: 'Alice Martin', role: 'use' }] } })
    })
    await openCatalog(page)
    const dialog = await openShare(page)
    await expect(dialog.getByTestId('share-error')).toContainText('Sharing did not answer. Try again in a moment.')
    up = true
    await dialog.getByRole('button', { name: 'Retry' }).click()
    await expect(dialog.getByTestId('share-grant')).toHaveCount(1)
    await page.unroute(`${BASE}/grants`)
    await page.route(`${BASE}/grants`, route => route.fulfill({ status: 403, json: { error: { type: 'addon_error', message: 'denied' } } }))
    await dialog.getByRole('button', { name: 'Close' }).last().click()
    const again = await openShare(page)
    await expect(again.getByTestId('share-error')).toHaveText('You can no longer share this. It may have been deleted.')
    await expect(again.getByRole('button', { name: 'Retry' })).toHaveCount(0)
  })

  test('local-owner mode says sharing is not available and calls nothing', async ({ page }) => {
    const server = await stubShares(page)
    await openCatalog(page)
    const dialog = await openShare(page, 'local')
    await expect(dialog.getByTestId('share-disabled')).toContainText('Sharing is not available')
    await expect(dialog.getByLabel('Find a person by name')).toHaveCount(0)
    await page.waitForTimeout(300)
    expect(server.requests).toHaveLength(0)
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
  })

  test('with references, nothing is shared until every one has a choice, and the choices reach the page', async ({ page }) => {
    const server = await stubShares(page)
    await openCatalog(page)
    const dialog = await openShare(page, 'refs')
    await expect(dialog.getByTestId('share-ref')).toHaveCount(2)
    await expect(dialog.getByTestId('share-refs-pending')).toBeVisible()
    await expect(dialog.getByRole('button', { name: 'Share with everyone' })).toBeDisabled()
    await dialog.getByLabel('Find a person by name').fill('car')
    const add = dialog.getByTestId('share-user').filter({ hasText: 'Carla Rossi' }).getByRole('button')
    await expect(add).toBeDisabled()
    await dialog.getByLabel('Access to Product docs').selectOption('view')
    await expect(add).toBeDisabled()
    // A connection offers only use or nothing.
    await expect(dialog.getByLabel('Access to Team chat').locator('option[value="view"]')).toHaveCount(0)
    await dialog.getByLabel('Access to Team chat').selectOption('none')
    await expect(dialog.getByTestId('share-refs-pending')).toHaveCount(0)
    await add.click()
    await expect(dialog.getByTestId('share-notice')).toHaveText('Carla Rossi now has access.')
    expect(server.requests.filter(r => r.method === 'PUT')).toHaveLength(1)
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('cat-share-choices')).toHaveText(JSON.stringify({ principal: 'u-carla', role: 'use', choices: { c1: 'view', k1: 'none' } }))
  })

  test('Tab stays inside the dialog and Escape returns the focus to the opener', async ({ page }) => {
    await stubShares(page, { grants: [{ userId: 'u-alice', displayName: 'Alice Martin', role: 'use' }] })
    await openCatalog(page)
    const dialog = await openShare(page)
    await expect(dialog.getByTestId('share-grant')).toHaveCount(1)
    for (let i = 0; i < 12; i++) {
      await page.keyboard.press('Tab')
      expect(await dialog.evaluate(el => el.contains(document.activeElement))).toBe(true)
    }
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('cat-share-open')).toBeFocused()
  })

  test('the sharing line of a list row', async ({ page }) => {
    await openCatalog(page)
    await expect(page.getByTestId('cat-shared-owned')).toContainText('Owner')
    await expect(page.getByTestId('cat-shared-owned')).toContainText('Shared with 2')
    await expect(page.getByTestId('cat-shared-private')).toContainText('Private')
    await expect(page.getByTestId('cat-shared-by')).toContainText('Can use')
    await expect(page.getByTestId('cat-shared-by')).toContainText('Shared by Alice')
    await expect(page.getByTestId('cat-shared-everyone')).toContainText('Shared with everyone by Alice')
  })

  test.describe('screenshots', () => {
    test.skip(!SHOTS, 'set SHARE_SHOTS_DIR to save the share dialog screenshots')
    for (const theme of ['light', 'dark']) {
      for (const [size, viewport] of [['desktop', { width: 1280, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
        test(`share dialog states, ${theme}, ${size}`, async ({ page }) => {
          await page.setViewportSize(viewport)
          // Each shot waits for the dialog's entry motion to end.
          const shot = async (name) => {
            await page.evaluate(() => Promise.all(document.getAnimations()
              .filter(a => a.effect?.getTiming().iterations !== Infinity && a.effect?.target?.closest?.('.dk-veil'))
              .map(a => a.finished.catch(() => {}))))
            await page.screenshot({ path: `${SHOTS}/share-${name}-${theme}-${size}.png` })
          }
          const server = await stubShares(page, { searchDelay: 60_000 })
          await openCatalog(page, { theme })
          await page.evaluate(() => document.fonts.ready)

          let dialog = await openShare(page)
          await expect(dialog.getByTestId('share-no-grants')).toBeVisible()
          await shot('empty')

          server.grants.push({ userId: 'u-alice', displayName: 'Alice Martin', role: 'manage' }, { userId: 'u-bob', displayName: 'Bob Okafor', role: 'use' }, { userId: '@everyone', role: 'use' })
          await page.keyboard.press('Escape')
          dialog = await openShare(page)
          await expect(dialog.getByTestId('share-grant')).toHaveCount(3)
          await shot('people')
          await dialog.getByRole('button', { name: 'Stop sharing with Bob Okafor' }).click()
          await shot('revoke-confirm')
          await dialog.getByRole('button', { name: 'Cancel' }).click()

          await dialog.getByLabel('Find a person by name').fill('car')
          await expect(dialog.getByTestId('share-searching')).toBeVisible()
          await shot('searching')
          await page.keyboard.press('Escape')

          await page.route(`${BASE}/grants`, route => route.fulfill({ status: 503, json: { error: { message: 'down' } } }))
          dialog = await openShare(page)
          await expect(dialog.getByTestId('share-error')).toBeVisible()
          await shot('error')
          await page.keyboard.press('Escape')
          await page.unroute(`${BASE}/grants`)

          dialog = await openShare(page, 'local')
          await expect(dialog.getByTestId('share-disabled')).toBeVisible()
          await shot('local-owner')
          await page.keyboard.press('Escape')

          server.grants.splice(0)
          dialog = await openShare(page, 'refs')
          await expect(dialog.getByTestId('share-no-grants')).toBeVisible()
          await dialog.getByLabel('Access to Product docs').selectOption('view')
          await shot('references')
        })
      }
    }
  })
})
