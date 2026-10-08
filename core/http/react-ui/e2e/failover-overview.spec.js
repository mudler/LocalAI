import { test, expect } from './coverage-fixtures.js'
import { mockSwarm, clusterNodes } from './swarm-fixtures.js'

// Failover overview page (Operate -> Runtime -> Failover on a single install,
// Operate -> Swarm -> Failover on a cluster) and the matching
// "chain" badge on an installed model that is itself a failover chain.
//
// The overview is a dense table over GET /api/failover, kept live by the
// /api/failover/events SSE stream (same hook the Model Editor's chain strip
// uses), plus an empty state that points at the Failover Chain template.

const NO_AUTH = { authEnabled: false, staticApiKeyRequired: false, providers: [] }

const CHAIN_A = {
  name: 'chain-a',
  state: 'primary',
  active: 'a',
  active_since: '2026-09-26T09:00:00Z',
  pinned: null,
  targets: [
    { model: 'a', kind: 'local', warm: true, state: 'healthy', last_probe: '2026-09-26T09:59:00Z' },
    { model: 'b', kind: 'remote', warm: false, state: 'healthy' },
  ],
}

const CHAIN_B = {
  name: 'chain-b',
  state: 'degraded',
  active: 'x',
  active_since: '2026-09-26T08:00:00Z',
  pinned: null,
  targets: [
    { model: 'x', kind: 'local', warm: true, state: 'down' },
    { model: 'y', kind: 'local', warm: false, state: 'healthy' },
  ],
}

async function mockAuth(page, authStatus = NO_AUTH) {
  await page.route('**/api/auth/status', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify(authStatus) }))
}

async function mockFailover(page, chains) {
  await page.route('**/api/failover', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify({ chains }) }))
  await page.route('**/api/failover/events', (route) =>
    route.fulfill({
      status: 200,
      headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' },
      body: `event: snapshot\ndata: ${JSON.stringify({ chains })}\n\n`,
    }))
}

test.describe('Failover overview', () => {
  test.beforeEach(async ({ page }) => {
    page.on('pageerror', (err) => {
      throw new Error(`uncaught page error: ${err.message}`)
    })
  })

  test('admin reaches Failover from the Runtime tab', async ({ page }) => {
    await mockAuth(page)
    await mockFailover(page, [CHAIN_A])
    await page.goto('/app/backends')

    const link = page.locator('.hub-subnav a[href="/app/failover"]')
    await expect(link).toBeVisible({ timeout: 10_000 })
    await expect(link).toContainText('Failover')
    await link.click()
    await expect(page.locator('.dk-hubtabs [data-hub-tab="runtime"]')).toHaveAttribute('aria-current', 'page')
  })

  test('lists chains from GET /api/failover with their live health', async ({ page }) => {
    await mockAuth(page)
    await mockFailover(page, [CHAIN_A, CHAIN_B])
    await page.goto('/app/failover')

    const rows = page.locator('[data-testid="failover-overview"] tbody tr')
    await expect(rows).toHaveCount(2)

    const first = rows.nth(0)
    await expect(first.getByRole('link', { name: 'chain-a' })).toHaveAttribute('href', '/app/model-editor/chain-a')
    await expect(first.locator('.status-pill').first()).toHaveText(/primary/i)
    await expect(first).toContainText('a')
    await expect(first.locator('.failover-overview__targets .status-pill')).toHaveCount(2)

    const second = rows.nth(1)
    await expect(second.getByRole('link', { name: 'chain-b' })).toHaveAttribute('href', '/app/model-editor/chain-b')
    await expect(second.locator('.status-pill').first()).toHaveText(/degraded/i)
  })

  test('follows target.state over SSE without a page reload', async ({ page }) => {
    await mockAuth(page)
    await page.route('**/api/failover', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ chains: [CHAIN_A] }) }))
    const sseBody =
      `event: snapshot\ndata: ${JSON.stringify({ chains: [CHAIN_A] })}\n\n` +
      'event: target.state\ndata: {"type":"target.state","chain":"chain-a","target":"b","from":"healthy","to":"down","error":"dial tcp: refused"}\n\n'
    await page.route('**/api/failover/events', (route) =>
      route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' }, body: sseBody }))
    await page.goto('/app/failover')

    const row = page.locator('[data-testid="failover-overview"] tbody tr').first()
    await expect(row).toBeVisible({ timeout: 10_000 })
    const targetB = row.locator('.failover-overview__targets .status-pill', { hasText: 'b' })
    await expect(targetB).toHaveClass(/status-pill--error/)
  })

  test('a single install shows the chains and none of the node sections', async ({ page }) => {
    await mockAuth(page)
    await mockFailover(page, [CHAIN_A])
    await page.goto('/app/failover')
    await expect(page.locator('[data-testid="failover-overview"] tbody tr')).toHaveCount(1)
    await expect(page.getByText('When a node stops answering')).toHaveCount(0)
    await expect(page.getByTestId('failover-try')).toHaveCount(0)
  })

  test('shows an empty state pointing at the Failover Chain template', async ({ page }) => {
    await mockAuth(page)
    await mockFailover(page, [])
    await page.goto('/app/failover')

    await expect(page.locator('.dk-empty')).toBeVisible({ timeout: 10_000 })
    const cta = page.getByRole('link', { name: /create a failover chain/i })
    await expect(cta).toHaveAttribute('href', '/app/model-editor?template=failover')
  })
})

