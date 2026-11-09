import { test, expect } from './coverage-fixtures.js'
import { mockOperate, installedBackend } from './operate-fixtures.js'
import { MEMBER, statusFixture } from './access-fixtures.js'
import { addonRecord, failedOp, galleryItem, mockUpdates, snapshotOf, updateAvailable } from './updates-fixtures.js'

// The per-node view of a backend update and its confirmation. The API is
// stubbed; the page, the dialog, the shell provider and the run are the real
// ones. A node update is a 202 with a job id: the stub keeps the operation in
// /api/operations for a moment, then moves the node to the new version, or
// leaves a failed operation when the node is set to fail.

const NODE = (id, name, version, models, extra = {}) => ({ nodeId: id, nodeName: name, status: 'healthy', version, models, ...extra })
const CLUSTER = () => ({
  a: NODE('a', 'gpu-a', '0.9.4', ['qwen3-coder', 'gemma-3']),
  b: NODE('b', 'gpu-b', '0.9.4', []),
  c: NODE('c', 'cpu-c', '0.9.7', []),
})

// Installs the stubs and returns the state a spec reads and changes.
//   nodes    the nodes the read model reports, by id
//   fail     ids of the nodes whose update fails
//   posts    { id, backend } of every node update started, in order
async function setup(page, { nodes = CLUSTER(), fail = [], itemState = 'update', extra = {}, moreItems = [], addons = [], operations = [] } = {}) {
  await mockOperate(page)
  const state = { nodes, fail: new Set(fail), posts: [], ends: [], ...extra }
  const build = () => {
    const behind = Object.values(state.nodes).some(n => n.version !== '0.9.7')
    return snapshotOf(behind
      ? [galleryItem('llama-cpp', '0.9.4', '0.9.7', {
        state: itemState,
        nodes: Object.values(state.nodes),
        driftNodes: Object.values(state.nodes).filter(n => n.drift).length,
      }), ...moreItems]
      : moreItems)
  }
  const api = await mockUpdates(page, {
    snapshot: build(),
    installed: [installedBackend('llama-cpp', '0.9.4')],
    addons,
    operations,
  })
  state.api = api
  await page.route('**/api/nodes/*/backends/upgrade', (route) => {
    const id = route.request().url().split('/').at(-3)
    const backend = route.request().postDataJSON().backend
    state.posts.push({ id, backend, at: Date.now() })
    state.jobs = (state.jobs || 0) + 1
    const jobID = `node-${id}-${state.jobs}`
    const failing = state.fail.has(id)
    api.operations = [...api.operations, {
      id: backend, name: backend, fullName: backend, jobID, nodeID: id, progress: 30, taskType: 'upgrade', isDeletion: false,
      isBackend: true, isQueued: false, cancellable: true, message: 'installing',
      ...(failing ? { error: 'download interrupted' } : {}),
    }]
    if (!failing) {
      setTimeout(() => {
        api.operations = api.operations.filter(o => o.jobID !== jobID)
        state.nodes[id] = { ...state.nodes[id], version: '0.9.7' }
        api.snapshot = build()
        state.ends.push({ id, at: Date.now() })
      }, 1300)
    }
    return route.fulfill({ status: 202, json: { jobID, message: 'backend upgrade started' } })
  })
  state.rebuild = () => { api.snapshot = build() }
  return state
}

const dialog = (page) => page.getByTestId('node-update-dialog')
const itemOf = (page, node) => page.locator(`[data-testid="node-update-item"][data-node="${node}"]`)

