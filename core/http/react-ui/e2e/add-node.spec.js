import { test, expect } from './coverage-fixtures.js'
import { mockSwarm, clusterNodes } from './swarm-fixtures.js'

// "Add a node": which way the machine joins, the one command to run on it, and
// a live line that says when it arrived. "Found" is a real node that was not in
// the roster when the page opened, never a timer.

const command = page => page.getByTestId('command-block').last()

test.describe('Add a node', () => {
  test('starts on a registered worker with a copyable command that carries no secret', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/nodes/add')
    await expect(page.getByRole('heading', { name: 'Add a node' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('radio', { name: 'Registered worker' })).toHaveAttribute('aria-checked', 'true')
    await expect(command(page)).toContainText('LOCALAI_REGISTER_TO="http://127.0.0.1:8089"')
    await expect(command(page)).toContainText('LOCALAI_REGISTRATION_TOKEN="$TOKEN"')
    await expect(command(page)).toContainText('localai/localai:latest-cpu worker')
    await expect(page.getByText('This page never shows it.')).toBeVisible()
    await expect(page.getByRole('link', { name: 'All nodes' })).toHaveAttribute('href', '/app/nodes')
    // A cluster needs no step for turning distributed mode on.
    await expect(page.getByText('Turn on distributed mode first')).toHaveCount(0)
  })

  test('changes the command with the hardware, the way to run it and the worker type', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/nodes/add')
    await page.getByRole('radio', { name: 'CUDA 12', exact: true }).click()
    await expect(command(page)).toContainText('--gpus all')
    await expect(command(page)).toContainText('latest-gpu-nvidia-cuda-12 worker')
    await page.getByRole('radio', { name: 'Agent worker' }).click()
    await expect(command(page)).toContainText('latest-gpu-nvidia-cuda-12 agent-worker')
    await page.getByRole('button', { name: 'Dev' }).click()
    await expect(command(page)).toContainText('master-gpu-nvidia-cuda-12 agent-worker')
    await page.getByRole('radio', { name: 'CLI' }).click()
    await expect(command(page)).toContainText('local-ai agent-worker')
    await expect(command(page)).toContainText('--registration-token "$LOCALAI_REGISTRATION_TOKEN"')
    // Hardware means nothing to a plain command.
    await expect(page.getByRole('radio', { name: 'CUDA 12', exact: true })).toHaveCount(0)
  })

  test('copies the command and says so', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']).catch(() => {})
    await mockSwarm(page)
    await page.goto('/app/nodes/add')
    const copy = page.getByRole('button', { name: 'Copy command' }).last()
    await copy.click()
    await expect(copy).toContainText('Copied')
    const text = await command(page).locator('code').innerText()
    expect(text).toContain('LOCALAI_REGISTER_TO')
  })

  test('waits for a worker, then shows it when one registers, and approves it', async ({ page }) => {
    let polls = 0
    const known = clusterNodes()
    const arrived = { id: 'n-new', name: 'gpu-box-9', node_type: 'backend', address: '10.0.4.19:50051', status: 'pending', version: 'v3.9.0', labels: {}, last_heartbeat: new Date().toISOString() }
    const log = await mockSwarm(page, { onNodes: () => { polls += 1; return polls < 3 ? known : [...known, arrived] } })
    await page.goto('/app/nodes/add')

    const wait = page.getByRole('status').filter({ hasText: 'Listening for a worker' })
    await expect(wait).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('found-node')).toHaveCount(0)

    const found = page.getByTestId('found-node')
    await expect(found).toBeVisible({ timeout: 20_000 })
    await expect(found).toContainText('gpu-box-9')
    await expect(found).toContainText('Waiting for approval')
    await expect(page.getByRole('heading', { name: 'A machine arrived' })).toBeVisible()
    await expect(wait).toHaveCount(0)
    await expect(found.getByRole('link', { name: 'Open node' })).toHaveAttribute('href', '/app/nodes/n-new')

    await found.getByRole('button', { name: 'Approve' }).click()
    await expect.poll(() => log.filter(entry => entry.path === '/api/nodes/n-new/approve').length).toBe(1)
    await expect(page.getByText('gpu-box-9 approved')).toBeVisible()
  })

  test('does not treat the nodes it already knew as arrivals', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/nodes/add')
    await expect(page.getByRole('status').filter({ hasText: 'Listening for a worker' })).toBeVisible({ timeout: 15_000 })
    await page.waitForTimeout(3500)
    await expect(page.getByTestId('found-node')).toHaveCount(0)
  })

  test('a single install starts with turning distributed mode on, as text it can copy', async ({ page }) => {
    await mockSwarm(page, { nodes: null, features: { distributed: false } })
    await page.goto('/app/nodes/add')
    await expect(page.getByRole('heading', { name: 'Turn on distributed mode first' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('command-block').first()).toContainText('local-ai run --distributed')
    await expect(page.getByTestId('command-block').first()).toContainText('--distributed-nats')
    await expect(page.getByRole('link', { name: 'distributed mode documentation' })).toHaveAttribute('href', 'https://localai.io/features/distributed-mode/')
    await expect(page.getByRole('heading', { name: 'Run this on the new machine' })).toBeVisible()
  })

  test.describe('over P2P', () => {
    const p2p = { token: 'net-token-1', stats: { federated: { online: 1, total: 1 }, llama_cpp_workers: { online: 0, total: 0 }, mlx_workers: { online: 0, total: 0 } } }

    test('a peer instance joins with the network token', async ({ page }) => {
      await mockSwarm(page, { p2p })
      await page.goto('/app/nodes/add?join=peer')
      await expect(page.getByRole('radio', { name: 'Peer instance' })).toHaveAttribute('aria-checked', 'true', { timeout: 15_000 })
      await expect(command(page)).toContainText('-e TOKEN="net-token-1"')
      await expect(command(page)).toContainText('run --federated --p2p')
      await expect(page.getByText('1 peers online')).toBeVisible()
    })

    test('a memory shard offers llama.cpp RPC and MLX', async ({ page }) => {
      await mockSwarm(page, { p2p })
      await page.goto('/app/nodes/add?join=shard')
      await expect(command(page)).toContainText('worker p2p-llama-cpp-rpc', { timeout: 15_000 })
      await page.getByRole('radio', { name: 'MLX (Apple Silicon)' }).click()
      await expect(command(page)).toContainText('worker p2p-mlx')
      await expect(command(page)).toContainText('latest-metal-darwin-arm64')
      // MLX runs on a Mac: GPU build targets do not apply.
      await expect(page.getByRole('radio', { name: 'CUDA 12', exact: true })).toHaveCount(0)
    })

    test('says to turn P2P on first when it is off', async ({ page }) => {
      await mockSwarm(page)
      await page.goto('/app/nodes/add?join=peer')
      await expect(page.getByRole('heading', { name: 'Turn on P2P first' })).toBeVisible({ timeout: 15_000 })
      await expect(page.getByTestId('command-block').first()).toContainText('run --p2p')
      await expect(page.getByTestId('command-block').last()).toContainText('your-token-here')
    })

    test('shows a new peer when the online count goes up', async ({ page }) => {
      let calls = 0
      await mockSwarm(page)
      await page.route('**/api/p2p/token', route => route.fulfill({ status: 200, contentType: 'text/plain', body: 'net-token-1' }))
      await page.route('**/api/p2p/stats', route => {
        calls += 1
        const online = calls < 3 ? 1 : 2
        return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ federated: { online, total: 2 }, llama_cpp_workers: { online: 0, total: 0 }, mlx_workers: { online: 0, total: 0 } }) })
      })
      await page.goto('/app/nodes/add?join=peer')
      await expect(page.getByTestId('found-peer')).toContainText('A new peer joined: 2 online', { timeout: 20_000 })
    })
  })

  test('first run of a cluster carries the same steps in place', async ({ page }) => {
    await mockSwarm(page, { nodes: [] })
    await page.goto('/app/nodes')
    await expect(page.getByTestId('swarm-first-run')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('heading', { name: 'How should it join?' })).toBeVisible()
    await expect(command(page)).toContainText('LOCALAI_REGISTER_TO')
  })

  test('a phone scrolls the command sideways instead of the page', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockSwarm(page)
    await page.goto('/app/nodes/add')
    const block = command(page).locator('pre')
    await expect(block).toBeVisible({ timeout: 15_000 })
    const { scrollWidth, clientWidth } = await block.evaluate(el => ({ scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }))
    expect(scrollWidth).toBeGreaterThan(clientWidth)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    const copy = await page.getByRole('button', { name: 'Copy command' }).last().boundingBox()
    expect(copy.x + copy.width).toBeLessThanOrEqual(390)
    // The method switch scrolls too; none of its choices is cut off for good.
    await page.getByRole('radio', { name: 'Memory shard' }).scrollIntoViewIfNeeded()
    await expect(page.getByRole('radio', { name: 'Memory shard' })).toBeInViewport()
  })
})
