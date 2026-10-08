import { test, expect } from './coverage-fixtures.js'
import { mockSwarm } from './swarm-fixtures.js'

// P2P (Swarm) admin page — renders in the no-auth test harness (isAdmin).
test.describe('P2P page', () => {
  test.describe('when P2P is off', () => {
    test.beforeEach(async ({ page }) => {
      await page.goto('/app/p2p')
    })

    test('renders the P2P distribution overview and capability list', async ({ page }) => {
      await expect(page).toHaveURL(/\/app\/p2p$/)
      await expect(page.getByRole('heading', { name: /P2P is not enabled/i })).toBeVisible()
      await expect(page.getByRole('heading', { name: 'Instance federation' })).toBeVisible()
      await expect(page.getByRole('heading', { name: 'Model sharding' })).toBeVisible()
      await expect(page.getByRole('heading', { name: 'Resource sharing' })).toBeVisible()
      await expect(page.getByRole('heading', { name: /How to enable P2P/i })).toBeVisible()
    })

    test('hardware selector offers build targets and changes the command', async ({ page }) => {
      const cpu = page.getByRole('radio', { name: 'CPU', exact: true })
      const cuda = page.getByRole('radio', { name: 'CUDA 12', exact: true })
      await expect(cpu).toBeVisible()
      await expect(cuda).toBeVisible()
      await expect(page.getByTestId('command-block').first()).not.toContainText('--gpus all')
      await cuda.click()
      await expect(cuda).toHaveAttribute('aria-checked', 'true')
      await expect(page.getByTestId('command-block').first()).toContainText('--gpus all')
      await expect(page.getByRole('heading', { name: /How to enable P2P/i })).toBeVisible()
    })
  })

  test.describe('when P2P is on', () => {
    const p2p = {
      token: 'b64-network-token',
      stats: { federated: { online: 2, total: 3 }, llama_cpp_workers: { online: 1, total: 1 }, mlx_workers: { online: 0, total: 0 } },
      federation: { nodes: [{ id: 'peer-a', isOnline: true }, { id: 'peer-b', isOnline: true }, { id: 'peer-c', isOnline: false }] },
      workers: { llama_cpp: { nodes: [{ id: 'rpc-1', isOnline: true }] }, mlx: { nodes: [] } },
    }

    test('shows the network token with a copy button, and who is online', async ({ page }) => {
      await mockSwarm(page, { p2p })
      await page.goto('/app/p2p')
      await expect(page.getByTestId('p2p-token')).toContainText('b64-network-token', { timeout: 15_000 })
      await expect(page.getByRole('tab', { name: /Federation/ })).toContainText('2/3 online')
      const table = page.getByRole('table', { name: 'Connected instances' })
      await expect(table.getByRole('row', { name: /peer-a/ })).toContainText('Online')
      await expect(table.getByRole('row', { name: /peer-c/ })).toContainText('Offline')
      await page.getByRole('button', { name: 'Copy token' }).click()
      await expect(page.getByText(/Token copied to clipboard|Could not copy/)).toBeVisible()
    })

    test('shows the model sharding workers on their own tab', async ({ page }) => {
      await mockSwarm(page, { p2p })
      await page.goto('/app/p2p')
      await page.getByRole('tab', { name: /Model sharding/ }).click()
      await expect(page.getByRole('table', { name: 'llama.cpp RPC workers' })).toContainText('rpc-1')
      await expect(page.getByTestId('peer-empty')).toContainText('No MLX workers connected yet')
      await expect(page.getByRole('link', { name: 'Add a memory shard' }).first()).toHaveAttribute('href', '/app/nodes/add?join=shard')
    })

    test('hands the person to Add a node to bring a peer in', async ({ page }) => {
      await mockSwarm(page, { p2p })
      await page.goto('/app/p2p')
      await page.getByRole('link', { name: 'Add a peer instance' }).click()
      await expect(page).toHaveURL(/\/app\/nodes\/add\?join=peer$/)
      await expect(page.getByRole('radio', { name: 'Peer instance' })).toHaveAttribute('aria-checked', 'true')
    })

    test('builds the federated server command from the token', async ({ page }) => {
      await mockSwarm(page, { p2p })
      await page.goto('/app/p2p')
      await expect(page.getByTestId('command-block')).toContainText('-e TOKEN="b64-network-token"', { timeout: 15_000 })
      await expect(page.getByTestId('command-block')).toContainText('local-ai-federated')
    })

    test('fits a phone', async ({ page }) => {
      await page.setViewportSize({ width: 390, height: 844 })
      await mockSwarm(page, { p2p })
      await page.goto('/app/p2p')
      await expect(page.getByTestId('p2p-token')).toBeVisible({ timeout: 15_000 })
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    })
  })
})
