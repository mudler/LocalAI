import { test, expect } from './coverage-fixtures.js'
import { startChat, b64 } from './journey-fixtures.js'

// Recurring updates against the real test server and the mock model: the
// model proposes a trigger that rewrites the data file of a Home widget, the
// card says what a run reads, writes and costs, the widget waits for it, and
// once created the widget's footer runs, pauses and resumes the real trigger.
// The run states the server cannot be made to show on demand (a failed run, a
// broken file next to a good one) are drawn by ws-widgets.spec.js.

test.describe.configure({ mode: 'serial', timeout: 120_000 })

const api = (request, path) => request.get(path).then(r => r.json())
const upload = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
// Screenshots wait for the entry animations, so a sheet is drawn as it stays.
const settle = (page) => page.evaluate(() => Promise.all(document.getAnimations().filter(a => a.effect?.getTiming().iterations !== Infinity).map(a => a.finished.catch(() => {}))))
const shot = async (page, name) => { await settle(page); await page.screenshot({ path: `test-results/ws-share/${name}.png` }) }
const VIEWS = [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'phone', width: 390, height: 844 },
]

async function clean(request) {
  for (const p of ['/LocalAI/home.json', '/data/inbox.json', '/LocalAI/ui.json']) await request.delete(`/api/chats/files?path=${encodeURIComponent(p)}`)
  const all = await api(request, '/api/chats/triggers')
  for (const t of all.triggers || []) if (t.name.startsWith('Morning inbox')) await request.delete(`/api/chats/triggers/${t.id}`)
  const props = await api(request, '/api/chats/triggers/proposals')
  for (const t of props.triggers || []) await request.delete(`/api/chats/triggers/proposals/${t.id}`)
}

test.beforeAll(async ({ request }) => { await clean(request) })
test.afterAll(async ({ request }) => { await clean(request) })

let proposalId = ''

test('a chat proposes a trigger for a widget: the card lists when, as whom, what it reads and writes, its cost and how to stop it', async ({ page }) => {
  await upload(page.request, '/LocalAI/home.json', { widgets: [{ id: 'inbox', type: 'list', title: 'Inbox this morning', source: 'data/inbox.json', note: 'kept' }] })
  await upload(page.request, '/data/inbox.json', {
    title: 'Inbox', updatedAt: new Date().toISOString(), source: { kind: 'assistant' },
    items: [{ label: '3 need a reply: the printer quote, Sara, the dentist' }, { label: '9 newsletters and 2 receipts' }],
  })
  const args = {
    name: 'Morning inbox summary', prompt: 'Rewrite data/inbox.json with the mail of the last day.', when: 'daily 08:00', time_zone: 'Europe/Rome',
    reads: 'Mail from the last 24 hours', writes: 'data/inbox.json', widget: 'inbox', estimated_tokens: 1500,
  }
  const id = await startChat(page, `ADDON_TOOL_CALL:propose_trigger:${b64(args)}`)
  const card = page.getByTestId('trigger-proposal').first()
  await expect(card).toHaveAttribute('data-state', 'proposed', { timeout: 30_000 })
  const plan = card.getByTestId('trigger-plan')
  await expect(plan.locator('[data-row="when"]')).toContainText('Europe/Rome')
  await expect(plan.locator('[data-row="when"]')).toContainText('First run')
  await expect(plan.locator('[data-row="runsAs"]')).toContainText('You, with mock-model')
  await expect(plan.locator('[data-row="reads"]')).toContainText('Mail from the last 24 hours')
  await expect(plan.locator('[data-row="writes"]')).toContainText('data/inbox.json, shown in the "Inbox this morning" widget on Home')
  await expect(plan.locator('[data-row="cost"]')).toContainText('About 1,500 tokens a run on a local model. At most 1 run a day.')
  await expect(plan.locator('[data-row="stop"]')).toContainText('Pause it from the widget, or delete it in Triggers')
  await card.scrollIntoViewIfNeeded()
  await shot(page, 's3-proposal-card-desktop-dark')
  const props = await api(page.request, '/api/chats/triggers/proposals')
  proposalId = props.triggers.find(t => t.name === args.name).id
  expect(id).toBeTruthy()

  // Home: the widget says it waits for the trigger and links to the chat.
  await page.goto('/app')
  const foot = page.getByTestId('home-widget-inbox').getByTestId('widget-footer')
  await expect(foot).toHaveAttribute('data-state', 'waiting')
  await expect(foot).toContainText('Waiting for you to create its trigger in the chat.')
  await expect(foot.getByTestId('widget-open-chat')).toHaveAttribute('href', `/app/chats/${id}`)
})

