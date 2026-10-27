import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The triggers page, proposals, the inbox and the unread marks, with the API stubbed.

const TRIGGER = {
  id: 't1', name: 'Morning digest', prompt: 'Summarise yesterday.', kind: 'schedule', schedule: 'weekdays 09:00', time_zone: 'Europe/Rome',
  daily_cap: 24, enabled: true, model: 'qwen3-8b', summary: 'Every weekday at 09:00 (Europe/Rome)', next_run: '2026-10-28T08:00:00Z',
  last_run: { id: 'r1', status: 'failed', reason: 'the agent was deleted', due: '2026-10-27T08:00:00Z' },
}

function api(page, over = {}) {
  const s = {
    triggers: [structuredClone(TRIGGER)], proposals: [], notifications: [], unread: 0, chats: [], calls: [], ...over,
  }
  const rec = (method, url, body) => s.calls.push({ method, url: new URL(url).pathname + new URL(url).search, body })
  return {
    s,
    async install() {
      await page.route('**/api/chats/notifications/events', route => route.abort())
      await page.route('**/api/chats/push/**', route => route.fulfill({ status: 409, json: { error: { message: 'push off', type: 'push_off', code: 409 } } }))
      await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
      await page.route('**/api/chats/files*', route => route.fulfill({ json: { path: '/', entries: [{ path: '/Contracts', name: 'Contracts', kind: 'dir' }, { path: '/a.txt', name: 'a.txt', kind: 'file' }] } }))
      await page.route(/\/api\/chats\/notifications\/read$/, route => {
        const b = route.request().postDataJSON()
        rec('POST', route.request().url(), b)
        s.notifications = s.notifications.map(n => (b.all || (b.ids || []).includes(n.id) ? { ...n, read: true } : n))
        return route.fulfill({ json: { unread: s.notifications.filter(n => !n.read).length } })
      })
      await page.route(/\/api\/chats\/notifications(\?.*)?$/, route => route.fulfill({ json: { notifications: s.notifications, unread: s.notifications.filter(n => !n.read).length } }))
      await page.route('**/api/chats/triggers/preview*', route => {
        const when = new URL(route.request().url()).searchParams.get('when') || ''
        if (when.includes('25:00')) return route.fulfill({ status: 400, json: { error: { message: 'hour must be 0 to 23', type: 'invalid_request_error', code: 400 } } })
        return route.fulfill({ json: { summary: 'Every day at 08:00', times: ['2026-10-28T08:00:00Z', '2026-10-29T08:00:00Z', '2026-10-30T08:00:00Z'] } })
      })
      await page.route('**/api/chats/triggers/proposals/*/confirm', route => {
        rec('POST', route.request().url(), route.request().postDataJSON())
        s.proposals = []
        return route.fulfill({ json: { ...TRIGGER, id: 't9', name: 'Proposed digest' } })
      })
      await page.route(/\/api\/chats\/triggers\/proposals(\?.*)?$/, route => route.fulfill({ json: { triggers: s.proposals } }))
      await page.route('**/api/chats/triggers/*/runs', route => route.fulfill({ json: { runs: [
        { id: 'r1', status: 'failed', reason: 'the agent was deleted', due: '2026-10-27T08:00:00Z', started: '2026-10-27T08:00:00Z', chat_id: '' },
        { id: 'r0', status: 'done', due: '2026-10-26T08:00:00Z', started: '2026-10-26T08:00:00Z', chat_id: 'c7' },
      ] } }))
      await page.route(/\/api\/chats\/triggers\/[^/?]+\/(run|enable|disable)$/, route => {
        const url = route.request().url()
        rec('POST', url, null)
        const verb = new URL(url).pathname.split('/').pop()
        if (verb !== 'run') s.triggers = s.triggers.map(t => ({ ...t, enabled: verb === 'enable' }))
        return route.fulfill({ status: verb === 'run' ? 202 : 200, json: verb === 'run' ? { id: 'r2', status: 'queued', due: '2026-10-27T09:00:00Z' } : s.triggers[0] })
      })
      await page.route(/\/api\/chats\/triggers\/(?!proposals$|preview)[^/?]+$/, route => {
        const req = route.request()
        rec(req.method(), req.url(), req.postDataJSON?.())
        if (req.method() === 'DELETE') { s.triggers = []; return route.fulfill({ status: 204, body: '' }) }
        return route.fulfill({ json: { ...s.triggers[0], ...req.postDataJSON() } })
      })
      await page.route(/\/api\/chats\/triggers(\?.*)?$/, route => {
        const req = route.request()
        if (req.method() === 'POST') {
          const b = req.postDataJSON()
          rec('POST', req.url(), b)
          s.triggers.push({ ...TRIGGER, id: 't2', ...b, summary: 'Every day at 08:00', last_run: undefined })
          return route.fulfill({ status: 201, json: s.triggers.at(-1) })
        }
        return route.fulfill({ json: { triggers: s.triggers } })
      })
      await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: s.chats, persistent: true, files: true } }))
    },
  }
}

