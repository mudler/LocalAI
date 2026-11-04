import { test, expect } from './coverage-fixtures.js'
import { inboxButton, makeInbox, stubInbox } from './inbox-fixtures.js'

// A screenshot of the page in both themes, for review; the files go to test-results.
async function shot(page, name, options = {}) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
    // The theme fades in; let it finish so the picture shows the final colours.
    await page.waitForTimeout(500)
    await page.screenshot({ path: `test-results/${name}-${theme}.png`, ...options })
  }
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
}


// The changes that wait for the person when they are not in the chat: a count
// on the inbox button, a row in the inbox panel that opens the Proposals page,
// and the same card there for each change. The API is stubbed.

const AT = '2026-11-03T09:00:00Z'
const later = () => new Date(Date.now() + 5 * 3600_000).toISOString()
const make = (id, extra = {}) => ({
  v: 1, id, owner: 'alice', kind: 'memory', scope: 'user', target: `Memory/${id}.md`, summary: `Note ${id}`, content: `- fact ${id}\n`,
  source: { kind: 'connector', id: 'team-bot', chat: 'x1' }, status: 'pending', created: AT, expires: later(), ...extra,
})

async function serve(page, list) {
  const s = { list, calls: [] }
  await stubInbox(page, makeInbox(0))
  await page.route('**/api/chats/files/content*', route => route.fulfill({ status: 404, json: { error: { message: 'not found' } } }))
  await page.route(/\/api\/chats\/changes(\?.*)?$/, route => route.fulfill({ json: s.list.filter(p => p.status === 'pending') }))
  await page.route(/\/api\/chats\/changes\/([a-z0-9]+)(\/[a-z]+)?$/, (route) => {
    const req = route.request()
    const parts = new URL(req.url()).pathname.split('/')
    const verb = parts.length > 5 ? parts[5] : ''
    const p = s.list.find(x => x.id === parts[4])
    s.calls.push(`${req.method()} ${parts[4]} ${verb}`.trim())
    if (!p) return route.fulfill({ status: 404, json: { error: { message: 'not found' } } })
    if (verb === 'confirm') Object.assign(p, { status: 'applied', rev: 3 })
    else if (verb === 'dismiss') p.status = 'dismissed'
    return route.fulfill({ json: p })
  })
  return s
}

test('the inbox button counts the waiting changes and the panel opens the Proposals page', async ({ page }) => {
  await serve(page, [make('a1'), make('b2')])
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('inbox-count')).toHaveText('2')
  await expect(inboxButton(page)).toHaveAccessibleName('Notifications, 2 unread')
  await inboxButton(page).click()
  const row = page.getByTestId('inbox-proposals')
  await expect(row).toContainText('2 changes wait for you')
  await shot(page, 'proposals-inbox-row')
  await row.click()
  await expect(page).toHaveURL(/\/app\/chats\/proposals$/)
  await expect(page.getByRole('heading', { level: 1, name: 'Proposals' })).toBeVisible()
  await expect(page.getByTestId('change-proposal')).toHaveCount(2)
  await expect(page.getByTestId('change-proposal').first()).toContainText('Connector team-bot')
})

test('an answered change keeps its receipt on the page and leaves the count', async ({ page }) => {
  const s = await serve(page, [make('a1'), make('b2')])
  await page.goto('/app/chats/proposals')
  const cards = page.getByTestId('change-proposal')
  await expect(cards).toHaveCount(2)
  await cards.first().getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(cards.first()).toHaveAttribute('data-state', 'applied')
  await expect(cards).toHaveCount(2)
  await cards.nth(1).getByRole('button', { name: 'Dismiss' }).click()
  await expect(cards.nth(1)).toHaveAttribute('data-state', 'dismissed')
  expect(s.calls).toEqual(expect.arrayContaining(['POST a1 confirm', 'POST b2 dismiss']))
  await shot(page, 'proposals-page')
})

test('nothing waiting: no row in the inbox, no count, and the page says so', async ({ page }) => {
  await serve(page, [])
  await page.goto('/app/chats/c1')
  await inboxButton(page).click()
  await page.getByRole('dialog', { name: 'Notifications' }).waitFor()
  await expect(page.getByTestId('inbox-proposals')).toHaveCount(0)
  await expect(page.getByTestId('inbox-count')).toHaveCount(0)
  await page.goto('/app/chats/proposals')
  await expect(page.getByTestId('proposals-empty')).toContainText('Nothing is waiting')
})

test('a server that keeps no proposals leaves the inbox as it was', async ({ page }) => {
  await stubInbox(page, makeInbox(0))
  await page.route(/\/api\/chats\/changes(\?.*)?$/, route => route.fulfill({ status: 409, json: { error: { type: 'files_off', message: 'off' } } }))
  await page.goto('/app/chats/c1')
  await inboxButton(page).click()
  await page.getByRole('dialog', { name: 'Notifications' }).waitFor()
  await expect(page.getByTestId('inbox-proposals')).toHaveCount(0)
})

test('on a phone the inbox row fits the sheet', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 })
  await serve(page, [make('a1')])
  await page.goto('/app/chats/c1')
  await inboxButton(page).click()
  await expect(page.getByTestId('inbox-proposals')).toContainText('1 change waits for you')
  await shot(page, 'proposals-inbox-row-phone')
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
})
