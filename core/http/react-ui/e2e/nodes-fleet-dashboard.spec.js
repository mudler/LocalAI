import { test, expect } from './coverage-fixtures.js'

// The Swarm Nodes page: a sortable table of the cluster, a map of the same
// nodes, and the models running across them. Every call is stubbed, so the
// suite needs no workers, no NATS and no database.

const baseNodes = [
  { id: 'n1', name: 'atlas', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy', labels: { zone: 'east' }, total_vram: 100, available_vram: 40, total_ram: 200, available_ram: 100, total_disk: 1000, available_disk: 600, cpu_logical_cores: 8, cpu_usage_percent: 25, cpu_load_1: 1.5, model_count: 3, in_flight_count: 2, last_heartbeat: '2026-09-14T00:00:00Z', version: 'v3.9.0' },
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

const rows = page => page.locator('tbody tr[data-row]')

test.describe('Swarm nodes: navigation', () => {
  test('uses the standard Operate navigation at a desktop viewport', async ({ page }) => {
    await mockFullOperateNavigation(page)
    await mockNodes(page, [baseNodes[0]])
    await page.goto('/app/nodes')

    const primaryOperate = page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' })
    await expect(primaryOperate).toBeVisible({ timeout: 15_000 })
    await expect(primaryOperate).toHaveClass(/active/)

    // Distributed mode on: Swarm replaces This machine, and owns the Nodes route.
    const bar = page.locator('.hub-layout > .hub-bar .dk-hubtabs')
    await expect(bar).toBeVisible()
    await expect(bar.locator('[data-hub-tab="swarm"]')).toHaveAttribute('aria-current', 'page')
    await expect(bar.locator('[data-hub-tab="machine"]')).toHaveCount(0)
    await expect(bar.locator('a[href$="/swagger/index.html"]')).toHaveAttribute('target', '_blank')
    const sub = page.locator('.hub-subnav')
    for (const name of ['Nodes', 'Placement rules', 'Failover', 'P2P']) {
      await expect(sub.getByRole('link', { name })).toBeVisible()
    }
    await expect(sub.getByRole('link', { name: 'Nodes' })).toHaveAttribute('aria-current', 'page')
  })

  test('keeps the Swarm tab on a node detail page and on the add page', async ({ page }) => {
    await mockFullOperateNavigation(page)
    await mockNodes(page, [baseNodes[0]])
    for (const path of [`/app/nodes/${baseNodes[0].id}`, '/app/nodes/add']) {
      await page.goto(path)
      await expect(page.locator('.dk-hubtabs [data-hub-tab="swarm"]')).toHaveAttribute('aria-current', 'page')
    }
  })

  test('uses the Operate tab bar on mobile', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockFullOperateNavigation(page)
    await mockNodes(page, [baseNodes[0]])
    await page.goto('/app/nodes')

    await page.getByRole('button', { name: 'Open menu' }).click()
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' })).toBeVisible()
    await page.getByRole('button', { name: 'Close menu' }).click()

    const bar = page.locator('.hub-layout > .hub-bar .dk-hubtabs')
    await expect(bar).toBeVisible()
    expect((await bar.boundingBox()).width).toBeLessThanOrEqual(390)
    await bar.locator('[data-hub-tab="settings"]').scrollIntoViewIfNeeded()
    await expect(bar.locator('[data-hub-tab="settings"]')).toBeInViewport()
  })
})

test.describe('Swarm nodes: the table', () => {
  test('lists every node with its state in words and opens a node from its row', async ({ page }) => {
    await mockNodes(page)
    await page.goto('/app/nodes')

    const table = page.getByRole('table', { name: /Nodes with their state/ })
    await expect(table).toBeVisible({ timeout: 15_000 })
    await expect(rows(page)).toHaveCount(3)
    await expect(page.getByRole('row', { name: /atlas/ })).toContainText('Healthy')
    await expect(page.getByRole('row', { name: /borealis/ })).toContainText('Waiting for approval')
    await expect(page.getByRole('row', { name: /legacy/ })).toContainText('Offline')
    await expect(page.getByRole('row', { name: /atlas/ })).toContainText('v3.9.0')
    await expect(page.getByRole('link', { name: 'atlas' })).toHaveAttribute('href', '/app/nodes/n1')

    await page.getByRole('row', { name: /atlas/ }).locator('td').nth(2).click()
    await expect(page).toHaveURL(/\/app\/nodes\/n1$/)
  })

  test('filters to what needs attention, then by reason, then back', async ({ page }) => {
    await mockNodes(page)
    await page.goto('/app/nodes')
    const needs = page.getByRole('button', { name: /^Needs attention/ })
    await expect(needs).toContainText('2', { timeout: 15_000 })
    await expect(page.getByRole('button', { name: /^All/ })).toHaveAttribute('aria-pressed', 'true')

    await needs.click()
    await expect(needs).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByRole('row', { name: /borealis/ })).toBeVisible()
    await expect(page.getByRole('row', { name: /legacy/ })).toBeVisible()
    await expect(page.getByRole('row', { name: /atlas/ })).toHaveCount(0)

    // Only the reasons that apply are offered.
    await expect(page.getByRole('button', { name: /^Low models disk/ })).toBeVisible()
    await expect(page.getByRole('button', { name: /^Not answering/ })).toBeVisible()
    await page.getByRole('button', { name: /^Not answering/ }).click()
    await expect(page.getByRole('row', { name: /legacy/ })).toBeVisible()
    await expect(page.getByRole('row', { name: /borealis/ })).toHaveCount(0)

    await page.getByRole('button', { name: /^All/ }).click()
    await expect(rows(page)).toHaveCount(3)
  })

  test('says a low reading in the row and never flags a node that does not report', async ({ page }) => {
    await mockNodes(page)
    await page.goto('/app/nodes')
    await expect(page.getByRole('row', { name: /borealis/ })).toContainText('Low GPU memory', { timeout: 15_000 })
    await expect(page.getByRole('row', { name: /atlas/ })).not.toContainText('Low')
    await expect(page.getByRole('row', { name: /legacy/ })).not.toContainText('Low')
  })

  test('searches, filters by state and type, sorts and groups', async ({ page }) => {
    await mockNodes(page)
    await page.goto('/app/nodes')
    await expect(rows(page)).toHaveCount(3, { timeout: 15_000 })

    await page.getByRole('searchbox', { name: 'Search nodes' }).fill('legacy')
    await expect(rows(page)).toHaveCount(1)
    await expect(page.getByRole('row', { name: /legacy/ })).toBeVisible()
    await page.getByRole('searchbox', { name: 'Search nodes' }).fill('')

    await page.getByLabel('Filter state').selectOption('pending')
    await expect(rows(page)).toHaveCount(1)
    await expect(page.getByRole('row', { name: /borealis/ })).toBeVisible()
    await page.getByLabel('Filter state').selectOption('')
    await page.getByLabel('Filter type').selectOption('agent')
    await expect(rows(page)).toHaveCount(1)
    await page.getByLabel('Filter type').selectOption('')

    const nodeHeader = page.getByRole('columnheader', { name: 'Node', exact: true })
    await expect(nodeHeader).toHaveAttribute('aria-sort', 'ascending')
    await nodeHeader.getByRole('button').click()
    await expect(nodeHeader).toHaveAttribute('aria-sort', 'descending')
    await expect(rows(page).first()).toContainText('legacy')
    await page.getByRole('columnheader', { name: 'State' }).getByRole('button').click()
    await expect(page.getByRole('columnheader', { name: 'State' })).toHaveAttribute('aria-sort', 'ascending')
    await expect(nodeHeader).not.toHaveAttribute('aria-sort', /./)

    await page.getByLabel('Group nodes').selectOption('label:zone')
    await expect(page.getByText('Unlabelled', { exact: true })).toBeVisible()
    await expect(page.getByText('east', { exact: true })).toBeVisible()
  })

  test('switches between comfortable and compact rows and remembers the choice', async ({ page }) => {
    await mockNodes(page)
    await page.goto('/app/nodes')
    const table = page.getByRole('table', { name: /Nodes with their state/ })
    await expect(table).not.toHaveClass(/dk-table--compact/, { timeout: 15_000 })
    await page.getByRole('radio', { name: 'Compact' }).click()
    await expect(table).toHaveClass(/dk-table--compact/)
    await expect(page.getByRole('radio', { name: 'Compact' })).toHaveAttribute('aria-checked', 'true')
    await page.reload()
    await expect(page.getByRole('table', { name: /Nodes with their state/ })).toHaveClass(/dk-table--compact/, { timeout: 15_000 })
  })

  test('mounts only 50 rows and clamps pagination for a 1,000-node fleet', async ({ page }) => {
    const nodes = Array.from({ length: 1000 }, (_, index) => ({ id: `node-${index}`, name: `worker-${String(index).padStart(4, '0')}`, node_type: 'backend', address: `10.0.${Math.floor(index / 255)}.${index % 255}:50051`, status: 'healthy' }))
    await mockNodes(page, nodes)
    await page.goto('/app/nodes')
    await expect(rows(page)).toHaveCount(50, { timeout: 15_000 })
    await expect(page.getByText('Page 1 of 20')).toBeVisible()
    await page.getByRole('button', { name: 'Next page' }).click()
    await expect(page.getByText('Page 2 of 20')).toBeVisible()
    await page.getByRole('searchbox', { name: 'Search nodes' }).fill('worker-0000')
    await expect(page.getByText('Page 1 of 1')).toBeVisible()
  })

  test('keeps incomplete readings unknown', async ({ page }) => {
    await mockNodes(page, [{
      id: 'incomplete', name: 'incomplete-capacity', node_type: 'backend', status: 'healthy',
      total_vram: 100, total_ram: 200, total_disk: 300,
    }])
    await page.goto('/app/nodes')
    const row = page.getByRole('row', { name: /incomplete-capacity/ })
    await expect(row.getByText('No reading')).toHaveCount(1, { timeout: 15_000 })
    await expect(row).not.toContainText('Low')
  })

  test('keeps pending approval visible and approves from the row', async ({ page }) => {
    await mockNodes(page, [baseNodes[1]])
    let approvals = 0
    await page.route('**/api/nodes/n2/approve', route => {
      approvals += 1
      return route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    })
    await page.goto('/app/nodes')
    await page.getByRole('button', { name: 'Approve borealis' }).click()
    await expect.poll(() => approvals).toBe(1)
    await expect(page.getByText('Node approved')).toBeVisible()
  })

  test('selects with the keyboard without opening the node, and shows the partial-page state', async ({ page }) => {
    await mockNodes(page)
    await page.goto('/app/nodes')

    const checkbox = page.getByRole('checkbox', { name: 'Select atlas' })
    await checkbox.focus()
    await checkbox.press('Space')
    await expect(checkbox).toBeChecked()
    await expect(page).toHaveURL(/\/app\/nodes$/)
    await expect(page.getByRole('row', { name: /atlas/ })).toHaveAttribute('data-selected', 'true')

    const selectPage = page.getByRole('checkbox', { name: 'Select page' })
    await expect.poll(() => selectPage.evaluate(input => input.indeterminate)).toBe(true)
  })
})

