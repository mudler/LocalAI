import { test, expect } from './coverage-fixtures.js'

async function mockCluster(page, nodes) {
  await page.route('**/api/nodes', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(nodes) }))
}

test.describe('Nodes fleet roster', () => {
  test('uses the fleet response without prefetching models or backends', async ({ page }) => {
    const requests = []
    page.on('request', request => requests.push(request.url()))
    await mockCluster(page, [
      { id: 'n1', name: 'alpha', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy', model_count: 3 },
      { id: 'a1', name: 'agent-1', node_type: 'agent', address: '10.0.0.9:50051', status: 'draining', model_count: 0 },
    ])
    await page.goto('/app/nodes')
    await expect(page.getByRole('table', { name: 'Fleet nodes' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('tab', { name: 'Nodes' })).toHaveAttribute('aria-selected', 'true')
    await page.getByRole('tab', { name: 'Nodes' }).click()
    await expect(page.getByRole('row', { name: /alpha/ })).toContainText('3')
    expect(requests.some(url => url.includes('/api/nodes/models'))).toBe(false)
    expect(requests.some(url => /\/api\/nodes\/[^/]+\/backends/.test(url))).toBe(false)
  })

  test('preserves the empty worker setup experience', async ({ page }) => {
    await mockCluster(page, [])
    await page.goto('/app/nodes')
    await expect(page.getByText('No workers registered yet')).toBeVisible({ timeout: 15_000 })
  })

  test('preserves the distributed-disabled setup experience', async ({ page }) => {
    await page.route('**/api/nodes', route => route.fulfill({ status: 503, body: 'Service Unavailable' }))
    await page.goto('/app/nodes')
    await expect(page.getByText('Distributed Mode Not Enabled')).toBeVisible({ timeout: 15_000 })
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
