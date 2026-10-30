import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The chats page with the API stubbed, so the moments that are too quick to
// catch on a real server can be held still: messages waiting while a turn runs,
// editing and cancelling them, a Stop, and what happens to the waiting messages
// afterwards.

const composer = (page) => page.getByTestId('chat-input')

function newChatsApi(page, initial) {
  const state = {
    id: 'c1', title: 'Quotes', model: 'qwen3-8b', running: true, last_turn: '',
    pending: [{ id: 'p1', text: 'use euros', seq: 1 }, { id: 'p2', text: 'add delivery time', seq: 2 }],
    calls: [], ...initial,
  }
  const detail = () => ({
    id: state.id, title: state.title, model: state.model, kind: 'chat',
    created: '2026-10-25T10:00:00Z', updated: state.updated || '2026-10-25T10:00:00Z',
    running: state.running, last_turn: state.last_turn, pending: state.pending,
  })
  const record = (m, url, body) => state.calls.push({ method: m, url: new URL(url).pathname, body })

  return {
    state,
    async install() {
      await page.route('**/api/chats/c1/events', route => route.abort())
      await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, async route => {
        const req = route.request()
        if (req.method() === 'POST') {
          if ((req.headers()['content-type'] || '').startsWith('multipart/')) {
            record('POST', req.url(), { multipart: true, raw: req.postData() || '' })
            return state.failSend
              ? route.fulfill({ status: 500, json: { error: 'boom' } })
              : route.fulfill({ json: { started: true } })
          }
          record('POST', req.url(), req.postDataJSON())
          return (state.failSend ? route.fulfill({ status: 500, json: { error: 'boom' } }) : route.fulfill({ json: { started: false, pending_id: 'p9' } }))
        }
        return route.fulfill({ json: { messages: [{ seq: 1, turn: 1, role: 'user', text: 'compare the quotes', at: '2026-10-25T10:00:00Z' }] } })
      })
      await page.route('**/api/chats/c1/pending/*', async route => {
        const req = route.request()
        const pid = new URL(req.url()).pathname.split('/').pop()
        record(req.method(), req.url(), req.postDataJSON?.())
        if (req.method() === 'PATCH') {
          const text = req.postDataJSON().text
          state.pending = state.pending.map(p => (p.id === pid ? { ...p, text } : p))
        } else {
          state.pending = state.pending.filter(p => p.id !== pid)
        }
        return route.fulfill({ json: { ok: true } })
      })
      await page.route('**/api/chats/c1/stop', route => {
        record('POST', route.request().url())
        state.running = false
        state.last_turn = 'stopped'
        return route.fulfill({ json: { stopped: true } })
      })
      await page.route('**/api/chats/c1/send-now', route => {
        record('POST', route.request().url())
        state.running = true
        state.last_turn = ''
        state.pending = []
        return route.fulfill({ json: { sent: true } })
      })
      await page.route('**/api/chats/c1', route => route.fulfill({ json: detail() }))
      await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
        json: { conversations: [{ ...detail(), pending: undefined }], persistent: state.persistent !== false, files: state.files !== false, files_reason: state.filesReason || '' },
      }))
    },
  }
}

