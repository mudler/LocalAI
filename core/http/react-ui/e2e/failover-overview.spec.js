import { test, expect } from './coverage-fixtures.js'
import { adminItem } from './admin-nav.js'
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

  test('admin reaches Failover from the Models and runtime group', async ({ page }) => {
    await mockAuth(page)
    await mockFailover(page, [CHAIN_A])
    await page.goto('/app/backends')

    const link = adminItem(page, '/app/failover')
    await expect(link).toBeVisible({ timeout: 10_000 })
    await expect(link).toContainText('Failover')
    await link.click()
    await expect(link).toHaveAttribute('aria-current', 'page')
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

    await expect(page.locator('.ui-empty')).toBeVisible({ timeout: 10_000 })
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

  test('is the Failover view of Placement, and no longer a Models and runtime page', async ({ page }) => {
    await mockSwarm(page, { chains: [CHAIN_A] })
    await page.goto('/app/failover')
    await expect(page).toHaveURL(/\/app\/scheduling\/failover$/)
    await expect(adminItem(page, '/app/scheduling')).toHaveAttribute('aria-current', 'page', { timeout: 15_000 })
    await expect(page.getByTestId('placement-view-failover')).toHaveAttribute('aria-current', 'page')
    await page.goto('/app/backends')
    await expect(adminItem(page, '/app/failover')).toHaveCount(0)
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

  test('shows how the lost node was connected, from what it reported', async ({ page }) => {
    const nodes = clusterNodes().map(node => (node.id === 'n-gpu2' ? { ...node, attached: 'tunnel', peer_id: '12D3KooWQx8tR2vN6mHcR8yLdQe5Gh6iKoPzTz1', tunnel_transport: 'p2p', tunnel_path: 'relayed', tunnel_limited: true, route_state: [{ route: 'heartbeat', state: 'down', reason: 'the heartbeat is stale' }, { route: 'tunnel', transport: 'p2p', state: 'down', reason: 'the tunnel is gone' }] } : node))
    await mockSwarm(page, { nodes, chains: [] })
    await page.goto('/app/failover')
    const connections = page.getByTestId('failover-lost-connections')
    await expect(connections).toContainText('gpu-box-2', { timeout: 15_000 })
    await expect(connections.getByTestId('connections')).toContainText('P2P')
    await expect(connections.getByTestId('connections')).toContainText('the tunnel is gone')
  })

  test('a lost node that reported nothing about its connection shows no connection line', async ({ page }) => {
    const nodes = clusterNodes().map(node => { const { route_state: _unused, ...rest } = node; return rest })
    await mockSwarm(page, { nodes, chains: [] })
    await page.goto('/app/failover')
    await expect(page.getByTestId('failover-lost')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('failover-lost-connections')).toHaveCount(0)
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

// Placement: the tabs are real views, the one primary action is the header's
// New rule, and a rule's routing policy is called a policy, never a route.
test.describe('Placement page', () => {
  test.beforeEach(async ({ page }) => {
    await mockAuth(page)
    page.on('pageerror', (err) => { throw new Error(`uncaught page error: ${err.message}`) })
  })

  test('the Rules and Failover tabs switch the view and the address', async ({ page }) => {
    await mockSwarm(page, { chains: [] })
    await page.goto('/app/scheduling')
    await expect(page.getByTestId('placement-rules')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('placement-view-rules')).toHaveAttribute('aria-current', 'page')
    await page.getByTestId('placement-view-failover').click()
    await expect(page).toHaveURL(/\/app\/scheduling\/failover$/)
    await expect(page.getByTestId('failover-overview')).toBeVisible()
    await expect(page.getByTestId('placement-rules')).toHaveCount(0)
    await page.getByTestId('placement-view-rules').click()
    await expect(page).toHaveURL(/\/app\/scheduling$/)
  })

  test('New rule is the header action of the Rules view only, and opens the rule sheet', async ({ page }) => {
    await mockSwarm(page, { chains: [] })
    await page.goto('/app/scheduling')
    const header = page.locator('.page-header')
    await expect(header.getByRole('button', { name: 'New rule' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('button', { name: 'New rule' })).toHaveCount(1)
    await header.getByRole('button', { name: 'New rule' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await page.keyboard.press('Escape')
    await page.getByTestId('placement-view-failover').click()
    await expect(header.getByRole('button', { name: 'New rule' })).toHaveCount(0)
  })

  test('says policy where it means the routing policy of a rule', async ({ page }) => {
    await mockSwarm(page, { chains: [] })
    await page.goto('/app/scheduling')
    const rows = page.getByTestId('rule-row')
    await expect(rows.first()).toContainText('Its policy is prefix cache.', { timeout: 15_000 })
    await expect(rows.first()).toContainText('Prefix cache policy')
    await expect(rows.nth(1)).toContainText('Default policy')
    await expect(page.getByTestId('placement-rules')).not.toContainText(/routed|Default routing|routing$/i)
  })
})
