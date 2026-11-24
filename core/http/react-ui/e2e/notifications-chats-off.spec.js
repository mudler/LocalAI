import { test, expect } from './coverage-fixtures.js'
import { makeInbox, stubInbox } from './inbox-fixtures.js'
import { MEMBER, statusFixture } from './access-fixtures.js'

// The notification center does not depend on the chats. With the chats off the
// server still reports notifications: true, and the bell, the digest of the
// updates and the panel work. A row that points into the chats stays in the
// list and opens nothing. The API and its stream are stubbed.

const ago = (min) => new Date(Date.now() - min * 60_000).toISOString()

function rows() {
  const inbox = makeInbox(0)
  inbox.rows = [
    {
      id: 3, kind: 'needs_attention', status: 'ok', source: 'system', code: 'updates.digest',
      title: 'STORED ENGLISH TITLE', body: 'STORED ENGLISH BODY', link: '/app/updates?filter=ready', read: false, created: ago(5),
      params: { family: 'backends', count: 2, names: ['llama-cpp', 'whisper'] },
    },
    { id: 2, kind: 'run_ended', status: 'ok', source: 'trigger', title: 'Morning digest finished', body: '', link: '/app/chats/r1', read: false, created: ago(90) },
    { id: 1, kind: 'needs_attention', status: 'ok', source: 'connector', title: 'A connector stopped', body: '', link: '', read: true, created: ago(60 * 30) },
  ]
  return inbox
}

const panelOf = (page) => page.getByRole('dialog', { name: 'Notifications' })
const itemOf = (page, id) => page.locator(`[data-testid="inbox-item"][data-id="${id}"]`)

// The chats are off on this server; the notification center runs.
const chatsOff = (page) => page.route('**/api/features', route => route.fulfill({ json: { chats: false, notifications: true } }))

async function open(page) {
  await page.goto('/app')
  await page.getByTestId('nav-inbox').click()
  await expect(panelOf(page)).toBeVisible()
}

test.describe('Notification center with the chats off', () => {
  test('an administrator has the bell, the digest in the language of the person and the count', async ({ page }) => {
    await stubInbox(page, rows())
    await chatsOff(page)
    const chatsCalls = []
    page.on('request', (req) => { if (/\/api\/chats\/(changes|notifications)/.test(req.url())) chatsCalls.push(req.url()) })
    await page.goto('/app')
    await expect(page.getByTestId('nav-inbox')).toBeVisible()
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
    await page.getByTestId('nav-inbox').click()
    await expect(panelOf(page)).toBeVisible()
    await expect(itemOf(page, 3)).toContainText('2 backend updates available')
    await expect(itemOf(page, 3)).toContainText('llama-cpp, whisper')
    await expect(itemOf(page, 3)).not.toContainText('STORED ENGLISH')
    // Nothing here asks the chats for anything: not the proposals, not the old inbox path.
    expect(chatsCalls).toEqual([])
  })

  test('a row that points into the chats says so, opens nothing, and is still marked read', async ({ page }) => {
    const inbox = rows()
    await stubInbox(page, inbox)
    await chatsOff(page)
    await open(page)
    const chatRow = itemOf(page, 2)
    await expect(chatRow.getByTestId('inbox-chats-off')).toHaveText('Chats are off')
    await expect(itemOf(page, 3).getByTestId('inbox-chats-off')).toHaveCount(0)
    // A connector row with no link falls back to the connectors page, which is also a chats page.
    await expect(itemOf(page, 1).getByTestId('inbox-chats-off')).toBeVisible()
    const before = page.url()
    await chatRow.getByTestId('inbox-open').click()
    await expect.poll(() => inbox.reads.length).toBe(1)
    expect(inbox.reads[0]).toEqual({ ids: [2] })
    expect(page.url()).toBe(before)
    await expect(panelOf(page)).toBeVisible()
  })

  test('the digest still opens the Updates page', async ({ page }) => {
    await stubInbox(page, rows())
    await chatsOff(page)
    await open(page)
    await itemOf(page, 3).getByTestId('inbox-open').click()
    await expect(page).toHaveURL(/\/app\/updates\?filter=ready/)
  })

  test('an empty inbox does not talk about triggers and connectors', async ({ page }) => {
    await stubInbox(page, makeInbox(0))
    await chatsOff(page)
    await open(page)
    await expect(page.getByTestId('inbox-empty')).toContainText('Updates and finished operations appear here.')
    await expect(page.getByTestId('inbox-empty')).not.toContainText('triggers')
  })

  test('a person who is not an administrator has it once there are items', async ({ page }) => {
    await stubInbox(page, rows())
    await chatsOff(page)
    await page.route('**/api/auth/status', route => route.fulfill({
      json: statusFixture('member', { user: { ...MEMBER, provider: 'local', permissions: { chat: true } } }),
    }))
    await page.goto('/app')
    await expect(page.getByTestId('nav-inbox')).toBeVisible()
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
  })

  test('with the chats on, the same row opens the chat', async ({ page }) => {
    await stubInbox(page, rows())
    await page.route('**/api/features', route => route.fulfill({ json: { chats: true, notifications: true } }))
    await open(page)
    await expect(itemOf(page, 2).getByTestId('inbox-chats-off')).toHaveCount(0)
    await itemOf(page, 2).getByTestId('inbox-open').click()
    await expect(page).toHaveURL(/\/app\/chats\/r1/)
  })
})

// Screenshots for the review: light and dark, wide and phone.
const SHOTS = process.env.UI_SHOTS
for (const scheme of ['light', 'dark']) {
  for (const [name, size] of [['desktop', { width: 1280, height: 800 }], ['phone', { width: 390, height: 844 }]]) {
    test(`shot: chats-off panel, ${scheme}, ${name}`, async ({ page }) => {
      test.skip(!SHOTS, 'set UI_SHOTS to a folder to write the review screenshots')
      await page.setViewportSize(size)
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' })
      await stubInbox(page, rows())
      await chatsOff(page)
      await page.goto('/app')
      await page.evaluate((s) => document.documentElement.setAttribute('data-theme', s), scheme)
      await page.locator('[data-testid="nav-inbox"]:visible, [data-testid="mobile-inbox"]:visible').click()
      await expect(panelOf(page)).toBeVisible()
      await page.screenshot({ path: `${SHOTS}/chats-off-${scheme}-${name}.png` })
    })
  }
}