test.describe('Chats page with a stubbed API', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('lists the waiting messages with their count while a turn runs', async ({ page }) => {
    const api = newChatsApi(page)
    await api.install()
    await page.goto('/app/chats/c1')

    await expect(page.getByTestId('pending-list')).toContainText('2 messages waiting')
    await expect(page.getByTestId('pending-row')).toHaveText([/use euros/, /add delivery time/])
    await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Send now' })).toBeHidden() // only after a stop
  })

  test('edits a waiting message', async ({ page }) => {
    const api = newChatsApi(page)
    await api.install()
    await page.goto('/app/chats/c1')

    await page.getByTestId('pending-row').nth(1).getByRole('button', { name: 'Edit' }).click()
    await page.getByRole('textbox', { name: 'Edit' }).fill('add delivery time in days')
    await page.getByRole('button', { name: 'Save' }).click()

    await expect(page.getByTestId('pending-row').nth(1)).toContainText('add delivery time in days')
    expect(api.state.calls.find(c => c.method === 'PATCH')).toMatchObject({ body: { text: 'add delivery time in days' } })
  })

  test('cancels a waiting message', async ({ page }) => {
    const api = newChatsApi(page)
    await api.install()
    await page.goto('/app/chats/c1')

    await page.getByTestId('pending-row').first().getByRole('button', { name: 'Cancel' }).click()

    await expect(page.getByTestId('pending-row')).toHaveCount(1)
    await expect(page.getByTestId('pending-list')).toContainText('1 message waiting')
    expect(api.state.calls.some(c => c.method === 'DELETE' && c.url.endsWith('/pending/p1'))).toBe(true)
  })

  test('after a Stop the waiting messages stay and wait for Send now', async ({ page }) => {
    const api = newChatsApi(page)
    await api.install()
    await page.goto('/app/chats/c1')

    await page.getByRole('button', { name: 'Stop' }).click()

    await expect(page.getByRole('button', { name: 'Stop' })).toBeHidden()
    await expect(page.getByTestId('pending-list')).toContainText('You stopped the turn')
    await expect(page.getByTestId('pending-row')).toHaveCount(2) // nothing started by itself
    expect(api.state.calls.filter(c => c.url.endsWith('/send-now'))).toHaveLength(0)

    await page.getByRole('button', { name: 'Send now' }).click()
    await expect.poll(() => api.state.calls.filter(c => c.url.endsWith('/send-now')).length).toBe(1)
    await expect(page.getByTestId('pending-list')).toBeHidden() // they are on their way to the model
  })

  test('sending while a turn runs queues the message', async ({ page }) => {
    const api = newChatsApi(page)
    await api.install()
    await page.goto('/app/chats/c1')

    await composer(page).fill('one more thing')
    await composer(page).press('Enter')

    await expect.poll(() => api.state.calls.find(c => c.method === 'POST' && c.url.endsWith('/messages'))?.body).toEqual({ text: 'one more thing' })
    await expect(composer(page)).toHaveValue('') // the draft is cleared once it is sent
  })

  test('shift+enter adds a line instead of sending', async ({ page }) => {
    const api = newChatsApi(page, { pending: [], running: false })
    await api.install()
    await page.goto('/app/chats/c1')

    await composer(page).fill('line one')
    await composer(page).press('Shift+Enter')
    await composer(page).pressSequentially('line two')

    await expect(composer(page)).toHaveValue('line one\nline two')
    expect(api.state.calls.filter(c => c.url.endsWith('/messages'))).toHaveLength(0)
  })

  test('says that history will not survive a restart when chats live in memory', async ({ page }) => {
    const api = newChatsApi(page, { persistent: false })
    await api.install()
    await page.goto('/app/chats')
    await expect(page.getByRole('note').filter({ hasText: 'restart' })).toBeVisible()
  })

  test('says nothing about restarts when chats are kept', async ({ page }) => {
    const api = newChatsApi(page)
    await api.install()
    await page.goto('/app/chats')
    await expect(page.getByTestId('chats-page')).toBeVisible()
    await expect(page.getByRole('note').filter({ hasText: 'restart' })).toHaveCount(0)
  })

  test('a message that could not be sent stays in the composer', async ({ page }) => {
    const api = newChatsApi(page, { failSend: true })
    await api.install()
    await page.goto('/app/chats/c1')
    await composer(page).fill('keep this text')
    await composer(page).press('Enter')
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(composer(page)).toHaveValue('keep this text')
  })

  // Deleting is a request: the row goes, an Undo shows, and the chat is deleted
  // on the server when the undo time ends (or the toast is dismissed).
  const deleteFromMenu = async (page) => {
    await page.getByRole('button', { name: 'More' }).click()
    await page.getByRole('menuitem', { name: 'Delete chat' }).click()
  }

  test('deleting a chat leaves its address, offers Undo, then deletes it on the server', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    let deletes = 0
    await page.route('**/api/chats/c1', route => {
      if (route.request().method() !== 'DELETE') return route.fallback()
      deletes++
      return route.fulfill({ status: 204, body: '' })
    })
    await page.goto('/app/chats/c1')
    await deleteFromMenu(page)
    await expect(page).toHaveURL(/\/app\/chats$/)
    const toast = page.getByTestId('chat-undo-toast')
    await expect(toast).toContainText('Quotes')
    await toast.getByRole('button', { name: 'Undo' }).click()
    await expect(page).toHaveURL(/\/app\/chats\/c1$/)
    expect(deletes).toBe(0)

    await deleteFromMenu(page)
    await page.getByTestId('chat-undo-toast').getByRole('button', { name: 'Dismiss' }).click()
    await expect.poll(() => deletes).toBe(1)
  })

  test('a chat the server will not delete comes back, and the reason shows', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    await page.route('**/api/chats/c1', route => (route.request().method() === 'DELETE'
      ? route.fulfill({ status: 500, json: { error: { message: 'the store is read-only' } } })
      : route.fallback()))
    await page.goto('/app/chats/c1')
    await deleteFromMenu(page)
    await page.getByTestId('chat-undo-toast').getByRole('button', { name: 'Dismiss' }).click()
    await expect(page.getByRole('alert')).toContainText('the store is read-only')
    await page.keyboard.press('Control+k')
    await expect(page.getByTestId('chats-row')).toContainText(['Quotes'])
  })

  test.describe('attachments', () => {
    const attach = (page, name = 'notes.txt', body = 'alpha') =>
      page.getByTestId('chat-file-input').setInputFiles({ name, mimeType: 'text/plain', buffer: Buffer.from(body) })

    test('an attached file is listed, can be removed, and goes with the message', async ({ page }) => {
      const api = newChatsApi(page, { running: false, pending: [] })
      await api.install()
      await page.goto('/app/chats/c1')
      await attach(page)
      await expect(page.getByTestId('attachment')).toContainText('notes.txt')
      await page.getByRole('button', { name: 'Remove notes.txt' }).click()
      await expect(page.getByTestId('attachment')).toHaveCount(0)

      await attach(page)
      await composer(page).fill('read this')
      await composer(page).press('Enter')
      await expect.poll(() => api.state.calls.filter(c => c.body?.multipart).length).toBe(1)
      const sent = api.state.calls.find(c => c.body?.multipart).body.raw
      expect(sent).toContain('filename="notes.txt"')
      expect(sent).toContain('read this')
      await expect(page.getByTestId('attachment')).toHaveCount(0)
    })

    test('a message can go with files and no text', async ({ page }) => {
      const api = newChatsApi(page, { running: false, pending: [] })
      await api.install()
      await page.goto('/app/chats/c1')
      await attach(page, 'photo.png', 'png')
      await page.getByTestId('chat-send').click()
      await expect.poll(() => api.state.calls.filter(c => c.body?.multipart).length).toBe(1)
    })

    // The server takes files during a turn: the message and its files wait.
    test('a file can be attached while a turn runs', async ({ page }) => {
      const api = newChatsApi(page, { running: true })
      await api.install()
      await page.goto('/app/chats/c1')
      await expect(page.getByRole('button', { name: 'Attach file' })).toBeEnabled()
      await attach(page)
      await expect(page.getByTestId('attachment')).toContainText('notes.txt')
    })

    test('with files off there is no attach control, and the reason is shown', async ({ page }) => {
      const api = newChatsApi(page, { running: false, pending: [], files: false, filesReason: 'There is no data path, so files are off.' })
      await api.install()
      await page.goto('/app/chats/c1')
      await expect(page.getByTestId('chat-file-input')).toHaveCount(0)
      await expect(page.getByRole('note').filter({ hasText: 'files are off' })).toBeVisible()
    })

    test('a message that could not be sent keeps its text and its files', async ({ page }) => {
      const api = newChatsApi(page, { running: false, pending: [], failSend: true })
      await api.install()
      await page.goto('/app/chats/c1')
      await attach(page)
      await composer(page).fill('keep me')
      await composer(page).press('Enter')
      await expect(page.getByRole('alert')).toBeVisible()
      await expect(composer(page)).toHaveValue('keep me')
      await expect(page.getByTestId('attachment')).toContainText('notes.txt')
    })
  })

  // The error event of a turn that failed, as the server sends it.
  const failedTurn = async (page, error) => {
    await page.route('**/api/chats/c1/events', route => route.fulfill({
      contentType: 'text/event-stream',
      body: `event: error\ndata: ${JSON.stringify({ kind: 'error', turn: 1, ...error })}\n\n`,
    }))
  }
  const failed = (over = {}) => ({ running: false, last_turn: 'failed', pending: [], updated: new Date().toISOString(), ...over })

  test('a model without its backend fails with a plain reason, a link to Backends and one banner', async ({ page }) => {
    const api = newChatsApi(page, failed())
    await api.install()
    await failedTurn(page, {
      text: 'The model "test" could not be started: its backend is not installed. Install the backend, or pick another model.',
      code: 'model_backend_missing', model: 'test', retry: 8, fatal: true,
      detail: 'failed to select tool: localai stream: request: Post "[address]": the model could not be loaded: model "test" load is in cooldown after a recent failure; retry after 8s',
    })
    await page.goto('/app/chats/c1')

    const banner = page.getByTestId('chat-error')
    await expect(banner).toBeVisible()
    await expect(page.getByTestId('chat-error-reason')).toHaveText('The model "test" could not be started: its backend is not installed.')
    await expect(page.getByTestId('chat-error-hint')).toContainText('Backends page')
    await expect(page.getByTestId('chat-error-backends')).toHaveAttribute('href', '/app/backends')
    await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()
    // The long text is in the technical detail, cleaned of addresses.
    await banner.getByText('Technical detail').click()
    await expect(page.getByTestId('chat-error-detail')).toContainText('load is in cooldown')
    await expect(page.getByTestId('chat-error-detail')).not.toContainText('studio.invalid')
    // No second line about the same failure, and no orphan under the composer.
    await expect(page.getByText('The last turn failed.')).toHaveCount(0)
    await expect(page.getByTestId('chat-foot').getByRole('status')).toHaveText('')
    await expect(page.getByRole('alert')).toHaveCount(1)
    await page.screenshot({ path: process.env.ERRCAUSE_SHOTS ? `${process.env.ERRCAUSE_SHOTS}/backend-missing.png` : 'test-results/backend-missing.png' })
  })

  test('an upstream that rejects the address names the endpoint and does not blame the backend', async ({ page }) => {
    const api = newChatsApi(page, failed())
    await api.install()
    await failedTurn(page, { text: 'The upstream does not accept this request at that address.', code: 'upstream_path', fatal: true, detail: 'HTTP 405' })
    await page.goto('/app/chats/c1')

    await expect(page.getByTestId('chat-error-reason')).toHaveText('The upstream does not accept this request at that address.')
    await expect(page.getByTestId('chat-error-hint')).toContainText('/v1/chat/completions')
    await expect(page.getByTestId('chat-error')).not.toContainText(/backend/i)
    await expect(page.getByTestId('chat-error-backends')).toHaveCount(0)
    await page.screenshot({ path: process.env.ERRCAUSE_SHOTS ? `${process.env.ERRCAUSE_SHOTS}/upstream-path.png` : 'test-results/upstream-path.png' })
  })

  test('a reload shows a recent failure as one banner without a footer line', async ({ page }) => {
    const api = newChatsApi(page, failed())
    await api.install()
    await page.goto('/app/chats/c1')

    await expect(page.getByTestId('chat-error-reason')).toContainText('stopped because of an error')
    await expect(page.getByText('The last turn failed.')).toHaveCount(0)
    await expect(page.getByTestId('chat-error').getByText('Technical detail')).toHaveCount(0)
  })

  test('a failure from long ago does not show as a banner after a reload', async ({ page }) => {
    const api = newChatsApi(page, failed({ updated: '2026-10-25T10:00:00Z' }))
    await api.install()
    await page.goto('/app/chats/c1')

    await expect(page.getByTestId('chats-page')).toBeVisible()
    await expect(page.getByTestId('chat-error')).toHaveCount(0)
    await expect(page.getByText('The last turn failed.')).toHaveCount(0)
  })
})