test.describe('Updates: the nodes of a backend', () => {
  test('the Nodes action shows on a backend that several nodes report and opens the per-node page', async ({ page }) => {
    await setup(page)
    await page.goto('/app/updates')
    const row = page.locator('[data-testid="update-row"][data-name="llama-cpp"]')
    await expect(row).toBeVisible()
    const nodes = row.getByRole('link', { name: 'Show the nodes of llama-cpp' })
    await expect(nodes).toHaveText('Nodes')
    await nodes.click()
    await expect(page).toHaveURL(/\/app\/updates\/backend\/llama-cpp$/)
    await expect(page.getByRole('heading', { level: 1, name: 'llama-cpp' })).toBeVisible()
    // The Updates tab stays the current one.
    await expect(page.locator('[data-hub-tab="updates"]')).toHaveAttribute('aria-current', 'page')
  })

  test('a single node has no Nodes action and its Update starts at once, with no dialog', async ({ page }) => {
    const state = await setup(page, { nodes: { a: NODE('a', 'only', '0.9.4', ['m']) } })
    const direct = []
    await page.route('**/api/backends/upgrade/*', (route) => { direct.push(route.request().url()); return route.fulfill({ json: { jobID: 'j', uuid: 'j' } }) })
    await page.goto('/app/updates')
    const row = page.locator('[data-testid="update-row"][data-name="llama-cpp"]')
    await expect(row).toBeVisible()
    await expect(row.getByTestId('update-row-nodes')).toHaveCount(0)
    // A backend with no nodes at all is the same.
    state.api.snapshot = snapshotOf([galleryItem('llama-cpp', '0.9.4', '0.9.7')])
    await page.getByRole('button', { name: 'Check now' }).click()
    await expect(row.getByTestId('update-row-nodes')).toHaveCount(0)
    await row.getByRole('button', { name: 'Update llama-cpp' }).click()
    await expect.poll(() => direct.length).toBe(1)
    await expect(dialog(page)).toHaveCount(0)
  })

  test('the page lists each node with its version, models and state, and Update only where it is behind', async ({ page }) => {
    await setup(page)
    await page.goto('/app/updates/backend/llama-cpp')
    await expect(page.getByRole('heading', { level: 1, name: 'llama-cpp' })).toBeVisible()
    await expect(page.getByText('Version v0.9.7 is available. 3 nodes have it installed, and 2 of them need the update.')).toBeVisible()
    await expect(page.getByTestId('node-warning')).toContainText('Updating stops the models that use this backend on each node.')
    const rows = page.getByTestId('node-row')
    await expect(rows).toHaveCount(3)
    await expect(rows.nth(0)).toContainText('cpu-c')
    await expect(rows.nth(0)).toContainText('Up to date')
    await expect(rows.nth(0).getByRole('button')).toHaveCount(0)
    await expect(rows.nth(1)).toContainText('gpu-a')
    await expect(rows.nth(1)).toContainText('v0.9.4')
    await expect(rows.nth(1)).toContainText('qwen3-coder, gemma-3')
    await expect(rows.nth(1)).toContainText('Update available')
    await expect(rows.nth(1).getByRole('button', { name: 'Update gpu-a' })).toBeVisible()
    // An empty list is not "none": the registry may miss a model.
    await expect(rows.nth(2)).toContainText('None recorded')
    await expect(page.getByTestId('update-nodes')).toHaveText('Update 2 nodes')
    // The breadcrumb leads back.
    await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: 'Updates' }).click()
    await expect(page).toHaveURL(/\/app\/updates$/)
  })

  test('a node that differs from the cluster is said to differ, never to be behind', async ({ page }) => {
    await setup(page, {
      itemState: 'drift',
      nodes: {
        a: NODE('a', 'gpu-a', '0.9.9', [], { drift: true }),
        b: NODE('b', 'gpu-b', '0.9.7', []),
        c: NODE('c', 'cpu-c', '0.9.7', []),
      },
      extra: {},
    })
    // The read model of a drift item says what the cluster has: the gallery version.
    await page.goto('/app/updates/backend/llama-cpp')
    const row = page.locator('[data-testid="node-row"][data-node="a"]')
    await expect(row).toContainText('Differs from the cluster')
    await expect(page.getByTestId('node-row')).toHaveCount(3)
    await expect(page.getByTestId('node-row').filter({ hasText: /behind/i })).toHaveCount(0)
    await expect(page.getByTestId('update-backend-page').getByText(/\d+ of 3 nodes (differs|differ) from the cluster/)).toBeVisible()
    await expect(page.getByTestId('node-drift-note')).toContainText('can be newer')
  })

  test('a backend with nothing listed says so and leads back', async ({ page }) => {
    await setup(page, { nodes: { a: NODE('a', 'gpu-a', '0.9.7', []), b: NODE('b', 'gpu-b', '0.9.7', []) } })
    await page.goto('/app/updates/backend/llama-cpp')
    await expect(page.getByText('llama-cpp has no node to update')).toBeVisible()
    await page.getByRole('link', { name: 'Back to Updates' }).click()
    await expect(page).toHaveURL(/\/app\/updates$/)
  })
})

