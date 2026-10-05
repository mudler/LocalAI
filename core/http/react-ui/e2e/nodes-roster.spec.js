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
