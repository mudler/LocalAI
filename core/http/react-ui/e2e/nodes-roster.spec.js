import { test, expect } from './coverage-fixtures.js'

async function mockCluster(page, nodes) {
  await page.route('**/api/nodes', r => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(nodes) }))
  await page.route('**/api/nodes/models', r => r.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
  await page.route('**/api/nodes/scheduling', r => r.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
}

test.describe('Nodes roster header', () => {
  test('shows a cluster pulse line and no stat-card grid', async ({ page }) => {
    await mockCluster(page, [
      { id: 'n1', name: 'alpha', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy' },
      { id: 'n2', name: 'beta', node_type: 'backend', address: '10.0.0.2:50051', status: 'draining' },
    ])
    await page.goto('/app/nodes')
    await expect(page.locator('.cluster-pulse')).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('.cluster-pulse')).toContainText('2 nodes')
    await expect(page.locator('.stat-grid')).toHaveCount(0)
  })

  test('shows an approval callout for pending nodes', async ({ page }) => {
    await mockCluster(page, [{ id: 'n3', name: 'gamma', node_type: 'backend', address: '10.0.0.3:50051', status: 'pending' }])
    await page.goto('/app/nodes')
    await expect(page.locator('.attention-callout')).toContainText('approval', { timeout: 15_000 })
  })
})

test.describe('Nodes roster panels', () => {
  test('shows used and total system RAM reported by a worker', async ({ page }) => {
    await mockCluster(page, [
      {
        id: 'n1',
        name: 'alpha',
        node_type: 'backend',
        address: '10.0.0.1:50051',
        status: 'healthy',
        total_ram: 8_000_000_000,
        available_ram: 3_000_000_000,
      },
    ])

    await page.goto('/app/nodes')
    await expect(page.locator('.node-panel').filter({ hasText: 'alpha' })).toContainText('RAM 4.7 GB / 7.5 GB', { timeout: 15_000 })
  })

  test('shows model chips without clicking and filters by type', async ({ page }) => {
    await page.route('**/api/nodes', r => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([
      { id: 'n1', name: 'alpha', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy' },
      { id: 'a1', name: 'agent-1', node_type: 'agent', address: '10.0.0.9:50051', status: 'healthy' },
    ]) }))
    await page.route('**/api/nodes/models', r => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([
      { node_id: 'n1', model_name: 'llama-3.3', state: 'loaded', in_flight: 2, replica_index: 0 },
    ]) }))
    await page.route('**/api/nodes/scheduling', r => r.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))

    await page.goto('/app/nodes')
    // model chip visible without any expand click
    await expect(page.locator('.node-panel').filter({ hasText: 'alpha' }).getByText('llama-3.3')).toBeVisible({ timeout: 15_000 })
    // segmented filter: Agent shows the agent node, hides the backend node
    await page.getByRole('radio', { name: /Agent/ }).click()
    await expect(page.getByText('agent-1')).toBeVisible()
    await expect(page.getByText('alpha')).toHaveCount(0)
  })
})

test.describe('Nodes join command', () => {
  // The panel emits BOTH the backend and the agent join command from one
  // component, so the bus flag has to differ per tab rather than be deleted.
  // Backend workers connect to no NATS server; agent workers still do.
  test('omits the NATS flag for a backend worker and keeps it for an agent worker', async ({ page }) => {
    await mockCluster(page, [])
    await page.goto('/app/nodes')

    await page.getByRole('radio', { name: /^Backend$/ }).click()
    const backendCli = page.locator('.p2p-cmd pre').first()
    await expect(backendCli).toContainText('local-ai worker', { timeout: 15_000 })
    await expect(backendCli).not.toContainText('--nats-url')
    const backendDocker = page.locator('.p2p-cmd pre').nth(1)
    await expect(backendDocker).not.toContainText('LOCALAI_NATS_URL')

    await page.getByRole('radio', { name: /^Agent$/ }).click()
    const agentCli = page.locator('.p2p-cmd pre').first()
    await expect(agentCli).toContainText('local-ai agent-worker', { timeout: 15_000 })
    await expect(agentCli).toContainText('--nats-url')
    const agentDocker = page.locator('.p2p-cmd pre').nth(1)
    await expect(agentDocker).toContainText('LOCALAI_NATS_URL')
  })

  test('does not advertise flags the CLI does not have', async ({ page }) => {
    // The "How to Enable Distributed Mode" card renders ONLY on the disabled
    // state, which the page enters when /api/nodes answers 503. Mocking a
    // healthy cluster here would assert absence against a card that was never
    // on the page.
    await page.route('**/api/nodes', r => r.fulfill({ status: 503, contentType: 'application/json', body: '{}' }))
    await page.route('**/api/nodes/models', r => r.fulfill({ status: 503, contentType: 'application/json', body: '{}' }))
    await page.route('**/api/nodes/scheduling', r => r.fulfill({ status: 503, contentType: 'application/json', body: '{}' }))
    await page.goto('/app/nodes')

    const card = page.locator('.p2p-enable')
    await expect(card).toBeVisible({ timeout: 15_000 })
    // --distributed-nats and --distributed-db were never real flags; a copied
    // command carrying them fails at kong before LocalAI does anything.
    await expect(card).not.toContainText('--distributed-nats')
    await expect(card).not.toContainText('--distributed-db')
    // And the worker step no longer tells an operator to point a backend
    // worker at a bus it does not dial.
    await expect(card.locator('.p2p-cmd pre').nth(1)).not.toContainText('--nats-url')
  })
})
