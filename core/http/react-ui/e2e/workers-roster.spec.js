import { test, expect } from './coverage-fixtures.js'
import { clusterNodes, clusterSettingsFixture, mockSwarm } from './swarm-fixtures.js'
import { mockSplit, splitWorkers } from './split-fixtures.js'
import { mockReadModel } from './operate-fixtures.js'

// The Workers roster: one list of every machine of the cluster, a chip for each
// connection it holds, the inspector on the right with the health of each
// connection and the actions of its state, the connectivity pill, the summary
// strip and the cluster settings button. Every call is stubbed.

const row = (page, name) => page.locator(`[data-row][data-entity="${name}"]`)
const open = async (page, name) => {
  await row(page, name).getByRole('button', { name: new RegExp(`^${name}`) }).click()
  return page.getByTestId('node-inspector')
}

test.describe('Workers: the header and the strip', () => {
  test('shows the connectivity of the frontends, the two buttons and the summary strip', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/nodes')
    await expect(page.getByRole('heading', { name: 'Workers', level: 1 })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('connectivity-pill')).toHaveText('NATS connected · 2 frontends')
    await expect(page.getByRole('button', { name: 'Cluster settings' })).toBeVisible()
    await expect(page.getByTestId('add-worker')).toHaveAttribute('href', '/app/nodes/add')
    // The big transport block is gone from the top of the page.
    await expect(page.getByTestId('cluster-transport')).toHaveCount(0)
    const strip = page.locator('dl[aria-label="Fleet summary"]')
    await expect(strip).toContainText('Workers online')
    await expect(strip).toContainText('of 5')
    await expect(strip).toContainText('Models loaded')
    await expect(strip).toContainText('In flight')
    await expect(strip).toContainText('Needs attention')
    await expect(strip).toContainText('1 offline')
  })

  test('says PostgreSQL when the frontend broadcasts over it, and shows no pill when the cluster routes are off', async ({ page }) => {
    await mockSwarm(page, { cluster: clusterSettingsFixture({ bus: { bus: 'postgres', nats_configured: false, nats_connected: false }, replicas: [] }) })
    await page.goto('/app/nodes')
    await expect(page.getByTestId('connectivity-pill')).toHaveText('PostgreSQL', { timeout: 15_000 })
    const off = await page.context().newPage()
    await mockSwarm(off, { clusterStatus: 503 })
    await off.goto('/app/nodes')
    await expect(off.getByTestId('swarm-nodes')).toBeVisible({ timeout: 15_000 })
    await expect(off.getByTestId('connectivity-pill')).toHaveCount(0)
  })
})

