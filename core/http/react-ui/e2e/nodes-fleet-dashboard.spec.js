import { test, expect } from './coverage-fixtures.js'

const baseNodes = [
  { id: 'n1', name: 'atlas', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy', labels: { zone: 'east' }, total_vram: 100, available_vram: 40, total_ram: 200, available_ram: 100, total_disk: 1000, available_disk: 600, cpu_logical_cores: 8, cpu_usage_percent: 25, cpu_load_1: 1.5, model_count: 3, in_flight_count: 2, last_heartbeat: '2026-09-14T00:00:00Z' },
  { id: 'n2', name: 'borealis', node_type: 'backend', address: '10.0.0.2:50051', status: 'pending', labels: { zone: 'west' }, total_vram: 100, available_vram: 10, total_ram: 200, available_ram: 10, total_disk: 1000, available_disk: 100, cpu_logical_cores: 16, cpu_usage_percent: 50, cpu_load_1: 4, model_count: 1, in_flight_count: 0 },
  { id: 'n3', name: 'legacy', node_type: 'agent', address: '10.0.0.3:50051', status: 'offline', labels: {} },
]

async function mockNodes(page, nodes = baseNodes) {
  await page.route('**/api/nodes', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(nodes) }))
}

const baseModels = [
  { id: 'r1', node_id: 'n1', model_name: 'Llama 3.2', replica_index: 0, address: '10.0.0.1:50101', state: 'loaded', in_flight: 2, backend_type: 'llama-cpp', last_used: '2026-09-14T10:00:00Z' },
  { id: 'r2', node_id: 'n1', model_name: 'Llama 3.2', replica_index: 1, address: '10.0.0.1:50102', state: 'loaded', in_flight: 0, backend_type: 'llama-cpp', last_used: '2026-09-14T10:30:00Z' },
  { id: 'r3', node_id: 'n2', model_name: 'Llama 3.2', replica_index: 0, address: '10.0.0.2:50101', state: 'loaded', in_flight: 1, backend_type: 'vllm', last_used: '2026-09-14T11:00:00Z' },
  { id: 'r4', node_id: 'missing', model_name: 'Whisper large v3', replica_index: 0, address: '10.0.0.9:50101', state: 'loaded', in_flight: 0, backend_type: 'whisper', last_used: '2026-09-14T09:00:00Z' },
]

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

  test('keeps the approved compact composition while inspecting at a desktop viewport', async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 1050 })
    await mockNodes(page)
    await page.route('**/api/nodes/n1/backends', route => route.fulfill({ status: 200, contentType: 'application/json', body: '[{"name":"llama-cpp"},{"name":"whisper"}]' }))
    await page.goto('/app/nodes')

    const overview = page.getByRole('region', { name: 'Fleet overview' })
    const workbench = page.getByRole('region', { name: 'Fleet workbench' })
    await expect(overview).toBeVisible({ timeout: 15_000 })
    await expect(workbench).toBeVisible()
    await expect(page.locator('.fleet-select-wrap')).toHaveCount(3)
    await expect(page.getByLabel('Filter status')).toHaveCSS('appearance', 'none')

    const overviewBefore = await overview.boundingBox()
    const fleetBefore = await page.locator('.fleet-workbench__fleet').boundingBox()
    const cellTops = await overview.locator('.fleet-overview__cell').evaluateAll(cells => cells.map(cell => Math.round(cell.getBoundingClientRect().top)))
    expect(new Set(cellTops).size).toBe(1)

    const checkbox = page.getByRole('checkbox', { name: 'Select atlas' })
    await checkbox.check()
    await expect(page.getByRole('row', { name: /atlas/ })).toHaveClass(/is-selected/)

    await page.getByRole('button', { name: 'Inspect atlas' }).click()
    const inspector = page.getByRole('complementary', { name: 'Node inspector' })
    await expect(inspector).toBeVisible()
    await expect(inspector.getByRole('heading', { name: 'Node' })).toBeVisible()
    await expect(inspector.getByRole('heading', { name: 'Resources' })).toBeVisible()
    await expect(inspector.getByRole('heading', { name: 'Workload' })).toBeVisible()
    await expect(inspector.getByRole('heading', { name: 'Runtime' })).toBeVisible()
    await expect(inspector.locator('.node-inspector__resource')).toHaveCount(2)

    const overviewAfter = await overview.boundingBox()
    const fleetAfter = await page.locator('.fleet-workbench__fleet').boundingBox()
    expect(Math.abs(overviewAfter.width - overviewBefore.width)).toBeLessThanOrEqual(1)
    expect(Math.abs(fleetAfter.width - fleetBefore.width)).toBeLessThanOrEqual(1)
    await expect(inspector).toHaveCSS('position', 'absolute')
  })

  test('loads running models once on activation and drills model to node and back', async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 1050 })
    await mockNodes(page, baseNodes.map(node => node.id === 'n2' ? { ...node, status: 'healthy' } : node))
    let modelRequests = 0
    let backendRequests = 0
    await page.route('**/api/nodes/models', route => {
      modelRequests += 1
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(baseModels) })
    })
    await page.route('**/api/nodes/n1/backends', route => {
      backendRequests += 1
      return route.fulfill({ status: 200, contentType: 'application/json', body: '[{"name":"llama-cpp"}]' })
    })
    await page.goto('/app/nodes')
    await expect(page.getByRole('table', { name: 'Fleet nodes' })).toBeVisible({ timeout: 15_000 })
    expect(modelRequests).toBe(0)
    expect(backendRequests).toBe(0)

    const nodesTab = page.getByRole('tab', { name: 'Nodes' })
    const modelsTab = page.getByRole('tab', { name: 'Running models' })
    await expect(nodesTab).toHaveAttribute('aria-controls', 'fleet-nodes-panel')
    await expect(nodesTab).toHaveAttribute('tabindex', '0')
    await expect(modelsTab).toHaveAttribute('aria-controls', 'fleet-models-panel')
    await expect(modelsTab).toHaveAttribute('tabindex', '-1')
    await expect(page.getByRole('tabpanel', { name: 'Nodes' })).toHaveAttribute('id', 'fleet-nodes-panel')

    await nodesTab.focus()
    await nodesTab.press('ArrowRight')
    await expect(modelsTab).toBeFocused()
    await expect(modelsTab).toHaveAttribute('aria-selected', 'true')
    await expect(modelsTab).toHaveAttribute('tabindex', '0')
    await expect(nodesTab).toHaveAttribute('tabindex', '-1')
    await expect(page.getByRole('tabpanel', { name: 'Running models' })).toHaveAttribute('id', 'fleet-models-panel')
    await expect(page.getByText('Current loaded replicas on healthy nodes')).toBeVisible()
    await expect(page.getByRole('table', { name: 'Running models' })).toBeVisible()
    await expect(page.getByRole('row', { name: /Llama 3.2/ })).toContainText('3')
    expect(modelRequests).toBe(1)
    expect(backendRequests).toBe(0)

    const modelControl = page.getByRole('button', { name: 'Inspect Llama 3.2' })
    await expect(modelControl).toHaveAttribute('aria-selected', 'false')
    await expect(modelControl).toHaveAttribute('aria-expanded', 'false')
    await modelControl.focus()
    await modelControl.press('Enter')
    const modelInspector = page.getByRole('complementary', { name: 'Model inspector' })
    const closeModel = page.getByRole('button', { name: 'Close model inspector' })
    await expect(closeModel).toBeFocused()
    await expect(modelControl).toHaveAttribute('aria-selected', 'true')
    await expect(modelControl).toHaveAttribute('aria-expanded', 'true')
    await expect(modelControl).toHaveAttribute('aria-current', 'true')
    await expect(modelControl).toHaveAttribute('aria-controls', 'model-inspector')
    await expect(modelInspector).toContainText('2 replicas')
    await expect(modelInspector).toContainText('borealis')
    const atlasControl = modelInspector.getByRole('button', { name: /Open node atlas/ })
    await atlasControl.focus()
    await atlasControl.press('Enter')
    await expect(page.getByRole('complementary', { name: 'Node inspector' })).toBeVisible()
    const backToModel = page.getByRole('button', { name: 'Back to Llama 3.2' })
    await expect(backToModel).toBeFocused()
    await expect.poll(() => backendRequests).toBe(1)
    expect(modelRequests).toBe(1)
    await backToModel.press('Enter')
    await expect(page.getByRole('complementary', { name: 'Model inspector' })).toBeVisible()
    await expect(page.getByRole('button', { name: /Open node atlas/ })).toBeFocused()
    await closeModel.click()
    await expect(page.getByRole('complementary', { name: 'Model inspector' })).toHaveCount(0)
    await expect(modelControl).toBeFocused()
    await expect(modelControl).toHaveAttribute('aria-selected', 'false')
    await expect(modelControl).toHaveAttribute('aria-expanded', 'false')

    await page.getByRole('button', { name: 'Inspect Whisper large v3' }).click()
    await expect(page.getByRole('complementary', { name: 'Model inspector' })).toContainText('missing')
    await expect(page.getByRole('complementary', { name: 'Model inspector' })).toContainText('Unknown')
    await page.getByRole('button', { name: 'Close model inspector' }).click()

    await modelsTab.focus()
    await modelsTab.press('ArrowLeft')
    await expect(nodesTab).toBeFocused()
    await expect(nodesTab).toHaveAttribute('aria-selected', 'true')
    await nodesTab.press('ArrowRight')
    expect(modelRequests).toBe(1)
  })

  test('moves focus into and restores it from the node inspector', async ({ page }) => {
    await mockNodes(page, [baseNodes[0]])
    await page.route('**/api/nodes/n1/backends', route => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await page.goto('/app/nodes')

    const nodeControl = page.getByRole('button', { name: 'Inspect atlas' })
    await nodeControl.click()
    const closeNode = page.getByRole('button', { name: 'Close node inspector' })
    await expect(closeNode).toBeFocused()
    await closeNode.click()
    await expect(nodeControl).toBeFocused()
  })

  test('shows model loading, error, retry, and empty states', async ({ page }) => {
    await mockNodes(page)
    let finishFirst
    let attempts = 0
    await page.route('**/api/nodes/models', async route => {
      attempts += 1
      if (attempts === 1) {
        await new Promise(resolve => { finishFirst = resolve })
        return route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"database unavailable"}' })
      }
      return route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    })
    await page.goto('/app/nodes')
    await page.getByRole('tab', { name: 'Running models' }).click()
    await expect(page.getByText('Loading running models…')).toBeVisible()
    finishFirst()
    await expect(page.getByText('Unable to load running models')).toBeVisible()
    await page.getByRole('button', { name: 'Retry loading running models' }).click()
    await expect(page.getByText('No running models')).toBeVisible()
    expect(attempts).toBe(2)
  })

  test('mounts 50 of 1,000 running models and supports search and sorting', async ({ page }) => {
    await mockNodes(page)
    const models = Array.from({ length: 1000 }, (_, index) => ({
      id: `replica-${index}`,
      node_id: 'n1',
      model_name: `model-${String(index).padStart(4, '0')}`,
      replica_index: 0,
      address: `10.0.0.1:${51000 + index}`,
      state: 'loaded',
      in_flight: index % 7,
      backend_type: 'llama-cpp',
      last_used: new Date(Date.UTC(2026, 8, 1, 0, index)).toISOString(),
    }))
    await page.route('**/api/nodes/models', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(models) }))
    await page.goto('/app/nodes')
    await page.getByRole('tab', { name: 'Running models' }).click()
    await expect(page.getByRole('table', { name: 'Running models' }).locator('tbody tr')).toHaveCount(50)
    await expect(page.getByText('Page 1 of 20')).toBeVisible()
    await page.getByRole('button', { name: 'Next model page' }).click()
    await expect(page.getByText('Page 2 of 20')).toBeVisible()
    await page.getByRole('searchbox', { name: 'Search running models' }).fill('model-0000')
    await expect(page.getByText('Page 1 of 1')).toBeVisible()
    await page.getByRole('searchbox', { name: 'Search running models' }).fill('')
    await page.getByRole('button', { name: /Sort by model/ }).click()
    await expect(page.getByRole('table', { name: 'Running models' }).locator('tbody tr').first()).toContainText('model-0999')
  })

})