test.describe('Chat skills menu', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('lists what the chat can use and sends the switches that change', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    const calls = []
    await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({ json: { skills: [
      { name: 'review', description: 'Check a change', layer: 'personal', path: '/a', enabled: true, editable: true },
      { name: 'notes', description: 'Keep notes', layer: 'shared', path: '/b', enabled: false, editable: false },
      { name: 'old', description: 'Hidden', layer: 'shared', path: '/c', enabled: true, shadowed: 'personal', editable: false },
    ] } }))
    await page.route('**/api/chats/c1/skills', route => {
      calls.push(route.request().postDataJSON())
      return route.fulfill({ json: { off: route.request().postDataJSON().off } })
    })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-skills-button').click()
    await expect(page.getByRole('checkbox', { name: 'review' })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'notes' })).not.toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'old' })).toHaveCount(0)
    await page.getByRole('checkbox', { name: 'review' }).uncheck()
    await expect.poll(() => calls.at(-1)).toEqual({ off: ['notes', 'review'] })
  })
})

test.describe('Chat add-on changes', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('says what a change tool did and that it cannot be undone, with no Confirm or Cancel', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    const applied = '{"kind":"applied","tool":"addon__tables__rows-insert","applied":{"id":"o1","addonId":"tables","action":"write","count":2,"undoable":false},"note":"n"}'
    await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [
      { seq: 1, turn: 1, role: 'user', text: 'add two rows', at: '2026-10-25T10:00:00Z' },
      { seq: 2, turn: 1, role: 'tool', text: `addon__tables__rows-insert: {"table":"t"}\n${applied}`, at: '2026-10-25T10:00:01Z' },
    ] } }))
    await page.goto('/app/chats/c1')

    await expect(page.getByText('Changed tables: 2 applied.')).toBeVisible()
    await expect(page.getByText('This cannot be undone.')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Confirm' })).toHaveCount(0)
  })

  test('says why a change cannot be undone when the add-on said so', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    const applied = '{"kind":"applied","tool":"addon__tables__rows-delete","applied":{"id":"o1","addonId":"tables","action":"write","count":900,"undoable":false,"reason":"cannot be undone: the data it changed is too large to keep"},"note":"n"}'
    await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [
      { seq: 1, turn: 1, role: 'user', text: 'delete the old rows', at: '2026-10-25T10:00:00Z' },
      { seq: 2, turn: 1, role: 'tool', text: `addon__tables__rows-delete: {"table":"t"}\n${applied}`, at: '2026-10-25T10:00:01Z' },
    ] } }))
    await page.goto('/app/chats/c1')
    await expect(page.getByText('This cannot be undone: the data it changed is too large to keep.')).toBeVisible()
    await expect(page.getByTestId('undo-change')).toHaveCount(0)
  })

  // A change the add-on kept a change set for has an Undo button instead of the
  // sentence that it cannot be undone. The button calls the host's route as the
  // person's own tab: the resource in the confirmation header, one operation ID.
  const undoable = '{"kind":"applied","tool":"addon__tables__rows-insert","applied":{"id":"o1","addonId":"tables","resourceId":"r0123456789abcdef0123456789abcde","action":"write","count":2,"undoable":true,"changeId":"t0123abc","undo":"Delete the 2 rows added to People"},"note":"n"}'
  const RES = 'r0123456789abcdef0123456789abcde'
  async function withUndoableChange(page, answer) {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [
      { seq: 1, turn: 1, role: 'user', text: 'add two rows', at: '2026-10-25T10:00:00Z' },
      { seq: 2, turn: 1, role: 'tool', text: `addon__tables__rows-insert: {"table":"t"}\n${undoable}`, at: '2026-10-25T10:00:01Z' },
    ] } }))
    await page.route('**/api/addons/apps', route => route.fulfill({ json: { apps: [{ id: 'tables', generation: 3 }] } }))
    const calls = []
    await page.route(`**/api/addons/resources/${RES}/changes/t0123abc/undo`, route => {
      const req = route.request()
      calls.push({ headers: req.headers(), body: req.postDataJSON() })
      return answer(route, calls.length)
    })
    await page.goto('/app/chats/c1')
    return calls
  }

  test('offers Undo for a change that has a change set, and says Undone after it', async ({ page }) => {
    const calls = await withUndoableChange(page, route => route.fulfill({ json: { resourceId: RES, changeId: 't0123abc', operationId: 'x', revision: { schema: 2, data: 3 } } }))
    await expect(page.getByText('Changed tables: 2 applied.')).toBeVisible()
    await expect(page.getByText('This cannot be undone.')).toHaveCount(0)
    const button = page.getByTestId('undo-change')
    await expect(button).toHaveText('Undo')
    await expect(button).toHaveAttribute('title', 'Delete the 2 rows added to People')
    await button.click()
    await expect(page.getByTestId('undo-result')).toHaveText('Undone.')
    await expect(page.getByTestId('undo-change')).toHaveCount(0)
    expect(calls).toHaveLength(1)
    expect(calls[0].headers['x-localai-confirm']).toBe(RES)
    expect(calls[0].body.generation).toBe(3)
    expect(calls[0].body.operationId).toBeTruthy()
  })

  test('explains a stale refusal with what changed, and does not offer Undo again', async ({ page }) => {
    await withUndoableChange(page, route => route.fulfill({ status: 409, json: { error: { type: 'stale', code: 409, message: 'after this change the database inserted 1 row into People' } } }))
    await page.getByTestId('undo-change').click()
    await expect(page.getByTestId('undo-result')).toContainText('The data changed after this change')
    await expect(page.getByTestId('undo-result')).toContainText('inserted 1 row into People')
    await expect(page.getByTestId('undo-change')).toHaveCount(0)
  })

  test('says an old change is too old to undo', async ({ page }) => {
    await withUndoableChange(page, route => route.fulfill({ status: 410, json: { error: { type: 'expired', code: 410, message: 'The change set expired' } } }))
    await page.getByTestId('undo-change').click()
    await expect(page.getByTestId('undo-result')).toHaveText('Not undone. This change is too old to undo.')
    await expect(page.getByTestId('undo-change')).toHaveCount(0)
  })

  test('keeps the same operation ID when the outcome is unknown, so a second press is not a second undo', async ({ page }) => {
    const calls = await withUndoableChange(page, (route, n) => (n === 1
      ? route.fulfill({ status: 503, json: { error: { type: 'outcome_unknown', operationId: 'x' } } })
      : route.fulfill({ json: { resourceId: RES, changeId: 't0123abc' } })))
    await page.getByTestId('undo-change').click()
    await expect(page.getByTestId('undo-result')).toContainText('Not sure the undo went through')
    await page.getByTestId('undo-change').click()
    await expect(page.getByTestId('undo-result')).toHaveText('Undone.')
    expect(calls).toHaveLength(2)
    expect(calls[1].body.operationId).toBe(calls[0].body.operationId)
  })
})

