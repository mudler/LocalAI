import { test, expect } from './coverage-fixtures.js'

// These specs cover the per-node backend row in the Nodes page:
//   - the upgrade affordance is self-explanatory (icon + tooltip)
//   - a delete affordance is present and goes through ConfirmDialog
//
// We mock the distributed-mode API so the tests can run against the
// standalone ui-test-server without spinning up workers.

const NODE_ID = 'test-node-1'
const NODE_NAME = 'worker-test'
const BACKEND_NAME = 'cuda12-vllm-development'

async function mockDistributedNodes(page, { onDelete } = {}) {
  const nodeRecord = {
    id: NODE_ID,
    name: NODE_NAME,
    node_type: 'backend',
    address: '10.0.0.1:50051',
    http_address: '10.0.0.1:8090',
    status: 'healthy',
    total_vram: 0,
    available_vram: 0,
    total_ram: 8_000_000_000,
    available_ram: 4_000_000_000,
    gpu_vendor: '',
    last_heartbeat: new Date().toISOString(),
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  }

  await page.route('**/api/nodes', (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([nodeRecord]),
    })
  })

  // The detail page fetches the single node via nodesApi.get(id).
  await page.route(`**/api/nodes/${NODE_ID}`, (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(nodeRecord),
    })
  })

  await page.route('**/api/nodes/scheduling', (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '[]',
    })
  })

  await page.route(`**/api/nodes/${NODE_ID}/models`, (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '[]',
    })
  })

  await page.route(`**/api/nodes/${NODE_ID}/backends`, (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        {
          name: BACKEND_NAME,
          is_system: false,
          is_meta: false,
          installed_at: new Date().toISOString(),
        },
      ]),
    })
  })

  await page.route(`**/api/nodes/${NODE_ID}/backends/delete`, async (route) => {
    if (onDelete) {
      await onDelete(route)
    }
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ message: 'backend deleted' }),
    })
  })
}

async function openNodeDetail(page) {
  // The per-node backend table now lives on the deep-linkable detail page
  // at /app/nodes/:id (the old expand-row + "Manage" disclosure was removed
  // when the roster was restructured). Navigate straight there.
  await page.goto(`/app/nodes/${NODE_ID}`)
  await expect(page.getByRole('cell', { name: BACKEND_NAME, exact: true })).toBeVisible({ timeout: 10_000 })
}

test.describe('Nodes page — per-node backend actions', () => {
  test('backend log page uses one-based replica labels, token classes, and a safe direct-link return', async ({ page }) => {
    await mockDistributedNodes(page)
    await page.route(`**/api/nodes/${NODE_ID}/models`, route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        { model_name: 'llama', replica_index: 0 },
        { model_name: 'llama', replica_index: 1 },
      ]),
    }))
    await page.route(`**/api/nodes/${NODE_ID}/backend-logs/**`, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([
      { timestamp: '2026-09-15T12:00:00Z', stream: 'stdout', text: 'ready' },
    ]) }))

    await page.goto(`/app/node-backend-logs/${NODE_ID}/llama%230`)
    await expect(page.getByText('Replica 1', { exact: true }).first()).toBeVisible({ timeout: 10_000 })
    await expect(page.getByRole('radio', { name: 'Replica 2' })).toBeVisible()
    await expect(page.locator('.node-backend-logs__toolbar')).toBeVisible()
    await expect(page.locator('.node-backend-logs__output')).toBeVisible()
    await expect(page.getByText('ready', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Clear' }).click()
    await expect(page.getByText('ready', { exact: true })).toBeHidden()
    await expect(page.locator('.node-backend-logs [style]')).toHaveCount(0)
    await expect(page.getByRole('link', { name: 'Back to nodes' })).toHaveAttribute('href', '/app/nodes')

    await page.getByRole('radio', { name: 'Replica 2' }).click()
    await expect(page).toHaveURL(/llama%231$/)
  })

  test('upgrade affordance is self-explanatory (not "Reinstall backend" with a sync icon)', async ({ page }) => {
    await mockDistributedNodes(page)
    await openNodeDetail(page)

    await expect(page.locator('.node-detail__metrics')).toContainText('RAM')
    await expect(page.locator('.node-detail__metrics')).toContainText('3.7 GB / 7.5 GB')

    // Negative: the old, ambiguous wording must not be used.
    await expect(page.locator('button[title="Reinstall backend"]')).toHaveCount(0)
    await expect(page.locator('button[title="Reinstall backend"] i.fa-sync-alt')).toHaveCount(0)

    // Positive: a self-explanatory upgrade affordance is rendered next to the
    // backend row. We accept either an arrow-up or arrows-rotate glyph; both
    // map to "upgrade" semantics in FontAwesome 6 unambiguously.
    await page.getByRole('button', { name: `Actions for backend ${BACKEND_NAME}` }).click()
    const upgradeBtn = page.getByRole('menuitem', { name: 'Upgrade backend' })
    await expect(upgradeBtn).toBeVisible()
    const iconClass = await upgradeBtn.locator('i').getAttribute('class')
    expect(iconClass).toMatch(/fa-(arrow-up|arrows-rotate|up-long)/)
  })

  test('per-node backend row shows a delete (trash) button next to upgrade', async ({ page }) => {
    await mockDistributedNodes(page)
    await openNodeDetail(page)

    await page.getByRole('button', { name: `Actions for backend ${BACKEND_NAME}` }).click()
    const deleteBtn = page.getByRole('menuitem', { name: 'Delete backend…' })
    await expect(deleteBtn).toBeVisible()
    await expect(deleteBtn.locator('i.fa-trash')).toBeVisible()
  })

  test('clicking delete opens the confirm dialog and POSTs to the per-node delete endpoint', async ({ page }) => {
    let postedBody = null
    await mockDistributedNodes(page, {
      onDelete: async (route) => {
        postedBody = route.request().postDataJSON()
      },
    })
    await openNodeDetail(page)

    await page.getByRole('button', { name: `Actions for backend ${BACKEND_NAME}` }).click()
    await page.getByRole('menuitem', { name: 'Delete backend…' }).click()

    // ConfirmDialog uses role="alertdialog" and a danger confirm button.
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toBeVisible()
    const confirmBtn = dialog.locator('button.btn-danger')
    await expect(confirmBtn).toBeVisible()
    await confirmBtn.click()

    // Wait until the POST landed.
    await expect.poll(() => postedBody, { timeout: 5_000 }).toEqual({ backend: BACKEND_NAME })
  })

  test('clicking delete and cancelling does not POST', async ({ page }) => {
    let deleteCalls = 0
    await mockDistributedNodes(page, {
      onDelete: () => {
        deleteCalls += 1
      },
    })
    await openNodeDetail(page)

    await page.getByRole('button', { name: `Actions for backend ${BACKEND_NAME}` }).click()
    await page.getByRole('menuitem', { name: 'Delete backend…' }).click()

    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: /cancel/i }).click()
    await expect(dialog).toBeHidden()

    // Give any errant request a moment to fire so a regression would be caught.
    await page.waitForTimeout(500)
    expect(deleteCalls).toBe(0)
  })
})