test.describe('Updates: the node confirmation', () => {
  test('names each node, its versions and the models that stop, with the cost on the button', async ({ page }) => {
    const state = await setup(page, { nodes: { ...CLUSTER(), b: NODE('b', 'gpu-b', '0.9.4', ['llama-3.1-8b']) } })
    await page.goto('/app/updates/backend/llama-cpp')
    await page.getByTestId('update-nodes').click()
    const d = dialog(page)
    await expect(d).toHaveAttribute('role', 'alertdialog')
    await expect(d.getByRole('heading', { name: 'Update llama-cpp on 2 nodes?' })).toBeVisible()
    await expect(d).toContainText('Each node stops the models that use llama-cpp')
    await expect(itemOf(page, 'a')).toContainText('gpu-a')
    await expect(itemOf(page, 'a')).toContainText('v0.9.4 to v0.9.7')
    await expect(itemOf(page, 'a')).toContainText('Stops qwen3-coder, gemma-3')
    await expect(itemOf(page, 'b')).toContainText('Stops llama-3.1-8b')
    await expect(page.getByTestId('node-update-total')).toContainText('3 models stop. Nodes update one after another.')
    await expect(page.getByTestId('node-update-confirm')).toHaveText('Update 2 nodes and stop 3 models')
    expect(state.posts).toEqual([])
  })

  test('says "none recorded", not "none", when the registry names no model for a node', async ({ page }) => {
    await setup(page)
    await page.goto('/app/updates/backend/llama-cpp')
    await page.getByTestId('update-nodes').click()
    await expect(itemOf(page, 'a')).toContainText('Stops qwen3-coder, gemma-3')
    await expect(itemOf(page, 'b')).toContainText('None recorded')
    // Some are known and some are not: a lower bound, said as one.
    await expect(page.getByTestId('node-update-total')).toContainText('2 models are recorded as stopping.')
    await expect(page.getByTestId('node-update-total')).toContainText('the record can miss one, so more models can stop')
    await expect(page.getByTestId('node-update-confirm')).toHaveText('Update 2 nodes and stop at least 2 models')
  })

  test('with no model named anywhere the button says only what it updates, and the line does not claim that nothing stops', async ({ page }) => {
    await setup(page, { nodes: { a: NODE('a', 'gpu-a', '0.9.4', []), b: NODE('b', 'gpu-b', '0.9.4'), c: NODE('c', 'cpu-c', '0.9.7') } })
    await page.goto('/app/updates/backend/llama-cpp')
    await page.getByTestId('update-nodes').click()
    await expect(page.getByTestId('node-update-confirm')).toHaveText('Update 2 nodes')
    const total = page.getByTestId('node-update-total')
    await expect(total).toContainText('No loaded model is recorded on these nodes.')
    await expect(total).toContainText('can still stop')
    await expect(total).not.toContainText(/0 models|no model stops|nothing stops/i)
  })

  test('cancel sends nothing, and Escape does the same and gives the focus back', async ({ page }) => {
    const state = await setup(page)
    await page.goto('/app/updates/backend/llama-cpp')
    const opener = page.getByRole('button', { name: 'Update gpu-a' })
    await opener.click()
    await expect(page.getByTestId('node-update-cancel')).toBeFocused()
    await page.getByTestId('node-update-cancel').click()
    await expect(dialog(page)).toHaveCount(0)
    await expect(opener).toBeFocused()
    await opener.click()
    await expect(dialog(page)).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(dialog(page)).toHaveCount(0)
    await expect(opener).toBeFocused()
    expect(state.posts).toEqual([])
  })

  test('keyboard: Tab stays inside the dialog and Enter on the focused Cancel closes it', async ({ page }) => {
    await setup(page)
    await page.goto('/app/updates/backend/llama-cpp')
    await page.getByTestId('update-nodes').click()
    await expect(page.getByTestId('node-update-cancel')).toBeFocused()
    for (let i = 0; i < 6; i += 1) {
      await page.keyboard.press('Tab')
      const inside = await page.evaluate(() => Boolean(document.activeElement && document.activeElement.closest('[data-testid="node-update-dialog"]')))
      expect(inside).toBe(true)
    }
    await page.getByTestId('node-update-cancel').focus()
    await page.keyboard.press('Enter')
    await expect(dialog(page)).toHaveCount(0)
  })

  test('the Update of one row asks the same question for that node only', async ({ page }) => {
    await setup(page)
    await page.goto('/app/updates/backend/llama-cpp')
    await page.getByRole('button', { name: 'Update gpu-b' }).click()
    await expect(dialog(page).getByRole('heading', { name: 'Update llama-cpp on 1 node?' })).toBeVisible()
    await expect(page.getByTestId('node-update-item')).toHaveCount(1)
    await expect(page.getByTestId('node-update-confirm')).toHaveText('Update 1 node')
  })

  test('the Update of a cluster row on the Updates page asks the question too', async ({ page }) => {
    const state = await setup(page)
    await page.goto('/app/updates')
    await page.getByRole('button', { name: 'Update llama-cpp' }).click()
    await expect(dialog(page)).toBeVisible()
    await expect(page.getByTestId('node-update-item')).toHaveCount(2)
    expect(state.posts).toEqual([])
  })
})

