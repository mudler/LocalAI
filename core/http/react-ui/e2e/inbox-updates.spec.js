import { test, expect } from './coverage-fixtures.js'
import { makeInbox, stubInbox } from './inbox-fixtures.js'
import { mockOperate } from './operate-fixtures.js'
import { galleryItem, mockUpdates, snapshotOf } from './updates-fixtures.js'
import { MEMBER, statusFixture } from './access-fixtures.js'

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

  test('the model update items are worded from their params', async ({ page }) => {
    const inbox = makeInbox(0)
    inbox.rows = [
      {
        id: 14, kind: 'needs_attention', status: 'failed', source: 'system', code: 'models.upgradeFailed',
        title: 'STORED', body: 'the model did not load', link: '/app/updates?filter=needs-you', read: false, created: ago(1),
        params: { name: 'whisper-base', from: 'sha256:1a2b3c4d5e6f7a8b', to: 'sha256:9f8e7d6c5b4a3928', rolledBack: true, error: 'the model did not load' },
      },
      {
        id: 13, kind: 'run_ended', status: 'ok', source: 'system', code: 'models.upgradeDone',
        title: 'STORED', body: '', link: '/app/updates', read: false, created: ago(5),
        params: { name: 'qwen3-8b', from: 'sha256:1a2b3c4d5e6f7a8b', to: 'sha256:9f8e7d6c5b4a3928' },
      },
      {
        id: 12, kind: 'run_ended', status: 'ok', source: 'system', code: 'models.removedOld',
        title: 'STORED', body: '', link: '/app/updates', read: true, created: ago(20),
        params: { name: 'gemma-3', files: 2 },
      },
      {
        id: 11, kind: 'needs_attention', status: 'ok', source: 'system', code: 'models.updateAvailable',
        title: 'STORED', body: '', link: '/app/updates?filter=ready', read: false, created: ago(40),
        params: { family: 'models', count: 2, names: ['qwen3-8b', 'llama-3.2-3b'] },
      },
    ]
    await stubInbox(page, inbox)
    await open(page)
    await expect(itemOf(page, 14)).toContainText('Model whisper-base: the upgrade failed')
    await expect(itemOf(page, 14)).toContainText('The old version was put back and keeps serving. Reason: the model did not load')
    await expect(itemOf(page, 14)).toHaveClass(/is-failed/)
    await expect(itemOf(page, 13)).toContainText('Model qwen3-8b upgraded')
    await expect(itemOf(page, 13)).toContainText('From sha256:1a2b3c4d5e6f to sha256:9f8e7d6c5b4a.')
    await expect(itemOf(page, 12)).toContainText('Model gemma-3: old version removed')
    await expect(itemOf(page, 12)).toContainText('2 old files deleted.')
    await expect(itemOf(page, 11)).toContainText('2 model updates available')
    await expect(itemOf(page, 11)).toContainText('qwen3-8b, llama-3.2-3b')
    await expect(panelOf(page)).not.toContainText('STORED')
  })
})

// An update run has one live item, changed in place while it runs, and a
// summary once it ended. Both lead to the run on the Updates page.
function runRows() {
  const inbox = makeInbox(0)
  inbox.rows = [
    {
      id: 21, kind: 'system', status: 'ok', source: 'system', code: 'updates.run', title: 'STORED', body: 'STORED',
      link: '/app/updates?run=run-7', read: false, created: ago(1),
      params: { run: 'run-7', progress: 33, done: 2, total: 6, updated: 2, running: 2, queued: 2, source: 'manual' },
    },
    {
      id: 20, kind: 'needs_attention', status: 'ok', source: 'system', code: 'updates.run.end', title: 'STORED', body: 'STORED',
      link: '', read: false, created: ago(30),
      params: { run: 'run-6', total: 6, updated: 5, failed: 1, source: 'manual', first_name: 'Notes sync', first_from: 'v0.8.1', first_error: 'the new version did not start' },
    },
  ]
  return inbox
}

async function withUpdates(page) {
  await mockOperate(page)
  await mockUpdates(page, { snapshot: snapshotOf([galleryItem('kokoro', '0.3.1', '0.4.0'), galleryItem('piper', '1.2.0', '1.3.1'), galleryItem('sglang', '0.5.2', '0.5.4')]) })
}

