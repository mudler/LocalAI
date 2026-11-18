import { test, expect } from './coverage-fixtures.js'
import { mockSwarm, clusterNodes } from './swarm-fixtures.js'
import { mockAdmin, mockP2PShare, SERVER_PEER, DEVICE_PEER } from './p2p-share-fixtures.js'

// A compute node that joins over P2P: "Add a node" with a worker invite, the
// route of each node in the roster (NATS, WebSocket or P2P and its path), and
// the Worker kind of the Share dialog.

const command = page => page.getByTestId('command-block').last()

// Nodes with every route the roster shows.
function routedNodes() {
  const [agent, edge, gpu1, gpu2, gpu3] = clusterNodes()
  return [
    { ...agent, attached: 'tunnel' },
    { ...edge, attached: 'tunnel', address: '', peer_id: DEVICE_PEER, tunnel_transport: 'p2p', tunnel_path: 'relayed', tunnel_limited: true },
    { ...gpu1, attached: 'nats,tunnel' },
    gpu2,
    { ...gpu3, address: '', peer_id: SERVER_PEER, attached: 'tunnel', tunnel_transport: 'p2p', tunnel_path: 'hole_punched' },
  ]
}

async function openAdd(page, { mode = 'share', onNodes } = {}) {
  await mockSwarm(page, { onNodes })
  const state = await mockP2PShare(page, { mode })
  await page.goto('/app/nodes/add')
  await expect(page.getByRole('heading', { name: 'Add a node' })).toBeVisible({ timeout: 15_000 })
  await page.getByRole('radio', { name: 'P2P invite' }).click()
  return state
}

test.describe('Add a compute node over P2P', () => {
  test('makes a single-use worker invite and puts it in a command with no token', async ({ page }) => {
    const state = await openAdd(page)
    await expect(page.getByText('It needs no open port and no registration token.')).toBeVisible()
    await expect(page.getByText('Order in this cluster: NATS, then WebSocket, then P2P.', { exact: false })).toBeVisible()
    await expect(page.getByText('Make an invite first.', { exact: false })).toBeVisible()
    await expect(page.getByTestId('command-block')).toHaveCount(0)

    await page.getByLabel('Node name (optional)').fill('gpu-box-5')
    await page.getByTestId('make-worker-invite').click()
    const mint = state.calls.find(c => c.method === 'POST')
    expect(mint.body).toEqual({ kind: 'worker', label: 'gpu-box-5' })

    const ready = page.getByTestId('worker-invite')
    await expect(ready).toContainText('Invite ready')
    await expect(ready).toContainText('Single use')
    await expect(ready).toContainText('relay pass on relay.example.org')
    await expect(command(page)).toContainText('LOCALAI_JOIN="localai://pair?v=1&k=worker')
    await expect(command(page)).toContainText('LOCALAI_P2P_IDENTITY=/data/p2p/identity.key')
    await expect(command(page)).not.toContainText('TOKEN')
    await page.getByRole('radio', { name: 'CLI' }).click()
    await expect(command(page)).toContainText('local-ai worker')
    await expect(command(page)).toContainText('--join "localai://pair?v=1&k=worker')
    // A worker over P2P is a backend worker: no agent worker choice.
    await expect(page.getByRole('radio', { name: 'Agent worker' })).toHaveCount(0)

    await page.getByRole('button', { name: 'New invite' }).click()
    await expect(page.getByTestId('make-worker-invite')).toBeVisible()
  })

  test('asks for mode share before it makes an invite', async ({ page }) => {
    await openAdd(page, { mode: 'connect' })
    const note = page.getByTestId('invite-needs-share')
    await expect(note).toContainText('Worker invites need P2P in mode Connect and share.')
    await expect(note.getByRole('link', { name: 'Open P2P settings' })).toHaveAttribute('href', '/app/p2p/settings')
    await expect(page.getByTestId('make-worker-invite')).toHaveCount(0)
  })

  test('shows the node that joined over P2P with its peer and route, and asks to check the peer', async ({ page }) => {
    let joined = false
    const known = clusterNodes()
    const arrived = {
      id: 'n-p2p', name: 'gpu-box-5', node_type: 'backend', address: '', status: 'pending', version: 'v4.0.0', labels: {},
      peer_id: DEVICE_PEER, attached: 'tunnel', tunnel_transport: 'p2p', tunnel_path: 'hole_punched', last_heartbeat: new Date().toISOString(),
    }
    await openAdd(page, { onNodes: () => (joined ? [...known, arrived] : known) })
    await page.getByTestId('make-worker-invite').click()
    await expect(page.getByTestId('worker-invite')).toBeVisible()
    joined = true
    const found = page.getByTestId('found-node')
    await expect(found).toBeVisible({ timeout: 20_000 })
    await expect(found).toContainText('gpu-box-5')
    await expect(found).toContainText('12D3KooWLp…o2Qd')
    await expect(found.getByTestId('node-route')).toContainText('P2P')
    await expect(found.getByTestId('node-route')).toContainText('Hole-punched')
    await expect(found).toContainText('Waiting for approval')
    await expect(page.getByText('Check the peer ID with the person who runs the machine before you approve.')).toBeVisible()
  })
})

test.describe('The route of each node', () => {
  test('the roster says NATS, WebSocket or P2P with its path, and nothing for a node that reports nothing', async ({ page }) => {
    await mockSwarm(page, { nodes: routedNodes() })
    await page.goto('/app/nodes')
    const row = name => page.locator('[data-row]').filter({ hasText: name })
    await expect(row('gpu-box-1').getByTestId('node-route')).toHaveText('NATS', { timeout: 15_000 })
    await expect(row('agent-host').getByTestId('node-route')).toHaveText('WebSocket')
    await expect(row('edge-cpu').getByTestId('node-route')).toContainText('P2P')
    await expect(row('edge-cpu').getByTestId('node-route')).toContainText('Via a relay')
    await expect(row('edge-cpu').getByTestId('node-route')).toContainText('Limited')
    await expect(row('gpu-box-3').getByTestId('node-route')).toContainText('Hole-punched')
    await expect(row('gpu-box-2').getByTestId('node-route')).toHaveCount(0)
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
