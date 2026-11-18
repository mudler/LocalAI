import { test, expect } from './coverage-fixtures.js'
import { mockSwarm, GB } from './swarm-fixtures.js'
import { LINK, PEER, mockSplit } from './split-fixtures.js'

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
    await mockSwarm(page)
    const log = await mockSplit(page)
    await page.goto('/app/nodes/add?join=shard')
    await expect(page.getByRole('radio', { name: 'Model-split worker' })).toHaveAttribute('aria-checked', 'true', { timeout: 15_000 })
    await expect(page.getByText('Make the invite first: the command carries its link.')).toBeVisible()
    await page.getByTestId('split-engine-mlx').click()
    await page.getByTestId('split-name').fill('mac-studio-3')
    await page.getByTestId('split-mint').click()
    await expect.poll(() => log.find(e => e.method === 'POST')?.body).toEqual({ kind: 'split', label: 'mac-studio-3', ttl_seconds: 600, labels: { engine: 'mlx' } })
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

  test('asks for P2P sharing first', async ({ page }) => {
    await mockSwarm(page)
    await mockSplit(page, { mode: 'connect' })
    await page.goto('/app/nodes/add?join=shard')
    await expect(page.getByTestId('split-needs-share')).toContainText('Connect and share', { timeout: 15_000 })
    await expect(page.getByRole('link', { name: 'Open P2P settings' })).toHaveAttribute('href', /\/app\/p2p\/settings$/)
  })
})