test.describe('Inbox: update runs', () => {
  test('the live item has a bar that follows the run, and leads to the run', async ({ page }) => {
    const inbox = runRows()
    await stubInbox(page, inbox)
    await withUpdates(page)
    await open(page)
    const live = itemOf(page, 21)
    await expect(live).toContainText('Updating 6 items')
    await expect(live).toContainText('2 updated, 2 running, 2 waiting')
    await expect(live.getByTestId('inbox-run-link')).toHaveText('View progress')
    await expect(live.getByTestId('inbox-run-progress')).toHaveAttribute('aria-valuenow', '33')
    // The server changes the row in place and says so.
    inbox.rows[0] = { ...inbox.rows[0], params: { ...inbox.rows[0].params, progress: 80, done: 5, updated: 5, running: 1, queued: 0 } }
    await page.evaluate(() => window.__inboxEmit('notifications_changed'))
    await expect(live.getByTestId('inbox-run-progress')).toHaveAttribute('aria-valuenow', '80')
    await expect(live).toContainText('5 updated, 1 running')
    await live.getByTestId('inbox-open').click()
    await expect(page).toHaveURL(/\/app\/updates\?run=run-7$/)
  })

  test('the summary of a failed run keeps the case of the name and says once that the new version did not start', async ({ page }) => {
    const inbox = makeInbox(0)
    inbox.rows = [{
      id: 30, kind: 'needs_attention', status: 'failed', source: 'system', code: 'updates.run.end', title: 'STORED', body: 'STORED',
      link: '', read: false, created: ago(2),
      params: { run: 'run-8', total: 3, updated: 2, failed: 1, source: 'manual', first_name: 'e2e-bad', first_from: '1.0.0', first_key: 'start', first_error: 'the backend process exited (exit code 3)' },
    }]
    await stubInbox(page, inbox)
    await withUpdates(page)
    await open(page)
    const end = itemOf(page, 30)
    await expect(end).toContainText('e2e-bad is still on 1.0.0: the new version did not start: the backend process exited (exit code 3).')
    await expect(end).not.toContainText('E2e-bad')
  })

  test('the summary of a cancelled run says it was cancelled, as the Updates page does', async ({ page }) => {
    const inbox = makeInbox(0)
    inbox.rows = [{
      id: 31, kind: 'run_ended', status: 'ok', source: 'system', code: 'updates.run.end', title: 'STORED', body: 'STORED',
      link: '', read: false, created: ago(2),
      params: { run: 'run-9', total: 3, updated: 1, failed: 0, source: 'manual', state: 'cancelled', cancelled: 2 },
    }]
    await stubInbox(page, inbox)
    await withUpdates(page)
    await open(page)
    await expect(itemOf(page, 31)).toContainText('Update run cancelled: 1 updated')
  })

  test('the summary names the first item that needs attention and leads to its run', async ({ page }) => {
    await stubInbox(page, runRows())
    await withUpdates(page)
    await open(page)
    const end = itemOf(page, 20)
    await expect(end).toContainText('5 updated, 1 needs attention')
    await expect(end).toContainText('Notes sync is still on v0.8.1: the new version did not start.')
    await expect(end.getByTestId('inbox-run-link')).toHaveText('See the run')
    await expect(end.getByTestId('inbox-run-progress')).toHaveCount(0)
    await end.getByTestId('inbox-open').click()
    await expect(page).toHaveURL(/\/app\/updates\?run=run-6$/)
  })

  test('the footer leads an administrator to the Updates page with the number of updates', async ({ page }) => {
    await stubInbox(page, runRows())
    await withUpdates(page)
    await open(page)
    const jump = page.getByTestId('inbox-open-updates')
    await expect(jump.getByTestId('inbox-updates-count')).toHaveText('3 updates available')
    await jump.getByRole('link', { name: 'Open Updates' }).click()
    await expect(page).toHaveURL(/\/app\/updates$/)
    await expect(panelOf(page)).toBeHidden()
  })

  test('someone who is not an administrator has no Open Updates link', async ({ page }) => {
    await stubInbox(page, runRows())
    await page.route('**/api/auth/status', route => route.fulfill({ json: statusFixture('member', { user: { ...MEMBER, provider: 'local', permissions: { chat: true, chats: true } } }) }))
    await page.goto('/app')
    await page.getByTestId('nav-inbox').click()
    await expect(panelOf(page)).toBeVisible()
    await expect(page.getByTestId('inbox-open-updates')).toHaveCount(0)
  })
})