test.describe('Swarm nodes: bulk actions', () => {
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

  test('disables bulk controls and the remove confirmation while removal is running', async ({ page }) => {
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

  test('updates a backend on the nodes that drifted, or on the selected ones', async ({ page }) => {
    const nodes = [
      { ...baseNodes[0], id: 'a', name: 'alpha' },
      { ...baseNodes[0], id: 'b', name: 'beta' },
      { ...baseNodes[0], id: 'c', name: 'gamma', status: 'pending' },
      { ...baseNodes[2], id: 'd', name: 'agent' },
    ]
    await mockNodes(page, nodes)
    await page.route('**/api/backends/upgrades', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ 'llama-cpp': { backend_name: 'llama-cpp', installed_version: '0.9.4', available_version: '0.9.7', node_drift: [{ node_id: 'a' }, { node_id: 'b' }, { node_id: 'c' }] } }),
    }))
    const posts = []
    await page.route('**/api/nodes/*/backends/upgrade', route => {
      posts.push({ id: route.request().url().split('/').at(-3), body: route.request().postDataJSON() })
      return route.fulfill({ status: 202, contentType: 'application/json', body: '{}' })
    })
    await page.goto('/app/nodes')

    // The pending node and the agent worker take no backend update.
    const button = page.getByRole('button', { name: 'Update llama-cpp on 2 nodes' })
    await expect(button).toBeVisible({ timeout: 15_000 })
    await page.getByRole('checkbox', { name: 'Select alpha' }).check()
    await expect(page.getByRole('button', { name: 'Update llama-cpp on 1 node' })).toBeVisible()
    await page.getByRole('button', { name: 'Clear selection' }).click()

    await button.click()
    await expect.poll(() => posts.length).toBe(2)
    expect(posts.map(post => post.id).sort()).toEqual(['a', 'b'])
    expect(posts.every(post => post.body.backend === 'llama-cpp')).toBe(true)
    await expect(page.getByText('Updating backends on 2 nodes')).toBeVisible()
  })
})

