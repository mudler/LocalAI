import { test, expect } from './coverage-fixtures.js'

// The footer of every state of a Home widget fed by a trigger (fresh, from a
// page, stale after a failed run, paused, running, first run, failed with no
// data, broken file, missing file, waiting for confirmation). The files are
// real, on the test server; the trigger list is answered by the spec, because
// the server cannot be made to fail a run or hold one running on demand.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

const upload = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
const shot = (page, name) => page.screenshot({ path: `test-results/ws-share/${name}.png`, fullPage: true })
const now = new Date()
const iso = (ms) => new Date(now.getTime() + ms).toISOString()
const H = 3600_000

const widgets = [
  { id: 'inbox', type: 'list', title: 'Inbox this morning', source: 'data/w-inbox.json', trigger: 'tr-fresh' },
  { id: 'tasks', type: 'list', title: 'Open tasks', source: 'data/w-tasks.json' },
  { id: 'week', type: 'metric', title: 'Week in review', source: 'data/w-week.json', trigger: 'tr-stale' },
  { id: 'reading', type: 'list', title: 'Reading digest', source: 'data/w-reading.json', trigger: 'tr-paused' },
  { id: 'releases', type: 'list', title: 'Release calendar', source: 'data/w-releases.json', trigger: 'tr-running' },
  { id: 'standup', type: 'list', title: 'Standup notes', source: 'data/w-standup.json', trigger: 'tr-first' },
  { id: 'empty', type: 'list', title: 'Build health', source: 'data/w-none.json', trigger: 'tr-failed' },
  { id: 'broken', type: 'list', title: 'Invoices', source: 'data/w-broken.json' },
  { id: 'later', type: 'list', title: 'Mentions', source: 'data/w-later.json' },
]
const data = {
  'data/w-inbox.json': { updatedAt: iso(-2 * H), source: { kind: 'trigger' }, items: [{ label: '3 need a reply: the printer quote, Sara, the dentist' }, { label: '9 newsletters and 2 receipts, nothing urgent' }] },
  'data/w-tasks.json': { updatedAt: iso(-H), source: { kind: 'page', page: 'tasks' }, items: [{ label: 'Send the print invoice', meta: 'Wed' }, { label: 'Review the release checklist', meta: 'Thu' }, { label: 'Call the dentist', meta: 'Fri' }] },
  'data/w-week.json': { updatedAt: iso(-4 * 24 * H), value: 12, note: 'chats this week, 4 with files' },
  'data/w-reading.json': { updatedAt: iso(-3 * 24 * H), items: [{ label: 'Local models on small GPUs', meta: '12 min' }, { label: 'Notes on writing short docs', meta: '6 min' }] },
  'data/w-releases.json': { updatedAt: iso(-H), items: [{ label: 'v3.9 code freeze', meta: 'Mon 2' }, { label: 'v3.9 release', meta: 'Thu 5' }] },
  'data/w-standup.json': { items: [] },
  'data/w-later.json': { items: [{ label: '2 mentions in the release thread' }] },
}
const trig = (id, over) => ({
  id, name: id, kind: 'schedule', schedule: 'daily 08:00', time_zone: '', enabled: true, summary: 'Every day at 08:00', daily_cap: 24,
  next_run: iso(20 * H), plan: {}, cost: { estimated_tokens: 1500, max_runs_per_day: 1, remote: false }, ...over,
})
const TRIGGERS = [
  trig('tr-fresh', { last_run: { id: 'r1', status: 'done', due: iso(-2 * H) }, last_done: iso(-2 * H), cost: { average_tokens: 1420, runs: 5, max_runs_per_day: 1 } }),
  trig('tr-stale', { schedule: 'weekly fri 08:00', summary: 'Every Friday at 08:00', last_run: { id: 'r2', status: 'failed', reason: 'the model is not loaded', due: iso(-H) } }),
  trig('tr-paused', { enabled: false, next_run: undefined, last_run: { id: 'r3', status: 'done', due: iso(-3 * 24 * H) } }),
  trig('tr-running', { schedule: '0 * * * *', summary: 'Every hour at :00', last_run: { id: 'r4', status: 'running', chat_id: 'c-run', due: iso(-60_000) } }),
  trig('tr-first', { schedule: 'weekdays 09:00', summary: 'Every weekday at 09:00' }),
  trig('tr-failed', { last_run: { id: 'r5', status: 'failed', due: iso(-H) } }),
]
const PROPOSALS = [trig('pr-later', { plan: { widget: 'later' }, source_chat_id: 'c-prop', enabled: false })]

