import { test, expect } from '@playwright/test'
import { ADMIN, release, sessionFor, mockAddons } from './addons-fixtures.js'

// The background install queue of the add-on library: tick several add-ons,
// review the host access of each one after another, send one queue request,
// and follow the installs as rows of /api/operations. The owner decides every
// answer, so these specs drive the page against a stand-in for the host-grants
// routes and the operation routes, and assert what the page sent.

const shots = process.env.UI_SHOTS_DIR
const MB = 1024 * 1024

const op = (name, title, text, risk, over = {}) => ({ name, title, text, risk, status: 'not_allowed', ...over })
const previewFor = (id, over = {}) => ({
  digest: `sha256:${id[0].repeat(64)}`, version: '1.0.0', revision: 7, nonce: `nonce-${id}`, official: true, fromIndex: true,
  requested: { required: ['workspace.materialize'], optional: ['egress.check'] },
  ops: [
    op('workspace.materialize', 'Copy files in', 'Copy files of a chat into the working area of the add-on.', 'changes data'),
    op('egress.check', 'Ask about the network', 'Ask the host whether a network connection is allowed.', 'network'),
  ],
  ...over,
})

const row = (id, over = {}) => ({
  id: `addon:${id}`, name: id, jobID: `job-${id}`, isAddon: true, taskType: 'installation', isBackend: false, isDeletion: false,
  isQueued: false, phase: 'downloading', progress: 0, currentBytes: 0, totalBytes: 0, cancellable: true, message: '', ...over,
})

async function open(page, { operations = [], previews = {}, queue = null, releases } = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN, { addons: true }) }))
  const addons = await mockAddons(page, {
    installed: [],
    releases: releases || [release('alpha', { name: 'Alpha' }), release('beta', { name: 'Beta' }), release('gamma', { name: 'Gamma' })],
  })
  const state = { ops: operations, log: [], cancels: [], dismissals: [], queue }
  await page.route('**/api/addons/host-grants**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/addons/host-grants', '') || '/'
    const body = req.postData() ? JSON.parse(req.postData()) : null
    const json = (payload, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(payload) })
    state.log.push({ method: req.method(), path, body, confirm: req.headers()['x-localai-confirm'] })
    if (req.method() === 'GET') return json({ revision: 1, addons: [] })
    if (path === '/preview') return json(previews[body.addonId] || previewFor(body.addonId))
    if (path === '/queue') {
      if (state.queue) return state.queue(body, state)
      state.ops = [...state.ops, ...body.items.map(item => row(item.addonId, { jobID: `job-${item.operationId}`, isQueued: true, phase: 'queued', cancellable: true }))]
      return json({ items: body.items.map(item => ({ addonId: item.addonId, operationId: item.operationId, jobId: `job-${item.operationId}` })) })
    }
    return json({ error: { type: 'addon_error', message: 'unexpected' } }, 404)
  })
  await page.route(/\/api\/operations\/history$/, route => route.fulfill({ json: { operations: [] } }))
  await page.route(/\/api\/operations\/[^/]+\/(cancel|dismiss)$/, route => {
    const parts = new URL(route.request().url()).pathname.split('/')
    const verb = parts.at(-1)
    const job = parts.at(-2)
    ;(verb === 'cancel' ? state.cancels : state.dismissals).push(job)
    state.ops = state.ops.filter(o => o.jobID !== job)
    return route.fulfill({ json: { success: true } })
  })
  await page.route(/\/api\/operations$/, route => route.fulfill({ json: { operations: state.ops } }))
  return { addons, state }
}

const card = (page, title) => page.locator('.cat-item', { hasText: title })
const queued = (state) => state.log.filter(l => l.path === '/queue')

