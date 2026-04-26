import { test, expect } from '@playwright/test'

// These specs cover the per-node backend row in the Nodes page:
//   - the upgrade affordance is self-explanatory (icon + tooltip)
//   - a delete affordance is present and goes through ConfirmDialog
//
// We mock the distributed-mode API so the tests can run against the
// standalone ui-test-server without spinning up workers/NATS.

const NODE_ID = 'test-node-1'
const NODE_NAME = 'worker-test'
const BACKEND_NAME = 'cuda12-vllm-development'

async function mockDistributedNodes(page, { onDelete } = {}) {
  await page.route('**/api/nodes', (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        {
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
        },
      ]),
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

async function expandNodeAndWaitForBackends(page) {
  await page.goto('/app/nodes')
  // Click the row to expand it. The chevron toggle and the row both work,
  // but clicking the name cell is the most user-like.
  await page.getByText(NODE_NAME).first().click()
  await expect(page.getByRole('cell', { name: BACKEND_NAME, exact: true })).toBeVisible({ timeout: 10_000 })
}

test.describe('Nodes page — per-node backend actions', () => {
  test('upgrade affordance is self-explanatory (not "Reinstall backend" with a sync icon)', async ({ page }) => {
    await mockDistributedNodes(page)
    await expandNodeAndWaitForBackends(page)

    // Negative: the old, ambiguous wording must not be used.
    await expect(page.locator('button[title="Reinstall backend"]')).toHaveCount(0)
    await expect(page.locator('button[title="Reinstall backend"] i.fa-sync-alt')).toHaveCount(0)

    // Positive: a self-explanatory upgrade affordance is rendered next to the
    // backend row. We accept either an arrow-up or arrows-rotate glyph; both
    // map to "upgrade" semantics in FontAwesome 6 unambiguously.
    const upgradeBtn = page.locator('button[title="Upgrade backend on this node"]')
    await expect(upgradeBtn).toBeVisible()
    const iconClass = await upgradeBtn.locator('i').getAttribute('class')
    expect(iconClass).toMatch(/fa-(arrow-up|arrows-rotate|up-long)/)
  })

  test('per-node backend row shows a delete (trash) button next to upgrade', async ({ page }) => {
    await mockDistributedNodes(page)
    await expandNodeAndWaitForBackends(page)

    const deleteBtn = page.locator('button[title="Delete backend from this node"]')
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
    await expandNodeAndWaitForBackends(page)

    await page.locator('button[title="Delete backend from this node"]').click()

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
    await expandNodeAndWaitForBackends(page)

    await page.locator('button[title="Delete backend from this node"]').click()

    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: /cancel/i }).click()
    await expect(dialog).toBeHidden()

    // Give any errant request a moment to fire so a regression would be caught.
    await page.waitForTimeout(500)
    expect(deleteCalls).toBe(0)
  })
})
