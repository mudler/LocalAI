import { test, expect } from './coverage-fixtures.js'

const node = { id: 'n1', name: 'atlas', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy', labels: {}, total_vram: 100, available_vram: 40, total_ram: 200, available_ram: 100, total_disk: 1000, available_disk: 600, model_count: 0, in_flight_count: 0 }

const clusterSettings = {
  nats_url: 'nats://nats:4222',
  nats_worker_url: '',
  bus: { bus: 'nats', nats_configured: true, nats_connected: true },
  replicas: [
    { id: 'frontend-a', version: 'v4.12.0', last_seen: '2026-11-21T10:00:00Z' },
    { id: 'frontend-b', version: 'v4.12.0', last_seen: '2026-11-21T10:00:00Z' },
  ],
}

async function mockCluster(page, { settings = clusterSettings, settingsStatus = 200, nodes = [node] } = {}) {
  await page.route('**/api/features', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ distributed: true }) }))
  await page.route('**/api/auth/status', route => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user: { id: 'admin', name: 'Admin', role: 'admin', provider: 'local' } }),
  }))
  await page.route('**/api/nodes', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(nodes) }))
  const calls = { puts: [], carrier: 0 }
  const state = { settings }
  // The change of carrier is gone: the page must not call its route.
  await page.route('**/api/cluster/carrier', route => {
    calls.carrier++
    return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
  })
  await page.route('**/api/cluster/settings', async route => {
    const request = route.request()
    if (request.method() === 'PUT') {
      const body = request.postDataJSON()
      calls.puts.push(body)
      const answer = (state.onPut || (() => ({ status: 200, body: { saved: true, nats: { reachable: true } } })))(body)
      return route.fulfill({ status: answer.status, contentType: 'application/json', body: JSON.stringify(answer.body) })
    }
    return route.fulfill({ status: settingsStatus, contentType: 'application/json', body: JSON.stringify(settingsStatus === 200 ? state.settings : { error: 'distributed mode is not enabled on this frontend' }) })
  })
  return { calls, state }
}

