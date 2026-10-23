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

  test('the Runtime tab reports backend updates beside the label', async ({ page }) => {
    await mockQuiet(page, { upgrades: ONE_UPGRADE })
    await page.goto('/app/operate')
    const runtime = page.locator('.dk-hubtabs [data-hub-tab="runtime"]')
    await expect(runtime).toBeVisible()
    await expect(runtime.locator('.dk-hubtab-attn')).toContainText('1')
  })

  test('the Status tab carries the attention count and nothing else claims one', async ({ page }) => {
    await mockQuiet(page, { upgrades: ONE_UPGRADE })
    await page.goto('/app/operate')
    await expect(page.locator('.dk-hubtabs [data-hub-tab="status"] .dk-hubtab-attn')).toContainText('1')
    await expect(page.locator('.dk-hubtabs [data-hub-tab="settings"] .dk-hubtab-attn')).toHaveCount(0)
    await expect(page.locator('.dk-hubtabs [data-hub-tab="settings"] .dk-hubtab-count')).toHaveCount(0)
  })

  test('the tab bar offers Status, Runtime, Traffic and Settings', async ({ page }) => {
    await mockQuiet(page)
    await page.goto('/app/operate')
    const bar = page.locator('.dk-hubtabs')
    for (const id of ['status', 'machine', 'runtime', 'traffic', 'settings']) {
      await expect(bar.locator(`[data-hub-tab="${id}"]`)).toBeVisible()
    }
    // The second row names the routes inside a tab.
    await page.goto('/app/backends')
    const sub = page.locator('.hub-subnav')
    for (const name of ['Backends', 'Activity', 'Failover']) {
      await expect(sub.getByRole('link', { name })).toBeVisible()
    }
  })

  test('regrouping does not change what a non-distributed host can see', async ({ page }) => {
    await page.route('**/api/features', route =>
      route.fulfill({ json: { distributed: false, agents: true, mcp: true } }))
    await mockQuiet(page)
    await page.goto('/app/operate')
    const bar = page.locator('.dk-hubtabs')
    await expect(bar.locator('a[href="/app/backends"]')).toBeVisible()
    // Gating is the thing most likely to break silently when items move.
    // The Nodes route stays reachable, but as "This machine": a single host is
    // not a cluster. Swarm only exists in distributed mode.
    await expect(bar.locator('a[href="/app/nodes"]')).toHaveCount(1)
    await expect(bar.locator('a[href="/app/nodes"]')).toContainText('This machine')
    await expect(bar.locator('[data-hub-tab="swarm"]')).toHaveCount(0)
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

test.describe('Operate overview ledger', () => {
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
    // Failed requests are a problem, so the row opens by itself.
    const row = page.getByTestId('operate-row-failures')
    await expect(row).toContainText('37 failed of 18,402 requests')
    await expect(row.getByTestId('operate-traffic')).toContainText('842 ms')
    // The whole point of the endpoint: three numbers, not the buffer.
    expect(listCalls).toBe(0)
  })

  test('a quiet installation keeps the row and says why it is empty', async ({ page }) => {
    // Hiding the row removed the page's structure exactly when someone was
    // most likely to be looking at it, and "0 failed" is information.
    await page.route('**/api/traces/summary', route =>
      route.fulfill({ json: { total: 0, errors: 0, p95_ms: 0, window_hours: 24, buckets: [] } }))
    await mockQuiet(page)
    await page.goto('/app/operate')
    const row = page.getByTestId('operate-row-failures')
    await expect(row).toBeVisible()
    await expect(row).toContainText('No requests recorded in the last 24 hours.')
    await expect(row.locator('.op-row__head')).toHaveAttribute('aria-expanded', 'false')
  })

  test('the Running now row states counts rather than listing destinations', async ({ page }) => {
    await mockQuiet(page, {
      operations: [{ id: 'op-1', name: 'qwen3-8b', type: 'install', progress: 40 }],
    })
    // A single node: the cluster API is not mounted.
    await page.route('**/api/features', route => route.fulfill({ json: { distributed: false, agents: true, mcp: true } }))
    await page.route('**/api/nodes', route => route.fulfill({ status: 404, json: { message: 'Not Found' } }))
    await page.route('**/system', route => route.fulfill({ json: { backends: [], loaded_models: [{ id: 'm1', backend: 'llama-cpp' }] } }))
    await page.goto('/app/operate')
    const row = page.getByTestId('operate-row-running')
    await expect(row).toContainText('1 operation in progress')
    await expect(row).toContainText('1 model loaded')
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

    const capacity = page.getByTestId('operate-row-capacity')
    await expect(capacity).toContainText('GPU memory')
    await expect(capacity).toContainText('42%')
    await expect(page.getByTestId('operate-capacity')).toContainText('GPU memory in use')
  })

  test('states when host capacity is unavailable', async ({ page }) => {
    await page.route('**/api/resources', route => route.fulfill({
      status: 503,
      json: { error: 'resource monitor disabled' },
    }))
    await mockQuiet(page)

    await page.goto('/app/operate')

    await expect(page.getByTestId('operate-row-capacity')).toContainText('Capacity is not reported by this host.')
    await expect(page.getByTestId('operate-capacity-unavailable')).toBeVisible()
  })

  test('states when the host reports no capacity fields', async ({ page }) => {
    await page.route('**/api/resources', route => route.fulfill({ json: {} }))
    await mockQuiet(page)

    await page.goto('/app/operate')

    await expect(page.getByTestId('operate-row-capacity')).toContainText('Capacity is not reported by this host.')
  })
})
