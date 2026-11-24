import { test, expect } from './coverage-fixtures.js'
import { mockOperate } from './operate-fixtures.js'
import { ADMIN, sessionFor, stubAddonRoutes } from './addons-fixtures.js'
import { addonRecord, mockUpdateRuns, mockUpdates, runItem, runOf, snapshotOf, updateAvailable } from './updates-fixtures.js'

// Add-on upgrades go on the shared operation queue. The Updates page starts
// them as an update run that the server owns, so the browser never posts an
// add-on upgrade itself; an upgrade the server lists (another tab, an earlier
// run) is a row that says it waits and can be cancelled by its job. The API is
// stubbed; the page, the upgrade store and the rules are the real ones.

const rowOf = (page, name) => page.locator(`[data-testid="update-row"][data-name="${name}"]`)
const IDS = ['notes', 'tables', 'mail']
const TITLES = { notes: 'Notes sync', tables: 'Tables', mail: 'Mail connector' }

const serverRow = (id, over = {}) => ({
  id: `addon:${id}`, name: id, fullName: id, jobID: `job-${id}`, isAddon: true, taskType: 'upgrade', isBackend: false, isDeletion: false,
  isQueued: true, phase: 'queued', progress: 0, cancellable: true, message: 'queued', ...over,
})

// The owner's side: a POST of an upgrade is accepted and put on the queue, which
// /api/operations then lists; the status stays "accepted" while it waits.
async function setup(page, { respondAfterMs = 0 } = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN, { addons: true }) }))
  await stubAddonRoutes(page)
  await mockOperate(page)
  const state = await mockUpdates(page, {
    snapshot: snapshotOf([]),
    addons: IDS.map(id => addonRecord(id, TITLES[id], updateAvailable('2.0.0'))),
  })
  const posts = []
  const cancels = []
  await page.route(/\/api\/addons\/[^/]+\/upgrade$/, async (route) => {
    const id = new URL(route.request().url()).pathname.split('/').at(-2)
    const body = route.request().postDataJSON() || {}
    posts.push({ id, confirm: route.request().headers()['x-localai-confirm'], body })
    state.operations = [...state.operations, serverRow(id)]
    // A slow answer: the page may be left before it arrives.
    if (respondAfterMs) await new Promise(r => setTimeout(r, respondAfterMs))
    return route.fulfill({ json: { payload: { OperationID: body.operationId, AddonID: id, Phase: 'accepted', jobId: `job-${id}` } } })
  })
  await page.route('**/api/addons/dispatch', (route) => {
    const body = route.request().postDataJSON() || {}
    if (body.action === 'operation-status') {
      return route.fulfill({ json: { payload: { OperationID: body.operationId, AddonID: body.addonId, Phase: 'accepted', jobId: `job-${body.addonId}` } } })
    }
    return route.fallback()
  })
  await page.route(/\/api\/operations\/[^/]+\/cancel$/, (route) => {
    const job = new URL(route.request().url()).pathname.split('/').at(-2)
    cancels.push(job)
    state.operations = state.operations.filter(o => o.jobID !== job)
    return route.fulfill({ json: { success: true } })
  })
  return { state, posts, cancels }
}

test.describe('Updates: add-on upgrades on the shared queue', () => {
  test('Update ready starts one run with every add-on and never posts an add-on upgrade from the browser', async ({ page }) => {
    const { posts } = await setup(page)
    const runs = await mockUpdateRuns(page, { startReply: () => runOf(IDS.map(id => runItem(`addon:${id}`, 'queued', { queued_reason: 'one_addon_at_a_time' }))) })
    await page.goto('/app/updates')
    const button = page.getByTestId('updates-update-ready')
    await expect(button).toContainText('Update 3 ready')
    await button.click()
    await page.getByTestId('update-review-start').click()
    await expect.poll(() => runs.starts.length).toBe(1)
    expect(runs.starts[0].items.map(i => i.name).sort()).toEqual(['mail', 'notes', 'tables'])
    // Leaving the page does not stop the run: it is the server's.
    await page.getByRole('link', { name: 'Back to LocalAI' }).click()
    await expect(page).not.toHaveURL(/\/app\/updates/)
    expect(posts).toEqual([])
  })

  test('a queued add-on upgrade that only the server lists shows, and Cancel calls the cancel route of its job', async ({ page }) => {
    const { state, cancels } = await setup(page)
    state.operations = [serverRow('notes')]
    await page.goto('/app/updates')
    const row = rowOf(page, 'Notes sync')
    await expect(row).toContainText('Queued, waiting for a free slot')
    await expect(row.getByRole('progressbar')).toHaveAttribute('data-queued', '')
    await expect(row.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '0')
    await row.getByRole('button', { name: /^Cancel/ }).click()
    await expect.poll(() => cancels).toEqual(['job-notes'])
  })

})
