import { test, expect } from './coverage-fixtures.js'
import { adminItem, adminGroup, ADMIN_NAV } from './admin-nav.js'
import { mockReadModel, mockOperate } from './operate-fixtures.js'
import { addonRecord, galleryItem, mockUpdates, snapshotOf, updateAvailable } from './updates-fixtures.js'

// Administration overview (src/pages/AdminOverview.jsx): the sidebar counts
// and the items of "Needs you". The cards and the row actions are in
// admin-overview.spec.js.
//
// The page exists to answer "is anything wrong" without visiting four other
// pages, so the tests are written against that behaviour rather than against
// the markup: what does it say when nothing is wrong, and does each source of
// trouble actually surface.

const OVERVIEW = '[data-testid="operate-overview"]'
const CLEAR = '[data-testid="operate-attention-clear"]'
const ITEM = '[data-testid="operate-attention-item"]'

const NO_UPGRADES = {}
const ONE_UPGRADE = {
  'llama-cpp': {
    backend_name: 'llama-cpp',
    installed_version: '0.9.4',
    available_version: '0.9.7',
  },
}

// A quiet installation: nothing running, nothing stale, every node healthy.
async function mockQuiet(page, { upgrades = NO_UPGRADES, operations = [] } = {}) {
  // The shell reads the update read model; the Status page names its items.
  await mockReadModel(page, upgrades)
  await page.route('**/api/operations', route =>
    route.fulfill({ json: operations }))
  await page.route('**/api/nodes', route =>
    route.fulfill({ json: [{ id: 'node-a', status: 'healthy', healthy: true }] }))
}