test.describe('Workers: the connections of each worker', () => {
  test('a chip for each connection, a warning with its reason, and Not reported', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/nodes')
    await expect(row(page, 'edge-cpu')).toBeVisible({ timeout: 15_000 })
    await expect(row(page, 'edge-cpu').getByTestId('conn-chip')).toHaveText(['NATS live', 'WebSocket live'])
    await expect(row(page, 'gpu-box-1').getByTestId('conn-chip')).toHaveText(['NATS live'])
    // A failing route is a warning chip with the reason the worker gave.
    const down = row(page, 'gpu-box-2').getByTestId('conn-chip')
    await expect(down.first()).toContainText('NATS no recent report from the worker')
    await expect(row(page, 'gpu-box-3').getByTestId('conn-chip')).toContainText('WebSocket not attached')
  })

  test('a P2P worker says its path, a limited relay is marked, and nothing reported says so', async ({ page }) => {
    const [agent, edge, gpu1, gpu2, gpu3] = clusterNodes()
    const p2p = (node, over) => ({ ...node, address: '', peer_id: '12D3KooWLp7mQe3Vx2cYpTzS8mFJuX3aWbLdQe5Gh6iKo2Qd', route_state: [{ route: 'heartbeat', state: 'live' }, { route: 'tunnel', transport: 'p2p', state: 'live' }], ...over })
    await mockSwarm(page, {
      nodes: [
        { ...agent, route_state: undefined, attached: undefined },
        p2p(edge, { tunnel_path: 'relayed', tunnel_limited: true }),
        p2p(gpu1, { tunnel_path: 'hole_punched' }),
        p2p(gpu2, { tunnel_path: 'direct' }),
        gpu3,
      ],
    })
    await page.goto('/app/nodes')
    await expect(row(page, 'agent-host').getByTestId('conn-chip')).toHaveText('Not reported', { timeout: 15_000 })
    await expect(row(page, 'edge-cpu').getByTestId('conn-chip')).toContainText('P2P relayed')
    await expect(row(page, 'edge-cpu').getByTestId('conn-limited')).toHaveText('Limited')
    await expect(row(page, 'gpu-box-1').getByTestId('conn-chip')).toContainText('P2P hole-punched')
    await expect(row(page, 'gpu-box-2').getByTestId('conn-chip')).toContainText('P2P direct')
  })

  test('a model-split worker is a row of the same roster, marked as such, with its P2P path and round trip', async ({ page }) => {
    await mockSwarm(page)
    await mockSplit(page)
    await page.goto('/app/nodes')
    const mac = page.getByTestId('split-worker-w-mac')
    await expect(mac).toBeVisible({ timeout: 15_000 })
    await expect(mac.getByTestId('split-mark')).toHaveText('Model-split')
    await expect(mac.getByTestId('conn-chip')).toContainText('P2P hole-punched · 9 ms')
    await expect(page.locator('[data-row]')).toHaveCount(5 + splitWorkers().length)
    // They count in the chips and the strip like any worker.
    await expect(page.getByRole('button', { name: /^All/ })).toContainText('8')
    await expect(page.getByTestId('split-workers')).toHaveCount(0)
  })
})

