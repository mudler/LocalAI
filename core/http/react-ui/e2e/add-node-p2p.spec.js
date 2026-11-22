import { test, expect } from './coverage-fixtures.js'
import { mockSwarm, clusterNodes } from './swarm-fixtures.js'
import { mockAdmin, mockP2PShare, SERVER_PEER, DEVICE_PEER } from './p2p-share-fixtures.js'

// A compute node that joins over P2P: the connections of each worker in the
// roster (NATS, WebSocket or P2P and its path), and the Worker kind of the
// Share dialog. The Add worker flow has its own spec (add-worker.spec.js).

// Nodes with every route the roster shows.
function routedNodes() {
  const [agent, edge, gpu1, gpu2, gpu3] = clusterNodes()
  const live = (route, transport) => ({ route, state: 'live', ...(transport ? { transport } : {}) })
  const state = (...routes) => [{ route: 'heartbeat', state: 'live' }, ...routes]
  return [
    { ...agent, attached: 'tunnel', route_state: state(live('tunnel', 'websocket')) },
    { ...edge, attached: 'tunnel', address: '', peer_id: DEVICE_PEER, tunnel_transport: 'p2p', tunnel_path: 'relayed', tunnel_limited: true, route_state: state(live('tunnel', 'p2p')) },
    { ...gpu1, attached: 'nats', route_state: state(live('nats')) },
    { ...gpu2, attached: undefined, route_state: undefined },
    { ...gpu3, address: '', peer_id: SERVER_PEER, attached: 'tunnel', tunnel_transport: 'p2p', tunnel_path: 'hole_punched', route_state: state(live('tunnel', 'p2p')) },
  ]
}

test.describe('The connections of each worker', () => {
  test('the roster says NATS, WebSocket or P2P with its path, and says so for a worker that reports nothing', async ({ page }) => {
    await mockSwarm(page, { nodes: routedNodes() })
    await page.goto('/app/nodes')
    const row = name => page.locator('[data-row]').filter({ hasText: name })
    await expect(row('gpu-box-1').getByTestId('conn-chip')).toHaveText('NATS live', { timeout: 15_000 })
    await expect(row('agent-host').getByTestId('conn-chip')).toHaveText('WebSocket live')
    await expect(row('edge-cpu').getByTestId('conn-chip')).toContainText('P2P relayed')
    await expect(row('edge-cpu').getByTestId('conn-limited')).toHaveText('Limited')
    await expect(row('gpu-box-3').getByTestId('conn-chip')).toContainText('P2P hole-punched')
    await expect(row('gpu-box-2').getByTestId('conn-chip')).toHaveText('Not reported')
  })
})

test.describe('The Share dialog in distributed mode', () => {
  test('offers a worker invite, without a QR code, that says the node waits for approval', async ({ page }) => {
    await mockAdmin(page)
    await mockSwarm(page)
    const state = await mockP2PShare(page)
    await page.goto('/app/p2p')
    await expect(page.getByTestId('p2p-page')).toBeVisible({ timeout: 15_000 })
    await page.getByTestId('p2p-share-open').click()
    const dialog = page.getByTestId('share-dialog')
    await dialog.getByTestId('share-kind-worker').click()
    await expect(dialog.getByTestId('share-kind-note')).toContainText('A node for this cluster.')
    await dialog.getByTestId('share-mint').click()
    const mint = state.calls.find(c => c.method === 'POST')
    expect(mint.body.kind).toBe('worker')
    const result = page.getByTestId('share-result')
    await expect(result.getByTestId('share-link')).toContainText('k=worker')
    await expect(result.getByTestId('qr-code')).toHaveCount(0)
    await expect(result).toContainText('local-ai worker --join')
    await expect(result).toContainText('Waiting for the worker')
  })

  test('has no worker kind on a single server', async ({ page }) => {
    await mockAdmin(page)
    await mockSwarm(page, { nodes: null, features: { distributed: false } })
    await mockP2PShare(page)
    await page.goto('/app/p2p')
    await expect(page.getByTestId('p2p-page')).toBeVisible({ timeout: 15_000 })
    await page.getByTestId('p2p-share-open').click()
    await expect(page.getByTestId('share-kind-device')).toBeVisible()
    await expect(page.getByTestId('share-kind-worker')).toHaveCount(0)
  })
})
