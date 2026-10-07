import { test, expect } from './coverage-fixtures.js'

// Failover overview page (Operate -> Runtime -> Failover) and the matching
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

  test('shows an empty state pointing at the Failover Chain template', async ({ page }) => {
    await mockAuth(page)
    await mockFailover(page, [])
    await page.goto('/app/failover')

    await expect(page.locator('.empty-state')).toBeVisible({ timeout: 10_000 })
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
