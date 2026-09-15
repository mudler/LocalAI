import { test, expect } from './coverage-fixtures.js'

const baseNodes = [
  { id: 'n1', name: 'atlas', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy', labels: { zone: 'east' }, total_vram: 100, available_vram: 40, total_ram: 200, available_ram: 100, total_disk: 1000, available_disk: 600, cpu_logical_cores: 8, cpu_usage_percent: 25, cpu_load_1: 1.5, model_count: 3, in_flight_count: 2, last_heartbeat: '2026-09-14T00:00:00Z' },
  { id: 'n2', name: 'borealis', node_type: 'backend', address: '10.0.0.2:50051', status: 'pending', labels: { zone: 'west' }, total_vram: 100, available_vram: 10, total_ram: 200, available_ram: 10, total_disk: 1000, available_disk: 100, cpu_logical_cores: 16, cpu_usage_percent: 50, cpu_load_1: 4, model_count: 1, in_flight_count: 0 },
  { id: 'n3', name: 'legacy', node_type: 'agent', address: '10.0.0.3:50051', status: 'offline', labels: {} },
]

async function mockNodes(page, nodes = baseNodes) {
  await page.route('**/api/nodes', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(nodes) }))
}

async function mockFullOperateNavigation(page) {
  await page.route('**/api/features', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ distributed: true }),
  }))
  await page.route('**/api/auth/status', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      authEnabled: true,
      staticApiKeyRequired: false,
      providers: ['local'],
      user: { id: 'admin', name: 'Admin', role: 'admin', provider: 'local' },
    }),
  }))
}

const baseModels = [
  { id: 'r1', node_id: 'n1', model_name: 'Llama 3.2', replica_index: 0, address: '10.0.0.1:50101', state: 'loaded', in_flight: 2, backend_type: 'llama-cpp', last_used: '2026-09-14T10:00:00Z' },
  { id: 'r2', node_id: 'n1', model_name: 'Llama 3.2', replica_index: 1, address: '10.0.0.1:50102', state: 'loaded', in_flight: 0, backend_type: 'llama-cpp', last_used: '2026-09-14T10:30:00Z' },
  { id: 'r3', node_id: 'n2', model_name: 'Llama 3.2', replica_index: 0, address: '10.0.0.2:50101', state: 'loaded', in_flight: 1, backend_type: 'vllm', last_used: '2026-09-14T11:00:00Z' },
  { id: 'r4', node_id: 'missing', model_name: 'Whisper large v3', replica_index: 0, address: '10.0.0.9:50101', state: 'loaded', in_flight: 0, backend_type: 'whisper', last_used: '2026-09-14T09:00:00Z' },
]

