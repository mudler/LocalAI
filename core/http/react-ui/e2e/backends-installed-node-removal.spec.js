// SPDX-License-Identifier: MIT
import { test, expect } from './coverage-fixtures.js'

const BUILD = 'cpu-example-engine-development'
const SIBLING = 'cpu-example-engine'
const ids = ['worker-alpha', 'worker-beta']
const remove = (page, id) => page.getByRole('button', { name: `Remove from ${id}…`, exact: true })

async function setup(page, options = {}) {
  const state = { deletes: [], inventoryCalls: {}, installedCalls: 0, failRefresh: false, ...options }
  const nodes = ids.map(id => ({ id, name: id, node_type: 'backend', status: 'healthy', address: `${id}.example:50051` }))
  const inventories = Object.fromEntries(ids.map(id => [id, [{ name: BUILD, is_system: false, is_meta: false }, { name: SIBLING, is_system: false, is_meta: false }]]))
  state.nodes = nodes
  state.inventories = inventories
  await page.route('**/api/nodes', route => route.fulfill({ json: state.nodes }))
  await page.route('**/api/backends?*', route => state.failRefresh
    ? route.fulfill({ status: 500, json: { error: 'catalog unavailable' } })
    : route.fulfill({ json: { backends: [BUILD, SIBLING].map(name => ({ id: name, name, installed: true, isAlias: true, isDevelopment: name === BUILD, nodes: ids.map(id => ({ node_id: id, node_name: id })) })) } }))
  await page.route('**/api/backends/upgrades', route => route.fulfill({ json: {} }))
  await page.route('**/backends', route => {
    state.installedCalls++
    return state.failRefresh ? route.fulfill({ status: 500, json: { error: 'installed unavailable' } }) : route.fulfill({ json: [BUILD, SIBLING].map(Name => ({ Name, IsSystem: !!state.aggregateSystem, Nodes: ids.filter(id => inventories[id].some(b => b.name === Name)).map(id => ({ node_id: id, node_name: id, node_status: 'healthy' })) })) })
  })
  for (const id of ids) {
    await page.route(`**/api/nodes/${id}/backends`, async route => {
      state.inventoryCalls[id] = (state.inventoryCalls[id] || 0) + 1
      if (state.inventoryWait) await state.inventoryWait
      return state.inventoryError ? route.fulfill({ status: 500, json: { error: 'inventory unavailable' } }) : route.fulfill({ json: inventories[id] })
    })
    await page.route(`**/api/nodes/${id}/backends/delete`, async route => {
      state.deletes.push({ id, method: route.request().method(), body: route.request().postDataJSON() })
      if (state.deleteWait) await state.deleteWait
      if (state.deleteError) return route.fulfill({ status: 500, json: { error: 'removal rejected' } })
      inventories[id] = inventories[id].filter(b => b.name !== BUILD)
      if (state.refreshError) state.failRefresh = true
      return route.fulfill({ json: { message: 'removed' } })
    })
  }
  await page.route('**/backends/delete/**', () => { throw new Error('Global deletion is forbidden') })
  return state
}
async function open(page, extra = '') {
  await page.goto(`/app/backends?view=installed&show_all=1&development=1${extra}`)
  await expect(page.locator(`[data-entity="${BUILD}"]`)).toBeVisible()
  await page.locator(`[data-entity="${BUILD}"]`).click()
}
const confirm = page => page.getByRole('alertdialog').locator('button.btn-danger')

test('Installed concrete build has independent lazy per-node actions and scoped deletion', async ({ page }) => {
  const s = await setup(page)
  await page.goto('/app/backends?view=installed&show_all=1&development=1')
  await expect(page.locator(`[data-entity="${BUILD}"]`)).toBeVisible()
  expect(s.inventoryCalls).toEqual({})
  await page.locator(`[data-entity="${BUILD}"]`).click()
  await expect(remove(page, ids[0])).toBeEnabled()
  await expect(remove(page, ids[1])).toBeEnabled()
  await remove(page, ids[0]).click()
  await expect(page.getByRole('alertdialog')).toContainText(BUILD)
  await expect(page.getByRole('alertdialog')).toContainText(ids[0])
  expect(s.deletes).toEqual([])
  await confirm(page).click()
  await expect(page.getByRole('alertdialog')).toBeHidden()
  expect(s.deletes).toEqual([{ id: ids[0], method: 'POST', body: { backend: BUILD } }])
  await expect(remove(page, ids[1])).toBeEnabled()
  await expect(page.locator(`[data-entity="${SIBLING}"]`)).toBeVisible()
  expect(s.installedCalls).toBeGreaterThan(1)
})

