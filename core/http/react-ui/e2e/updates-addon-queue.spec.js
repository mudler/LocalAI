import { test, expect } from './coverage-fixtures.js'
import { mockOperate } from './operate-fixtures.js'
import { ADMIN, sessionFor, stubAddonRoutes } from './addons-fixtures.js'
import { addonRecord, mockUpdates, snapshotOf, updateAvailable } from './updates-fixtures.js'

// Add-on upgrades of the Updates page go on the shared operation queue: all of
// them are submitted at the click, whatever the page does next, and each one is
// a row that says it waits and can be cancelled by its job. The API is stubbed;
// the page, the upgrade store and the rules are the real ones.

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
  test('Update ready sends every add-on upgrade at the click, even when the page is left at once', async ({ page }) => {
    const { posts } = await setup(page, { respondAfterMs: 400 })
    await page.goto('/app/updates')
    const button = page.getByTestId('updates-update-ready')
    await expect(button).toContainText('Update 3 ready')
    await button.click()
    // Leave the page right after the click: the Updates page unmounts.
    await page.getByRole('link', { name: 'Home' }).first().click()
    await expect(page).not.toHaveURL(/\/app\/updates/)
    await expect.poll(() => posts.map(p => p.id).sort(), { timeout: 10_000 }).toEqual(['mail', 'notes', 'tables'])
    // Each has its own operation identity, named in the confirm header.
    expect(new Set(posts.map(p => p.body.operationId)).size).toBe(3)
    for (const p of posts) expect(p.confirm).toBe(p.body.operationId)
  })

  test('coming back shows each upgrade once, queued, with an empty bar', async ({ page }) => {
    await setup(page)
    await page.goto('/app/updates')
    await page.getByTestId('updates-update-ready').click()
    await page.getByRole('link', { name: 'Home' }).first().click()
    await page.goto('/app/updates')
    for (const id of IDS) {
      const row = rowOf(page, TITLES[id])
      await expect(row).toHaveCount(1)
      await expect(row).toContainText('Queued, waiting for a free slot')
      await expect(row.getByRole('progressbar')).toHaveAttribute('data-queued', '')
    }
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

  test('Cancel on a row started here cancels the job on the server, not only the page', async ({ page }) => {
    const { cancels } = await setup(page)
    await page.goto('/app/updates')
    await rowOf(page, 'Notes sync').getByRole('button', { name: /^Update/ }).click()
    // The confirmation of one add-on upgrade.
    await page.getByRole('alertdialog').getByRole('button', { name: 'Update', exact: true }).click()
    const row = rowOf(page, 'Notes sync')
    await expect(row).toContainText('Queued, waiting for a free slot')
    await row.getByRole('button', { name: /^Cancel/ }).click()
    await expect.poll(() => cancels).toEqual(['job-notes'])
  })
})
