import { test, expect } from './coverage-fixtures.js'
import { installAgents, TRIGGERS } from './agents-fixtures.js'

// A watch: an event trigger of an add-on that keeps reminders for its
// items. The journey makes one from the Triggers page, then acts on its
// reminders (snooze, undo, skip, change time) on the watch's page.

const SOURCES = [{
  id: 'calendar', title: 'Calendar', events: [
    { type: 'calendar.event.created', lifecycle: 'created', label: 'An event is added' },
    { type: 'calendar.event.updated', lifecycle: 'updated', label: 'An event changes' },
  ],
}]

const WATCH = {
  id: 'w1', name: 'My calendar', prompt: 'Keep me on top of my calendar.', kind: 'event', source: 'calendar', time_zone: 'UTC',
  daily_cap: 24, enabled: true, model: 'qwen3-8b', summary: '', delivery: { target: 'inbox' },
  watch: { offset_minutes: -15, quiet_from: '22:00', quiet_to: '07:00' },
}

const soon = () => new Date(Date.now() + 40 * 60 * 1000).toISOString()
const later = () => new Date(Date.now() + 26 * 3600 * 1000).toISOString()

async function installWatch(page, { sources = SOURCES, reminders } = {}) {
  const s = await installAgents(page, { triggers: [...structuredClone(TRIGGERS), structuredClone(WATCH)] })
  const state = {
    patches: [],
    reminders: reminders || [
      { id: 'r1', title: 'Standup', task: 'remind me', due: soon(), item_time: soon(), offset_minutes: -15, snoozable: true, link: '/app/addons/calendar/calendar?event=1' },
      { id: 'r2', title: 'Call with Lea', task: 'prepare a brief from my mail', due: later(), offset_minutes: -15, lead_minutes: 30 },
    ],
  }
  await page.route(/\/api\/chats\/triggers\/sources(\?.*)?$/, route => route.fulfill({ json: { sources } }))
  await page.route(/\/api\/chats\/triggers\/[^/?]+\/reminders(\/[^/?]+)?$/, route => {
    const req = route.request()
    // The last segment is the reminder: read it once, not inside find, which
    // calls its callback for each reminder.
    const rid = new URL(req.url()).pathname.split('/').pop()
    if (req.method() === 'PATCH') {
      const body = req.postDataJSON()
      state.patches.push(body)
      const r = state.reminders.find(x => x.id === rid)
      if (body.snooze_minutes) { r.snoozed_to = new Date(Date.now() + body.snooze_minutes * 60000).toISOString(); r.due = r.snoozed_to }
      if (body.snoozed_to === '') { r.snoozed_to = null; r.due = soon() }
      if (body.skip !== undefined) r.skipped = body.skip
      if (body.at) r.due = new Date(body.at.replace(' ', 'T') + ':00Z').toISOString()
      return route.fulfill({ json: r })
    }
    return route.fulfill({ json: { reminders: state.reminders } })
  })
  return { s, state }
}

test.describe('Watches', () => {
  test('a new watch is made from the Triggers page with the rule of its source', async ({ page }) => {
    const { s } = await installWatch(page)
    await page.goto('/app/triggers')
    await page.getByTestId('new-watch').click()
    await expect(page).toHaveURL(/\/app\/triggers\/new\?watch=1$/)
    await expect(page.getByRole('heading', { name: 'New watch', level: 1 })).toBeVisible()
    await page.getByLabel('Name', { exact: true }).fill('My calendar')
    await page.getByLabel('What to do', { exact: true }).fill('Keep me on top of my calendar.')
    await page.getByLabel('Watch', { exact: true }).selectOption('calendar')
    await page.getByLabel('An event is added').check()
    await page.getByLabel('Remind me').selectOption('-30')
    await page.getByLabel('Task defaults').fill('For calls with other people, prepare a brief 30 min ahead.')
    await page.getByRole('button', { name: 'Save watch', exact: true }).first().click()
    await expect(page).toHaveURL(/\/app\/triggers\/t\d+$/)
    const post = s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/triggers')
    expect(post.body.kind).toBe('event')
    expect(post.body.source).toBe('calendar')
    expect(post.body.watch).toEqual({ offset_minutes: -30, types: ['calendar.event.created'], tasks: 'For calls with other people, prepare a brief 30 min ahead.' })
  })

  test('says how to get a source when no add-on reports changes', async ({ page }) => {
    await installWatch(page, { sources: [] })
    await page.goto('/app/triggers/new?watch=1')
    await expect(page.getByTestId('watch-empty')).toContainText('Install the Calendar add-on')
  })

  test('the watch page lists the reminders, and each action can be undone', async ({ page }) => {
    const { state } = await installWatch(page)
    await page.goto('/app/triggers/w1')
    await expect(page.getByTestId('watch-state')).toHaveText('On')
    await expect(page.getByTestId('watch-permissions')).toHaveText('Edit permissions')
    const rows = page.getByTestId('watch-reminder')
    await expect(rows).toHaveCount(2)
    await expect(rows.nth(0)).toContainText('Standup')
    await expect(rows.nth(0)).toContainText('15 min before')
    await expect(rows.nth(1).getByRole('button', { name: 'Snooze' })).toHaveCount(0, { timeout: 1000 })

    await rows.nth(0).getByRole('button', { name: 'Snooze' }).click()
    await expect(page.getByTestId('watch-done')).toContainText('Snoozed to')
    expect(state.patches.at(-1)).toEqual({ snooze_minutes: 5 })
    await page.getByTestId('watch-done').getByRole('button', { name: 'Undo' }).click()
    await expect(page.getByTestId('watch-done')).toContainText('Undone.')
    expect(state.patches.at(-1)).toEqual({ snoozed_to: '' })

    await rows.nth(1).getByRole('button', { name: 'Skip' }).click()
    await expect(page.getByTestId('watch-done')).toContainText('Skipped')
    expect(state.patches.at(-1)).toEqual({ skip: true })
    await expect(rows.nth(1)).toContainText('Skipped')

    await rows.nth(0).getByRole('button', { name: 'Change time…' }).click()
    await rows.nth(0).getByLabel('New time').fill('2030-01-02T09:15')
    await rows.nth(0).getByRole('button', { name: 'Save' }).click()
    await expect(page.getByTestId('watch-done')).toContainText('Moved to')
    expect(state.patches.at(-1)).toEqual({ at: '2030-01-02 09:15' })
  })

  test('the watch page fits a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await installWatch(page)
    await page.goto('/app/triggers/w1')
    await expect(page.getByTestId('watch-reminder')).toHaveCount(2)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })
})