test.describe('Chat tools menu: add-ons', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('lists the add-ons of the user and sends the switches that change', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    const calls = []
    await page.route('**/api/chats/c1/addons', route => {
      if (route.request().method() === 'PUT') {
        const off = route.request().postDataJSON().off
        calls.push(off)
        return route.fulfill({ json: { addons: [
          { id: 'office', tools: 4, off: off.includes('office') },
          { id: 'sandbox', tools: 2, off: off.includes('sandbox') },
        ] } })
      }
      return route.fulfill({ json: { addons: [
        { id: 'office', tools: 4, off: false },
        { id: 'sandbox', tools: 2, off: true },
      ] } })
    })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByRole('checkbox', { name: 'office' })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'sandbox' })).not.toBeChecked()
    await page.getByRole('checkbox', { name: 'office' }).uncheck()
    await expect.poll(() => calls.at(-1)).toEqual(['office', 'sandbox'])
  })

  test('shows no menu when the user has no add-on tools', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [] } }))
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-tools')).toHaveCount(0)
  })

  test('puts the switch back when the server refuses it', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    await page.route('**/api/chats/c1/addons', route => {
      if (route.request().method() === 'PUT') return route.fulfill({ status: 400, json: { error: { message: 'no' } } })
      return route.fulfill({ json: { addons: [{ id: 'office', tools: 4, off: false }] } })
    })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    // A click, not uncheck(): the switch goes back as soon as the server refuses.
    await page.getByRole('checkbox', { name: 'office' }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(page.getByRole('checkbox', { name: 'office' })).toBeChecked()
  })
})