for (const cancel of ['Cancel', 'Escape', 'backdrop']) test(`cancellation ${cancel} sends no request`, async ({ page }) => {
  const s = await setup(page); await open(page)
  await remove(page, ids[0]).click()
  if (cancel === 'Escape') await page.keyboard.press('Escape')
  else if (cancel === 'backdrop') await page.locator('.confirm-dialog-backdrop').click({ position: { x: 2, y: 2 } })
  else await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByRole('alertdialog')).toBeHidden()
  expect(s.deletes).toEqual([])
  await expect(remove(page, ids[0])).toBeEnabled()
})

for (const aggregateSystem of [true, false]) test(`actual mixed system inventory overrides aggregate ${aggregateSystem}`, async ({ page }) => {
  const s = await setup(page, { aggregateSystem }); s.inventories[ids[0]][0].is_system = true
  await open(page)
  await expect(remove(page, ids[0])).toBeDisabled()
  await expect(remove(page, ids[1])).toBeEnabled()
})

for (const mode of ['system', 'missing', 'offline', 'inventory error']) test(`confirmation revalidation blocks ${mode}`, async ({ page }) => {
  const s = await setup(page); await open(page); await remove(page, ids[0]).click()
  if (mode === 'system') s.inventories[ids[0]][0].is_system = true
  if (mode === 'missing') s.inventories[ids[0]] = []
  if (mode === 'offline') s.nodes[0].status = 'offline'
  if (mode === 'inventory error') s.inventoryError = true
  await confirm(page).click()
  await expect(page.getByRole('alertdialog')).toBeHidden()
  await expect(page.getByRole('alert').filter({ hasText: ids[0] })).toBeVisible()
  expect(s.deletes).toEqual([])
})

for (const mode of ['unknown', 'meta', 'alias', 'offline', 'draining', 'missing target']) test(`fail closed for ${mode}`, async ({ page }) => {
  const s = await setup(page)
  if (mode === 'unknown') delete s.inventories[ids[0]][0].is_system
  if (mode === 'meta') s.inventories[ids[0]][0].is_meta = true
  if (mode === 'alias') s.inventories[ids[0]] = []
  if (mode === 'offline' || mode === 'draining') s.nodes[0].status = mode
  if (mode === 'missing target') s.nodes.shift()
  await open(page, `&target=${ids[0]}`)
  if (mode === 'alias') await expect(remove(page, ids[0])).toHaveCount(0)
  else await expect(remove(page, ids[0])).toBeDisabled()
  await expect(remove(page, ids[1])).toHaveCount(0)
  expect(s.deletes).toEqual([])
})

test('inventory error retry and deferred loading fail closed', async ({ page }) => {
  let release; const wait = new Promise(resolve => { release = resolve })
  const s = await setup(page, { inventoryWait: wait, inventoryError: true }); await open(page)
  await expect(remove(page, ids[0])).toBeDisabled(); release()
  await expect(page.getByRole('button', { name: `Retry inventory for ${ids[0]}` })).toBeVisible()
  s.inventoryError = false
  await page.getByRole('button', { name: `Retry inventory for ${ids[0]}` }).click()
  await expect(remove(page, ids[0])).toBeEnabled()
})

test('immutable URL target and duplicate confirmation guard', async ({ page }) => {
  let release; const wait = new Promise(resolve => { release = resolve })
  const s = await setup(page, { deleteWait: wait }); await open(page, `&target=${ids[0]}`)
  await remove(page, ids[0]).click()
  await page.evaluate(id => { const url = new URL(location.href); url.searchParams.set('target', id); history.pushState({}, '', url); dispatchEvent(new PopStateEvent('popstate')) }, ids[1])
  await confirm(page).evaluate(el => { el.click(); el.click() })
  await expect.poll(() => s.deletes.length).toBe(1)
  await expect(confirm(page)).toBeDisabled()
  release(); await expect(page.getByRole('alertdialog')).toBeHidden()
  expect(s.deletes).toEqual([{ id: ids[0], method: 'POST', body: { backend: BUILD } }])
})