test.describe('Updates: the node run', () => {
  test.setTimeout(90_000)

  test('updates the nodes one after another and shows each result when it ends', async ({ page }) => {
    const state = await setup(page)
    await page.goto('/app/updates/backend/llama-cpp')
    await page.getByTestId('update-nodes').click()
    await page.getByTestId('node-update-confirm').click()
    // The first node runs alone; the second has not been asked yet.
    await expect(itemOf(page, 'a')).toHaveAttribute('data-phase', 'upgrading')
    await expect(itemOf(page, 'b')).toHaveAttribute('data-phase', 'queued')
    expect(state.posts.map(p => p.id)).toEqual(['a'])
    await expect(page.getByTestId('node-update-progress')).toContainText('Updating 1 of 2')
    // Escape does not close a running update.
    await page.keyboard.press('Escape')
    await expect(dialog(page)).toBeVisible()
    await expect(itemOf(page, 'a')).toHaveAttribute('data-phase', 'done', { timeout: 30_000 })
    await expect(itemOf(page, 'b')).toHaveAttribute('data-phase', /upgrading|queued/)
    await expect(itemOf(page, 'b')).toHaveAttribute('data-phase', 'done', { timeout: 30_000 })
    expect(state.posts.map(p => p.id)).toEqual(['a', 'b'])
    // The second request came after the first node ended.
    expect(state.posts[1].at).toBeGreaterThanOrEqual(state.ends[0].at)
    await expect(page.getByTestId('node-update-summary')).toHaveText('2 updated, 0 failed, 0 skipped')
    await expect(page.getByTestId('node-update-close')).toBeFocused()
    expect(state.api.checks).toBeGreaterThanOrEqual(2)
    await page.getByTestId('node-update-close').click()
    await expect(page.getByText('llama-cpp has no node to update')).toBeVisible()
  })

  test('one node that fails does not stop the others, and Retry runs only that node', async ({ page }) => {
    const state = await setup(page, { fail: ['a'] })
    await page.goto('/app/updates/backend/llama-cpp')
    await page.getByTestId('update-nodes').click()
    await page.getByTestId('node-update-confirm').click()
    await expect(itemOf(page, 'a')).toHaveAttribute('data-phase', 'failed', { timeout: 30_000 })
    await expect(itemOf(page, 'a').getByTestId('node-update-error')).toContainText('download interrupted')
    await expect(itemOf(page, 'b')).toHaveAttribute('data-phase', 'done', { timeout: 30_000 })
    await expect(page.getByTestId('node-update-summary')).toHaveText('1 updated, 1 failed, 0 skipped')
    expect(state.posts.map(p => p.id)).toEqual(['a', 'b'])

    // The node is fixed; Retry on that node runs only it.
    state.fail.clear()
    state.api.operations = []
    await itemOf(page, 'a').getByTestId('node-update-retry').click()
    await expect(itemOf(page, 'a')).toHaveAttribute('data-phase', 'done', { timeout: 30_000 })
    expect(state.posts.map(p => p.id)).toEqual(['a', 'b', 'a'])
    await expect(page.getByTestId('node-update-summary')).toHaveText('1 updated, 0 failed, 0 skipped')
  })

  test('a node that ends without error but still shows the old version is a failure', async ({ page }) => {
    const state = await setup(page)
    await page.goto('/app/updates/backend/llama-cpp')
    // The stub ends node a without moving it.
    await page.route('**/api/nodes/a/backends/upgrade', route => route.fulfill({ status: 202, json: { jobID: 'node-a-stuck' } }))
    await page.getByRole('button', { name: 'Update gpu-a' }).click()
    await page.getByTestId('node-update-confirm').click()
    await expect(itemOf(page, 'a')).toHaveAttribute('data-phase', 'failed', { timeout: 60_000 })
    await expect(itemOf(page, 'a').getByTestId('node-update-error')).toContainText('the version did not change')
    expect(state.api.checks).toBeGreaterThan(0)
  })

  test('Stop keeps the nodes that have not started from starting', async ({ page }) => {
    const state = await setup(page)
    await page.goto('/app/updates/backend/llama-cpp')
    await page.getByTestId('update-nodes').click()
    await page.getByTestId('node-update-confirm').click()
    await expect(page.getByTestId('node-update-stop')).toBeFocused()
    await page.getByTestId('node-update-stop').click()
    await expect(itemOf(page, 'a')).toHaveAttribute('data-phase', 'done', { timeout: 30_000 })
    await expect(itemOf(page, 'b')).toHaveAttribute('data-phase', 'skipped')
    await expect(page.getByTestId('node-update-summary')).toHaveText('1 updated, 0 failed, 1 skipped')
    expect(state.posts.map(p => p.id)).toEqual(['a'])
  })
})