test.describe('Administration overview', () => {
  test('Administration opens the overview, not whichever page happens to be first', async ({ page }) => {
    await mockQuiet(page)
    await page.goto('/app')
    await page.locator('.sidebar-nav a.nav-item', { hasText: 'Administration' }).click()
    // Today this lands on /app/backends purely because Backends is the first
    // entry in operateConsole.groups — an ordering accident, not a decision.
    await expect(page).toHaveURL(/\/app\/operate$/)
    await expect(page.locator(OVERVIEW)).toBeVisible()
  })

  test('says so plainly when nothing needs attention', async ({ page }) => {
    await mockQuiet(page)
    await page.goto('/app/operate')
    await expect(page.locator(CLEAR)).toBeVisible()
    // The empty state is one line, not a panel full of reassuring green.
    await expect(page.locator(ITEM)).toHaveCount(0)
  })

  test('the backends with an update are one attention item that opens the Updates page', async ({ page }) => {
    await mockQuiet(page, { upgrades: { ...ONE_UPGRADE, whisper: { backend_name: 'whisper', installed_version: '1.7.2', available_version: '1.8.0' } } })
    let started = 0
    await page.route('**/api/backends/upgrade/*', route => { started += 1; return route.fulfill({ json: {} }) })
    await page.goto('/app/operate')
    const items = page.locator(ITEM)
    await expect(items).toHaveCount(1)
    const item = items.first()
    await expect(item).toContainText('2 updates available')
    await expect(item).toContainText('llama-cpp, whisper')
    await expect(page.locator(CLEAR)).toHaveCount(0)
    // It is a link: nothing starts here.
    await item.getByTestId('operate-review-updates').click()
    await expect(page).toHaveURL(/\/app\/updates\?filter=ready/)
    expect(started).toBe(0)
  })

  test('the update item counts and names add-ons with the backends, as the Updates page lists them', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, {
      snapshot: snapshotOf([galleryItem('llama-cpp', '0.9.4', '0.9.7')]),
      addons: [addonRecord('notes', 'Notes sync', updateAvailable('0.8.2'), { version: '0.8.1' })],
    })
    await page.goto('/app/operate')
    const item = page.locator(ITEM, { hasText: 'updates available' })
    await expect(item).toContainText('2 updates available')
    await expect(item).toContainText('llama-cpp')
    await expect(item).toContainText('Notes sync')
    await item.getByTestId('operate-review-updates').click()
    await expect(page).toHaveURL(/\/app\/updates\?filter=ready/)
    await expect(page.locator('[data-group="ready"] [data-kind="addon"]')).toHaveCount(1)
    await expect(page.locator('[data-group="ready"] [data-kind="gallery"]')).toHaveCount(1)
  })

  test('a failed operation becomes an attention item', async ({ page }) => {
    await mockQuiet(page, {
      operations: [{ id: 'op-1', name: 'qwen3-8b', type: 'install', error: 'no space left on device' }],
    })
    await page.goto('/app/operate')
    await expect(page.locator(ITEM, { hasText: 'qwen3-8b' })).toBeVisible()
  })

  test('the Updates item reports the updates that need a person, and Backends no longer does', async ({ page }) => {
    await mockQuiet(page, { upgrades: ONE_UPGRADE })
    await page.goto('/app/operate')
    const updates = adminItem(page, '/app/updates')
    await expect(updates).toBeVisible()
    await expect(updates.getByTestId('admin-signal-updates')).toContainText('1')
    await expect(adminItem(page, '/app/backends').locator('.nav-badge')).toHaveCount(0)
  })

  test('the Overview item carries the attention count and nothing else claims one', async ({ page }) => {
    await mockQuiet(page, { upgrades: ONE_UPGRADE })
    await page.goto('/app/operate')
    await expect(adminItem(page, '/app/operate').getByTestId('admin-signal-attention')).toContainText('1')
    await expect(adminItem(page, '/app/settings').locator('.nav-badge')).toHaveCount(0)
  })

  test('the sidebar offers the groups and their pages', async ({ page }) => {
    await mockQuiet(page)
    await page.goto('/app/operate')
    for (const path of ['/app/operate', '/app/nodes', '/app/backends', '/app/activity', '/app/updates', '/app/failover', '/app/settings']) {
      await expect(adminItem(page, path)).toBeVisible()
    }
    await expect(adminGroup(page, 'traffic').getByRole('button', { name: /Traffic/ })).toBeVisible()
  })

  test('regrouping does not change what a non-distributed host can see', async ({ page }) => {
    await page.route('**/api/features', route =>
      route.fulfill({ json: { distributed: false, agents: true, mcp: true } }))
    await mockQuiet(page)
    await page.goto('/app/operate')
    const bar = page.locator(ADMIN_NAV)
    await expect(bar.locator('a[href="/app/backends"]')).toBeVisible()
    // Gating is the thing most likely to break silently when items move.
    // The Nodes route stays reachable, but as "This machine": a single host is
    // not a cluster. The Cluster group only exists in distributed mode.
    await expect(bar.locator('a[href="/app/nodes"]')).toHaveCount(1)
    await expect(bar.locator('a[href="/app/nodes"]')).toContainText('This machine')
    await expect(adminGroup(page, 'cluster')).toHaveCount(0)
    await expect(bar.locator('a[href="/app/scheduling"]')).toHaveCount(0)
  })

  test('the sidebar keeps its operations badge', async ({ page }) => {
    // Regression guard: this change edits the same config the badge reads, and
    // the badge deliberately lives on the always-visible sidebar entry rather
    // than the collapsible rail.
    await mockQuiet(page, {
      operations: [{ id: 'op-1', name: 'qwen3-8b', type: 'install', progress: 40 }],
    })
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav .nav-badge')).toBeVisible()
  })

  test('does not poll the summary away from Operate', async ({ page }) => {
    let summaryCalls = 0
    await page.route('**/api/traces/summary', route => {
      summaryCalls += 1
      route.fulfill({ json: {} })
    })
    await page.route('**/api/operations', route => route.fulfill({ json: [] }))
    await page.goto('/app/chats')
    await expect(page.locator('.sidebar')).toBeVisible()
    await page.waitForTimeout(1500)
    // Nobody asked for this data outside Operate; a dashboard-shaped poll on
    // every page is exactly what OperationsContext exists to avoid.
    expect(summaryCalls).toBe(0)
  })
})