test('API failure preserves rows and permits explicit confirmed retry', async ({ page }) => {
  const s = await setup(page, { deleteError: true }); await open(page)
  await remove(page, ids[0]).click(); await confirm(page).click()
  await expect(page.getByRole('alertdialog')).toBeHidden()
  await expect(page.getByRole('alert').filter({ hasText: ids[0] })).toBeVisible()
  await expect(remove(page, ids[0])).toBeEnabled()
  expect(s.deletes).toHaveLength(1)
  s.deleteError = false
  await remove(page, ids[0]).click(); expect(s.deletes).toHaveLength(1)
  await confirm(page).click(); await expect(page.getByRole('alertdialog')).toBeHidden()
  expect(s.deletes).toHaveLength(2)
})

test('successful deletion with refresh failure retains stale rows and offers refresh only', async ({ page }) => {
  const s = await setup(page, { refreshError: true }); await open(page)
  await remove(page, ids[0]).click(); await confirm(page).click()
  await expect(page.getByRole('alertdialog')).toBeHidden()
  await expect(page.getByRole('button', { name: 'Retry backend refresh' })).toBeVisible()
  await expect(page.locator(`[data-entity="${BUILD}"]`)).toBeVisible()
  await expect(page.locator(`[data-entity="${SIBLING}"]`)).toBeVisible()
  s.failRefresh = false
  await page.getByRole('button', { name: 'Retry backend refresh' }).click()
  await expect(remove(page, ids[1])).toBeEnabled()
  expect(s.deletes).toHaveLength(1)
})


test('catalog target uses the same confirmed exact-node flow', async ({ page }) => {
  const s = await setup(page)
  await page.goto(`/app/backends?view=catalog&show_all=1&development=1&target=${ids[0]}&backend=${BUILD}`)
  await expect(remove(page, ids[0])).toBeEnabled()
  await remove(page, ids[0]).click()
  expect(s.deletes).toHaveLength(0)
  await expect(page.getByRole('alertdialog')).toContainText(BUILD)
  await confirm(page).click()
  await expect(page.getByRole('alertdialog')).toBeHidden()
  expect(s.deletes).toEqual([{ id: ids[0], method: 'POST', body: { backend: BUILD } }])
})

test('cold installed failure is not empty success, and retry recovers', async ({ page }) => {
  const s = await setup(page, { failRefresh: true })
  await page.goto('/app/backends?view=installed&show_all=1&development=1')
  await expect(page.getByRole('button', { name: 'Retry backend refresh' })).toBeVisible()
  await expect(page.getByText('No backends installed', { exact: true })).toHaveCount(0)
  s.failRefresh = false
  await page.getByRole('button', { name: 'Retry backend refresh' }).click()
  await expect(page.locator(`[data-entity="${BUILD}"]`)).toBeVisible()
})

test('background installed refresh retains rows on failure without claiming deletion success', async ({ page }) => {
  const s = await setup(page); await open(page)
  s.failRefresh = true
  // An operations change triggers the existing background list refresh.
  await page.route('**/api/operations', route => route.fulfill({ json: { operations: [{ name: SIBLING, jobID: 'synthetic-job', isQueued: true }] } }))
  await expect(page.getByRole('button', { name: 'Retry backend refresh' })).toBeVisible({ timeout: 15000 })
  await expect(page.locator(`[data-entity="${BUILD}"]`)).toBeVisible()
  expect(s.deletes).toHaveLength(0)
})

test('conflicting queued operation disables removal', async ({ page }) => {
  const s = await setup(page)
  await page.route('**/api/operations', route => route.fulfill({ json: { operations: [{ name: BUILD, jobID: 'synthetic-job', isQueued: true }] } }))
  await open(page)
  await expect(remove(page, ids[0])).toBeDisabled()
  expect(s.deletes).toHaveLength(0)
})

test('standalone system protection has no scoped removal', async ({ page }) => {
  const s = await setup(page, { aggregateSystem: true })
  await page.route('**/api/nodes', route => route.fulfill({ status: 503, json: { error: 'distributed disabled' } }))
  await open(page)
  await expect(remove(page, ids[0])).toHaveCount(0)
  await expect(page.locator(`[data-entity="${BUILD}"]`)).toContainText('Protected')
  expect(s.deletes).toHaveLength(0)
})