test.describe('Swarm nodes: the map', () => {
  test('draws the cluster once the replicas are read, and links every node', async ({ page }) => {
    await mockNodes(page, baseNodes.map(node => (node.id === 'n2' ? { ...node, status: 'healthy' } : node)))
    let modelRequests = 0
    await page.route('**/api/nodes/models', route => {
      modelRequests += 1
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(baseModels) })
    })
    await page.goto('/app/nodes')
    await expect(rows(page)).toHaveCount(3, { timeout: 15_000 })
    expect(modelRequests).toBe(0)

    await page.getByRole('radio', { name: /Map/ }).click()
    const map = page.getByTestId('nodes-map')
    await expect(map).toBeVisible()
    await expect(map).toContainText('This instance')
    await expect(page.getByTestId('map-node')).toHaveCount(3)
    await expect(page.getByTestId('map-node').first()).toHaveAttribute('href', '/app/nodes/n1')
    await expect(page.getByTestId('map-node').first()).toContainText('Llama 3.2')
    // An agent worker runs jobs, not models.
    await expect(page.getByTestId('map-node').nth(2)).toContainText('runs agent jobs')
    expect(modelRequests).toBe(1)
    await page.getByRole('radio', { name: /List/ }).click()
    await page.getByRole('radio', { name: /Map/ }).click()
    expect(modelRequests).toBe(1)

    await page.getByTestId('map-node').first().click()
    await expect(page).toHaveURL(/\/app\/nodes\/n1$/)
  })

  test('is not drawn on a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockNodes(page)
    await page.goto('/app/nodes')
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('radio', { name: /Map/ })).toHaveCount(0)
    await expect(page.getByRole('radio', { name: /List/ })).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('nodes-map')).toHaveCount(0)
  })
})