test.describe('Workers: the inspector', () => {
  test('shows each connection with its weight and health, the models and the labels', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/nodes')
    const inspector = await open(page, 'edge-cpu')
    const routes = inspector.getByTestId('route-health')
    await expect(routes).toHaveCount(2)
    await expect(routes.nth(0)).toContainText('NATS')
    await expect(routes.nth(0)).toContainText('Live')
    await expect(routes.nth(0)).toContainText('Weight 100')
    await expect(routes.nth(1)).toContainText('WebSocket')
    await expect(routes.nth(1)).toContainText('Weight 50')
    await expect(inspector).toContainText('bge-m3')
    await expect(inspector).toContainText('role=edge')
    const down = await open(page, 'gpu-box-2')
    await expect(down.getByTestId('route-health').first()).toContainText('no recent report from the worker')
    await expect(down.getByTestId('route-health').first()).toContainText('Down')
  })

  test('approves a pending worker from the inspector', async ({ page }) => {
    const log = []
    await mockSwarm(page, { log })
    await page.goto('/app/nodes')
    const inspector = await open(page, 'gpu-box-3')
    await inspector.getByRole('button', { name: 'Approve' }).click()
    await expect.poll(() => log.filter(entry => entry.path === '/api/nodes/n-gpu3/approve').length).toBe(1)
  })

  test('drains after a confirmation, resumes without one, and removes after a confirmation', async ({ page }) => {
    const log = []
    const nodes = clusterNodes().map(node => (node.id === 'n-edge' ? { ...node, status: 'draining' } : node))
    await mockSwarm(page, { log, nodes })
    await page.goto('/app/nodes')
    let inspector = await open(page, 'edge-cpu')
    await inspector.getByRole('button', { name: 'Resume' }).click()
    await expect.poll(() => log.filter(entry => entry.path === '/api/nodes/n-edge/resume').length).toBe(1)
    inspector = await open(page, 'gpu-box-1')
    await inspector.getByRole('button', { name: /^Drain/ }).click()
    await expect(page.getByRole('alertdialog').or(page.getByRole('dialog'))).toContainText('Drain gpu-box-1')
    expect(log.filter(entry => entry.path.endsWith('/drain'))).toHaveLength(0)
    await page.getByRole('button', { name: 'Drain node' }).click()
    await expect.poll(() => log.filter(entry => entry.path === '/api/nodes/n-gpu1/drain').length).toBe(1)
    inspector = await open(page, 'gpu-box-1')
    await inspector.getByRole('button', { name: /^Remove/ }).click()
    await expect(page.getByRole('alertdialog').or(page.getByRole('dialog'))).toContainText('Remove gpu-box-1')
    await page.getByRole('button', { name: 'Remove node' }).click()
    await expect.poll(() => log.filter(entry => entry.method === 'DELETE' && entry.path === '/api/nodes/n-gpu1').length).toBe(1)
  })

  test('offers the backend update of the worker that drifted, and asks first', async ({ page }) => {
    await mockSwarm(page)
    await mockReadModel(page, { 'llama-cpp': { backend_name: 'llama-cpp', installed_version: '0.9.4', available_version: '0.9.7', node_drift: [{ node_id: 'n-gpu1' }] } })
    await page.goto('/app/nodes')
    await expect(row(page, 'gpu-box-1')).toBeVisible({ timeout: 15_000 })
    const other = await open(page, 'edge-cpu')
    await expect(other.getByTestId('inspector-update')).toHaveCount(0)
    const inspector = await open(page, 'gpu-box-1')
    await inspector.getByTestId('inspector-update').click()
    const dialog = page.getByTestId('node-update-dialog')
    await expect(dialog.getByTestId('node-update-item')).toHaveCount(1)
    await expect(dialog).toContainText('gpu-box-1')
  })

  test('keeps the node detail routes working from the inspector', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/nodes')
    const inspector = await open(page, 'gpu-box-1')
    await expect(inspector.getByRole('link', { name: 'Logs' })).toHaveAttribute('href', '/app/nodes/n-gpu1?tab=logs')
    await inspector.getByRole('link', { name: 'Open full page' }).click()
    await expect(page).toHaveURL(/\/app\/nodes\/n-gpu1$/)
  })
})

test.describe('Workers: the cluster settings button and light and dark', () => {
  for (const theme of ['light', 'dark']) {
    test(`the roster and the sheet read in ${theme}`, async ({ page }) => {
      await mockSwarm(page)
      await page.goto('/app/nodes')
      await page.evaluate(value => document.documentElement.setAttribute('data-theme', value), theme)
      await expect(page.getByTestId('conn-chip').first()).toBeVisible({ timeout: 15_000 })
      // A chip is readable: its text is not the colour of its background.
      const colours = await page.getByTestId('conn-chip').first().evaluate(el => {
        const style = getComputedStyle(el)
        return { color: style.color, background: style.backgroundColor }
      })
      expect(colours.color).not.toBe(colours.background)
      await page.getByTestId('cluster-settings-open').click()
      await expect(page.getByTestId('cluster-sheet')).toBeVisible()
    })
  }
})

test.describe('Workers: a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('has no horizontal scroll, shows the chips under each worker, and opens the inspector as a sheet', async ({ page }) => {
    await mockSwarm(page)
    await mockSplit(page)
    await page.goto('/app/nodes')
    await expect(row(page, 'gpu-box-1')).toBeVisible({ timeout: 15_000 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
    const list = page.getByTestId('nodes-table')
    expect(await list.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true)
    await expect(row(page, 'gpu-box-1').getByTestId('conn-chip')).toBeVisible()
    await expect(page.getByTestId('node-inspector')).toHaveCount(0)
    await row(page, 'gpu-box-1').getByRole('button', { name: /^gpu-box-1/ }).click()
    await expect(page.getByTestId('node-sheet').getByTestId('node-inspector')).toContainText('gpu-box-1')
  })
})