test.describe('Cluster transport panel', () => {
  test('is hidden when the cluster routes answer 503', async ({ page }) => {
    await mockCluster(page, { settingsStatus: 503 })
    await page.goto('/app/nodes')
    await expect(page.getByTestId('fleet-hero')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('cluster-transport')).toHaveCount(0)
  })

  test('shows the bus of this frontend, the replicas and the workers, with no switch', async ({ page }) => {
    const nodes = [
      { ...node, id: 'w1', name: 'dual-worker', attached: 'nats,tunnel' },
      { ...node, id: 'w2', name: 'tunnel-worker', attached: 'tunnel' },
      { ...node, id: 'w3', name: 'old-worker' },
    ]
    const { calls } = await mockCluster(page, { nodes })
    await page.goto('/app/nodes')
    const panel = page.getByTestId('cluster-transport')
    await expect(panel).toBeVisible({ timeout: 15_000 })
    await expect(panel).toContainText('Broadcast bus between frontends')
    await expect(panel.getByTestId('transport-bus')).toHaveText('NATS connected')
    await expect(panel.getByTestId('transport-bus-note')).toHaveCount(0)
    await expect(panel.getByTestId('transport-replica')).toHaveCount(2)
    await expect(panel.getByTestId('transport-replica').first()).toContainText('frontend-a')
    const workers = panel.getByTestId('transport-worker')
    await expect(workers).toHaveCount(3)
    await expect(workers.nth(0)).toContainText('NATS + WebSocket')
    await expect(workers.nth(1)).toContainText('WebSocket')
    await expect(workers.nth(2)).toContainText('not reported')
    await expect(panel.getByRole('button', { name: /Switch|Abort|Dry run/ })).toHaveCount(0)
    await expect(panel).not.toContainText(/epoch|draining|carrier setting|database tunnel/i)
    await expect(panel).toContainText('Workers never connect to PostgreSQL')
    await expect(panel.getByLabel('NATS URL', { exact: true })).toHaveValue('nats://nats:4222')
    expect(calls.carrier).toBe(0)
  })

  test('shows PostgreSQL when this frontend broadcasts over PostgreSQL, and says why when NATS is out of reach', async ({ page }) => {
    const { state } = await mockCluster(page, { settings: { ...clusterSettings, nats_url: '', bus: { bus: 'postgres', nats_configured: false, nats_connected: false } } })
    await page.goto('/app/nodes')
    const panel = page.getByTestId('cluster-transport')
    await expect(panel.getByTestId('transport-bus')).toHaveText('PostgreSQL', { timeout: 15_000 })
    await expect(panel.getByTestId('transport-bus-note')).toHaveCount(0)

    state.settings = { ...clusterSettings, bus: { bus: 'postgres', nats_configured: true, nats_connected: false } }
    await page.reload()
    await expect(panel.getByTestId('transport-bus')).toHaveText('PostgreSQL', { timeout: 15_000 })
    await expect(panel.getByTestId('transport-bus-note')).toContainText('does not reach it')
  })

  test('shows the live state of each route of a worker from the node list', async ({ page }) => {
    const nodes = [
      { ...node, id: 'w1', name: 'dual-worker', route_state: [
        { route: 'heartbeat', state: 'live' },
        { route: 'nats', state: 'down', reason: 'connecting' },
        { route: 'tunnel', transport: 'websocket', state: 'live' },
      ] },
      { ...node, id: 'w2', name: 'tunnel-worker', route_state: [
        { route: 'heartbeat', state: 'down', reason: 'the heartbeat is stale' },
        { route: 'tunnel', transport: 'p2p', state: 'reconnecting' },
      ] },
      { ...node, id: 'w3', name: 'old-worker' },
    ]
    await mockCluster(page, { nodes })
    await page.goto('/app/nodes')
    const panel = page.getByTestId('cluster-transport')
    await expect(panel).toBeVisible({ timeout: 15_000 })
    const workers = panel.getByTestId('transport-worker')
    const dual = workers.nth(0).getByTestId('transport-route')
    await expect(dual).toHaveCount(2)
    await expect(dual.nth(0)).toHaveText('NATS: down')
    await expect(dual.nth(0)).toHaveAttribute('title', 'down: connecting')
    await expect(dual.nth(1)).toHaveText('WebSocket: live')
    const tunnel = workers.nth(1).getByTestId('transport-route')
    await expect(tunnel).toHaveCount(2)
    await expect(tunnel.nth(0)).toHaveText('Heartbeat: down')
    await expect(tunnel.nth(1)).toHaveText('P2P: reconnecting')
    // A worker whose node has no route state and no report.
    await expect(workers.nth(2)).toContainText('not reported')
  })

  test('saving the NATS settings reports reachability and says when the address is used', async ({ page }) => {
    const { calls } = await mockCluster(page)
    await page.goto('/app/nodes')
    const panel = page.getByTestId('cluster-transport')
    await panel.getByLabel('NATS URL', { exact: true }).fill('nats://nats.example:4222')
    await panel.getByLabel('Worker NATS URL').fill('nats://nats.public.example:4222')
    await panel.getByRole('button', { name: 'Save' }).click()
    await expect(panel.getByTestId('transport-settings-result')).toContainText('reaches the NATS server')
    await expect(panel.getByTestId('transport-settings-result')).toContainText('Restart the frontends')
    expect(calls.puts[0]).toEqual({ nats_url: 'nats://nats.example:4222', nats_worker_url: 'nats://nats.public.example:4222' })
  })

  test('shows the three carrier weights with their defaults when the server reports none', async ({ page }) => {
    await mockCluster(page)
    await page.goto('/app/nodes')
    const form = page.getByTestId('carrier-weights')
    await expect(form).toBeVisible({ timeout: 15_000 })
    await expect(form.getByLabel('NATS weight')).toHaveValue('100')
    await expect(form.getByLabel('WebSocket weight')).toHaveValue('50')
    await expect(form.getByLabel('P2P weight')).toHaveValue('25')
    await expect(form).toContainText('never overrides fit')
    await expect(form.getByRole('button', { name: 'Apply weights' })).toBeDisabled()
    await expect(form.getByRole('button', { name: 'Use defaults' })).toBeDisabled()
  })

  test('saving a weight sends only the weight that changed', async ({ page }) => {
    const { calls } = await mockCluster(page)
    await page.goto('/app/nodes')
    const form = page.getByTestId('carrier-weights')
    await form.getByLabel('P2P weight').fill('300')
    await form.getByRole('button', { name: 'Apply weights' }).click()
    await expect(form.getByTestId('carrier-weights-result')).toContainText('within a few seconds')
    expect(calls.puts).toEqual([{ carrier_weights: { p2p: 300 } }])
  })

  test('shows the weights in force from the server and puts the defaults back', async ({ page }) => {
    const settings = { ...clusterSettings, carrier_weights: { nats: 10, websocket: 50, p2p: 25 }, carrier_weights_default: { nats: 100, websocket: 50, p2p: 25 } }
    const { calls } = await mockCluster(page, { settings })
    await page.goto('/app/nodes')
    const form = page.getByTestId('carrier-weights')
    await expect(form.getByLabel('NATS weight')).toHaveValue('10')
    await form.getByRole('button', { name: 'Use defaults' }).click()
    await expect(form.getByTestId('carrier-weights-result')).toContainText('default weights are back')
    expect(calls.puts).toEqual([{ carrier_weights: { nats: null } }])
  })

  test('refuses a weight outside 0 to 1000 before it calls the server', async ({ page }) => {
    const { calls } = await mockCluster(page)
    await page.goto('/app/nodes')
    const form = page.getByTestId('carrier-weights')
    await form.getByLabel('WebSocket weight').fill('1001')
    await expect(form.getByTestId('carrier-weight-error-websocket')).toContainText('0 to 1000')
    await expect(form.getByRole('button', { name: 'Apply weights' })).toBeDisabled()
    await form.getByLabel('WebSocket weight').fill('0')
    await expect(form.getByTestId('carrier-weight-error-websocket')).toHaveCount(0)
    await expect(form.getByRole('button', { name: 'Apply weights' })).toBeEnabled()
    expect(calls.puts).toHaveLength(0)
  })

  test('shows the error when the server refuses the weights', async ({ page }) => {
    const { state } = await mockCluster(page)
    state.onPut = () => ({ status: 400, body: { error: 'carrier_weights.nats: a weight must be between 0 and 1000, not 5000' } })
    await page.goto('/app/nodes')
    const form = page.getByTestId('carrier-weights')
    await form.getByLabel('NATS weight').fill('5')
    await form.getByRole('button', { name: 'Apply weights' }).click()
    await expect(form.getByTestId('carrier-weights-result')).toContainText('between 0 and 1000')
  })

  test('a NATS URL that cannot be reached shows the error', async ({ page }) => {
    const { state } = await mockCluster(page)
    state.onPut = () => ({ status: 422, body: { error: 'nats_url: this replica cannot reach the NATS server at nats://bad:4222: connection refused' } })
    await page.goto('/app/nodes')
    const panel = page.getByTestId('cluster-transport')
    await panel.getByLabel('NATS URL', { exact: true }).fill('nats://bad:4222')
    await panel.getByRole('button', { name: 'Save' }).click()
    await expect(panel.getByTestId('transport-settings-result')).toContainText('cannot reach the NATS server')
  })

  test('renders on a narrow viewport without horizontal overflow', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockCluster(page)
    await page.goto('/app/nodes')
    const panel = page.getByTestId('cluster-transport')
    await expect(panel).toBeVisible({ timeout: 15_000 })
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })

  test('the worker command does not ask for a NATS address', async ({ page }) => {
    await mockCluster(page, { nodes: [] })
    await page.goto('/app/nodes')
    const commands = page.getByTestId('add-node').getByTestId('command-block')
    const worker = commands.filter({ hasText: 'LOCALAI_REGISTER_TO' })
    await expect(worker).toBeVisible({ timeout: 15_000 })
    // One flag on each line, as a shell reads it.
    expect(await worker.locator('pre').innerText()).toMatch(/ \\\n  -e LOCALAI_REGISTER_TO=.* \\\n  -e LOCALAI_REGISTRATION_TOKEN=/)
    for (const command of await commands.allTextContents()) {
      expect(command).not.toContain('nats')
      expect(command).not.toContain('NATS')
    }
  })
})