test.describe('Updates: nodes, access and layout', () => {
  test('a person who is not an administrator is sent Home and no update request is made', async ({ page }) => {
    await page.route('**/api/auth/status', route => route.fulfill({ json: statusFixture('member', { user: { ...MEMBER, provider: 'local', permissions: { chat: true } } }) }))
    const seen = []
    page.on('request', req => { if (/\/api\/(backends\/updates|addons\/host-grants)/.test(req.url())) seen.push(req.url()) })
    await page.goto('/app/updates/backend/llama-cpp')
    await expect(page).toHaveURL(/\/app$/)
    expect(seen).toEqual([])
  })

  test('narrow: the page and the dialog fit the screen and the nodes become cards', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await setup(page)
    await page.goto('/app/updates/backend/llama-cpp')
    await expect(page.getByTestId('node-row').first()).toBeVisible()
    const overflow = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(await overflow()).toBeLessThanOrEqual(0)
    // The table reflows into labelled cards.
    await expect(page.getByTestId('node-row').nth(1).locator('td').nth(1)).toHaveAttribute('data-label', 'Installed')
    await page.getByTestId('update-nodes').click()
    const box = await dialog(page).boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    expect(await overflow()).toBeLessThanOrEqual(0)
    const confirm = await page.getByTestId('node-update-confirm').boundingBox()
    expect(confirm.height).toBeGreaterThanOrEqual(32)
    expect(confirm.x + confirm.width).toBeLessThanOrEqual(390)
  })
})

