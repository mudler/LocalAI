import { test, expect } from './coverage-fixtures.js'
import { mockSwarm, GB } from './swarm-fixtures.js'
import { LINK, PEER, mockSplit, ownRelayWorker, splitServerNodes, splitWorkers } from './split-fixtures.js'

// Model-split workers: llama.cpp RPC and MLX workers that pair with an invite
// and lend their memory to models on this server. The Nodes page lists them in
// a group of their own; "Add a node" makes their invite. A worker that only a
// relay reaches waits for "Use anyway".

test.describe('Model-split workers on the Nodes page', () => {
  test('lists the workers with engine, path, memory and state, and warns about the slow one', async ({ page }) => {
    await mockSwarm(page)
    await mockSplit(page)
    await page.goto('/app/nodes')
    const group = page.getByTestId('split-workers')
    await expect(group.getByRole('heading', { name: /Model-split workers/ })).toBeVisible({ timeout: 15_000 })
    await expect(group.getByText('2 of 3 online')).toBeVisible()
    await expect(group.getByTestId('split-slow-callout')).toContainText('old-rig')
    const attic = group.getByTestId('split-worker-w-attic')
    await expect(attic).toContainText('attic-3090')
    await expect(attic).toContainText('llama.cpp RPC')
    await expect(attic).toContainText('Direct')
    await expect(attic).toContainText('24 GB')
    await expect(group.getByTestId('split-worker-w-mac')).toContainText('Hole-punched')
    const old = group.getByTestId('split-worker-w-old')
    await expect(old).toContainText('Via home relay')
    await expect(old).toContainText('Slow: through a relay')
    await expect(group.getByText('Memory added by workers in use: 120 GB.')).toBeVisible()
    await expect(group.getByRole('link', { name: 'Add a model-split worker' })).toHaveAttribute('href', /\/app\/nodes\/add\?join=shard$/)
  })

  test('uses a slow worker after Use anyway, and stops using it again', async ({ page }) => {
    await mockSwarm(page)
    const log = await mockSplit(page)
    await page.goto('/app/nodes')
    await page.getByTestId('split-use-w-old').click()
    await expect.poll(() => log.find(e => e.method === 'PATCH')?.body).toEqual({ allow_slow_path: true })
    await expect(page.getByTestId('split-worker-w-old')).toContainText('Online')
    await page.getByTestId('split-stop-w-old').click()
    await expect.poll(() => log.filter(e => e.method === 'PATCH').at(-1)?.body).toEqual({ allow_slow_path: false })
  })

  test('revokes a worker after a confirmation', async ({ page }) => {
    await mockSwarm(page)
    const log = await mockSplit(page)
    await page.goto('/app/nodes')
    await page.getByTestId('split-revoke-w-attic').click()
    const dialog = page.getByRole('alertdialog').or(page.getByRole('dialog'))
    await expect(dialog).toContainText('Revoke attic-3090?')
    await dialog.getByRole('button', { name: 'Revoke', exact: true }).click()
    await expect.poll(() => log.find(e => e.method === 'DELETE')?.path).toBe('/api/p2p/devices/w-attic')
  })

  test('offers Use anyway through a relay of the user that cuts each connection, and opens its limits', async ({ page }) => {
    await mockSwarm(page)
    const log = await mockSplit(page, { workers: [...splitWorkers(), ownRelayWorker()] })
    await page.goto('/app/nodes')
    const row = page.getByTestId('split-worker-w-home')
    await expect(row).toContainText('Slow: through a relay', { timeout: 15_000 })
    await expect(row).toContainText('Cut after 1 GiB, 30 min')
    await expect(page.getByTestId('split-cut-callout')).toContainText('home-box goes through a relay of yours')
    await expect(page.getByTestId('split-use-w-home')).toBeVisible()
    await page.getByTestId('split-limits-w-home').click()
    await expect(page.getByTestId('relay-limits-dialog')).toContainText('Limits for home relay')
    await expect.poll(() => log.some(e => e.path === '/api/p2p/relays/r1/limits')).toBe(true)
  })

  test('never offers Use anyway through a public relay', async ({ page }) => {
    const pub = { id: 'w-pub', label: 'cafe-laptop', peer_id: `${PEER}d`, engine: 'llama', state: 'limited', allow_slow_path: false, path: { kind: 'relayed', relay_label: 'public relay', limited: true, limit_bytes: 131072, rtt_ms: 80 }, memory_bytes: 8 * GB, created_at: new Date().toISOString() }
    await mockSwarm(page)
    await mockSplit(page, { workers: [pub] })
    await page.goto('/app/nodes')
    await expect(page.getByTestId('split-worker-w-pub')).toContainText('Public relay: not used', { timeout: 15_000 })
    await expect(page.getByTestId('split-limited-callout')).toContainText('A public relay cannot carry model layers.')
    await expect(page.getByTestId('split-use-w-pub')).toHaveCount(0)
    await expect(page.getByTestId('split-limits-w-pub')).toHaveCount(0)
  })

  test('in a cluster, shows the node of each worker, warns about one attached to no node, and attaches it', async ({ page }) => {
    const workers = splitWorkers().map(w => (w.id === 'w-attic' ? { ...w, node_id: 'n-gpu1', node_name: 'gpu-box-1' } : w))
    await mockSwarm(page, { nodes: splitServerNodes() })
    const log = await mockSplit(page, { workers, distributed: true })
    await page.goto('/app/nodes')
    const group = page.getByTestId('split-workers')
    await expect(group.getByText('Each one lends memory to the models of the node it is attached to.')).toBeVisible({ timeout: 15_000 })
    await expect(group.getByRole('columnheader', { name: 'Attached to' })).toBeVisible()
    await expect(page.getByTestId('split-node-w-attic')).toHaveValue('n-gpu1')
    await expect(page.getByTestId('split-unattached-callout')).toContainText('mac-studio-2, old-rig')
    // Only split servers are offered: not an agent node, not a worker that
    // was not declared.
    await expect(page.getByTestId('split-node-w-mac').locator('option')).toHaveText(['This instance', 'gpu-box-1', 'gpu-box-2'])
    await page.getByTestId('split-node-w-mac').selectOption('n-gpu2')
    await expect.poll(() => log.find(e => e.method === 'PATCH')?.body).toEqual({ node_id: 'n-gpu2' })
    await expect(page.getByTestId('split-unattached-callout')).not.toContainText('mac-studio-2')
    await page.getByTestId('split-node-w-attic').selectOption('')
    await expect.poll(() => log.filter(e => e.method === 'PATCH').at(-1)?.body).toEqual({ node_id: '' })
  })

  test('shows the relay admission of the node of each worker, a node that is offline, and why a worker was detached', async ({ page }) => {
    const workers = [
      { ...splitWorkers()[0], node_id: 'n-gpu1', node_name: 'gpu-box-1', node_relay: { state: 'admitted', relays: [{ relay_id: 'r1', label: 'home relay', state: 'admitted' }] } },
      { ...splitWorkers()[1], state: 'offline', node_id: 'n-gpu2', node_name: 'gpu-box-2', node_offline: true, node_relay: { state: 'refused', reason: 'the relay admits at most 4 devices for this server, and the paired devices come first' } },
      { ...splitWorkers()[2], node_note: 'edge-cpu is no longer a split server; attach the worker to a split server' },
    ]
    await mockSwarm(page, { nodes: splitServerNodes() })
    await mockSplit(page, { workers, distributed: true })
    await page.goto('/app/nodes')
    await expect(page.getByTestId('split-relay-w-attic')).toContainText('Relay: admitted', { timeout: 15_000 })
    await expect(page.getByTestId('split-relay-w-mac')).toContainText('Relay: refused')
    await expect(page.getByTestId('split-relay-w-mac')).toContainText('Node offline')
    // Two warnings apply (a node that is offline, a relay that refuses): the group shows the more important one.
    await expect(page.getByTestId('split-relay-refused-callout')).toContainText('gpu-box-2')
    await expect(page.getByTestId('split-offline-callout')).toHaveCount(0)
    await expect(page.getByTestId('split-workers').locator('.ui-callout')).toHaveCount(1)
    await expect(page.getByTestId('split-relay-refused-callout')).toContainText('at most 4 devices')
    await expect(page.getByTestId('split-note-w-old')).toContainText('no longer a split server')
    await expect(page.getByText('Changes to the workers of a node apply to the next model it loads.', { exact: false })).toBeVisible()
  })

  test('with no split server, says so and sets one up only after the confirm click', async ({ page }) => {
    const log = []
    await mockSwarm(page, { log })
    const splitLog = await mockSplit(page, { distributed: true })
    await page.goto('/app/nodes')
    const callout = page.getByTestId('split-no-server-callout')
    await expect(callout).toContainText('No worker is a split server yet.', { timeout: 15_000 })
    await expect(callout).toContainText('--split-server')
    await callout.getByTestId('split-setup-open').click()
    const dialog = page.getByTestId('split-server-setup')
    await dialog.getByTestId('split-server-node').selectOption('n-gpu1')
    const changes = dialog.getByTestId('split-server-changes')
    await expect(changes).toContainText('opens P2P ports')
    await expect(changes).toContainText('gated relays')
    await expect(changes).toContainText('next llama.cpp or MLX model')
    // Nothing changes before the confirm click.
    expect(log.filter(e => e.method === 'PUT')).toHaveLength(0)
    expect(splitLog.filter(e => e.method === 'PATCH')).toHaveLength(0)
    await dialog.getByTestId('split-server-helper-w-old').uncheck()
    await dialog.getByTestId('split-server-confirm').click()
    await expect.poll(() => log.find(e => e.method === 'PUT')).toEqual({ method: 'PUT', path: '/api/nodes/n-gpu1/split-server', body: { split_server: true } })
    await expect.poll(() => splitLog.filter(e => e.method === 'PATCH').map(e => [e.path, e.body.node_id])).toEqual([
      ['/api/p2p/split-workers/w-attic', 'n-gpu1'], ['/api/p2p/split-workers/w-mac', 'n-gpu1'],
    ])
    await expect(dialog).toHaveCount(0)
  })

  test('shows a split server on the node inspector, and stops it only after a confirmation', async ({ page }) => {
    const log = []
    await mockSwarm(page, { nodes: splitServerNodes(), log })
    await mockSplit(page, { distributed: true, workers: splitWorkers().map(w => ({ ...w, node_id: 'n-gpu1', node_name: 'gpu-box-1' })) })
    await page.goto('/app/nodes')
    await page.getByRole('button', { name: /^gpu-box-1/ }).first().click({ timeout: 15_000 })
    const inspector = page.getByTestId('node-inspector')
    await expect(inspector.getByTestId('split-server-pill')).toHaveText('Split server')
    await inspector.getByTestId('split-server-clear').click()
    const dialog = page.getByRole('alertdialog').or(page.getByRole('dialog'))
    await expect(dialog).toContainText('Stop gpu-box-1 being a split server?')
    await expect(dialog).toContainText('(3)')
    expect(log.filter(e => e.method === 'PUT')).toHaveLength(0)
    await dialog.getByRole('button', { name: 'Stop being a split server', exact: true }).click()
    await expect.poll(() => log.find(e => e.method === 'PUT')?.body).toEqual({ split_server: false })

    await page.getByRole('button', { name: /^edge-cpu/ }).first().click()
    await expect(inspector.getByTestId('split-server-pill')).toHaveCount(0)
    await inspector.getByTestId('split-server-make').click()
    await expect(page.getByTestId('split-server-node')).toHaveValue('n-edge')
  })

  test('shows no node column on a single instance', async ({ page }) => {
    await mockSwarm(page)
    await mockSplit(page)
    await page.goto('/app/nodes')
    await expect(page.getByTestId('split-worker-w-attic')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('split-node-w-attic')).toHaveCount(0)
    await expect(page.getByTestId('split-unattached-callout')).toHaveCount(0)
  })

  test('shows no group when the server does not share over P2P', async ({ page }) => {
    await mockSwarm(page)
    await mockSplit(page, { mode: 'connect' })
    await page.goto('/app/nodes')
    await expect(page.getByTestId('swarm-nodes')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('split-workers')).toHaveCount(0)
  })
})