test.describe('Add-on install queue', () => {
  test('ticks two add-ons, reviews each with its own ticks, and sends one queue request', async ({ page }) => {
    const { state } = await open(page)
    await page.goto('/app/addons?tab=browse')
    await card(page, 'Alpha').getByTestId('entry-select').check()
    await card(page, 'Beta').getByTestId('entry-select').check()
    await expect(page.getByTestId('queue-selection')).toContainText('Selected: 2')
    await page.getByTestId('queue-review').click()

    const dialog = page.getByTestId('install-dialog')
    await expect(dialog.getByTestId('install-position')).toContainText('Add-on 1 of 2')
    // The listing answered from the index: nothing says it is downloading.
    await expect(dialog.getByTestId('hostaccess-loading')).toHaveCount(0)
    await expect(dialog.getByTestId('install-host-access')).toBeVisible()
    // Alpha: allow the optional operation too.
    await dialog.locator('[data-op="egress.check"]').getByRole('switch').click()
    await dialog.getByTestId('install-confirm').click()

    await expect(dialog.getByTestId('install-position')).toContainText('Add-on 2 of 2')
    await expect(dialog.getByRole('heading', { name: /Install Beta\?|Install Beta/ }).or(dialog.getByText('Install Beta?'))).toBeVisible()
    // Beta keeps its own ticks: the optional one is off.
    await expect(dialog.locator('[data-op="egress.check"]').getByRole('switch')).toHaveAttribute('aria-checked', 'false')
    expect(queued(state)).toHaveLength(0)
    await dialog.getByTestId('install-confirm').click()

    await expect.poll(() => queued(state).length).toBe(1)
    const req = queued(state)[0]
    expect(req.confirm).toBe('queue')
    expect(req.body.items.map(i => i.addonId)).toEqual(['alpha', 'beta'])
    expect(req.body.items[0].approve).toMatchObject({ digest: previewFor('alpha').digest, revision: 7, nonce: 'nonce-alpha' })
    expect(req.body.items[0].approve.ops).toEqual(['workspace.materialize', 'egress.check'])
    expect(req.body.items[1].approve.ops).toEqual(['workspace.materialize'])
    expect(new Set(req.body.items.map(i => i.operationId)).size).toBe(2)
    expect(req.body.items[0]).not.toHaveProperty('action')
    await expect(page.getByTestId('install-dialog')).toHaveCount(0)
    // Both add-ons are in the queue now, and their cards say so.
    await expect(page.getByTestId('addon-queue').getByTestId('operation-card')).toHaveCount(2)
    await expect(card(page, 'Alpha').getByTestId('entry-queued')).toBeVisible()
  })

  test('a single add-on is queued from its own dialog and never turned on', async ({ page }) => {
    const { state, addons } = await open(page)
    await page.goto('/app/addons?tab=browse')
    await card(page, 'Gamma').getByRole('button', { name: /^Install / }).click()
    const dialog = page.getByTestId('install-dialog')
    await expect(dialog.getByTestId('install-position')).toHaveCount(0)
    await expect(dialog.getByTestId('install-confirm')).toHaveText('Add to queue')
    await dialog.getByTestId('install-confirm').click()
    await expect.poll(() => queued(state).length).toBe(1)
    expect(queued(state)[0].body.items).toHaveLength(1)
    expect(queued(state)[0].body.items[0].addonId).toBe('gamma')
    // No install and no enable went through the lifecycle routes.
    expect(addons.log.dispatch.filter(d => d.action === 'install' || d.action === 'enable')).toHaveLength(0)
  })

  test('an unpinned development entry is said so and its digest is sent back as it came', async ({ page }) => {
    const { state } = await open(page, { previews: { alpha: previewFor('alpha', { digest: 'unpinned' }) } })
    await page.goto('/app/addons?tab=browse')
    await card(page, 'Alpha').getByRole('button', { name: /^Install / }).click()
    await expect(page.getByTestId('install-unpinned')).toBeVisible()
    await page.getByTestId('install-confirm').click()
    await expect.poll(() => queued(state).length).toBe(1)
    expect(queued(state)[0].body.items[0].approve.digest).toBe('unpinned')
  })

  test('the queue rows show the phase, bytes and a bar, and Cancel calls the cancel route', async ({ page }) => {
    const { state } = await open(page, {
      operations: [
        row('alpha', { progress: 31, currentBytes: 12.3 * MB, totalBytes: 40 * MB }),
        row('beta', { isQueued: true, phase: 'queued' }),
      ],
    })
    await page.goto('/app/addons')
    const rows = page.getByTestId('addon-queue').getByTestId('operation-card')
    await expect(rows).toHaveCount(2)
    const alpha = rows.filter({ hasText: 'alpha' })
    await expect(alpha).toContainText('Downloading')
    await expect(alpha).toContainText('12.3 MB / 40 MB')
    await expect(alpha.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '31')
    // A queued row reads like a queued update: an empty bar and no progress.
    const beta = rows.filter({ hasText: 'beta' })
    await expect(beta).toContainText('Queued, waiting for a free slot')
    await expect(beta.getByRole('progressbar')).toHaveAttribute('data-queued', '')
    await expect(beta.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '0')
    await expect(beta.locator('.operation-card__fill')).toHaveCount(0)
    await alpha.getByRole('button', { name: /^Cancel/ }).click()
    await expect.poll(() => state.cancels).toEqual(['job-alpha'])
    await expect(rows).toHaveCount(1)
  })

  test('a row that is finalizing cannot be cancelled', async ({ page }) => {
    await open(page, { operations: [row('alpha', { phase: 'committing', progress: 100, cancellable: false })] })
    await page.goto('/app/addons')
    const alpha = page.getByTestId('addon-queue').getByTestId('operation-card')
    await expect(alpha).toContainText('Finalizing')
    await expect(alpha.getByRole('button', { name: /^Cancel/ })).toHaveCount(0)
  })

  test('a plain failure offers Retry and Dismiss; Dismiss calls the dismiss route', async ({ page }) => {
    const { state } = await open(page, { operations: [row('alpha', { phase: 'failed', error: 'download interrupted' })] })
    await page.goto('/app/addons')
    const alpha = page.getByTestId('addon-queue').getByTestId('operation-card')
    await expect(alpha).toContainText('download interrupted')
    await expect(alpha.getByRole('button', { name: /^Retry/ })).toBeVisible()
    await expect(alpha.getByTestId('operation-review')).toHaveCount(0)
    await alpha.getByRole('button', { name: /^Dismiss/ }).click()
    await expect.poll(() => state.dismissals).toEqual(['job-alpha'])
  })

  test('a failure that needs approval again shows Review, which takes a fresh preview and queues again', async ({ page }) => {
    const { state } = await open(page, {
      operations: [row('alpha', { phase: 'failed', error: 'the add-on needs your approval again: the package also asks for egress.check' })],
    })
    await page.goto('/app/addons')
    const alpha = page.getByTestId('addon-queue').getByTestId('operation-card')
    await expect(alpha).toContainText('Needs your review')
    await expect(alpha.getByTestId('operation-review-note')).toBeVisible()
    await expect(alpha.getByRole('button', { name: /^Retry/ })).toHaveCount(0)
    await alpha.getByTestId('operation-review').click()
    const dialog = page.getByTestId('install-dialog')
    await expect(dialog.getByTestId('install-review-again')).toBeVisible()
    await expect(dialog.getByTestId('install-host-access')).toBeVisible()
    expect(state.log.filter(l => l.path === '/preview').map(l => l.body.addonId)).toEqual(['alpha'])
    await dialog.getByTestId('install-confirm').click()
    await expect.poll(() => queued(state).length).toBe(1)
    expect(queued(state)[0].body.items[0].addonId).toBe('alpha')
    // The failed row it replaces is dismissed once the new install is queued.
    await expect.poll(() => state.dismissals).toEqual(['job-alpha'])
  })

  test('a refused item is said, and the others are still queued', async ({ page }) => {
    const { state } = await open(page)
    // The stand-in answers one job and one refusal.
    await page.unroute('**/api/addons/host-grants**')
    await page.route('**/api/addons/host-grants**', async route => {
      const req = route.request()
      const path = new URL(req.url()).pathname.replace('/api/addons/host-grants', '') || '/'
      const body = req.postData() ? JSON.parse(req.postData()) : null
      const json = (payload, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(payload) })
      if (req.method() === 'GET') return json({ revision: 1, addons: [] })
      if (path === '/preview') return json(previewFor(body.addonId))
      state.log.push({ path, body })
      state.ops = [row('alpha', { jobID: 'job-one', isQueued: true, phase: 'queued' })]
      return json({ items: [
        { addonId: 'alpha', operationId: body.items[0].operationId, jobId: 'job-one' },
        { addonId: 'beta', operationId: body.items[1].operationId, error: { type: 'approval_stale', code: 409, message: 'the package changed' } },
      ] })
    })
    await page.goto('/app/addons?tab=browse')
    await card(page, 'Alpha').getByTestId('entry-select').check()
    await card(page, 'Beta').getByTestId('entry-select').check()
    await page.getByTestId('queue-review').click()
    await page.getByTestId('install-confirm').click()
    await page.getByTestId('install-confirm').click()
    const refused = page.getByTestId('queue-refused')
    await expect(refused).toContainText('Beta was not queued')
    await expect(page.getByTestId('addon-queue').getByTestId('operation-card')).toHaveCount(1)
    await refused.getByRole('button', { name: 'Close' }).click()
    await expect(refused).toHaveCount(0)
  })

  test('the global activity list words an add-on install and reviews it on the Add-ons page', async ({ page }) => {
    await open(page, { operations: [row('alpha', { phase: 'failed', error: 'the add-on needs your approval again: more' })] })
    await page.goto('/app/activity')
    const card0 = page.getByTestId('operation-card')
    await expect(card0).toContainText('add-on')
    await expect(card0).toContainText('Needs your review')
    await expect(card0).not.toContainText('model')
    await card0.getByTestId('operation-review').click()
    await expect(page).toHaveURL(/\/app\/addons/)
    await expect(page.getByTestId('install-dialog')).toBeVisible()
  })
})

