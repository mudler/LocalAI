import { test, expect } from './coverage-fixtures.js'
import { makeInbox, stubInbox } from './inbox-fixtures.js'

// The items that the server writes for updates (the digest, the result of an
// automatic pass, a check that keeps failing). They carry a code and params,
// the panel words them in the language of the person, and a click opens the
// Updates page on the filter that shows what they are about. The inbox API and
// its stream are stubbed.

const ago = (min) => new Date(Date.now() - min * 60_000).toISOString()

function updateRows() {
  const inbox = makeInbox(0)
  inbox.rows = [
    {
      id: 4, kind: 'needs_attention', status: 'failed', source: 'system', code: 'updates.result',
      title: 'Backends: 1 failed', body: 'Failed: whisper (download interrupted).', link: '/app/updates?filter=needs-you', read: false, created: ago(2),
      params: { family: 'backends', failed: 1, failedItems: ['whisper (download interrupted)'] },
    },
    {
      id: 3, kind: 'run_ended', status: 'ok', source: 'system', code: 'updates.result',
      title: 'Backends: 2 updated automatically', body: 'Updated: llama-cpp, piper.', link: '/app/updates', read: true, created: ago(60),
      params: { family: 'backends', updated: 2, updatedItems: ['llama-cpp 0.9.4 to 0.9.7', 'piper'] },
    },
    {
      id: 2, kind: 'needs_attention', status: 'ok', source: 'system', code: 'updates.digest',
      title: 'STORED ENGLISH TITLE', body: 'STORED ENGLISH BODY', link: '/app/updates?filter=ready', read: false, created: ago(30),
      params: { family: 'backends', count: 3, names: ['llama-cpp', 'whisper', 'piper'] },
    },
    {
      id: 1, kind: 'needs_attention', status: 'failed', source: 'system', code: 'updates.checkFailed',
      title: 'The update check failed', body: 'Could not check: backend gallery.', link: '/app/updates', read: true, created: ago(600),
      params: { sources: ['gallery'] },
    },
  ]
  return inbox
}

const panelOf = (page) => page.getByRole('dialog', { name: 'Notifications' })
const itemOf = (page, id) => page.locator(`[data-testid="inbox-item"][data-id="${id}"]`)

async function open(page) {
  await page.goto('/app')
  await page.getByTestId('nav-inbox').click()
  await expect(panelOf(page)).toBeVisible()
}

test.describe('Inbox: update items', () => {
  test('the digest, the results and the failed check are worded from their params', async ({ page }) => {
    await stubInbox(page, updateRows())
    await open(page)
    // The digest shows the words of the UI, not the stored English text.
    const digest = itemOf(page, 2)
    await expect(digest).toContainText('3 backend updates available')
    await expect(digest).toContainText('llama-cpp, whisper, piper')
    await expect(digest).not.toContainText('STORED ENGLISH')
    await expect(itemOf(page, 4)).toContainText('Backends: 1 failed')
    await expect(itemOf(page, 4)).toContainText('Failed: whisper (download interrupted).')
    await expect(itemOf(page, 3)).toContainText('Backends: 2 updated automatically')
    await expect(itemOf(page, 3)).toContainText('Updated: llama-cpp 0.9.4 to 0.9.7, piper.')
    await expect(itemOf(page, 1)).toContainText('The update check failed')
    await expect(itemOf(page, 1)).toContainText('Could not check: backend gallery.')
    // A failed result is the red one and the unread count follows.
    await expect(itemOf(page, 4)).toHaveClass(/is-failed/)
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
    await expect(page.getByTestId('inbox-count')).toHaveClass(/nav-badge--error/)
  })

  test('an item of a code this build does not know shows the text the server stored', async ({ page }) => {
    const inbox = updateRows()
    inbox.rows[2] = { ...inbox.rows[2], code: 'future.thing' }
    await stubInbox(page, inbox)
    await open(page)
    await expect(itemOf(page, 2)).toContainText('STORED ENGLISH TITLE')
    await expect(itemOf(page, 2)).toContainText('STORED ENGLISH BODY')
  })

  test('a click on the digest opens the ready updates, marks it read and closes the panel', async ({ page }) => {
    const inbox = updateRows()
    await stubInbox(page, inbox)
    await open(page)
    await itemOf(page, 2).getByTestId('inbox-open').click()
    await expect(page).toHaveURL(/\/app\/updates\?filter=ready$/)
    await expect(panelOf(page)).toBeHidden()
    expect(inbox.reads.at(-1)).toEqual({ ids: [2] })
  })

  test('a click on a failed result opens the updates that need a person', async ({ page }) => {
    await stubInbox(page, updateRows())
    await open(page)
    await itemOf(page, 4).getByTestId('inbox-open').click()
    await expect(page).toHaveURL(/\/app\/updates\?filter=needs-you$/)
  })

  test('a digest that comes back after it was deleted is a new, unread item at the top', async ({ page }) => {
    const inbox = updateRows()
    await stubInbox(page, inbox)
    await open(page)
    await itemOf(page, 2).getByTestId('inbox-delete').click()
    await expect(itemOf(page, 2)).toHaveCount(0)
    // The server hides the dismissed digest and brings it back for a new update
    // as a new row with a higher id.
    inbox.rows.unshift({
      id: 9, kind: 'needs_attention', status: 'ok', source: 'system', code: 'updates.digest', title: 'x', body: 'x',
      link: '/app/updates?filter=ready', read: false, created: new Date().toISOString(),
      params: { family: 'backends', count: 4, names: ['llama-cpp', 'whisper', 'piper'] },
    })
    await page.evaluate(() => window.__inboxEmit('notification'))
    const first = page.getByTestId('inbox-item').first()
    await expect(first).toContainText('4 backend updates available')
    await expect(first).toContainText('llama-cpp, whisper, piper and 1 more')
    await expect(first).toHaveAttribute('data-read', 'false')
    await expect(first).toHaveClass(/is-fresh/)
    // The announcement of a coded item does not read the English title.
    await expect(page.getByTestId('inbox-live')).toHaveText('1 new notification')
  })

  test('a quiet edit of the digest changes its words in place', async ({ page }) => {
    const inbox = updateRows()
    await stubInbox(page, inbox)
    await open(page)
    inbox.rows[2] = { ...inbox.rows[2], params: { family: 'backends', count: 1, names: ['whisper'] } }
    await page.evaluate(() => window.__inboxEmit('notifications_changed'))
    await expect(itemOf(page, 2)).toContainText('1 backend update available')
    await expect(itemOf(page, 2)).toHaveAttribute('data-read', 'false')
  })

  test('in German the items are worded in German', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('localai-language', 'de'))
    await stubInbox(page, updateRows())
    await page.goto('/app')
    await page.getByTestId('nav-inbox').click()
    await expect(itemOf(page, 2)).toContainText('3 Backend-Updates verfügbar')
    await expect(itemOf(page, 4)).toContainText('Backends: 1 fehlgeschlagen')
    await expect(itemOf(page, 1)).toContainText('Die Update-Prüfung ist fehlgeschlagen')
  })

  test('on a phone the digest fits the sheet', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await stubInbox(page, updateRows())
    await page.goto('/app')
    await page.getByTestId('mobile-inbox').click()
    await expect(itemOf(page, 2)).toContainText('3 backend updates available')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })
})
