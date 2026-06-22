import { test, expect } from './coverage-fixtures.js'

const ID = 'n1'
async function mockNode(page) {
  await page.route(`**/api/nodes/${ID}`, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify({ id: ID, name: 'alpha', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy', total_vram: 24e9, available_vram: 12e9, max_replicas_per_model: 1, labels: { env: 'prod' } }) }))
  await page.route(`**/api/nodes/${ID}/models`, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify([{ node_id: ID, model_name: 'llama-3.3', state: 'loaded', in_flight: 0, replica_index: 0 }]) }))
  await page.route(`**/api/nodes/${ID}/backends`, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify([{ name: 'llama-cpp', type: 'system' }]) }))
}

test.describe('Node detail page', () => {
  test('renders sections for a node', async ({ page }) => {
    await mockNode(page)
    await page.goto(`/app/nodes/${ID}`)
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('alpha')).toBeVisible()
    await expect(page.getByText('llama-3.3')).toBeVisible()
    await expect(page.getByText('llama-cpp')).toBeVisible()
    await expect(page.getByText('env=prod')).toBeVisible()
  })

  test('is reachable by clicking a roster panel', async ({ page }) => {
    await page.route('**/api/nodes', r => r.fulfill({ status: 200, contentType: 'application/json',
      body: JSON.stringify([{ id: ID, name: 'alpha', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy' }]) }))
    await page.route('**/api/nodes/models', r => r.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await page.route('**/api/nodes/scheduling', r => r.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await mockNode(page)
    await page.goto('/app/nodes')
    await page.locator('.node-panel').filter({ hasText: 'alpha' }).getByText('alpha').click()
    await expect(page).toHaveURL(new RegExp(`/app/nodes/${ID}$`))
  })
})