// Screenshots for a visual review, with UI_SHOTS_DIR set.
test.describe('Add-on install queue: screenshots', () => {
  test.skip(!shots, 'UI_SHOTS_DIR is not set')
  for (const theme of ['light', 'dark']) {
    test(`queue and review dialog, ${theme}`, async ({ page }) => {
      await page.addInitScript((t) => localStorage.setItem('localai.theme', t), theme)
      await page.setViewportSize({ width: 1280, height: 900 })
      await open(page, {
        operations: [
          row('alpha', { progress: 31, currentBytes: 12.3 * MB, totalBytes: 40 * MB, etaSeconds: 40 }),
          row('beta', { isQueued: true, phase: 'queued' }),
          row('gamma', { phase: 'failed', error: 'the add-on needs your approval again: the package also asks for egress.check' }),
        ],
        releases: [release('alpha', { name: 'Alpha' }), release('beta', { name: 'Beta' }), release('gamma', { name: 'Gamma' }), release('delta', { name: 'Delta' }), release('epsilon', { name: 'Epsilon' })],
      })
      await page.goto('/app/addons?tab=browse')
      await card(page, 'Delta').getByTestId('entry-select').check()
      await card(page, 'Epsilon').getByTestId('entry-select').check()
      await page.emulateMedia({ reducedMotion: 'reduce' })
      // Ticking the cards scrolled the page: show it from the top.
      await page.evaluate(() => document.querySelectorAll('*').forEach(e => { if (e.scrollTop) e.scrollTop = 0 }))
      await page.waitForTimeout(500)
      await page.screenshot({ path: `${shots}/queue-${theme}.png` })
      await page.getByTestId('queue-review').click()
      await expect(page.getByTestId('install-host-access')).toBeVisible()
      await page.waitForTimeout(400)
      await page.screenshot({ path: `${shots}/review-${theme}.png` })
      // The phone width, which the repo asks of every UI change.
      await page.getByTestId('install-dialog').getByRole('button', { name: 'Cancel' }).click()
      await page.setViewportSize({ width: 390, height: 844 })
      await page.evaluate(() => document.querySelectorAll('*').forEach(e => { if (e.scrollTop) e.scrollTop = 0 }))
      await page.waitForTimeout(400)
      await page.screenshot({ path: `${shots}/queue-${theme}-phone.png` })
      // Opening the review cleared the ticks: tick again for the phone dialog.
      await card(page, 'Delta').getByTestId('entry-select').check()
      await page.getByTestId('queue-review').click()
      await expect(page.getByTestId('install-host-access')).toBeVisible()
      await page.waitForTimeout(400)
      await page.screenshot({ path: `${shots}/review-${theme}-phone.png` })
    })
  }
})
