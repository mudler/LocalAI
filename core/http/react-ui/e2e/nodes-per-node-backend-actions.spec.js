import { test, expect } from './coverage-fixtures.js'
import { galleryItem, mockUpdates, snapshotOf } from './updates-fixtures.js'

// These specs cover the per-node backend row on the node detail page:
//   - the update affordance is offered only when the node is behind, and asks first
//   - a delete affordance is present and goes through ConfirmDialog
//
// We mock the distributed-mode API so the tests can run against the
// standalone ui-test-server without spinning up workers/NATS.

const NODE_ID = 'test-node-1'
const NODE_NAME = 'worker-test'
const BACKEND_NAME = 'cuda12-vllm-development'

// `behind`: the read model lists an update for the backend on this node.
async function mockDistributedNodes(page, { onDelete, behind = true } = {}) {
  await mockUpdates(page, {
    snapshot: snapshotOf(behind
      ? [galleryItem(BACKEND_NAME, '0.9.4', '0.9.7', { nodes: [{ nodeId: NODE_ID, nodeName: NODE_NAME, status: 'healthy', version: '0.9.4', models: ['qwen3-coder'] }] })]
      : []),
  })
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
  // The per-node backend table is the Backends tab of the deep-linkable detail
  // page at /app/nodes/:id. Navigate straight there.
  await page.goto(`/app/nodes/${NODE_ID}?tab=backends`)
  await expect(page.getByRole('cell', { name: BACKEND_NAME, exact: true })).toBeVisible({ timeout: 10_000 })
}

async function openBackendActions(page) {
  const trigger = page.getByRole('button', { name: `Actions for backend ${BACKEND_NAME}` })
  await expect(trigger).toBeVisible()
  await trigger.click()

  const menu = page.getByRole('menu', { name: `${BACKEND_NAME} backend actions` })
  await expect(menu).toBeVisible()
  return menu
}

test.describe('Node detail — per-node backend actions', () => {
  test('update affordance is self-explanatory: "Update" with an up arrow, never "Reinstall backend"', async ({ page }) => {
    await mockDistributedNodes(page)
    await openNodeDetail(page)

    await expect(page.getByRole('region', { name: 'Node resources' })).toContainText('RAM')
    await expect(page.getByRole('region', { name: 'Node resources' })).toContainText('3.7 GB / 7.5 GB')

    const menu = await openBackendActions(page)

    // Negative: the old, ambiguous wording must not be used.
    await expect(menu.getByRole('menuitem', { name: 'Reinstall backend' })).toHaveCount(0)
    await expect(menu.getByRole('menuitem', { name: 'Upgrade backend' })).toHaveCount(0)

    // Positive: the action menu names the operation and uses an upgrade icon.
    const updateItem = menu.getByRole('menuitem', { name: 'Update', exact: true })
    await expect(updateItem).toBeVisible()
    await expect(updateItem.locator('svg[data-icon="arrow-up"]')).toBeVisible()
  })

  test('update is offered only when the node is behind', async ({ page }) => {
    await mockDistributedNodes(page, { behind: false })
    await openNodeDetail(page)
    const menu = await openBackendActions(page)
    await expect(menu.getByRole('menuitem', { name: 'Update', exact: true })).toHaveCount(0)
    await expect(menu.getByRole('menuitem', { name: 'Delete backend…' })).toBeVisible()
  })

  test('update asks first, naming the models that stop, and cancel sends nothing', async ({ page }) => {
    await mockDistributedNodes(page)
    const posts = []
    await page.route(`**/api/nodes/${NODE_ID}/backends/upgrade`, (route) => { posts.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: { jobID: 'j1' } }) })
    await openNodeDetail(page)
    const menu = await openBackendActions(page)
    await menu.getByRole('menuitem', { name: 'Update', exact: true }).click()
    const dialog = page.getByTestId('node-update-dialog')
    await expect(dialog.getByRole('heading', { name: `Update ${BACKEND_NAME} on 1 node?` })).toBeVisible()
    await expect(dialog).toContainText(NODE_NAME)
    await expect(dialog).toContainText('Stops qwen3-coder')
    await expect(page.getByTestId('node-update-confirm')).toHaveText('Update 1 node and stop 1 model')
    await page.getByTestId('node-update-cancel').click()
    await expect(dialog).toHaveCount(0)
    expect(posts).toEqual([])
  })

  test('per-node backend row shows a delete (trash) button next to upgrade', async ({ page }) => {
    await mockDistributedNodes(page)
    await openNodeDetail(page)

    const menu = await openBackendActions(page)
    const deleteItem = menu.getByRole('menuitem', { name: 'Delete backend…' })
    await expect(deleteItem).toBeVisible()
    await expect(deleteItem.locator('svg[data-icon="trash"]')).toBeVisible()
  })

  test('clicking delete opens the confirm dialog and POSTs to the per-node delete endpoint', async ({ page }) => {
    let postedBody = null
    await mockDistributedNodes(page, {
      onDelete: async (route) => {
        postedBody = route.request().postDataJSON()
      },
    })
    await openNodeDetail(page)

    const menu = await openBackendActions(page)
    await menu.getByRole('menuitem', { name: 'Delete backend…' }).click()

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

    const menu = await openBackendActions(page)
    await menu.getByRole('menuitem', { name: 'Delete backend…' }).click()

    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: /cancel/i }).click()
    await expect(dialog).toBeHidden()

    // Give any errant request a moment to fire so a regression would be caught.
    await page.waitForTimeout(500)
    expect(deleteCalls).toBe(0)
  })
})
