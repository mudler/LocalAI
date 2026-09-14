import { test, expect } from './coverage-fixtures.js'

const baseNodes = [
  { id: 'n1', name: 'atlas', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy', labels: { zone: 'east' }, total_vram: 100, available_vram: 40, total_ram: 200, available_ram: 100, total_disk: 1000, available_disk: 600, cpu_logical_cores: 8, cpu_usage_percent: 25, cpu_load_1: 1.5, model_count: 3, in_flight_count: 2, last_heartbeat: '2026-09-14T00:00:00Z' },
  { id: 'n2', name: 'borealis', node_type: 'backend', address: '10.0.0.2:50051', status: 'pending', labels: { zone: 'west' }, total_vram: 100, available_vram: 10, total_ram: 200, available_ram: 10, total_disk: 1000, available_disk: 100, cpu_logical_cores: 16, cpu_usage_percent: 50, cpu_load_1: 4, model_count: 1, in_flight_count: 0 },
  { id: 'n3', name: 'legacy', node_type: 'agent', address: '10.0.0.3:50051', status: 'offline', labels: {} },
]

async function mockNodes(page, nodes = baseNodes) {
  await page.route('**/api/nodes', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(nodes) }))
}

test.describe('Nodes fleet dashboard', () => {
  test('shows aggregate health, capacity, attention filtering, search, sorting, and grouping', async ({ page }) => {
    await mockNodes(page)
    await page.goto('/app/nodes')
    await expect(page.getByLabel('Fleet health summary')).toContainText('3 nodes', { timeout: 15_000 })
    await expect(page.getByLabel('VRAM capacity')).toContainText('150 B / 200 B')
    await expect(page.getByLabel('CPU capacity')).toContainText('10 busy / 24 cores')
    await expect(page.getByLabel('Models disk capacity')).toContainText('1.3 KB / 2 KB')
    await expect(page.getByRole('button', { name: /Needs attention.*2/ })).toBeVisible()
    await page.getByRole('button', { name: /Low VRAM/ }).click()
    await expect(page.getByRole('row', { name: /borealis/ })).toBeVisible()
    await expect(page.getByRole('row', { name: /atlas/ })).toHaveCount(0)
    await page.getByRole('button', { name: /Low VRAM/ }).click()
    await page.getByRole('searchbox', { name: 'Search nodes' }).fill('legacy')
    await expect(page.getByRole('row', { name: /legacy/ })).toBeVisible()
    await page.getByRole('searchbox', { name: 'Search nodes' }).fill('')
    await page.getByRole('button', { name: /Sort by node/ }).click()
    await expect(page.locator('tbody tr').first()).toContainText('legacy')
    await page.getByLabel('Group nodes').selectOption('label:zone')
    await expect(page.getByText('Unlabelled', { exact: true })).toBeVisible()
    await expect(page.getByText('east', { exact: true })).toBeVisible()
  })

  test('mounts only 50 rows and clamps pagination for a 1,000-node fleet', async ({ page }) => {
    const nodes = Array.from({ length: 1000 }, (_, index) => ({ id: `node-${index}`, name: `worker-${String(index).padStart(4, '0')}`, node_type: 'backend', address: `10.0.${Math.floor(index / 255)}.${index % 255}:50051`, status: 'healthy' }))
    await mockNodes(page, nodes)
    await page.goto('/app/nodes')
    await expect(page.locator('tbody tr')).toHaveCount(50, { timeout: 15_000 })
    await expect(page.getByText('Page 1 of 20')).toBeVisible()
    await page.getByRole('button', { name: 'Next page' }).click()
    await expect(page.getByText('Page 2 of 20')).toBeVisible()
    await page.getByRole('searchbox', { name: 'Search nodes' }).fill('worker-0000')
    await expect(page.getByText('Page 1 of 1')).toBeVisible()
  })

  test('filters bulk actions by lifecycle state, reports skipped nodes, and prevents overlapping batches', async ({ page }) => {
    const nodes = Array.from({ length: 12 }, (_, index) => ({
      id: `n${index}`,
      name: `worker-${index}`,
      node_type: 'backend',
      status: index === 9 ? 'pending' : index === 10 ? 'draining' : index === 11 ? 'offline' : 'healthy',
    }))
    await mockNodes(page, nodes)
    let active = 0
    let peak = 0
    const drainRequests = []
    const resumeRequests = []
    await page.route('**/api/nodes/*/drain', async route => {
      active += 1
      peak = Math.max(peak, active)
      await new Promise(resolve => setTimeout(resolve, 30))
      active -= 1
      const id = route.request().url().split('/').at(-2)
      drainRequests.push(id)
      await route.fulfill({ status: id === 'n8' ? 500 : 200, contentType: 'application/json', body: id === 'n8' ? '{"error":"failed"}' : '{}' })
    })
    await page.route('**/api/nodes/*/resume', async route => {
      const id = route.request().url().split('/').at(-2)
      resumeRequests.push(id)
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    })
    await page.goto('/app/nodes')
    await page.getByRole('checkbox', { name: 'Select visible nodes' }).check()
    await page.getByRole('searchbox', { name: 'Search nodes' }).fill('worker-1')
    await expect(page.getByText('12 selected')).toBeVisible()
    await page.getByRole('button', { name: 'Drain selected' }).evaluate(button => {
      button.click()
      button.click()
    })
    await expect.poll(() => drainRequests.length).toBe(9)
    expect(peak).toBeLessThanOrEqual(8)
    expect(drainRequests.sort()).toEqual(Array.from({ length: 9 }, (_, index) => `n${index}`).sort())
    await expect(page.getByText(/8 succeeded, 1 failed, 3 skipped/)).toBeVisible()

    await page.getByRole('button', { name: 'Resume selected' }).click()
    await expect.poll(() => resumeRequests).toEqual(['n10'])
    await expect(page.getByText(/1 succeeded, 0 failed, 11 skipped/)).toBeVisible()
    expect(drainRequests).not.toContain('n9')
    expect(resumeRequests).not.toContain('n9')
  })

  test('disables bulk controls and remove confirmation while removal is running', async ({ page }) => {
    await mockNodes(page, [baseNodes[0]])
    let finishRemove
    await page.route('**/api/nodes/n1', async route => {
      if (route.request().method() !== 'DELETE') return route.fallback()
      await new Promise(resolve => { finishRemove = resolve })
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    })
    await page.goto('/app/nodes')
    await page.getByRole('checkbox', { name: 'Select atlas' }).check()
    await page.getByRole('button', { name: 'Remove selected' }).click()
    await page.getByRole('button', { name: 'Remove nodes' }).click()

    await expect(page.getByRole('button', { name: 'Removing…' })).toBeDisabled()
    await expect(page.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    finishRemove()
    await expect(page.getByText(/1 succeeded, 0 failed, 0 skipped/)).toBeVisible()
  })

  test('fetches backends only when an inspector opens and shows unknown legacy metrics', async ({ page }) => {
    await mockNodes(page)
    let backendRequests = 0
    await page.route('**/api/nodes/n3/backends', route => {
      backendRequests += 1
      return route.fulfill({ status: 200, contentType: 'application/json', body: '[{"name":"tool-runner"}]' })
    })
    await page.goto('/app/nodes')
    expect(backendRequests).toBe(0)
    await page.getByRole('button', { name: 'Inspect legacy' }).click()
    const inspector = page.getByRole('complementary', { name: 'Node inspector' })
    await expect(inspector).toContainText('legacy')
    await expect(inspector).toContainText('No data')
    await expect(inspector).toContainText('1 backend')
    expect(backendRequests).toBe(1)
    await expect(page.getByRole('link', { name: 'Open full node details' })).toHaveAttribute('href', '/app/nodes/n3')
  })

  test('keeps pending approval visible', async ({ page }) => {
    await mockNodes(page, [baseNodes[1]])
    await page.route('**/api/nodes/n2/approve', route => route.fulfill({ status: 200, contentType: 'application/json', body: '{}' }))
    await page.goto('/app/nodes')
    await expect(page.getByRole('button', { name: 'Approve borealis' })).toBeVisible({ timeout: 15_000 })
  })

  test('keeps checkbox keyboard activation from opening the inspector', async ({ page }) => {
    await mockNodes(page, [baseNodes[0]])
    await page.goto('/app/nodes')

    const checkbox = page.getByRole('checkbox', { name: 'Select atlas' })
    await checkbox.focus()
    await checkbox.press('Space')

    await expect(checkbox).toBeChecked()
    await expect(page.getByRole('complementary', { name: 'Node inspector' })).toHaveCount(0)
  })
})
