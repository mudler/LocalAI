import { test, expect } from './coverage-fixtures.js'

// The standalone P2P (Swarm) page was merged into the Cluster page: /app/p2p now
// redirects to /app/cluster, and the p2p content lives under the "Swarm (p2p)"
// section. That section only mounts when p2p is enabled (a network token is
// present), so we mock /api/p2p/token to return a non-empty token and assert the
// swarm content renders under the cluster page.
const P2P_TOKEN = 'test-network-token'

async function mockSwarmEnabled(page) {
  await page.route('**/api/p2p/token', (route) => {
    route.fulfill({
      status: 200,
      contentType: 'text/plain',
      body: P2P_TOKEN,
    })
  })
  await page.route('**/api/p2p/workers', (route) => {
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"nodes":[]}' })
  })
  await page.route('**/api/p2p/federation', (route) => {
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"nodes":[]}' })
  })
  await page.route('**/api/p2p/stats', (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        llama_cpp_workers: { online: 0, total: 0 },
        federated: { online: 0, total: 0 },
        mlx_workers: { online: 0, total: 0 },
      }),
    })
  })
  // The cluster page also probes /api/nodes for the distributed section; keep it
  // failing (distributed disabled) so only the swarm section renders here.
  await page.route('**/api/nodes', (route) => {
    route.fulfill({ status: 503, contentType: 'application/json', body: '{}' })
  })
}

test.describe('P2P (Swarm) section on the Cluster page', () => {
  test('the old /app/p2p route lands on the cluster page', async ({ page }) => {
    await mockSwarmEnabled(page)
    // /app/p2p redirects to /app/cluster.
    await page.goto('/app/p2p')
    await expect(page).toHaveURL(/\/app\/cluster$/)
    await expect(page.getByRole('heading', { name: /Cluster/i })).toBeVisible()
  })

  test('renders the Swarm (p2p) section when p2p is enabled', async ({ page }) => {
    await mockSwarmEnabled(page)
    await page.goto('/app/cluster')
    await expect(page).toHaveURL(/\/app\/cluster$/)

    // The collapsible swarm section is titled "Swarm (p2p)".
    await expect(page.getByText(/Swarm \(p2p\)/i)).toBeVisible()

    // The enabled p2p content (Network Token panel + the federation / sharding
    // tabs) is rendered inside the swarm section.
    await expect(page.getByRole('heading', { name: /Network Token/i })).toBeVisible()
    await expect(page.getByText('Federation', { exact: true })).toBeVisible()
    await expect(page.getByText('Model Sharding', { exact: true })).toBeVisible()
  })
})
