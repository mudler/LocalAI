import { test, expect } from './coverage-fixtures.js'

const ID = 'n1'
async function mockNode(page, overrides = {}) {
  await page.route(`**/api/nodes/${ID}`, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify({ id: ID, name: 'alpha', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy', total_vram: 24e9, available_vram: 12e9, total_disk: 100e9, available_disk: 40e9, cpu_logical_cores: 16, cpu_usage_percent: 25, cpu_load_1: 2.5, max_replicas_per_model: 1, labels: { env: 'prod' }, ...overrides }) }))
  await page.route(`**/api/nodes/${ID}/models`, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify([{ node_id: ID, model_name: 'llama-3.3', state: 'loaded', in_flight: 0, replica_index: 0 }]) }))
  await page.route(`**/api/nodes/${ID}/backends`, r => r.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify([{ name: 'llama-cpp', is_system: true, installed_at: '2026-06-01T00:00:00Z' }]) }))
}

test.describe('Node detail page', () => {
  test('renders sections for a node', async ({ page }) => {
    await mockNode(page)
    await page.goto(`/app/nodes/${ID}`)
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('heading', { name: 'alpha' })).toBeVisible()
    await expect(page.getByText('llama-3.3')).toBeVisible()
    await expect(page.getByText('llama-cpp')).toBeVisible()
    await expect(page.getByText('env=prod')).toBeVisible()
    await expect(page.getByText('25.0% of 16 cores')).toBeVisible()
    await expect(page.getByText('2.50 load (1m)')).toBeVisible()
    await expect(page.getByText('37.3 GB / 93.1 GB')).toBeVisible()
    await expect(page.getByRole('link', { name: 'Nodes' })).toHaveAttribute('href', '/app/nodes')
    await expect(page.getByRole('region', { name: 'Node resources' })).toBeVisible()
    await expect(page.getByRole('region', { name: 'Running models' })).toHaveClass(/fleet-workbench/)
    await expect(page.getByRole('region', { name: 'Installed backends' })).toHaveClass(/fleet-workbench/)

    await page.getByRole('button', { name: 'Actions for llama-3.3 replica 1' }).click()
    await page.getByRole('menuitem', { name: 'View logs' }).click()
    await expect(page).toHaveURL(/\/app\/node-backend-logs\/n1\/llama-3.3%230$/)
    await page.getByRole('link', { name: 'Back to alpha' }).click()
    await expect(page).toHaveURL(/\/app\/nodes\/n1$/)
  })

  test('is reachable by clicking a roster panel', async ({ page }) => {
    await page.route('**/api/nodes', r => r.fulfill({ status: 200, contentType: 'application/json',
      body: JSON.stringify([{ id: ID, name: 'alpha', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy' }]) }))
    await mockNode(page)
    await page.goto('/app/nodes')
    await page.getByRole('button', { name: 'Inspect alpha' }).click()
    await page.getByRole('link', { name: 'Open full node details' }).click()
    await expect(page).toHaveURL(new RegExp(`/app/nodes/${ID}$`))
  })

  for (const [status, action] of [['healthy', 'Drain'], ['draining', 'Resume'], ['unhealthy', null], ['offline', null], ['unknown', null]]) {
    test(`shows only the accepted lifecycle action for ${status} nodes`, async ({ page }) => {
      await mockNode(page, { status })
      await page.goto(`/app/nodes/${ID}`)
      await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 15_000 })
      await expect(page.getByRole('button', { name: /Approve/ })).toHaveCount(0)
      await expect(page.getByRole('button', { name: /Drain/ })).toHaveCount(action === 'Drain' ? 1 : 0)
      await expect(page.getByRole('button', { name: /Resume/ })).toHaveCount(action === 'Resume' ? 1 : 0)
      await page.getByRole('button', { name: 'Actions for alpha' }).click()
      await expect(page.getByRole('menuitem', { name: 'Remove node…' })).toBeVisible()
    })
  }

  test('approves a pending node and refreshes its lifecycle controls', async ({ page }) => {
    let status = 'pending'
    let approvalRequests = 0
    await page.route(`**/api/nodes/${ID}`, r => r.fulfill({ status: 200, contentType: 'application/json',
      body: JSON.stringify({ id: ID, name: 'alpha', node_type: 'backend', status, labels: {} }) }))
    await page.route(`**/api/nodes/${ID}/models`, r => r.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await page.route(`**/api/nodes/${ID}/backends`, r => r.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await page.route(`**/api/nodes/${ID}/approve`, async r => {
      approvalRequests += 1
      status = 'healthy'
      await r.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    })
    await page.goto(`/app/nodes/${ID}`)

    await page.getByRole('button', { name: 'Approve' }).click()
    await expect.poll(() => approvalRequests).toBe(1)
    await expect(page.getByText('Node approved')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Drain' })).toBeVisible()
    await page.getByRole('button', { name: 'Actions for alpha' }).click()
    await expect(page.getByRole('menuitem', { name: 'Remove node…' })).toBeVisible()
  })

  test('renders valid totals with missing available capacity as No data', async ({ page }) => {
    await mockNode(page, {
      total_vram: 24e9, available_vram: undefined,
      total_ram: 32e9, available_ram: undefined,
      total_disk: 100e9, available_disk: undefined,
    })
    await page.goto(`/app/nodes/${ID}`)
    await expect(page.locator('.node-detail__metrics')).toContainText('VRAM')
    await expect(page.locator('.node-detail__metrics')).toContainText('RAM')
    await expect(page.locator('.node-detail__metrics')).toContainText('Models disk free')
    await expect(page.locator('.node-detail__metrics').getByText('No data')).toHaveCount(3)
  })

  test('keeps model operations visible on a narrow screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockNode(page)
    await page.goto(`/app/nodes/${ID}`)

    const action = page.getByRole('button', { name: 'Actions for llama-3.3 replica 1' })
    await expect(action).toBeVisible()
    const box = await action.boundingBox()
    expect(box.x + box.width).toBeLessThanOrEqual(390)
  })

  test('distinguishes a load failure from a missing node and retries in place', async ({ page }) => {
    let attempts = 0
    await page.route(`**/api/nodes/${ID}`, route => {
      attempts += 1
      if (attempts === 1) return route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"controller unavailable"}' })
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ id: ID, name: 'alpha', node_type: 'backend', status: 'healthy', labels: {} }) })
    })
    await page.route(`**/api/nodes/${ID}/models`, route => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await page.route(`**/api/nodes/${ID}/backends`, route => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))

    await page.goto(`/app/nodes/${ID}`)
    const error = page.getByRole('alert')
    await expect(error).toContainText('Could not load this node')
    await expect(page.getByText('Node not found')).toHaveCount(0)
    await error.getByRole('button', { name: 'Retry' }).click()
    await expect(page.getByRole('heading', { name: 'alpha' })).toBeVisible()
    expect(attempts).toBe(2)
  })
})
