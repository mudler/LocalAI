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

test.describe('Operate overview headline', () => {
  const SUMMARY = {
    total: 18402, errors: 37, p95_ms: 842, window_hours: 24,
    buckets: Array.from({ length: 12 }, (_, i) => ({ count: 100 + i * 10, errors: i })),
  }

  test('reports counted totals rather than fetching the trace list', async ({ page }) => {
    let listCalls = 0
    await page.route('**/api/traces?**', route => { listCalls += 1; route.fulfill({ json: [] }) })
    await page.route('**/api/traces/summary', route => route.fulfill({ json: SUMMARY }))
    await mockQuiet(page)
    await page.goto('/app/operate')
    const headline = page.locator('.operate-headline')
    await expect(headline).toBeVisible()
    await expect(headline).toContainText('18,402')
    await expect(headline).toContainText('37')
    await expect(headline).toContainText('842')
    // The whole point of the endpoint: three numbers, not the buffer.
    expect(listCalls).toBe(0)
  })

  test('a quiet installation keeps the grid and says why it is empty', async ({ page }) => {
    // Hiding the grid removed the page's structure exactly when someone was
    // most likely to be looking at it, and "0 failed" is information.
    await page.route('**/api/traces/summary', route =>
      route.fulfill({ json: { total: 0, errors: 0, p95_ms: 0, window_hours: 24, buckets: [] } }))
    await mockQuiet(page)
    await page.goto('/app/operate')
    await expect(page.locator('.operate-headline')).toBeVisible()
    await expect(page.locator('.operate-headline__cell')).toHaveCount(4)
    await expect(page.locator('.operate-headline__note')).toBeVisible()
  })

  test('the sections state counts rather than listing their destinations', async ({ page }) => {
    await page.route('**/api/traces/summary', route =>
      route.fulfill({ json: { total: 18402, errors: 37, p95_ms: 842, window_hours: 24, buckets: [] } }))
    await mockQuiet(page)
    await page.goto('/app/operate')
    const runtime = page.locator('.lanes--sections .lane').first()
    await expect(runtime).toContainText('backends')
    await expect(runtime).toContainText('running')
  })

  test('shows host capacity from the shared Operate summary', async ({ page }) => {
    await page.route('**/api/resources', route => route.fulfill({
      json: {
        type: 'gpu',
        gpus: [{ name: 'NVIDIA L40S', vendor: 'NVIDIA', usage_percent: 42, used_vram: 10_000, total_vram: 24_000 }],
        aggregate: { gpu_count: 1 },
        storage_size: 12_000,
      },
    }))
    await mockQuiet(page)

    await page.goto('/app/operate')

    const capacity = page.locator('[data-testid="operate-capacity"]')
    await expect(capacity).toBeVisible()
    await expect(capacity).toContainText('Host capacity')
    await expect(capacity).toContainText('NVIDIA L40S')
    await expect(capacity).toContainText('42%')
  })

  test('states when host capacity is unavailable', async ({ page }) => {
    await page.route('**/api/resources', route => route.fulfill({
      status: 503,
      json: { error: 'resource monitor disabled' },
    }))
    await mockQuiet(page)

    await page.goto('/app/operate')

    const capacity = page.locator('[data-testid="operate-capacity"]')
    await expect(capacity).toContainText('Host capacity unavailable')
  })

  test('states when the host reports no capacity fields', async ({ page }) => {
    await page.route('**/api/resources', route => route.fulfill({ json: {} }))
    await mockQuiet(page)

    await page.goto('/app/operate')

    const capacity = page.locator('[data-testid="operate-capacity"]')
    await expect(capacity).toContainText('No capacity data reported')
  })
})
