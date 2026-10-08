import { test, expect } from './coverage-fixtures.js'
import { mockSwarm, clusterNodes, clusterReplicas } from './swarm-fixtures.js'

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
  test('renders the header, the vitals and each tab for a node', async ({ page }) => {
    await mockNode(page)
    await page.goto(`/app/nodes/${ID}`)
    await expect(page.getByRole('heading', { name: 'alpha' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('25.0% of 16 cores')).toBeVisible()
    await expect(page.getByText('2.50 load (1m)')).toBeVisible()
    await expect(page.getByText('37.3 GB / 93.1 GB')).toBeVisible()
    await expect(page.getByRole('link', { name: 'All nodes' })).toHaveAttribute('href', '/app/nodes')
    await expect(page.getByRole('region', { name: 'Node resources' })).toBeVisible()

    // Models is the first tab.
    await expect(page.getByRole('tab', { name: /Models/ })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByRole('region', { name: 'Running models' })).toContainText('llama-3.3')

    await page.getByRole('tab', { name: /Backends/ }).click()
    await expect(page.getByRole('region', { name: 'Installed backends' })).toContainText('llama-cpp')
    await expect(page).toHaveURL(/tab=backends$/)

    await page.getByRole('tab', { name: 'Logs' }).click()
    await expect(page.getByRole('link', { name: 'llama-3.3' })).toHaveAttribute('href', /node-backend-logs\/n1\/llama-3\.3%230$/)

    await page.getByRole('tab', { name: 'Capacity and labels' }).click()
    await expect(page.getByText('env=prod')).toBeVisible()
  })

  test('opens straight onto a tab from the address, and moves between tabs with the arrow keys', async ({ page }) => {
    await mockNode(page)
    await page.goto(`/app/nodes/${ID}?tab=config`)
    await expect(page.getByRole('tab', { name: 'Capacity and labels' })).toHaveAttribute('aria-selected', 'true', { timeout: 15_000 })
    await page.getByRole('tab', { name: 'Capacity and labels' }).focus()
    await page.keyboard.press('ArrowRight')
    await expect(page.getByRole('tab', { name: /Models/ })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByRole('tab', { name: /Models/ })).toBeFocused()
    await expect(page).not.toHaveURL(/tab=/)
  })

  test('opens the logs of a replica from its row menu', async ({ page }) => {
    await mockNode(page)
    await page.goto(`/app/nodes/${ID}`)
    await page.getByRole('button', { name: 'Actions for llama-3.3 replica 1' }).click()
    await page.getByRole('menuitem', { name: 'View logs' }).click()
    await expect(page).toHaveURL(/\/app\/node-backend-logs\/n1\/llama-3.3%230$/)
  })

  test('is reached by clicking a node in the roster', async ({ page }) => {
    await page.route('**/api/nodes', r => r.fulfill({ status: 200, contentType: 'application/json',
      body: JSON.stringify([{ id: ID, name: 'alpha', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy' }]) }))
    await mockNode(page)
    await page.goto('/app/nodes')
    await page.getByRole('link', { name: 'alpha' }).click()
    await expect(page).toHaveURL(new RegExp(`/app/nodes/${ID}$`))
    await expect(page.getByRole('heading', { name: 'alpha' })).toBeVisible()
  })

  for (const [status, action] of [['healthy', 'Drain'], ['draining', 'Resume'], ['unhealthy', null], ['offline', null], ['unknown', null]]) {
    test(`shows only the accepted lifecycle action for ${status} nodes`, async ({ page }) => {
      await mockNode(page, { status })
      await page.goto(`/app/nodes/${ID}`)
      await expect(page.getByRole('heading', { name: 'alpha' })).toBeVisible({ timeout: 15_000 })
      await expect(page.getByRole('button', { name: /Approve/ })).toHaveCount(0)
      await expect(page.getByRole('button', { name: /Drain/ })).toHaveCount(action === 'Drain' ? 1 : 0)
      await expect(page.getByRole('button', { name: /Resume/ })).toHaveCount(action === 'Resume' ? 1 : 0)
      await expect(page.getByRole('button', { name: /Remove/ })).toBeVisible()
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

    await expect(page.getByTestId('pending-banner')).toContainText('It gets no requests until you approve it')
    await page.getByRole('button', { name: 'Approve' }).click()
    await expect.poll(() => approvalRequests).toBe(1)
    await expect(page.getByText('Node approved')).toBeVisible()
    await expect(page.getByRole('button', { name: /Drain/ })).toBeVisible()
    await expect(page.getByTestId('pending-banner')).toHaveCount(0)
  })

  test('renders valid totals with missing available capacity as No data', async ({ page }) => {
    await mockNode(page, {
      total_vram: 24e9, available_vram: undefined,
      total_ram: 32e9, available_ram: undefined,
      total_disk: 100e9, available_disk: undefined,
    })
    await page.goto(`/app/nodes/${ID}`)
    const vitals = page.getByRole('region', { name: 'Node resources' })
    await expect(vitals).toContainText('VRAM')
    await expect(vitals).toContainText('RAM')
    await expect(vitals).toContainText('Models disk free')
    await expect(vitals.getByText('No data')).toHaveCount(3)
  })

  test('keeps model operations visible on a narrow screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockNode(page)
    await page.goto(`/app/nodes/${ID}`)

    const action = page.getByRole('button', { name: 'Actions for llama-3.3 replica 1' })
    await expect(action).toBeVisible()
    const box = await action.boundingBox()
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
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

  test('says a missing node is missing', async ({ page }) => {
    await page.route(`**/api/nodes/${ID}`, route => route.fulfill({ status: 404, contentType: 'application/json', body: '{"error":"node not found"}' }))
    await page.goto(`/app/nodes/${ID}`)
    await expect(page.getByRole('heading', { name: 'Node not found' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('link', { name: 'All nodes' })).toBeVisible()
  })

  test('still shows the node when its backends cannot be read', async ({ page }) => {
    await mockNode(page)
    await page.route(`**/api/nodes/${ID}/backends`, r => r.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"nats unavailable"}' }))
    await page.goto(`/app/nodes/${ID}?tab=backends`)
    await expect(page.getByRole('heading', { name: 'alpha' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('alert')).toContainText('Could not read the backends on this node')
    await page.getByRole('tab', { name: /Models/ }).click()
    await expect(page.getByRole('region', { name: 'Running models' })).toContainText('llama-3.3')
  })
})

test.describe('Node detail: drain, resume and remove', () => {
  test('previews a drain from the loaded replicas and the rules, then drains', async ({ page }) => {
    const log = await mockSwarm(page)
    await page.goto('/app/nodes/n-gpu1')
    await expect(page.getByRole('heading', { name: 'gpu-box-1' })).toBeVisible({ timeout: 15_000 })

    // The section is open on a healthy node and says it is a preview.
    const section = page.getByRole('region', { name: 'What happens if I drain this node' })
    await expect(section).toContainText('Preview')
    await expect(section).toContainText('3 requests in flight finish first')
    await expect(section.getByTestId('leave-list')).toContainText('is unavailable until this node returns')
    await expect(section).toContainText('The server does not compute it')

    await page.getByRole('button', { name: /Drain/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Drain gpu-box-1?' })
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('Preview')
    await expect(dialog.getByTestId('leave-list')).toContainText('qwen3-8b-instruct')
    await dialog.getByRole('button', { name: 'Drain node' }).click()
    await expect.poll(() => log.filter(entry => entry.path === '/api/nodes/n-gpu1/drain').length).toBe(1)
    await expect(page.getByText('Node set to draining')).toBeVisible()
    await expect(dialog).toHaveCount(0)
  })

  test('says a model stays up when another healthy node has it, and which node', async ({ page }) => {
    const nodes = clusterNodes().map(node => (node.id === 'n-gpu2' ? { ...node, status: 'healthy' } : node))
    await mockSwarm(page, { nodes })
    await page.goto('/app/nodes/n-gpu1')
    await expect(page.getByTestId('leave-list')).toContainText('qwen3-8b-instruct stays available on gpu-box-2', { timeout: 15_000 })
  })

  test('closing the drain dialog sends nothing', async ({ page }) => {
    const log = await mockSwarm(page)
    await page.goto('/app/nodes/n-gpu1')
    await page.getByRole('button', { name: /Drain/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Drain gpu-box-1?' })
    await expect(dialog).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    await expect(page.getByRole('button', { name: /Drain/ })).toBeFocused()
    expect(log.filter(entry => entry.method === 'POST')).toEqual([])
  })

  test('says so plainly when the preview data cannot be read', async ({ page }) => {
    await mockSwarm(page, { replicas: null })
    await page.goto('/app/nodes/n-gpu1')
    await expect(page.getByRole('region', { name: 'What happens if I drain this node' })).toContainText('cannot be worked out now', { timeout: 15_000 })
  })

  test('resumes a draining node', async ({ page }) => {
    const nodes = clusterNodes().map(node => (node.id === 'n-gpu1' ? { ...node, status: 'draining' } : node))
    const log = await mockSwarm(page, { nodes })
    await page.goto('/app/nodes/n-gpu1')
    await expect(page.getByTestId('draining-banner')).toContainText('It takes no new requests')
    await page.getByRole('button', { name: /Resume/ }).click()
    await expect.poll(() => log.filter(entry => entry.path === '/api/nodes/n-gpu1/resume').length).toBe(1)
    await expect(page.getByText('Node resumed')).toBeVisible()
  })

  test('removes a node only after its name is typed', async ({ page }) => {
    const log = await mockSwarm(page)
    await page.goto('/app/nodes/n-gpu1')
    await page.getByRole('button', { name: /Remove/ }).click()
    const dialog = page.getByRole('alertdialog', { name: 'Remove gpu-box-1?' })
    await expect(dialog).toContainText('2 loaded replicas on this node stop serving')
    const confirm = dialog.getByRole('button', { name: 'Remove node' })
    await expect(confirm).toBeDisabled()
    const field = dialog.getByLabel('Type gpu-box-1 to confirm')
    await field.fill('gpu-box')
    await expect(confirm).toBeDisabled()
    await field.fill('gpu-box-1')
    await expect(confirm).toBeEnabled()
    await confirm.click()
    await expect.poll(() => log.filter(entry => entry.method === 'DELETE' && entry.path === '/api/nodes/n-gpu1').length).toBe(1)
    await expect(page).toHaveURL(/\/app\/nodes$/)
  })

  test('cancelling the remove dialog deletes nothing', async ({ page }) => {
    const log = await mockSwarm(page)
    await page.goto('/app/nodes/n-gpu1')
    await page.getByRole('button', { name: /Remove/ }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
    expect(log.filter(entry => entry.method === 'DELETE')).toEqual([])
  })

  test('a node that stopped answering says so, shows its last figures and offers no drain', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/nodes/n-gpu2')
    const banner = page.getByTestId('down-banner')
    await expect(banner).toContainText('Last heartbeat 4m ago', { timeout: 15_000 })
    await expect(banner.getByRole('link', { name: 'See failover' })).toHaveAttribute('href', '/app/failover')
    await expect(page.getByRole('button', { name: /Drain|Resume|Approve/ })).toHaveCount(0)
    await expect(page.getByRole('region', { name: 'What the loss of this node means' })).toContainText('flux-dev')
    await expect(page.getByText('Not answering').first()).toBeVisible()
  })

  test('unloads a model after asking, and warns when requests are running', async ({ page }) => {
    const log = await mockSwarm(page, {
      nodeModels: { 'n-gpu1': [{ node_id: 'n-gpu1', model_name: 'qwen3-8b-instruct', state: 'loaded', in_flight: 2, replica_index: 0 }] },
    })
    await page.goto('/app/nodes/n-gpu1')
    await page.getByRole('button', { name: 'Actions for qwen3-8b-instruct replica 1' }).click()
    await page.getByRole('menuitem', { name: 'Unload model…' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('2 request(s) in flight. Unloading interrupts them.')
    await dialog.getByRole('button', { name: 'Unload' }).click()
    await expect.poll(() => log.find(entry => entry.path === '/api/nodes/n-gpu1/models/unload')?.body).toEqual({ model_name: 'qwen3-8b-instruct' })
  })
})
