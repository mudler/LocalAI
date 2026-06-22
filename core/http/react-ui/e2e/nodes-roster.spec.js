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
