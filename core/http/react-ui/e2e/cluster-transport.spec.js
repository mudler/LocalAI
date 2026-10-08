import { test, expect } from './coverage-fixtures.js'

const node = { id: 'n1', name: 'atlas', node_type: 'backend', address: '10.0.0.1:50051', status: 'healthy', labels: {}, total_vram: 100, available_vram: 40, total_ram: 200, available_ram: 100, total_disk: 1000, available_disk: 600, model_count: 0, in_flight_count: 0 }

const stableReport = {
  row: { active: 'nats', epoch: 4, state: 'stable', changed_by: 'admin', changed_at: '2026-10-08T10:00:00Z', prev_epoch: 3 },
  active: 'nats', epoch: 4, state: 'stable', ok: true,
  replicas: [
    { id: 'frontend-a', version: 'v4.12.0', ready_epoch: 4 },
    { id: 'frontend-b', version: 'v4.12.0', ready_epoch: 4 },
  ],
  workers: [
    { id: 'w1', name: 'dual-worker', type: 'backend', attached: ['nats'], follow: ['nats', 'tunnel'], can_follow: true },
    { id: 'w2', name: 'tunnel-worker', type: 'backend', attached: ['tunnel'], follow: ['tunnel'], follow_error: 'no routable address: it cannot follow a change to NATS', can_follow: false },
    { id: 'w3', name: 'old-worker', type: 'backend', attached: [], can_follow: false },
  ],
  in_flight: { loads: 0, jobs: 1, pending_claims: 2, claimed_claims: 0 },
  timings: {},
  settings: { nats_url: 'nats://nats:4222', nats_worker_url: '', prepare_timeout: '', transition_window: '', max_drain: '' },
}

const blockedDryRun = {
  ...stableReport, target: 'tunnel', ok: false,
  blockers: [
    { kind: 'worker', id: 'w3', reason: 'the worker predates carrier switching: it reports no capabilities and stays on NATS', forceable: true },
  ],
}
const cleanDryRun = { ...stableReport, target: 'tunnel', ok: true }

async function mockCluster(page, { carrier = stableReport, carrierStatus = 200, nodes = [node] } = {}) {
  await page.route('**/api/features', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ distributed: true }) }))
  await page.route('**/api/auth/status', route => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user: { id: 'admin', name: 'Admin', role: 'admin', provider: 'local' } }),
  }))
  await page.route('**/api/nodes', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(nodes) }))
  const calls = { posts: [], puts: [] }
  const state = { carrier }
  await page.route('**/api/cluster/carrier', async route => {
    const request = route.request()
    if (request.method() === 'POST') {
      const body = request.postDataJSON()
      calls.posts.push(body)
      const handler = state.onPost || (() => ({ status: 202, body: { state: 'prepare', active: 'nats', target: 'tunnel', epoch: 5 } }))
      const answer = handler(body)
      return route.fulfill({ status: answer.status, contentType: 'application/json', body: JSON.stringify(answer.body) })
    }
    return route.fulfill({ status: carrierStatus, contentType: 'application/json', body: JSON.stringify(carrierStatus === 200 ? state.carrier : { error: 'distributed mode is not enabled on this frontend' }) })
  })
  await page.route('**/api/cluster/settings', async route => {
    const body = route.request().postDataJSON()
    calls.puts.push(body)
    const answer = (state.onPut || (() => ({ status: 200, body: { saved: true, nats: { reachable: true } } })))(body)
    return route.fulfill({ status: answer.status, contentType: 'application/json', body: JSON.stringify(answer.body) })
  })
  return { calls, state }
}