test.describe('Swarm nodes: first run, errors and a phone', () => {
  test('shows how to add the first worker when none has registered', async ({ page }) => {
    await mockNodes(page, [])
    await page.goto('/app/nodes')
    await expect(page.getByRole('heading', { name: 'No workers registered yet' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('command-block')).toContainText('LOCALAI_REGISTER_TO')
    await expect(page.getByRole('status').filter({ hasText: 'Listening for a worker' })).toBeVisible()
  })

  test('keeps the last list and says so when a refresh fails', async ({ page }) => {
    let calls = 0
    await page.route('**/api/nodes', route => {
      calls += 1
      if (calls === 1) return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(baseNodes) })
      return route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"database unavailable"}' })
    })
    await page.goto('/app/nodes')
    await expect(rows(page)).toHaveCount(3, { timeout: 15_000 })
    await expect(page.getByRole('alert')).toContainText('could not be refreshed', { timeout: 15_000 })
    await expect(rows(page)).toHaveCount(3)
  })

  test('lays nodes out as cards on a phone without sideways scrolling', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockNodes(page)
    await page.goto('/app/nodes')
    await expect(rows(page)).toHaveCount(3, { timeout: 15_000 })
    await expect(page.locator('.sw-table--nodes thead')).toBeHidden()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    const approve = page.getByRole('button', { name: 'Approve borealis' })
    const box = await approve.boundingBox()
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    await expect(page.getByRole('link', { name: 'Add a node' })).toBeVisible()
  })
})

