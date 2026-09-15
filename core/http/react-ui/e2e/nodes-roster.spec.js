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
  // component. Neither worker kind dials a message bus any more: each holds one
  // outward tunnel to --register-to and takes every verb on it. A join command
  // carrying --nats-url would tell an operator to stand up, secure and pay for a
  // broker that nothing in the deployment connects to, which is the one way this
  // migration can still cost money after the code stopped using it.
  //
  // Asserted on the RENDERED command text rather than on the component's
  // variables, because the variables are what the fix deletes: a spec reading
  // them would stop compiling instead of failing, and a compile error is not
  // evidence about what an operator is shown.
  test('emits no bus flag for either worker kind', async ({ page }) => {
    await mockCluster(page, [])
    await page.goto('/app/nodes')

    await page.getByRole('radio', { name: /^Backend$/ }).click()
    const backendCli = page.locator('.p2p-cmd pre').first()
    await expect(backendCli).toContainText('local-ai worker', { timeout: 15_000 })
    await expect(backendCli).not.toContainText('--nats-url')
    const backendDocker = page.locator('.p2p-cmd pre').nth(1)
    await expect(backendDocker).toContainText('LOCALAI_REGISTER_TO')
    await expect(backendDocker).not.toContainText('LOCALAI_NATS_URL')

    // The agent tab is the one that regressed: it was the last surface still
    // emitting the flag, and it kept emitting it for two tasks after the agent
    // worker stopped dialling.
    await page.getByRole('radio', { name: /^Agent$/ }).click()
    const agentCli = page.locator('.p2p-cmd pre').first()
    await expect(agentCli).toContainText('local-ai agent-worker', { timeout: 15_000 })
    await expect(agentCli).toContainText('--register-to',
    )
    await expect(agentCli).not.toContainText('--nats-url')
    const agentDocker = page.locator('.p2p-cmd pre').nth(1)
    await expect(agentDocker).toContainText('LOCALAI_REGISTER_TO')
    await expect(agentDocker).not.toContainText('LOCALAI_NATS_URL')
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
