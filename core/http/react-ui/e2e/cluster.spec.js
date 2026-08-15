import { test, expect } from './coverage-fixtures.js'

// The Cluster page composes two capability sections: "Distributed (NATS)" (the
// former Nodes page) and "Swarm (p2p)" (the former P2P page). Each section only
// mounts when its mode is enabled — distributed when /api/nodes answers OK, swarm
// when a non-empty p2p network token is present. We mock those probes so the page
// renders against the standalone ui-test-server without NATS / p2p running.

async function mockDistributedOnly(page) {
  await page.route('**/api/nodes', (route) => {
    route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
  })
  await page.route('**/api/nodes/scheduling', (route) => {
    route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
  })
  // Swarm disabled: token probe fails, so the swarm section stays hidden.
  await page.route('**/api/p2p/token', (route) => {
    route.fulfill({ status: 503, contentType: 'text/plain', body: '' })
  })
}

test.describe('Cluster page', () => {
  test('shows the page title', async ({ page }) => {
    await mockDistributedOnly(page)
    await page.goto('/app/cluster')
    await expect(page).toHaveURL(/\/app\/cluster$/)
    await expect(page.getByRole('heading', { name: /Cluster/i })).toBeVisible()
  })

  test('shows the distributed section when /api/nodes responds', async ({ page }) => {
    await mockDistributedOnly(page)
    await page.goto('/app/cluster')
    await expect(page).toHaveURL(/\/app\/cluster$/)
    // The distributed capability section is titled "Distributed (NATS)".
    await expect(page.getByText(/Distributed \(NATS\)/i)).toBeVisible()
  })

  test('summarizes both transports and collapses a section', async ({ page }) => {
    await page.route('**/api/nodes', (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          { id: 'node-a', in_flight_count: 2 },
          { id: 'node-b', in_flight_count: 3 },
        ]),
      })
    })
    await page.route('**/api/nodes/scheduling', (route) => {
      route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    })
    await page.route('**/api/p2p/token', (route) => {
      route.fulfill({ status: 200, contentType: 'text/plain', body: 'test-network-token' })
    })
    await page.route('**/api/p2p/stats', (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          federated: { online: 1, total: 2 },
          llama_cpp_workers: { online: 2, total: 3 },
          mlx_workers: { online: 1, total: 1 },
        }),
      })
    })
    await page.route('**/api/p2p/workers', (route) => {
      route.fulfill({ status: 200, contentType: 'application/json', body: '{"nodes":[]}' })
    })
    await page.route('**/api/p2p/federation', (route) => {
      route.fulfill({ status: 200, contentType: 'application/json', body: '{"nodes":[]}' })
    })

    await page.goto('/app/cluster')

    await expect(page.getByText('Distributed nodes').locator('..').getByText('2')).toBeVisible()
    await expect(page.getByText('In-flight requests').locator('..').getByText('5')).toBeVisible()
    await expect(page.getByText('Swarm peers online').locator('..').getByText('4/6')).toBeVisible()

    const distributedToggle = page.getByRole('button', { name: /Distributed \(NATS\)/i })
    const nodeTypeFilter = page.getByRole('radiogroup', { name: 'Node type' })
    await expect(distributedToggle).toHaveAttribute('aria-expanded', 'true')
    await expect(nodeTypeFilter).toBeVisible()
    await distributedToggle.click()
    await expect(distributedToggle).toHaveAttribute('aria-expanded', 'false')
    await expect(nodeTypeFilter).toBeHidden()
  })
})