test.describe('Cluster transport panel', () => {
  test('is hidden when the cluster routes answer 503', async ({ page }) => {
    await mockCluster(page, { carrierStatus: 503 })
    await page.goto('/app/nodes')
    await expect(page.getByLabel('Fleet health summary')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('cluster-transport')).toHaveCount(0)
  })

  test('shows the active carrier, epoch, replicas and workers in the stable state', async ({ page }) => {
    await mockCluster(page)
    await page.goto('/app/nodes')
    const panel = page.getByTestId('cluster-transport')
    await expect(panel).toBeVisible({ timeout: 15_000 })
    await expect(panel.getByTestId('transport-active')).toHaveText('NATS')
    await expect(panel.getByTestId('transport-state')).toHaveText('stable')
    await expect(panel.getByTestId('transport-epoch')).toHaveText('epoch 4')
    await expect(panel.getByTestId('transport-replica')).toHaveCount(2)
    await expect(panel.getByTestId('transport-replica').first()).toContainText('ready')
    const workers = panel.getByTestId('transport-worker')
    await expect(workers).toHaveCount(3)
    await expect(workers.nth(0)).toContainText('Dual-capable')
    await expect(workers.nth(1)).toContainText('Tunnel-only')
    await expect(workers.nth(1)).toContainText('no routable address')
    await expect(workers.nth(2)).toContainText('Legacy (NATS only)')
    await expect(panel.getByRole('button', { name: /Abort change/ })).toHaveCount(0)
    await expect(panel.getByLabel('NATS URL', { exact: true })).toHaveValue('nats://nats:4222')
  })

  test('dry run lists blockers and the confirmation gates the switch', async ({ page }) => {
    const { calls, state } = await mockCluster(page)
    state.onPost = body => body.dry_run ? { status: 200, body: blockedDryRun } : { status: 202, body: { state: 'prepare', active: 'nats', target: 'tunnel', epoch: 5 } }
    await page.goto('/app/nodes')
    await page.getByRole('button', { name: /Switch to Database tunnel/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Switch cluster transport' })
    const start = dialog.getByRole('button', { name: /^Switch to Database tunnel$/ })
    await expect(start).toBeDisabled()
    await dialog.getByRole('button', { name: 'Dry run' }).click()
    await expect(dialog.getByTestId('transport-blocker')).toHaveCount(1)
    await expect(dialog.getByTestId('transport-blocker')).toContainText('predates carrier switching')
    await expect(dialog.getByTestId('transport-dry-verdict')).toContainText('1 blocker')
    await expect(dialog.getByLabel('Live replicas').locator('li')).toHaveCount(2)
    expect(calls.posts[0]).toEqual({ target: 'tunnel', dry_run: true })
    // A blocker without force keeps the button off, even once confirmed.
    const confirm = dialog.getByLabel('I confirm these are all the frontends and all are upgraded')
    await confirm.check()
    await expect(start).toBeDisabled()
  })

  test('a clean dry run needs the confirmation, then starts the change', async ({ page }) => {
    const { calls, state } = await mockCluster(page)
    state.onPost = body => body.dry_run ? { status: 200, body: cleanDryRun } : { status: 202, body: { state: 'prepare', active: 'nats', target: 'tunnel', epoch: 5 } }
    await page.goto('/app/nodes')
    await page.getByRole('button', { name: /Switch to Database tunnel/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Switch cluster transport' })
    await dialog.getByRole('button', { name: 'Dry run' }).click()
    await expect(dialog.getByTestId('transport-dry-verdict')).toContainText('Nothing blocks')
    const start = dialog.getByRole('button', { name: /^Switch to Database tunnel$/ })
    await expect(start).toBeDisabled()
    await dialog.getByLabel('I confirm these are all the frontends and all are upgraded').check()
    await expect(start).toBeEnabled()
    await start.click()
    await expect(dialog).toHaveCount(0)
    expect(calls.posts.at(-1)).toEqual({ target: 'tunnel', force: false })
  })

  test('force needs its own confirmation', async ({ page }) => {
    const { calls, state } = await mockCluster(page)
    state.onPost = body => body.dry_run ? { status: 200, body: blockedDryRun } : { status: 202, body: { state: 'prepare', active: 'nats', target: 'tunnel', epoch: 5, force: true } }
    await page.goto('/app/nodes')
    await page.getByRole('button', { name: /Switch to Database tunnel/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Switch cluster transport' })
    await dialog.getByRole('button', { name: 'Dry run' }).click()
    await dialog.getByLabel('I confirm these are all the frontends and all are upgraded').check()
    await dialog.getByLabel('Force the change past the blockers above').check()
    await dialog.getByRole('button', { name: /^Force switch to Database tunnel$/ }).click()
    const confirm = page.getByRole('alertdialog')
    await expect(confirm).toContainText('Force the change?')
    // Nothing is sent until the second confirmation.
    expect(calls.posts.filter(p => !p.dry_run)).toHaveLength(0)
    await confirm.getByRole('button', { name: 'Force the change' }).click()
    await expect(dialog).toHaveCount(0)
    expect(calls.posts.at(-1)).toEqual({ target: 'tunnel', force: true })
  })

  test('a refused request shows the blockers of the server', async ({ page }) => {
    const { state } = await mockCluster(page)
    state.onPost = body => body.dry_run ? { status: 200, body: cleanDryRun } : { status: 422, body: { error: 'the change is blocked', blockers: [{ kind: 'replica', id: 'frontend-b', reason: 'frontend-b cannot build the tunnel carrier', forceable: true }] } }
    await page.goto('/app/nodes')
    await page.getByRole('button', { name: /Switch to Database tunnel/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Switch cluster transport' })
    await dialog.getByRole('button', { name: 'Dry run' }).click()
    await dialog.getByLabel('I confirm these are all the frontends and all are upgraded').check()
    await dialog.getByRole('button', { name: /^Switch to Database tunnel$/ }).click()
    await expect(dialog.getByRole('alert')).toContainText('frontend-b cannot build the tunnel carrier')
  })

  test('shows replica readiness and an abort button during prepare', async ({ page }) => {
    const prepare = {
      ...stableReport, state: 'prepare', epoch: 5, target: 'tunnel',
      row: { ...stableReport.row, epoch: 5, state: 'prepare', target: 'tunnel' },
      replicas: [{ id: 'frontend-a', version: 'v4.12.0', ready_epoch: 5 }, { id: 'frontend-b', version: 'v4.12.0', ready_epoch: 4 }],
    }
    const { calls, state } = await mockCluster(page, { carrier: prepare })
    state.onPost = () => ({ status: 200, body: { active: 'nats', state: 'stable', epoch: 6 } })
    await page.goto('/app/nodes')
    const panel = page.getByTestId('cluster-transport')
    await expect(panel.getByTestId('transport-state')).toHaveText('prepare')
    await expect(panel.getByRole('button', { name: /^Switch to/ })).toBeDisabled()
    const replicas = panel.getByTestId('transport-replica')
    await expect(replicas.nth(0)).toContainText('ready')
    await expect(replicas.nth(1)).toContainText('not ready')
    await panel.getByRole('button', { name: 'Abort change' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Abort the change' }).click()
    await expect.poll(() => calls.posts.length).toBe(1)
    expect(calls.posts[0]).toEqual({ abort: true })
  })

  test('counts down the drain after a commit', async ({ page }) => {
    const draining = {
      ...stableReport, active: 'tunnel', epoch: 6,
      row: { ...stableReport.row, active: 'tunnel', epoch: 6, draining: 'nats' },
      drain_remaining_ns: 125e9,
    }
    await mockCluster(page, { carrier: draining })
    await page.goto('/app/nodes')
    const panel = page.getByTestId('cluster-transport')
    await expect(panel.getByTestId('transport-active')).toHaveText('Database tunnel')
    await expect(panel.getByTestId('transport-state')).toHaveText('draining')
    const drain = panel.getByTestId('transport-drain')
    await expect(drain).toContainText('Draining NATS')
    await expect(drain).toContainText(/2:0\d left|2:1\d left|2:2\d left/)
    const first = await drain.textContent()
    await expect.poll(async () => drain.textContent(), { timeout: 5000 }).not.toBe(first)
  })

  test('saving the NATS settings reports reachability and does not switch', async ({ page }) => {
    const { calls } = await mockCluster(page)
    await page.goto('/app/nodes')
    const panel = page.getByTestId('cluster-transport')
    await panel.getByLabel('NATS URL', { exact: true }).fill('nats://nats.example:4222')
    await panel.getByLabel('Worker NATS URL').fill('nats://nats.public.example:4222')
    await panel.getByRole('button', { name: 'Save' }).click()
    await expect(panel.getByTestId('transport-settings-result')).toContainText('reaches the NATS server')
    await expect(panel.getByTestId('transport-settings-result')).toContainText('did not change')
    expect(calls.puts[0]).toEqual({ nats_url: 'nats://nats.example:4222', nats_worker_url: 'nats://nats.public.example:4222' })
    expect(calls.posts).toHaveLength(0)
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
})
