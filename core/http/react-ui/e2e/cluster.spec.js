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
})