async function mockTriggers(page) {
  const calls = []
  await page.route('**/api/chats/triggers', (r) => r.fulfill({ json: { triggers: TRIGGERS } }))
  await page.route('**/api/chats/triggers/proposals*', (r) => r.fulfill({ json: { triggers: PROPOSALS } }))
  await page.route('**/api/chats/triggers/*/{enable,disable,run}', (r) => { calls.push(new URL(r.request().url()).pathname); r.fulfill({ status: 200, json: {} }) })
  return calls
}

test.beforeAll(async ({ request }) => {
  await upload(request, '/home.json', { widgets })
  for (const [p, body] of Object.entries(data)) await upload(request, `/${p}`, body)
  await upload(request, '/data/w-broken.json', { items: [{ label: 'Paid: the studio rent' }] })
  await upload(request, '/data/w-broken.json', '{"items": "not a list"}')
  await request.delete(`/api/chats/files?path=${encodeURIComponent('/data/w-none.json')}`)
})
test.afterAll(async ({ request }) => {
  for (const p of ['/home.json', ...Object.keys(data).map(p => `/${p}`), '/data/w-broken.json']) await request.delete(`/api/chats/files?path=${encodeURIComponent(p)}`)
})

test('every footer state says what happens and offers the action that fits', async ({ page }) => {
  const calls = await mockTriggers(page)
  await page.goto('/app')
  const foot = (id) => page.getByTestId(`home-widget-${id}`).getByTestId('widget-footer')
  await expect(foot('inbox')).toHaveAttribute('data-state', 'fresh')
  await expect(foot('inbox')).toContainText('Every day at 08:00 · updated')
  await expect(foot('inbox')).toContainText('about 1,420 tokens a run')
  await expect(foot('tasks')).toHaveAttribute('data-state', 'page')
  await expect(foot('tasks')).toContainText('From the page tasks')
  await expect(foot('week')).toHaveAttribute('data-state', 'stale')
  await expect(foot('week')).toContainText(/Last update failed\. Showing \w+'s data\./)
  await expect(page.getByTestId('home-widget-week')).toHaveClass(/wk-widget--dim/)
  await expect(page.getByTestId('home-widget-week')).toContainText('Weekly')
  await expect(foot('reading')).toHaveAttribute('data-state', 'paused')
  await expect(page.getByTestId('home-widget-reading')).toContainText('Paused')
  await expect(foot('releases')).toHaveAttribute('data-state', 'running')
  await expect(foot('releases').getByTestId('widget-open-run')).toHaveAttribute('href', '/app/chats/c-run')
  await expect(page.getByTestId('home-widget-releases')).toContainText('Hourly')
  await expect(foot('standup')).toHaveAttribute('data-state', 'firstRun')
  await expect(foot('empty')).toHaveAttribute('data-state', 'failedEmpty')
  await expect(foot('empty')).toContainText('The update failed and there is no data yet.')
  await expect(foot('broken')).toHaveAttribute('data-state', 'invalid')
  await expect(foot('broken')).toContainText('data/w-broken.json has an error. Showing the last good version.')
  await expect(foot('later')).toHaveAttribute('data-state', 'waiting')
  await expect(foot('later').getByTestId('widget-open-chat')).toHaveAttribute('href', '/app/chats/c-prop')
  await expect(page.getByTestId('home-schedule-note')).toContainText('5 widgets update on a schedule.')

  await foot('inbox').getByTestId('widget-pause').click()
  await foot('reading').getByTestId('widget-resume').click()
  await foot('week').getByTestId('widget-retry').click()
  await foot('standup').getByTestId('widget-run-now').click()
  await expect.poll(() => calls.sort()).toEqual([
    '/api/chats/triggers/tr-fresh/disable', '/api/chats/triggers/tr-first/run', '/api/chats/triggers/tr-paused/enable', '/api/chats/triggers/tr-stale/run',
  ].sort())
  await expect(foot('week').getByTestId('widget-details')).toHaveAttribute('href', '/app/triggers/tr-stale')

  // Revert writes the last good version of the broken file.
  await foot('broken').getByTestId('widget-revert').click()
  await expect(foot('broken')).toHaveAttribute('data-state', 'file')
})

for (const v of [{ name: 'desktop', width: 1440, height: 900 }, { name: 'phone', width: 390, height: 844 }]) {
  for (const theme of ['dark', 'light']) {
    test(`widget states, ${v.name} ${theme}`, async ({ page, request }) => {
      await upload(request, '/data/w-broken.json', '{"items": "not a list"}')
      await mockTriggers(page)
      await page.setViewportSize({ width: v.width, height: v.height })
      await page.addInitScript((m) => localStorage.setItem('localai-theme', m), theme)
      await page.goto('/app')
      await expect(page.getByTestId('home-widget-later').getByTestId('widget-footer')).toBeVisible()
      await page.getByTestId('home-widgets').scrollIntoViewIfNeeded()
      await shot(page, `s3-widget-states-${v.name}-${theme}`)
    })
  }
}