test.describe('Chat triggers', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('lists a trigger with when it runs, how it last ended, and its runs', async ({ page }) => {
    await api(page).install()
    await page.goto('/app/chats/triggers')

    const row = page.getByTestId('trigger-row')
    await expect(row).toContainText('Morning digest')
    await expect(row).toContainText('Every weekday at 09:00 (Europe/Rome)')
    await expect(row).toContainText('Failed')
    await expect(row).toContainText('the agent was deleted')
    await row.getByRole('button', { name: 'Runs' }).click()
    await expect(row.getByRole('list', { name: 'Runs' }).getByRole('listitem')).toHaveCount(2)
    await expect(row.getByRole('link', { name: 'Open the chat' })).toHaveAttribute('href', '/app/chats/c7')
  })

  test('creates a schedule trigger and previews its next runs', async ({ page }) => {
    const a = api(page, { triggers: [] })
    await a.install()
    await page.goto('/app/chats/triggers')

    await expect(page.getByText('No triggers yet.')).toBeVisible()
    await page.getByRole('button', { name: 'New trigger' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Weekly report')
    await page.getByLabel('When', { exact: true }).selectOption('weekdays')
    await page.getByLabel('Time', { exact: true }).fill('09:30')
    await expect(page.getByTestId('trigger-preview')).toContainText('Every day at 08:00')
    await page.getByLabel('What to do').fill('Write the weekly report.')
    await page.getByRole('button', { name: 'Save' }).click()

    await expect(page.getByTestId('trigger-row')).toContainText('Weekly report')
    expect(a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/triggers').body).toMatchObject({
      name: 'Weekly report', kind: 'schedule', schedule: 'weekdays 09:30', prompt: 'Write the weekly report.', enabled: true, model: 'qwen3-8b',
    })
  })

  test('shows why a schedule is refused, before saving', async ({ page }) => {
    await api(page, { triggers: [] }).install()
    await page.goto('/app/chats/triggers')
    await page.getByRole('button', { name: 'New trigger' }).click()
    await page.getByLabel('When', { exact: true }).selectOption('cron')
    await page.getByLabel('Cron (five fields)').fill('daily 25:00')
    await expect(page.getByRole('alert')).toContainText('hour must be 0 to 23')
  })

  test('creates a file trigger on a folder of the user', async ({ page }) => {
    const a = api(page, { triggers: [] })
    await a.install()
    await page.goto('/app/chats/triggers')
    await page.getByRole('button', { name: 'New trigger' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Contracts')
    await page.getByLabel('When', { exact: true }).selectOption('file')
    await page.getByLabel('Folder in your files').fill('/Contracts')
    await page.getByLabel('What to do').fill('Summarise the new file.')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByTestId('trigger-row')).toHaveCount(1)
    expect(a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/triggers').body).toMatchObject({ kind: 'file', folder: '/Contracts' })
  })

  test('switches a trigger off, runs it now, and deletes it', async ({ page }) => {
    const a = api(page)
    await a.install()
    await page.goto('/app/chats/triggers')

    await page.getByRole('checkbox', { name: 'Morning digest on' }).uncheck()
    await expect.poll(() => a.s.calls.some(c => c.url === '/api/chats/triggers/t1/disable')).toBe(true)
    await page.getByRole('button', { name: 'Run now' }).click()
    await expect.poll(() => a.s.calls.some(c => c.url === '/api/chats/triggers/t1/run')).toBe(true)
    await page.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByTestId('trigger-row')).toHaveCount(0)
  })

  test('a proposal waits for the owner and is confirmed with the browser zone', async ({ page }) => {
    const a = api(page, { proposals: [{ id: 'p1', name: 'Proposed digest', prompt: 'Summarise.', summary: 'Every weekday at 09:00', kind: 'schedule', enabled: false }] })
    await a.install()
    await page.goto('/app/chats/triggers')

    await expect(page.getByTestId('proposals')).toContainText('A proposal does nothing until you confirm it.')
    await page.getByRole('button', { name: 'Confirm' }).click()
    await expect(page.getByTestId('proposals')).toBeHidden()
    const call = a.s.calls.find(c => c.url.endsWith('/proposals/p1/confirm'))
    expect(call.body.time_zone).toBeTruthy()
  })
})

test.describe('Chat inbox', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  const NOTES = [
    { id: 2, kind: 'run_failed', status: 'failed', title: 'Nightly failed', body: 'took longer than 15m', link: '/app/chats/c2', read: false, created: '2026-10-27T08:10:00Z' },
    { id: 1, kind: 'run_ended', status: 'ok', title: 'Digest finished', body: 'All caught up.', link: '/app/chats/c1', read: true, created: '2026-10-27T07:00:00Z' },
  ]
  const CHATS = [
    { id: 'c1', title: 'Digest · 2026-10-27', model: 'm', kind: 'chat', origin: 'trigger', origin_id: 't1', running: false },
    { id: 'c2', title: 'Nightly · 2026-10-27', model: 'm', kind: 'chat', origin: 'trigger', origin_id: 't2', running: false },
    { id: 'c3', title: 'A normal chat', model: 'm', kind: 'chat', running: false },
  ]

  test('counts unread notifications and marks the chat of an unread one', async ({ page }) => {
    await api(page, { notifications: structuredClone(NOTES), chats: CHATS }).install()
    await page.goto('/app/chats')

    await expect(page.getByTestId('inbox-count')).toHaveText('1')
    await expect(page.getByTestId('unread-dot')).toHaveCount(1)
    await expect(page.getByRole('button', { name: /Nightly/ }).getByTestId('unread-dot')).toBeVisible()
    await expect(page.getByRole('img', { name: 'Started by a trigger' })).toHaveCount(2)
  })

  test('opens the inbox, reads an item into its chat and marks all read', async ({ page }) => {
    const a = api(page, { notifications: structuredClone(NOTES), chats: CHATS })
    await a.install()
    await page.goto('/app/chats')

    await page.getByRole('button', { name: /Inbox/ }).click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(2)
    await page.getByRole('button', { name: 'Mark all read' }).click()
    await expect(page.getByTestId('inbox-count')).toBeHidden()
    expect(a.s.calls.find(c => c.url === '/api/chats/notifications/read').body).toEqual({ all: true })
  })
})
