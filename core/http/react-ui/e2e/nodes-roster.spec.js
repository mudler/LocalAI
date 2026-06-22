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