test.describe('Updates: every path to a cluster backend asks first', () => {
  const VLLM_NODES = [NODE('a', 'gpu-a', '0.9.4', ['m1'])]
  const vllm = galleryItem('vllm', '0.9.4', '0.9.7', { nodes: VLLM_NODES, driftNodes: 0 })

  test('Update ready with a cluster row opens one dialog first, and cancel runs nothing', async ({ page }) => {
    const state = await setup(page)
    const direct = []
    await page.route('**/api/backends/upgrade/*', (route) => { direct.push(route.request().url()); return route.fulfill({ json: { jobID: 'x' } }) })
    await page.goto('/app/updates')
    const button = page.getByTestId('updates-update-ready')
    await expect(button).toContainText('Update 1 ready')
    await button.click()
    await expect(dialog(page).getByRole('heading', { name: 'Update llama-cpp on 2 nodes?' })).toBeVisible()
    await expect(page.getByTestId('node-update-item')).toHaveCount(2)
    await expect(page.getByTestId('node-update-cancel')).toBeFocused()
    await page.getByTestId('node-update-cancel').click()
    await expect(dialog(page)).toHaveCount(0)
    await expect(page.getByTestId('updates-bulk')).toHaveCount(0)
    expect(state.posts).toEqual([])
    expect(direct).toEqual([])
    // Confirming runs the nodes one after another and never the plain update.
    await button.click()
    await page.getByTestId('node-update-confirm').click()
    await expect(page.getByTestId('node-update-summary')).toHaveText('2 updated, 0 failed, 0 skipped', { timeout: 60_000 })
    expect(state.posts.map(p => p.id)).toEqual(['a', 'b'])
    expect(direct).toEqual([])
  })

  test('a mixed selection asks about the nodes, and starts the other rows only when it is confirmed', async ({ page }) => {
    test.setTimeout(120_000)
    const state = await setup(page, {
      moreItems: [galleryItem('whisper', '1.7.2', '1.8.0')],
      addons: [addonRecord('notes', 'Notes sync', updateAvailable('0.8.2'), { version: '0.8.1' })],
    })
    const addonPosts = []
    await page.route(/\/api\/addons\/notes\/upgrade$/, (route) => { addonPosts.push(1); return route.fulfill({ status: 500, json: {} }) })
    await page.goto('/app/updates')
    const button = page.getByTestId('updates-update-ready')
    await expect(button).toContainText('Update 3 ready')
    await button.click()
    // The dialog lists the nodes only: the single-machine row and the add-on have no node to stop.
    await expect(dialog(page).getByRole('heading', { name: 'Update llama-cpp on 2 nodes?' })).toBeVisible()
    await expect(page.getByTestId('node-update-item')).toHaveCount(2)
    expect(state.api.upgrades).toEqual([])
    expect(addonPosts).toEqual([])
    await page.keyboard.press('Escape')
    await expect(dialog(page)).toHaveCount(0)
    expect(state.api.upgrades).toEqual([])
    expect(addonPosts).toEqual([])
    expect(state.posts).toEqual([])
    await button.click()
    await page.getByTestId('node-update-confirm').click()
    await expect.poll(() => state.api.upgrades, { timeout: 30_000 }).toEqual(['whisper'])
    await expect.poll(() => addonPosts.length, { timeout: 60_000 }).toBeGreaterThan(0)
    await expect(page.getByTestId('node-update-summary')).toHaveText('2 updated, 0 failed, 0 skipped', { timeout: 60_000 })
    expect(state.posts.map(p => p.id)).toEqual(['a', 'b'])
  })

  test('rows without a cluster start at once, with no dialog', async ({ page }) => {
    const state = await setup(page, { nodes: { c: NODE('c', 'cpu-c', '0.9.7', []), d: NODE('d', 'cpu-d', '0.9.7', []) }, moreItems: [galleryItem('whisper', '1.7.2', '1.8.0')] })
    await page.goto('/app/updates')
    await page.getByTestId('updates-update-ready').click()
    await expect.poll(() => state.api.upgrades).toEqual(['whisper'])
    await expect(dialog(page)).toHaveCount(0)
  })

  test('two cluster backends make one combined dialog that names the backend on each line', async ({ page }) => {
    const state = await setup(page, { moreItems: [vllm] })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-update-ready')).toContainText('Update 2 ready')
    await page.getByTestId('updates-update-ready').click()
    await expect(dialog(page).getByRole('heading', { name: 'Update 2 backends on 2 nodes?' })).toBeVisible()
    await expect(page.getByTestId('node-update-item')).toHaveCount(3)
    await expect(itemOf(page, 'a').first()).toContainText('llama-cpp')
    await expect(dialog(page)).toContainText('vllm')
    await expect(dialog(page)).toContainText('Stops m1')
    expect(state.posts).toEqual([])
  })

  test('Retry on a failed cluster row asks first and never sends the plain update', async ({ page }) => {
    const state = await setup(page, { operations: [failedOp('llama-cpp')] })
    const direct = []
    await page.route('**/api/backends/upgrade/*', (route) => { direct.push(1); return route.fulfill({ json: { jobID: 'x' } }) })
    await page.goto('/app/updates')
    await page.getByRole('button', { name: 'Retry llama-cpp' }).click()
    await expect(dialog(page).getByRole('heading', { name: 'Update llama-cpp on 2 nodes?' })).toBeVisible()
    expect(direct).toEqual([])
    expect(state.posts).toEqual([])
    await page.getByTestId('node-update-cancel').click()
    expect(direct).toEqual([])
  })

  test('narrow: the sticky Update button opens the same dialog, and it fits the screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    const state = await setup(page, { moreItems: [vllm] })
    await page.goto('/app/updates')
    await page.getByTestId('updates-sticky').getByTestId('updates-update-ready').click()
    const box = await dialog(page).boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    await expect(page.getByTestId('node-update-item')).toHaveCount(3)
    expect(state.posts).toEqual([])
  })
})