test.describe('Swarm nodes: running models', () => {
  test('reads the replicas once, on first use, and opens a model to its nodes', async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 1050 })
    await mockNodes(page, baseNodes.map(node => (node.id === 'n2' ? { ...node, status: 'healthy' } : node)))
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
    await expect(rows(page)).toHaveCount(3, { timeout: 15_000 })
    expect(modelRequests).toBe(0)
    expect(backendRequests).toBe(0)

    await page.getByRole('radio', { name: /Running models/ }).click()
    const table = page.getByRole('table', { name: /Running models/ })
    await expect(table).toBeVisible()
    await expect(page.getByRole('row', { name: /Llama 3.2/ })).toContainText('3')
    await expect(rows(page)).toHaveCount(2)
    expect(modelRequests).toBe(1)

    const toggle = page.getByRole('button', { name: 'Show replicas of Llama 3.2' })
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await toggle.click()
    await expect(page.getByRole('button', { name: 'Hide replicas of Llama 3.2' })).toHaveAttribute('aria-expanded', 'true')
    await expect(table).toContainText('2 replicas')
    await expect(table).toContainText('borealis')
    await expect(table.getByRole('link', { name: 'atlas' })).toHaveAttribute('href', '/app/nodes/n1')

    // A replica on a node the roster no longer lists still shows, unlinked.
    await page.getByRole('button', { name: 'Show replicas of Whisper large v3' }).click()
    await expect(table).toContainText('missing')

    await page.getByRole('radio', { name: /List/ }).click()
    await page.getByRole('radio', { name: /Running models/ }).click()
    expect(modelRequests).toBe(1)
    expect(backendRequests).toBe(0)
  })

  test('opens logs directly for one replica and asks for the replica when a model has several', async ({ page }) => {
    await mockNodes(page, baseNodes.map(node => (node.id === 'n2' ? { ...node, status: 'healthy' } : node)))
    await page.route('**/api/nodes/models', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(baseModels) }))
    await page.goto('/app/nodes')
    await page.getByRole('radio', { name: /Running models/ }).click()

    await page.getByRole('button', { name: 'Actions for Whisper large v3' }).click()
    await page.getByRole('menuitem', { name: 'View logs…' }).click()
    await expect(page).toHaveURL(/\/app\/node-backend-logs\/missing\/Whisper%20large%20v3%230$/)

    await page.goto('/app/nodes')
    await page.getByRole('radio', { name: /Running models/ }).click()
    await page.getByRole('button', { name: 'Actions for Llama 3.2' }).click()
    await page.getByRole('menuitem', { name: 'View logs…' }).click()

    await expect(page.getByRole('button', { name: 'Hide replicas of Llama 3.2' })).toBeVisible()
    await page.getByRole('button', { name: 'View logs for Llama 3.2 replica 1 on atlas' }).click()
    await expect(page).toHaveURL(/\/app\/node-backend-logs\/n1\/Llama%203.2%230$/)
  })

  test('stops a running model once from a row menu and refreshes the inventory', async ({ page }) => {
    await mockNodes(page)
    let modelRequests = 0
    let stopRequests = 0
    let stopBody
    let finishStop
    await page.route('**/api/nodes/models', route => {
      modelRequests += 1
      const list = modelRequests === 1 ? baseModels : baseModels.filter(row => row.model_name !== 'Llama 3.2')
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(list) })
    })
    await page.route('**/backend/shutdown', async route => {
      stopRequests += 1
      stopBody = route.request().postDataJSON()
      await new Promise(resolve => { finishStop = resolve })
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{"message":"ok"}' })
    })
    await page.goto('/app/nodes')
    await page.getByRole('radio', { name: /Running models/ }).click()

    const trigger = page.getByRole('button', { name: 'Actions for Llama 3.2' })
    await expect(trigger).toBeVisible()
    await trigger.focus()
    await trigger.press('Enter')
    const menu = page.getByRole('menu', { name: 'Llama 3.2 actions' })
    await expect(menu).toBeVisible()
    await menu.press('Escape')
    await expect(menu).toHaveCount(0)
    await expect(trigger).toBeFocused()

    await trigger.click()
    await menu.getByRole('menuitem', { name: 'Stop model…' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Stop Llama 3.2?')
    await expect(dialog).toContainText('Llama 3.2 has 3 replicas loaded across 2 nodes. This stops all loaded placements on those nodes.')
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
    await expect(page.getByRole('button', { name: 'Actions for Llama 3.2' })).toHaveCount(0)
  })

  test('refreshes the inventory and warns about a partial shutdown after a stop failure', async ({ page }) => {
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
    await page.getByRole('radio', { name: /Running models/ }).click()
    await page.getByRole('button', { name: 'Actions for Whisper large v3' }).click()
    await page.getByRole('menuitem', { name: 'Stop model…' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Stop model' }).click()

    await expect.poll(() => stopRequests).toBe(1)
    await expect.poll(() => modelRequests).toBe(2)
    await expect(page.getByText(/Could not stop Whisper large v3:.*Some replicas may already have stopped\./)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Actions for Whisper large v3' })).toBeVisible()
  })

  test('shows loading, error, retry and empty states', async ({ page }) => {
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
    await page.getByRole('radio', { name: /Running models/ }).click()
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
    await page.getByRole('radio', { name: /Running models/ }).click()
    await expect(rows(page)).toHaveCount(50)
    await expect(page.getByText('Page 1 of 20')).toBeVisible()
    await page.getByRole('button', { name: 'Next model page' }).click()
    await expect(page.getByText('Page 2 of 20')).toBeVisible()
    await page.getByRole('searchbox', { name: 'Search model or backend…' }).fill('model-0000')
    await expect(page.getByText('Page 1 of 1')).toBeVisible()
    await page.getByRole('searchbox', { name: 'Search model or backend…' }).fill('')
    await page.getByRole('columnheader', { name: 'Model' }).getByRole('button').click()
    await expect(rows(page).first()).toContainText('model-0999')
  })
})