test.describe('Add a node: model-split worker', () => {
  test('makes an invite for the chosen engine and puts its link in the command', async ({ page }) => {
    await mockSwarm(page, { nodes: splitServerNodes() })
    const log = await mockSplit(page)
    await page.goto('/app/nodes/add?join=shard')
    await expect(page.getByRole('radio', { name: 'Model-split worker' })).toHaveAttribute('aria-checked', 'true', { timeout: 15_000 })
    await expect(page.getByText('Make the invite first: the command carries its link.')).toBeVisible()
    await page.getByTestId('split-engine-mlx').click()
    await page.getByTestId('split-name').fill('mac-studio-3')
    await page.getByTestId('split-mint').click()
    // In a cluster the invite names the only online split server.
    await expect.poll(() => log.find(e => e.method === 'POST')?.body).toEqual({ kind: 'split', label: 'mac-studio-3', ttl_seconds: 600, labels: { engine: 'mlx', node: 'n-gpu1' } })
    await expect(page.getByTestId('split-invite-ready')).toContainText('Invite ready')
    const command = page.getByTestId('command-block').last()
    await expect(command).toContainText('local-ai worker mlx-distributed')
    await expect(command).toContainText(`--join "${LINK}"`)
    // MLX runs outside a container: no hardware and no Docker choice.
    await expect(page.getByRole('radio', { name: 'Docker' })).toHaveCount(0)
  })

  test('gives llama.cpp a Docker command with a volume for its pairing', async ({ page }) => {
    await mockSwarm(page)
    await mockSplit(page)
    await page.goto('/app/nodes/add?join=shard')
    await page.getByTestId('split-mint').click()
    await page.getByRole('radio', { name: 'CUDA 12', exact: true }).click()
    const command = page.getByTestId('command-block').last()
    await expect(command).toContainText('--gpus all')
    await expect(command).toContainText('-v localai-llama-cpp-rpc:/p2p')
    await expect(command).toContainText('latest-gpu-nvidia-cuda-12 worker llama-cpp-rpc')
    await page.getByRole('radio', { name: 'CLI' }).click()
    await expect(command).toContainText('local-ai worker llama-cpp-rpc')
  })

  test('follows the worker after it pairs and adds a relayed one only after Add anyway', async ({ page }) => {
    let redeemed = false
    const worker = { id: 'w-new', label: 'attic-3090', peer_id: `${PEER}n`, engine: 'llama', state: 'slow', allow_slow_path: false, path: { kind: 'relayed', relay_label: 'home relay', rtt_ms: 52 }, memory_bytes: 24 * GB, created_at: new Date().toISOString() }
    await mockSwarm(page)
    const log = await mockSplit(page, {
      workers: () => (redeemed ? [worker] : []),
      invites: () => [{ id: 'inv-1', kind: 'split', status: redeemed ? 'used' : 'pending', ...(redeemed ? { device_id: 'w-new' } : {}) }],
      onPatch: (id, body) => { if (id === 'w-new') Object.assign(worker, { allow_slow_path: body.allow_slow_path, state: 'online' }) },
    })
    await page.goto('/app/nodes/add?join=shard')
    await page.getByTestId('split-mint').click()
    await expect(page.getByText('Waiting for the worker to connect…')).toBeVisible()
    redeemed = true
    await expect(page.getByTestId('split-add-slow')).toContainText('attic-3090 has no direct or hole-punched path', { timeout: 10_000 })
    await expect(page.getByTestId('split-found')).toContainText('Via home relay')
    await expect(page.getByTestId('split-invite-used')).toContainText('Invite used')
    await page.getByTestId('split-add-anyway').click()
    await expect.poll(() => log.find(e => e.method === 'PATCH')?.body).toEqual({ allow_slow_path: true })
    await expect(page.getByRole('heading', { name: 'The worker is in use' })).toBeVisible({ timeout: 10_000 })
  })

  test('in a cluster, attaches the worker to the only online split server by default, to another one, or to this instance', async ({ page }) => {
    await mockSwarm(page, { nodes: splitServerNodes() })
    const log = await mockSplit(page)
    await page.goto('/app/nodes/add?join=shard')
    const node = page.getByTestId('split-add-node')
    // gpu-box-2 does not answer: gpu-box-1 is the only online split server.
    await expect(node).toHaveValue('n-gpu1', { timeout: 15_000 })
    await expect(page.getByText('The model runs on the node you choose next.', { exact: false })).toBeVisible()
    await page.getByTestId('split-mint').click()
    await expect.poll(() => log.find(e => e.method === 'POST')?.body?.labels).toEqual({ engine: 'llama', node: 'n-gpu1' })
    // A new node needs a new invite.
    await node.selectOption('n-gpu2')
    await expect(page.getByTestId('split-invite-ready')).toHaveCount(0)
    await expect(page.getByText('gpu-box-2 is offline.', { exact: false })).toBeVisible()
    await node.selectOption('__server')
    await page.getByTestId('split-mint').click()
    await expect.poll(() => log.filter(e => e.method === 'POST').at(-1)?.body?.labels).toEqual({ engine: 'llama', node: '' })
  })

  test('in a cluster with two online split servers, waits for the admin to pick one', async ({ page }) => {
    const nodes = splitServerNodes(['n-gpu1', 'n-edge'])
    await mockSwarm(page, { nodes })
    const log = await mockSplit(page)
    await page.goto('/app/nodes/add?join=shard')
    const node = page.getByTestId('split-add-node')
    await expect(node).toHaveValue('', { timeout: 15_000 })
    await expect(page.getByTestId('split-mint')).toBeDisabled()
    await node.selectOption('n-edge')
    await page.getByTestId('split-mint').click()
    await expect.poll(() => log.find(e => e.method === 'POST')?.body?.labels).toEqual({ engine: 'llama', node: 'n-edge' })
  })

  test('in a cluster with no split server, says so and offers the setup', async ({ page }) => {
    await mockSwarm(page)
    await mockSplit(page)
    await page.goto('/app/nodes/add?join=shard')
    await expect(page.getByTestId('split-add-no-server')).toContainText('No worker is a split server yet.', { timeout: 15_000 })
    await page.getByTestId('split-add-setup').click()
    await expect(page.getByTestId('split-server-setup')).toBeVisible()
  })

  test('on a single instance, asks for no node', async ({ page }) => {
    await mockSwarm(page, { nodes: null, features: {} })
    const log = await mockSplit(page)
    await page.goto('/app/nodes/add?join=shard')
    await page.getByTestId('split-mint').click({ timeout: 15_000 })
    await expect.poll(() => log.find(e => e.method === 'POST')?.body?.labels).toEqual({ engine: 'llama' })
    await expect(page.getByTestId('split-add-node')).toHaveCount(0)
  })

  test('asks for P2P sharing first', async ({ page }) => {
    await mockSwarm(page)
    await mockSplit(page, { mode: 'connect' })
    await page.goto('/app/nodes/add?join=shard')
    await expect(page.getByTestId('split-needs-share')).toContainText('Connect and share', { timeout: 15_000 })
    await expect(page.getByRole('link', { name: 'Open P2P settings' })).toHaveAttribute('href', /\/app\/p2p\/settings$/)
  })
})