test.describe('Installed Models - chain badge', () => {
  test.beforeEach(async ({ page }) => {
    await mockAuth(page)
    await page.route('**/api/models/capabilities', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data: [
        { id: 'chain-a', capabilities: ['chat'], backend: 'llama-cpp' },
      ] }) }))
    await page.route('**/api/aliases', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify([]) }))
    await mockFailover(page, [CHAIN_A])
  })

  test('renders a read-only chain -> target badge on a chain model', async ({ page }) => {
    await page.goto('/app/models?view=installed')
    await page.locator('[data-entity="chain-a"]').click()
    await expect(page.getByText('chain → a')).toBeVisible({ timeout: 10_000 })
  })
})

// With distributed mode on, Failover is a Swarm page. It keeps the chains and
// adds what the router and the health monitor do when a worker stops answering,
// and works out from the loaded replicas and the rules what would stop if a
// node went. The second half is a preview computed in the browser.
test.describe('Failover on a cluster', () => {
  test.beforeEach(async ({ page }) => {
    page.on('pageerror', (err) => { throw new Error(`uncaught page error: ${err.message}`) })
  })

  test('is a Swarm page, and no longer a Runtime one', async ({ page }) => {
    await mockSwarm(page, { chains: [CHAIN_A] })
    await page.goto('/app/failover')
    await expect(page.locator('.dk-hubtabs [data-hub-tab="swarm"]')).toHaveAttribute('aria-current', 'page', { timeout: 15_000 })
    await expect(page.locator('.hub-subnav a[href="/app/failover"]')).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('.dk-hubtabs [data-hub-tab="runtime"]')).not.toHaveAttribute('aria-current', 'page')
    await page.goto('/app/backends')
    await expect(page.locator('.hub-subnav a[href="/app/failover"]')).toHaveCount(0)
  })

  test('says what happens when a node stops answering, in four lines', async ({ page }) => {
    await mockSwarm(page, { chains: [CHAIN_A] })
    await page.goto('/app/failover')
    const section = page.getByRole('region', { name: 'When a node stops answering' })
    await expect(section.locator('dt')).toHaveText(['Declared not answering', 'Requests for its models', 'Replicas it was running', 'When it comes back'], { timeout: 15_000 })
    await expect(section).toContainText('After 5 minutes without a heartbeat by default')
    await expect(section).toContainText('timings are server settings that this page does not read')
  })

  test('names the node that is not answering and links to it', async ({ page }) => {
    await mockSwarm(page, { chains: [] })
    await page.goto('/app/failover')
    const banner = page.getByTestId('failover-lost')
    await expect(banner).toContainText('gpu-box-2 is not answering.', { timeout: 15_000 })
    await expect(banner.getByRole('link', { name: 'Open node' })).toHaveAttribute('href', '/app/nodes/n-gpu2')
  })

  test('shows no lost-node banner when every node answers', async ({ page }) => {
    const nodes = clusterNodes().map(node => (node.status === 'unhealthy' ? { ...node, status: 'healthy' } : node))
    await mockSwarm(page, { nodes, chains: [] })
    await page.goto('/app/failover')
    await expect(page.getByRole('region', { name: 'When a node stops answering' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('failover-lost')).toHaveCount(0)
  })

  test('works out what would stop if a node went away, labelled as a preview', async ({ page }) => {
    await mockSwarm(page, { chains: [] })
    await page.goto('/app/failover')
    const section = page.getByTestId('failover-try')
    await expect(section).toContainText('Preview', { timeout: 15_000 })
    // It starts on the healthy node that holds the most models.
    await expect(section.getByRole('radio', { name: 'gpu-box-1' })).toHaveAttribute('aria-checked', 'true')
    await expect(section.getByTestId('leave-list')).toContainText('3 requests were in flight on this node and cannot finish')
    await expect(section.getByTestId('leave-list')).toContainText('llama-3.3-70b-q4 is unavailable until this node returns')
    await expect(section).toContainText('The server does not compute it')

    await section.getByRole('radio', { name: 'edge-cpu' }).click()
    await expect(section.getByTestId('leave-list')).toContainText('bge-m3 is unavailable until this node returns')
    await expect(section.getByTestId('leave-list')).not.toContainText('requests were in flight')
  })

  test('lists models that run on one replica and links each to a rule for two', async ({ page }) => {
    await mockSwarm(page, { chains: [] })
    await page.goto('/app/failover')
    const single = page.getByTestId('failover-single')
    await expect(single).toContainText('4 models run on a single replica', { timeout: 15_000 })
    await expect(single.getByRole('link', { name: 'Run 2 replicas' }).first()).toHaveAttribute('href', '/app/scheduling?new=bge-m3&min=2')
    await single.getByRole('link', { name: 'Run 2 replicas' }).first().click()
    await expect(page.getByTestId('rule-sheet')).toBeVisible()
    await expect(page.getByLabel('Min replicas')).toHaveValue('2')
  })

  test('says the preview cannot be worked out when the replicas cannot be read', async ({ page }) => {
    await mockSwarm(page, { replicas: null, chains: [] })
    await page.goto('/app/failover')
    await expect(page.getByTestId('failover-try')).toContainText('cannot be worked out now', { timeout: 15_000 })
  })

  test('fits a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockSwarm(page, { chains: [CHAIN_A] })
    await page.goto('/app/failover')
    await expect(page.getByTestId('failover-try')).toBeVisible({ timeout: 15_000 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  })
})
