import { test, expect } from './coverage-fixtures.js'
import { mockOperate } from './operate-fixtures.js'
import {
  addonRecord, failedOp, galleryItem, heldGrant, libraryItem, mockUpdates, runningOp, snapshotOf, updateAvailable,
} from './updates-fixtures.js'

// The count on the Updates tab and on the Operate item of the sidebar: what
// needs a person, from one shell-level provider.

const tabCount = (page) => page.getByTestId('updates-tab-count')
const itemBadge = (page) => page.getByTestId('operate-updates-badge')

async function setup(page, { items, auto = false, addons = [], grants, operations = [], addonAuto = false } = {}) {
  await mockOperate(page)
  return mockUpdates(page, {
    snapshot: snapshotOf(items, { autoUpdate: { backends: auto } }),
    addons,
    autoAddons: addonAuto,
    operations,
    ...(grants ? { grants } : {}),
  })
}

test.describe('Updates badge', () => {
  test('counts what needs a person, on the tab and on the Operate item', async ({ page }) => {
    await setup(page, {
      items: [galleryItem('a', '1', '2'), galleryItem('b', '1', '2'), libraryItem('c', '1', '2')],
      addons: [addonRecord('tables', 'Tables', updateAvailable('2.4.0'))],
      grants: { revision: 1, addons: [heldGrant('tables', 'Tables', '2.4.0')] },
    })
    await page.goto('/app/operate')
    // Three ready backends and one held add-on.
    await expect(tabCount(page)).toHaveText('4')
    await expect(itemBadge(page)).toContainText('4')
    await expect(itemBadge(page)).toHaveAttribute('title', '4 updates need you')
    await expect(itemBadge(page)).not.toHaveClass(/nav-badge--error/)
    await expect(tabCount(page)).toHaveAttribute('data-level', 'update')
  })

  test('the Operate item carries the count on a page outside Operate', async ({ page }) => {
    await setup(page, { items: [galleryItem('a', '1', '2')] })
    await page.goto('/app')
    await expect(itemBadge(page)).toContainText('1')
    await expect(itemBadge(page).locator('.sr-only')).toHaveText('1 update needs you')
  })

  test('an update that the automatic setting applies is not counted', async ({ page }) => {
    await setup(page, { items: [galleryItem('a', '1', '2'), libraryItem('c', '1', '2')], auto: true })
    await page.goto('/app/operate')
    await expect(page.locator('[data-hub-tab="updates"]')).toBeVisible()
    await expect(tabCount(page)).toHaveCount(0)
    await expect(itemBadge(page)).toHaveCount(0)
    // The page still lists them.
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('2 updates available')
  })

  test('the add-on setting covers add-ons only', async ({ page }) => {
    await setup(page, {
      items: [galleryItem('a', '1', '2')],
      addons: [addonRecord('notes', 'Notes', updateAvailable('2.0.0'))],
      addonAuto: true,
    })
    await page.goto('/app/operate')
    // The backend is counted (backends are not automatic); the add-on is not.
    await expect(tabCount(page)).toHaveText('1')
  })

  test('a failed update counts even when it is automatic, and turns the badge red', async ({ page }) => {
    await setup(page, { items: [galleryItem('whisper', '1', '2')], auto: true, operations: [] })
    await page.route('**/api/operations', route => route.fulfill({ json: { operations: [] } }))
    await page.goto('/app/operate')
    await expect(tabCount(page)).toHaveCount(0)
    // The operation of the automatic update failed: that needs a person.
    await page.unroute('**/api/operations')
    await page.route('**/api/operations', route => route.fulfill({ json: { operations: [failedOp('whisper', 'download interrupted', { jobID: 'j1' })] } }))
    await page.reload()
    await expect(tabCount(page)).toHaveText('1')
    await expect(tabCount(page)).toHaveAttribute('data-level', 'error')
  })

  test('running updates are not counted, and the count is gone when nothing needs anyone', async ({ page }) => {
    await setup(page, { items: [galleryItem('piper', '1', '2')], operations: [runningOp('piper')] })
    await page.goto('/app/operate')
    await expect(page.locator('[data-hub-tab="updates"]')).toBeVisible()
    await expect(tabCount(page)).toHaveCount(0)
  })

  test('failed operations keep the red count of the Operate item, as before', async ({ page }) => {
    await setup(page, { items: [galleryItem('a', '1', '2')], operations: [failedOp('x', 'boom', { jobID: 'jx', isBackend: false })] })
    await page.goto('/app')
    await expect(page.locator('.sidebar .nav-badge--error').first()).toHaveText('1')
    await expect(itemBadge(page)).toHaveCount(0)
  })

  test('one poll feeds everything: the Operate hub no longer reads the old upgrades route', async ({ page }) => {
    const state = await setup(page, { items: [galleryItem('a', '1', '2')] })
    const old = []
    page.on('request', req => { if (/\/api\/backends\/upgrades$/.test(req.url())) old.push(req.url()) })
    await page.goto('/app/operate')
    await expect(tabCount(page)).toHaveText('1')
    await page.waitForTimeout(1500)
    expect(state.reads).toBe(1)
    expect(old).toEqual([])
    // The Status page still names the backend update, from the same data.
    await expect(page.getByTestId('operate-attention-item').first()).toBeVisible()
  })

  test('a person who is not an administrator triggers no update request', async ({ page }) => {
    await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], hasUsers: true, registrationMode: 'approval', user: { id: 'u2', email: 'b@x', name: 'bob', role: 'user', provider: 'local', permissions: { chat: true } } } }))
    const seen = []
    page.on('request', req => { if (/\/api\/(backends\/updates|addons\/host-grants|addons\/dispatch)/.test(req.url())) seen.push(req.url()) })
    await page.goto('/app')
    await expect(page.locator('.sidebar')).toBeVisible()
    await page.waitForTimeout(800)
    expect(seen).toEqual([])
    await expect(itemBadge(page)).toHaveCount(0)
  })
})