test.describe('Chat tools menu: MCP', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  const PANEL = {
    servers: [{ name: 'crm', off: false, usable: true }, { name: 'manual', off: false, usable: false, reason: 'the model does not use this server unless a saved agent names it' }],
    connectors: [
      { id: 'c1', name: 'Docs', off: false, usable: true },
      { id: 'c2', name: 'Parked', off: false, usable: false, reason: 'switched off on the Connectors page' },
    ],
    off: [],
  }

  test('lists add-ons, model servers and connectors with a switch each, and marks what cannot be used', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    const calls = []
    await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [{ id: 'office', tools: 4, off: false }] } }))
    await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: { addons: [], ...PANEL } }))
    await page.route('**/api/chats/c1/mcp', route => {
      const off = route.request().postDataJSON().off
      calls.push(off)
      return route.fulfill({ json: { addons: [], servers: PANEL.servers.map(s => ({ ...s, off: off.includes(s.name) })), connectors: PANEL.connectors.map(c => ({ ...c, off: off.includes(`connector:${c.id}`) })), off } })
    })
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-tools-count')).toHaveText('3')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByRole('checkbox', { name: 'office' })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'crm' })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'Docs' })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'manual' })).toBeDisabled()
    await expect(page.getByRole('checkbox', { name: 'Parked' })).toBeDisabled()
    await expect(page.getByText('switched off on the Connectors page')).toBeVisible()
    await page.getByRole('checkbox', { name: 'Docs' }).uncheck()
    await expect.poll(() => calls.at(-1)).toEqual(['connector:c1'])
    await page.getByRole('checkbox', { name: 'crm' }).uncheck()
    await expect.poll(() => calls.at(-1)).toEqual(['connector:c1', 'crm'])
    await expect(page.getByTestId('chat-tools-count')).toHaveText('1')
  })

  test('puts an MCP switch back when the server refuses it', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [] } }))
    await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: { addons: [], ...PANEL } }))
    await page.route('**/api/chats/c1/mcp', route => route.fulfill({ status: 400, json: { error: { message: 'no' } } }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await page.getByRole('checkbox', { name: 'Docs' }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(page.getByRole('checkbox', { name: 'Docs' })).toBeChecked()
  })

  test('shows no menu when there is nothing to switch', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [] } }))
    await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: { addons: [], servers: [], connectors: [], off: [] } }))
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-tools')).toHaveCount(0)
  })
})
