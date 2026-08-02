import { test, expect } from './coverage-fixtures.js'

// Operate overview (src/pages/OperateOverview.jsx).
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
  await page.route('**/api/backends/upgrades', route =>
    route.fulfill({ json: upgrades }))
  await page.route('**/api/operations', route =>
    route.fulfill({ json: operations }))
  await page.route('**/api/nodes', route =>
    route.fulfill({ json: [{ id: 'node-a', status: 'healthy', healthy: true }] }))
}

test.describe('Operate overview', () => {
  test('Operate opens the overview, not whichever page happens to be first', async ({ page }) => {
    await mockQuiet(page)
    await page.goto('/app')
    await page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' }).click()
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

  test('a stale backend becomes an attention item naming the version jump', async ({ page }) => {
    await mockQuiet(page, { upgrades: ONE_UPGRADE })
    await page.goto('/app/operate')
    const item = page.locator(ITEM, { hasText: 'llama-cpp' })
    await expect(item).toBeVisible()
    await expect(item).toContainText('0.9.4')
    await expect(item).toContainText('0.9.7')
    await expect(page.locator(CLEAR)).toHaveCount(0)
  })

  test('a failed operation becomes an attention item', async ({ page }) => {
    await mockQuiet(page, {
      operations: [{ id: 'op-1', name: 'qwen3-8b', type: 'install', error: 'no space left on device' }],
    })
    await page.goto('/app/operate')
    await expect(page.locator(ITEM, { hasText: 'qwen3-8b' })).toBeVisible()
  })

  test('the rail reports backend updates alongside the label', async ({ page }) => {
    await mockQuiet(page, { upgrades: ONE_UPGRADE })
    await page.goto('/app/operate')
    const backends = page.locator('.console-rail a.nav-item[href="/app/backends"]')
    await expect(backends).toBeVisible()
    await expect(backends.locator('.nav-signal')).toContainText('1')
  })

  test('the rail groups Runtime, Cluster, Observability and Administration', async ({ page }) => {
    await mockQuiet(page)
    await page.goto('/app/operate')
    const rail = page.locator('.console-rail')
    for (const group of ['Runtime', 'Cluster', 'Observability', 'Administration']) {
      await expect(rail.locator('.console-group-title', { hasText: group })).toBeVisible()
    }
    // Six headings for thirteen items was the defect; the old pairs are gone.
    for (const gone of ['Inference', 'Access']) {
      await expect(rail.locator('.console-group-title', { hasText: new RegExp(`^${gone}$`) })).toHaveCount(0)
    }
  })

  test('regrouping does not change what a non-distributed host can see', async ({ page }) => {
    await page.route('**/api/features', route =>
      route.fulfill({ json: { distributed: false, agents: true, mcp: true } }))
    await mockQuiet(page)
    await page.goto('/app/operate')
    const rail = page.locator('.console-rail')
    await expect(rail.locator('a.nav-item[href="/app/backends"]')).toBeVisible()
    // Gating is the thing most likely to break silently when items move group.
    await expect(rail.locator('a.nav-item[href="/app/nodes"]')).toHaveCount(0)
    await expect(rail.locator('a.nav-item[href="/app/scheduling"]')).toHaveCount(0)
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
    let upgradeCalls = 0
    await page.route('**/api/backends/upgrades', route => {
      upgradeCalls += 1
      route.fulfill({ json: NO_UPGRADES })
    })
    await page.route('**/api/operations', route => route.fulfill({ json: [] }))
    await page.goto('/app/chat')
    await expect(page.locator('.sidebar')).toBeVisible()
    await page.waitForTimeout(1500)
    // Nobody asked for this data outside Operate; a dashboard-shaped poll on
    // every page is exactly what OperationsContext exists to avoid.
    expect(upgradeCalls).toBe(0)
  })
})