test('a schedule more often than hourly from a chat is refused and nothing is stored', async ({ page }) => {
  const before = (await api(page.request, '/api/chats/triggers/proposals')).triggers.length
  await startChat(page, `ADDON_TOOL_CALL:propose_trigger:${b64({ name: 'Morning inbox fast', prompt: 'p', when: '*/15 * * * *' })}`)
  await expect.poll(async () => {
    const chats = await api(page.request, '/api/chats?limit=5')
    return chats.conversations?.[0]?.running
  }, { timeout: 60_000 }).toBe(false)
  expect((await api(page.request, '/api/chats/triggers/proposals')).triggers.length).toBe(before)
})

test('once created, the widget names its trigger and its footer runs, pauses and resumes it', async ({ page }) => {
  expect(proposalId).toBeTruthy()
  const ok = await page.request.post(`/api/chats/triggers/proposals/${proposalId}/confirm`, { data: { time_zone: 'Europe/Rome' } })
  expect(ok.ok()).toBe(true)
  const home = await api(page.request, '/api/chats/wellknown/home')
  expect(home.data.widgets[0]).toMatchObject({ id: 'inbox', trigger: proposalId, note: 'kept' })

  await page.goto('/app')
  const widget = page.getByTestId('home-widget-inbox')
  const foot = widget.getByTestId('widget-footer')
  await expect(foot).toHaveAttribute('data-state', 'firstRun')
  await expect(foot).toContainText('First run')
  await expect(widget).toContainText('Daily')
  await expect(page.getByTestId('home-schedule-note')).toContainText('1 widget updates on a schedule.')
  await expect(widget).toHaveAttribute('aria-describedby', 'wk-foot-inbox')

  await foot.getByTestId('widget-run-now').click()
  await expect.poll(async () => (await api(page.request, `/api/chats/triggers/${proposalId}`)).last_run?.status, { timeout: 60_000 }).toMatch(/done|failed/)
  await page.reload()
  await expect(foot).toHaveAttribute('data-state', /fresh|stale/, { timeout: 15_000 })
  if (await foot.getAttribute('data-state') === 'fresh') {
    await expect(foot).toContainText('updated')
    await foot.getByTestId('widget-pause').click()
  } else {
    await page.request.post(`/api/chats/triggers/${proposalId}/disable`)
    await page.reload()
  }
  await expect(foot).toHaveAttribute('data-state', 'paused')
  await expect(widget).toContainText('Paused')
  expect((await api(page.request, `/api/chats/triggers/${proposalId}`)).enabled).toBe(false)
  await foot.getByTestId('widget-resume').click()
  await expect.poll(async () => (await api(page.request, `/api/chats/triggers/${proposalId}`)).enabled).toBe(true)
  await expect(foot).not.toHaveAttribute('data-state', 'paused')
  await expect(foot.getByTestId('widget-details').or(foot.getByTestId('widget-pause'))).toBeVisible()
})

for (const v of VIEWS) {
  for (const theme of ['dark', 'light']) {
    test(`the trigger proposal card and the widget, ${v.name} ${theme}`, async ({ page }) => {
      await page.setViewportSize({ width: v.width, height: v.height })
      await page.addInitScript((m) => localStorage.setItem('localai-theme', m), theme)
      await page.goto('/app')
      await expect(page.getByTestId('home-widget-inbox').getByTestId('widget-footer')).toBeVisible()
      await page.getByTestId('home-widget-inbox').scrollIntoViewIfNeeded()
      await shot(page, `s3-home-widget-${v.name}-${theme}`)
      const args = { name: 'Morning inbox shot', prompt: 'p', when: 'daily 08:00', time_zone: 'Europe/Rome', reads: 'Mail from the last 24 hours', writes: 'data/inbox.json', widget: 'inbox', estimated_tokens: 1500 }
      await startChat(page, `ADDON_TOOL_CALL:propose_trigger:${b64(args)}`)
      const card = page.getByTestId('trigger-proposal').first()
      await expect(card.getByTestId('trigger-plan')).toBeVisible({ timeout: 30_000 })
      await card.scrollIntoViewIfNeeded()
      await shot(page, `s3-proposal-card-${v.name}-${theme}`)
    })
  }
}
