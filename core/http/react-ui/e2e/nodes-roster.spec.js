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

  // A single-node server does not register the cluster routes at all, so the
  // real answer is 404; 503 is what they say when mounted without a registry.
  // Either way the page is about this host, with the distributed setup one
  // click away rather than the whole page.
  for (const status of [404, 503]) {
    test(`shows this machine when the cluster API answers ${status}`, async ({ page }) => {
      await page.route('**/api/nodes', route => route.fulfill({ status, body: 'unavailable' }))
      await page.goto('/app/nodes')
      await expect(page.getByTestId('local-machine')).toBeVisible({ timeout: 15_000 })
      await expect(page.getByText('No workers registered yet')).toHaveCount(0)
      await expect(page.getByTestId('scale-out')).toHaveCount(0)
      await page.getByRole('button', { name: 'Add machines' }).click()
      await expect(page.getByTestId('scale-out')).toContainText('Distributed mode is not enabled')
    })
  }
})

test.describe('Nodes join command', () => {
  test('preserves the NATS connection in both worker command forms', async ({ page }) => {
    await mockCluster(page, [])
    await page.goto('/app/nodes')

    await page.getByRole('radio', { name: /^Backend$/ }).click()
    const backendCli = page.locator('.p2p-cmd pre').first()
    await expect(backendCli).toContainText('local-ai worker', { timeout: 15_000 })
    await expect(backendCli).toContainText('--nats-url "nats://nats:4222"')
    const backendDocker = page.locator('.p2p-cmd pre').nth(1)
    await expect(backendDocker).toContainText('LOCALAI_REGISTER_TO')
    await expect(backendDocker).toContainText('LOCALAI_NATS_URL="nats://nats:4222"')

    await page.getByRole('radio', { name: /^Agent$/ }).click()
    const agentCli = page.locator('.p2p-cmd pre').first()
    await expect(agentCli).toContainText('local-ai agent-worker', { timeout: 15_000 })
    await expect(agentCli).toContainText('--register-to',
    )
    await expect(agentCli).toContainText('--nats-url "nats://nats:4222"')
    const agentDocker = page.locator('.p2p-cmd pre').nth(1)
    await expect(agentDocker).toContainText('LOCALAI_REGISTER_TO')
    await expect(agentDocker).toContainText('LOCALAI_NATS_URL="nats://nats:4222"')
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
    // The disabled-state card starts the frontend. Worker commands appear
    // after distributed mode is enabled and are covered by the test above.
    const frontendCommand = card.locator('.p2p-cmd pre')
    await expect(frontendCommand).toHaveCount(1)
    await expect(frontendCommand).toContainText('local-ai run --distributed')
    await expect(frontendCommand).toContainText('--auth-database-url')
    await expect(frontendCommand).not.toContainText('--nats-url')
  })
})