test.describe('Nodes fleet dashboard', () => {
  test('uses the standard Operate navigation at a desktop viewport', async ({ page }) => {
    await mockFullOperateNavigation(page)
    await mockNodes(page, [baseNodes[0]])
    await page.goto('/app/nodes')

    const primaryOperate = page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' })
    await expect(primaryOperate).toBeVisible({ timeout: 15_000 })
    await expect(primaryOperate).toHaveClass(/active/)

    const rail = page.locator('.console-layout > .console-rail')
    await expect(rail).toBeVisible()
    await expect(rail.locator('a.nav-item')).toHaveCount(13)
    await expect(rail.locator('a[href="/app/nodes"]')).toHaveClass(/active/)
    await expect(rail.locator('a[href$="/swagger/index.html"]')).toHaveAttribute('target', '_blank')
  })

  test('uses the standard collapsible Operate rail on mobile', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockFullOperateNavigation(page)
    await mockNodes(page, [baseNodes[0]])
    await page.goto('/app/nodes')

    await page.getByRole('button', { name: 'Open menu' }).click()
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' })).toBeVisible()
    await page.getByRole('button', { name: 'Close menu' }).click()

    const rail = page.locator('.console-layout > .console-rail')
    await expect(rail).toBeVisible()
    await expect(rail.locator('.console-rail-groups')).toBeHidden()
    await rail.getByRole('button', { name: 'Expand Operate navigation' }).click()
    await expect(rail.locator('.console-rail-groups')).toBeVisible()
    await expect(rail.locator('a.nav-item')).toHaveCount(13)
  })

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
    await page.getByRole('checkbox', { name: 'Select page' }).check()
    await page.getByRole('searchbox', { name: 'Search nodes' }).fill('worker-1')
    await expect(page.getByText('12 selected')).toBeVisible()
    await expect(page.getByText('3 visible, 9 outside this view')).toBeVisible()
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

  test('opens Add worker as an accessible setup drawer and restores focus', async ({ page }) => {
    await mockNodes(page, [baseNodes[0]])
    await page.goto('/app/nodes')

    const trigger = page.getByRole('button', { name: 'Add worker' })
    await trigger.click()
    const drawer = page.getByRole('dialog', { name: 'Add worker' })
    await expect(drawer).toBeVisible()
    await expect(drawer.getByRole('radiogroup', { name: 'Worker type' })).toBeVisible()
    await expect(drawer.getByText('CLI', { exact: true })).toBeVisible()
    await expect(drawer.getByText('Docker', { exact: true })).toBeVisible()
    await expect(drawer.getByRole('button', { name: 'Copy command' })).toHaveCount(2)
    await expect(drawer.getByRole('link', { name: 'Distributed mode documentation' })).toBeVisible()
    await expect(drawer.getByRole('button', { name: 'Close worker setup' })).toBeFocused()

    await page.keyboard.press('Escape')
    await expect(drawer).toBeHidden()
    await expect(trigger).toBeFocused()
  })

  test('presents worker setup as a modal drawer on mobile', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockNodes(page, [baseNodes[0]])
    await page.goto('/app/nodes')
    await page.getByRole('button', { name: 'Add worker' }).click()
    await expect(page.getByRole('dialog', { name: 'Add worker' })).toHaveAttribute('aria-modal', 'true')
  })

  test('opens running-model logs with a fleet return destination', async ({ page }) => {
    await mockNodes(page)
    await page.route('**/api/nodes/models', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([baseModels[3]]) }))
    await page.goto('/app/nodes')
    await page.getByRole('tab', { name: /Running models/ }).click()
    await page.getByRole('button', { name: 'Actions for Whisper large v3' }).click()
    await page.getByRole('menuitem', { name: 'View logs…' }).click()
    await page.getByRole('link', { name: 'Back to nodes' }).click()
    await expect(page).toHaveURL(/\/app\/nodes$/)
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

  test('shows inspector lifecycle controls only for server-accepted states', async ({ page }) => {
    const statuses = ['healthy', 'draining', 'pending', 'unhealthy', 'offline', 'unknown']
    await mockNodes(page, statuses.map((status, index) => ({
      id: `state-${index}`,
      name: `node-${status}`,
      node_type: 'backend',
      status,
    })))
    await page.route('**/api/nodes/*/backends', route => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await page.goto('/app/nodes')

    for (const status of statuses) {
      await page.getByRole('button', { name: `Inspect node-${status}` }).click()
      const inspector = page.getByRole('complementary', { name: 'Node inspector' })
      await expect(inspector.getByRole('button', { name: 'Approve', exact: true })).toHaveCount(status === 'pending' ? 1 : 0)
      await expect(inspector.getByRole('button', { name: 'Drain', exact: true })).toHaveCount(status === 'healthy' ? 1 : 0)
      await expect(inspector.getByRole('button', { name: 'Resume', exact: true })).toHaveCount(status === 'draining' ? 1 : 0)
      await inspector.getByRole('button', { name: 'Close node inspector' }).click()
    }
  })

  test('approves a pending node from the model-to-node drilldown', async ({ page }) => {
    let status = 'pending'
    let approvalRequests = 0
    await page.route('**/api/nodes', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([
      { id: 'pending-node', name: 'pending-worker', node_type: 'backend', status },
    ]) }))
    await page.route('**/api/nodes/models', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([
      { id: 'replica', node_id: 'pending-node', model_name: 'Pending model', replica_index: 0, state: 'loaded' },
    ]) }))
    await page.route('**/api/nodes/pending-node/backends', route => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await page.route('**/api/nodes/pending-node/approve', async route => {
      approvalRequests += 1
      status = 'healthy'
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    })
    await page.goto('/app/nodes')
    await page.getByRole('tab', { name: 'Running models' }).click()
    await page.getByRole('button', { name: 'Inspect Pending model' }).click()
    await page.getByRole('button', { name: 'Open node pending-worker' }).click()

    const inspector = page.getByRole('complementary', { name: 'Node inspector' })
    await inspector.getByRole('button', { name: 'Approve', exact: true }).click()
    await expect.poll(() => approvalRequests).toBe(1)
    await expect(page.getByText('Node approved')).toBeVisible()
    await expect(inspector.getByRole('button', { name: 'Drain', exact: true })).toBeVisible()
  })

  test('keeps incomplete capacity unknown throughout the fleet view', async ({ page }) => {
    await mockNodes(page, [{
      id: 'incomplete', name: 'incomplete-capacity', node_type: 'backend', status: 'healthy',
      total_vram: 100, total_ram: 200, total_disk: 300,
    }])
    await page.route('**/api/nodes/incomplete/backends', route => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await page.goto('/app/nodes')

    await expect(page.getByLabel('VRAM capacity')).toContainText('No data')
    await expect(page.getByLabel('VRAM capacity')).toContainText('1 node unavailable')
    await expect(page.getByRole('button', { name: /Low VRAM.*0/ })).toBeVisible()
    const row = page.getByRole('row', { name: /incomplete-capacity/ })
    await expect(row.getByText('No data')).toHaveCount(3)
    await page.getByRole('button', { name: 'Inspect incomplete-capacity' }).click()
    const inspector = page.getByRole('complementary', { name: 'Node inspector' })
    await expect(inspector.getByText('No data')).toHaveCount(4)
  })

  test('announces complete and partial capacity coverage without adding visible clutter', async ({ page }) => {
    const partialNode = {
      ...baseNodes[0],
      id: 'n-partial',
      name: 'partial-capacity',
      total_vram: 100,
      available_vram: 50,
      total_ram: undefined,
      available_ram: undefined,
    }
    await mockNodes(page, [baseNodes[0], partialNode])
    await page.goto('/app/nodes')

    const vram = page.getByLabel('VRAM capacity', { exact: true })
    const ram = page.getByLabel('RAM capacity', { exact: true })
    await expect(vram).toContainText('Capacity coverage: 2 of 2 nodes reporting; 0 unknown.', { timeout: 15_000 })
    await expect(ram).toContainText('Capacity coverage: 1 of 2 nodes reporting; 1 unknown.')
    await expect(vram.locator('.fleet-gauge__coverage')).toHaveCount(0)
    await expect(ram.locator('.fleet-gauge__coverage')).toHaveText('1 node unavailable')
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

  test('styles fleet selection consistently and exposes the partial-page state', async ({ page }) => {
    await mockNodes(page)
    await page.goto('/app/nodes')

    const selectPage = page.getByRole('checkbox', { name: 'Select page' })
    const selectNode = page.getByRole('checkbox', { name: 'Select atlas' })
    await expect(selectPage).toHaveCSS('width', '18px')
    await expect(selectNode).toHaveCSS('height', '18px')
    await expect(selectNode).toHaveCSS('cursor', 'pointer')

    await selectNode.check()
    await expect.poll(() => selectPage.evaluate(input => input.indeterminate)).toBe(true)
  })

  test('keeps the low-density composition while inspecting at a desktop viewport', async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 1050 })
    await mockNodes(page)
    await page.route('**/api/nodes/n1/backends', route => route.fulfill({ status: 200, contentType: 'application/json', body: '[{"name":"llama-cpp"},{"name":"whisper"}]' }))
    await page.goto('/app/nodes')

    const overview = page.getByRole('region', { name: 'Fleet overview' })
    const workbench = page.getByRole('region', { name: 'Fleet workbench' })
    await expect(overview).toBeVisible({ timeout: 15_000 })
    await expect(workbench).toBeVisible()
    await expect(page.locator('.console-layout > .console-rail')).toBeVisible()
    await expect(page.locator('.fleet-select-wrap')).toHaveCount(3)
    await expect(page.getByLabel('Filter status')).toHaveCSS('appearance', 'none')
    await expect(page.locator('.fleet-bulkbar')).toHaveCount(0)

    const overviewBefore = await overview.boundingBox()
    const fleetBefore = await page.locator('#fleet-nodes-panel').boundingBox()
    const cells = overview.locator('.fleet-overview__cell')
    await expect(cells).toHaveCount(5)
    const cellTops = await cells.evaluateAll(items => items.map(cell => Math.round(cell.getBoundingClientRect().top)))
    expect(new Set(cellTops).size).toBe(1)
    const attention = page.getByRole('complementary', { name: 'Attention queue' })
    await expect(attention).toBeVisible()
    const overviewBox = await overview.boundingBox()
    const attentionBox = await attention.boundingBox()
    expect(attentionBox.y).toBeGreaterThanOrEqual(overviewBox.y + overviewBox.height)

    const checkbox = page.getByRole('checkbox', { name: 'Select atlas' })
    await checkbox.check()
    await expect(page.getByRole('row', { name: /atlas/ })).toHaveClass(/is-selected/)
    await expect(page.locator('.fleet-bulkbar')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Clear selection' })).toBeVisible()

    const inspectNode = page.getByRole('button', { name: 'Inspect atlas' })
    await inspectNode.focus()
    await inspectNode.press('Enter')
    const inspector = page.getByRole('complementary', { name: 'Node inspector' })
    await expect(inspector).toBeVisible()
    await expect(inspector.getByRole('heading', { name: 'Node' })).toBeVisible()
    await expect(inspector.getByRole('heading', { name: 'Resources' })).toBeVisible()
    await expect(inspector.getByRole('heading', { name: 'Workload' })).toBeVisible()
    await expect(inspector.locator('.node-inspector__resource')).toHaveCount(2)

    const overviewAfter = await overview.boundingBox()
    const fleetAfter = await page.locator('#fleet-nodes-panel').boundingBox()
    expect(Math.abs(overviewAfter.width - overviewBefore.width)).toBeLessThanOrEqual(1)
    expect(Math.abs(fleetBefore.width - fleetAfter.width)).toBeLessThanOrEqual(1)
    await expect(inspector).toHaveCSS('position', 'fixed')
    await expect.poll(async () => {
      const inspectorBox = await inspector.boundingBox()
      return Math.max(
        Math.abs(inspectorBox.x + inspectorBox.width - 1584),
        Math.abs(inspectorBox.y - 16),
        Math.abs(inspectorBox.height - 1018),
      )
    }).toBeLessThanOrEqual(1)
  })

  test('reflows the overview and presents a contained drawer at a narrow viewport', async ({ page }) => {
    await page.setViewportSize({ width: 640, height: 900 })
    await mockNodes(page, [baseNodes[0]])
    await page.route('**/api/nodes/n1/backends', route => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await page.goto('/app/nodes')

    const overview = page.getByRole('region', { name: 'Fleet overview' })
    await expect(overview).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('.console-layout > .console-rail')).toBeVisible()
    const overviewBox = await overview.boundingBox()
    expect(overviewBox.width).toBeGreaterThan(500)
    const cells = overview.locator('.fleet-overview__cell')
    const tops = await cells.evaluateAll(items => items.map(item => Math.round(item.getBoundingClientRect().top)))
    expect(new Set(tops).size).toBeGreaterThan(1)
    await expect(overview.getByLabel('Fleet health summary')).toHaveCSS('grid-column-start', '1')
    await expect(overview.getByLabel('Fleet health summary')).toHaveCSS('grid-column-end', '-1')

    const narrowInspectNode = page.getByRole('button', { name: 'Inspect atlas' })
    await narrowInspectNode.focus()
    await narrowInspectNode.press('Enter')
    const inspector = page.getByRole('dialog', { name: 'Node inspector' })
    await expect(inspector).toBeVisible()
    await expect(inspector).toHaveAttribute('aria-modal', 'true')
    await expect(inspector).toHaveCSS('position', 'fixed')
    await expect(page.locator('.node-inspector__scrim')).toBeVisible()
    await expect(page.locator('body')).toHaveCSS('overflow', 'hidden')
    await expect(page.getByRole('region', { name: 'Fleet workbench', includeHidden: true })).toHaveAttribute('inert', '')
    await expect(page.getByRole('region', { name: 'Fleet workbench', includeHidden: true })).toHaveAttribute('aria-hidden', 'true')
    const workbenchBox = await page.locator('.fleet-workbench').boundingBox()
    expect(workbenchBox.width).toBeLessThanOrEqual(600)
    await expect.poll(async () => (await inspector.boundingBox()).y).toBeLessThanOrEqual(1)
    const inspectorBox = await inspector.boundingBox()
    expect(inspectorBox.height).toBe(900)
    const close = inspector.getByRole('button', { name: 'Close node inspector' })
    await expect(close).toBeFocused()
    await close.press('Shift+Tab')
    await expect(inspector.getByRole('button', { name: 'Drain', exact: true })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(close).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(inspector).toHaveCount(0)
    await expect(page.locator('body')).not.toHaveCSS('overflow', 'hidden')
    await expect(page.getByRole('region', { name: 'Fleet workbench' })).not.toHaveAttribute('inert', '')
    await expect(page.getByRole('region', { name: 'Fleet workbench' })).not.toHaveAttribute('aria-hidden', 'true')
    await expect(narrowInspectNode).toBeFocused()
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
    const nodesPanel = page.locator('#fleet-nodes-panel')
    const modelsPanel = page.locator('#fleet-models-panel')
    await expect(nodesTab).toHaveAttribute('aria-controls', 'fleet-nodes-panel')
    await expect(nodesTab).toHaveAttribute('tabindex', '0')
    await expect(modelsTab).toHaveAttribute('aria-controls', 'fleet-models-panel')
    await expect(modelsTab).toHaveAttribute('tabindex', '-1')
    await expect(nodesPanel).toHaveAttribute('role', 'tabpanel')
    await expect(nodesPanel).toHaveAttribute('aria-labelledby', 'fleet-nodes-tab')
    await expect(nodesPanel).not.toHaveAttribute('hidden', '')
    await expect(nodesPanel).toBeVisible()
    await expect(modelsPanel).toHaveAttribute('role', 'tabpanel')
    await expect(modelsPanel).toHaveAttribute('aria-labelledby', 'fleet-models-tab')
    await expect(modelsPanel).toHaveAttribute('hidden', '')
    await expect(modelsPanel).toBeHidden()
    await expect(modelsPanel.getByRole('table', { name: 'Running models' })).toHaveCount(0)
    await expect(nodesPanel.locator('tbody tr')).toHaveCount(3)

    await nodesTab.focus()
    await nodesTab.press('ArrowRight')
    await expect(modelsTab).toBeFocused()
    await expect(modelsTab).toHaveAttribute('aria-selected', 'true')
    await expect(modelsTab).toHaveAttribute('tabindex', '0')
    await expect(nodesTab).toHaveAttribute('tabindex', '-1')
    await expect(nodesPanel).toHaveAttribute('hidden', '')
    await expect(nodesPanel).toBeHidden()
    await expect(modelsPanel).not.toHaveAttribute('hidden', '')
    await expect(modelsPanel).toBeVisible()
    await expect(page.getByText('Current loaded replicas on healthy nodes')).toBeVisible()
    await expect(page.getByRole('table', { name: 'Running models' })).toBeVisible()
    await expect(page.getByRole('row', { name: /Llama 3.2/ })).toContainText('3')
    expect(modelRequests).toBe(1)
    expect(backendRequests).toBe(0)

    const modelControl = page.getByRole('button', { name: 'Inspect Llama 3.2' })
    await expect(modelControl).not.toHaveAttribute('aria-selected')
    await expect(modelControl).toHaveAttribute('aria-pressed', 'false')
    await expect(modelControl).toHaveAttribute('aria-expanded', 'false')
    await expect(modelControl).not.toHaveAttribute('aria-controls')
    await modelControl.focus()
    await modelControl.press('Enter')
    const modelInspector = page.getByRole('complementary', { name: 'Model inspector' })
    const closeModel = page.getByRole('button', { name: 'Close model inspector' })
    await expect(closeModel).toBeFocused()
    await expect(modelControl).not.toHaveAttribute('aria-selected')
    await expect(modelControl).toHaveAttribute('aria-pressed', 'true')
    await expect(modelControl).toHaveAttribute('aria-expanded', 'true')
    await expect(modelControl).toHaveAttribute('aria-current', 'true')
    await expect(modelControl).toHaveAttribute('aria-controls', 'model-inspector')
    await expect(modelInspector).toContainText('2 replicas')
    await expect(modelInspector).toContainText('borealis')
    const atlasControl = modelInspector.getByRole('button', { name: /Open node atlas/ })
    await atlasControl.focus()
    await atlasControl.press('Enter')
    await expect(page.getByRole('complementary', { name: 'Node inspector' })).toBeVisible()
    await expect(modelControl).toHaveAttribute('aria-pressed', 'true')
    await expect(modelControl).toHaveAttribute('aria-expanded', 'false')
    await expect(modelControl).not.toHaveAttribute('aria-controls')
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
    await expect(modelControl).not.toHaveAttribute('aria-selected')
    await expect(modelControl).toHaveAttribute('aria-pressed', 'false')
    await expect(modelControl).toHaveAttribute('aria-expanded', 'false')
    await expect(modelControl).not.toHaveAttribute('aria-controls')

    const whisperControl = page.getByRole('button', { name: 'Inspect Whisper large v3' })
    await whisperControl.click()
    await expect(page.getByRole('complementary', { name: 'Model inspector' })).toContainText('missing')
    await expect(page.getByRole('complementary', { name: 'Model inspector' })).toContainText('Unknown')
    await page.getByRole('button', { name: 'Close model inspector' }).click()
    await expect(whisperControl).toBeFocused()

    await modelsTab.focus()
    await modelsTab.press('ArrowLeft')
    await expect(nodesTab).toBeFocused()
    await expect(nodesTab).toHaveAttribute('aria-selected', 'true')
    await expect(nodesPanel).not.toHaveAttribute('hidden', '')
    await expect(modelsPanel).toHaveAttribute('hidden', '')
    await expect(modelsPanel.locator('tbody tr')).toHaveCount(2)
    await nodesTab.press('ArrowRight')
    await expect(modelsPanel.locator('tbody tr')).toHaveCount(2)
    expect(modelRequests).toBe(1)
  })

  test('opens logs directly for one replica and asks for placement when a model has several', async ({ page }) => {
    await mockNodes(page, baseNodes.map(node => node.id === 'n2' ? { ...node, status: 'healthy' } : node))
    await page.route('**/api/nodes/models', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(baseModels) }))
    await page.goto('/app/nodes')
    await page.getByRole('tab', { name: 'Running models' }).click()

    await page.getByRole('button', { name: 'Actions for Whisper large v3' }).click()
    await page.getByRole('menuitem', { name: 'View logs…' }).click()
    await expect(page).toHaveURL(/\/app\/node-backend-logs\/missing\/Whisper%20large%20v3%230$/)

    await page.goto('/app/nodes')
    await page.getByRole('tab', { name: 'Running models' }).click()
    await page.getByRole('button', { name: 'Actions for Llama 3.2' }).click()
    await page.getByRole('menuitem', { name: 'View logs…' }).click()

    const inspector = page.getByRole('complementary', { name: 'Model inspector' })
    await expect(inspector).toBeVisible()
    await expect(inspector.getByRole('button', { name: 'View all Llama 3.2 logs on atlas' })).toBeVisible()
    await inspector.getByRole('button', { name: 'View logs for Llama 3.2 replica 1 on atlas' }).click()
    await expect(page).toHaveURL(/\/app\/node-backend-logs\/n1\/Llama%203.2%230$/)
  })

  test('treats the model inspector as a modal drawer on mobile', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockNodes(page)
    await page.route('**/api/nodes/models', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(baseModels) }))
    await page.goto('/app/nodes')
    await page.getByRole('tab', { name: 'Running models' }).click()
    await page.getByRole('button', { name: 'Inspect Llama 3.2' }).click()

    const inspector = page.getByRole('dialog', { name: 'Model inspector' })
    await expect(inspector).toHaveAttribute('aria-modal', 'true')
    await expect(inspector.getByRole('button', { name: 'Close model inspector' })).toBeFocused()
    await expect(page.locator('.fleet-workbench')).toHaveAttribute('inert', '')
    await inspector.getByRole('button', { name: 'Close model inspector' }).press('Shift+Tab')
    await expect(inspector.getByRole('button', { name: 'Close', exact: true })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(inspector.getByRole('button', { name: 'Close model inspector' })).toBeFocused()
  })

  test('keeps the node inspector open when Escape dismisses its confirmation dialog', async ({ page }) => {
    await mockNodes(page)
    await page.route('**/api/nodes/n1/backends', route => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await page.goto('/app/nodes')
    await page.getByRole('checkbox', { name: 'Select atlas' }).check()
    await page.getByRole('button', { name: 'Inspect atlas' }).click()
    const inspector = page.getByRole('complementary', { name: 'Node inspector' })
    await expect(inspector).toBeVisible()

    await page.getByRole('button', { name: 'Remove selected' }).evaluate(button => button.click())
    await expect(page.getByRole('alertdialog')).toBeVisible()
    await page.keyboard.press('Escape')

    await expect(page.getByRole('alertdialog')).toHaveCount(0)
    await expect(inspector).toBeVisible()
  })

  test('stops a running model once from an accessible row menu and refreshes inventory', async ({ page }) => {
    await mockNodes(page)
    let modelRequests = 0
    let stopRequests = 0
    let stopBody
    let finishStop
    await page.route('**/api/nodes/models', route => {
      modelRequests += 1
      const rows = modelRequests === 1 ? baseModels : baseModels.filter(row => row.model_name !== 'Llama 3.2')
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(rows) })
    })
    await page.route('**/backend/shutdown', async route => {
      stopRequests += 1
      stopBody = route.request().postDataJSON()
      await new Promise(resolve => { finishStop = resolve })
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{"message":"ok"}' })
    })
    await page.goto('/app/nodes')
    const modelsTab = page.getByRole('tab', { name: 'Running models' })
    await modelsTab.click()

    const trigger = page.getByRole('button', { name: 'Actions for Llama 3.2' })
    await expect(trigger).toBeVisible()
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    await expect(modelsTab).toBeFocused()

    await trigger.focus()
    await trigger.press('Enter')
    const menu = page.getByRole('menu', { name: 'Llama 3.2 actions' })
    await expect(menu).toBeVisible()
    await expect(menu).toBeFocused()
    await menu.press('Escape')
    await expect(menu).toHaveCount(0)
    await expect(trigger).toBeFocused()
    await expect(page.getByRole('complementary', { name: 'Model inspector' })).toHaveCount(0)

    await trigger.click()
    await page.locator('.model-workbench__scope').click()
    await expect(menu).toHaveCount(0)
    await expect(page.getByRole('complementary', { name: 'Model inspector' })).toHaveCount(0)

    await trigger.click()
    await menu.getByRole('menuitem', { name: 'Stop model…' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Stop Llama 3.2?')
    await expect(dialog).toContainText('Llama 3.2 has 3 loaded replicas across 2 unique nodes. This will stop all loaded placements on those nodes.')
    await expect(dialog.getByRole('button', { name: 'Stop model' })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
    await page.keyboard.press('Shift+Tab')
    await expect(dialog.getByRole('button', { name: 'Stop model' })).toBeFocused()
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toHaveCount(0)
    await expect(trigger).toBeFocused()

    await trigger.click()
    await menu.getByRole('menuitem', { name: 'Stop model…' }).click()
    await dialog.getByRole('button', { name: 'Stop model' }).evaluate(button => {
      button.click()
      button.click()
    })

    await expect(dialog.getByRole('button', { name: 'Stopping…' })).toBeDisabled()
    await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    await expect.poll(() => stopRequests).toBe(1)
    expect(stopBody).toEqual({ model: 'Llama 3.2' })
    finishStop()
    await expect.poll(() => modelRequests).toBe(2)
    await expect(page.getByText('Stopped Llama 3.2: 3 replicas across 2 nodes.')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Inspect Llama 3.2' })).toHaveCount(0)
  })

  test('refreshes model inventory and warns about partial shutdown after a stop failure', async ({ page }) => {
    await mockNodes(page)
    let modelRequests = 0
    let stopRequests = 0
    await page.route('**/api/nodes/models', route => {
      modelRequests += 1
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(baseModels) })
    })
    await page.route('**/backend/shutdown', route => {
      stopRequests += 1
      return route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"controller timed out"}' })
    })
    await page.goto('/app/nodes')
    await page.getByRole('tab', { name: 'Running models' }).click()
    await page.getByRole('button', { name: 'Actions for Whisper large v3' }).click()
    await page.getByRole('menuitem', { name: 'Stop model…' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Stop model' }).click()

    await expect.poll(() => stopRequests).toBe(1)
    await expect.poll(() => modelRequests).toBe(2)
    await expect(page.getByText(/Could not stop Whisper large v3:.*Some replicas may already have stopped\./)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Inspect Whisper large v3' })).toBeVisible()
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

  test('keeps drawer actions visible for a one-row filtered fleet', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await mockNodes(page, baseNodes)
    await page.route('**/api/nodes/n1/backends', route => route.fulfill({ status: 200, contentType: 'application/json', body: '[{"name":"llama-cpp"}]' }))
    await page.goto('/app/nodes')
    await page.getByRole('searchbox', { name: 'Search nodes' }).fill('atlas')
    await expect(page.getByRole('row', { name: /atlas/ })).toBeVisible()
    await page.getByRole('button', { name: 'Inspect atlas' }).click()

    const inspector = page.getByRole('complementary', { name: 'Node inspector' })
    await expect(inspector.getByRole('heading', { name: 'Resources' })).toBeVisible()
    await expect(inspector.getByRole('heading', { name: 'Workload' })).toBeVisible()
    await expect(inspector.getByRole('link', { name: 'Open full node details' })).toBeVisible()
    await expect(inspector.getByRole('button', { name: 'Drain', exact: true })).toBeVisible()

    const inspectorBox = await inspector.boundingBox()
    expect(Math.abs(inspectorBox.y - 16)).toBeLessThanOrEqual(1)
    expect(Math.abs(inspectorBox.height - 868)).toBeLessThanOrEqual(1)
    await expect(inspector.locator('.node-inspector__actions')).toHaveCSS('display', 'grid')
  })

  test('keeps a desktop drawer in the visible viewport after opening from a long roster', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    const nodes = Array.from({ length: 50 }, (_, index) => ({
      id: `long-${index}`,
      name: `long-worker-${String(index).padStart(2, '0')}`,
      node_type: 'backend',
      status: 'healthy',
    }))
    await mockNodes(page, nodes)
    await page.route('**/api/nodes/long-49/backends', route => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
    await page.goto('/app/nodes')

    await page.getByRole('button', { name: 'Inspect long-worker-49' }).click()
    expect(await page.evaluate(() => window.scrollY)).toBeGreaterThan(1000)
    const inspector = page.getByRole('complementary', { name: 'Node inspector' })
    await expect(inspector).toHaveCSS('position', 'fixed')
    await expect(inspector.getByRole('heading', { name: 'long-worker-49' })).toBeVisible()
    await expect(inspector.getByRole('link', { name: 'Open full node details' })).toBeVisible()
    const box = await inspector.boundingBox()
    expect(Math.abs(box.y - 16)).toBeLessThanOrEqual(1)
    expect(Math.abs(box.height - 768)).toBeLessThanOrEqual(1)
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
    await expect(page.locator('#fleet-models-panel').getByText('Page 1 of 1')).toBeVisible()
    await page.getByRole('searchbox', { name: 'Search running models' }).fill('')
    await page.getByRole('button', { name: /Sort by model/ }).click()
    await expect(page.getByRole('table', { name: 'Running models' }).locator('tbody tr').first()).toContainText('model-0999')
  })

})
