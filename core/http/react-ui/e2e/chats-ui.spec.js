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
    // /app/chats as an address opens Home; the notice is on every chat page.
    await page.goto('/app/chats/c1')
    await expect(page.getByRole('note').filter({ hasText: 'restart' })).toBeVisible()
  })

  test('says nothing about restarts when chats are kept', async ({ page }) => {
    const api = newChatsApi(page)
    await api.install()
    await page.goto('/app/chats/c1')
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
    // No second sentence about the same failure: the line has the state word only.
    await expect(page.getByText('The last turn failed.')).toHaveCount(0)
    await expect(page.getByTestId('chat-foot').getByRole('status')).toHaveText('Failed')
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
    await expect(page.getByRole('switch', { name: 'review' })).toBeChecked()
    await expect(page.getByRole('switch', { name: 'notes' })).not.toBeChecked()
    await expect(page.getByRole('switch', { name: 'old' })).toHaveCount(0)
    await page.getByRole('switch', { name: 'review' }).click()
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
    await expect(page.getByRole('switch', { name: 'office' })).toBeChecked()
    await expect(page.getByRole('switch', { name: 'sandbox' })).not.toBeChecked()
    await page.getByRole('switch', { name: 'office' }).uncheck()
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
    await page.getByRole('switch', { name: 'office' }).click()
    await expect(page.getByRole('alert')).toHaveText('Could not save. The switch is back as it was.')
    await expect(page.getByRole('alert').locator('span')).toHaveAttribute('title', 'no')
    await expect(page.getByRole('switch', { name: 'office' })).toBeChecked()
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
    await expect(page.getByRole('switch', { name: 'office' })).toBeChecked()
    await expect(page.getByRole('switch', { name: 'crm' })).toBeChecked()
    await expect(page.getByRole('switch', { name: 'Docs' })).toBeChecked()
    await expect(page.getByRole('switch', { name: 'manual' })).toBeDisabled()
    await expect(page.getByRole('switch', { name: 'Parked' })).toBeDisabled()
    await expect(page.getByText('switched off on the Connectors page')).toBeVisible()
    await page.getByRole('switch', { name: 'Docs' }).uncheck()
    await expect.poll(() => calls.at(-1)).toEqual(['connector:c1'])
    await page.getByRole('switch', { name: 'crm' }).uncheck()
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
    await page.getByRole('switch', { name: 'Docs' }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(page.getByRole('switch', { name: 'Docs' })).toBeChecked()
  })

  test('the Web row says who serves the web tools: the built-in, an add-on, or each by name', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    let web = { group: 'web', tools: ['web_search', 'web_fetch'], off: false, provider: 'builtin', providers: { web_search: 'builtin', web_fetch: 'builtin' } }
    await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [] } }))
    await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: { builtins: [web], addons: [], servers: [], connectors: [], off: [] } }))
    const row = () => page.getByTestId('chat-tools-item').filter({ has: page.getByRole('switch', { name: 'Web' }) })
    const open = async () => {
      await page.goto('/app/chats/c1')
      await page.getByTestId('chat-tools-button').click()
    }

    // Served by LocalAI: no add-on named, the tooltip says built-in.
    await open()
    await expect(row()).toHaveAttribute('title', 'web_search, web_fetch · built-in')
    await expect(row().getByTestId('chat-tools-note')).toHaveCount(0)

    // An add-on shadows both tools: the row names it.
    web = { ...web, provider: 'searx', providers: { web_search: 'searx', web_fetch: 'searx' } }
    await open()
    await expect(row().getByTestId('chat-tools-note')).toHaveText('Served by add-on searx')
    await expect(row()).toHaveAttribute('title', 'web_search, web_fetch · served by searx')

    // One tool shadowed: the row names the provider of each.
    web = { ...web, provider: 'mixed', providers: { web_search: 'searx', web_fetch: 'builtin' } }
    await open()
    await expect(row().getByTestId('chat-tools-note')).toHaveText('web_search: searx · web_fetch: built-in')
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

test.describe('Chat tools menu: built-in groups', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  const builtins = (on) => [
    { group: 'web', tools: ['web_search', 'web_fetch'], off: false, usable: true, provider: 'builtin' },
    { group: 'saved_agents', tools: ['list_agents'], off: !on, opt_in: true, usable: true, provider: 'builtin' },
  ]

  test('switches the saved agents on with the list of opt-in groups, and leaves them out of off', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    const calls = []
    await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [] } }))
    await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: { addons: [], servers: [], connectors: [], off: [], builtins: builtins(false) } }))
    await page.route('**/api/chats/c1/builtins', route => {
      const body = route.request().postDataJSON()
      calls.push(body)
      return route.fulfill({ json: { addons: [], servers: [], connectors: [], off: [], builtins: builtins((body.on || []).includes('saved_agents')) } })
    })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByRole('switch', { name: 'Web' })).toBeChecked()
    await expect(page.getByRole('switch', { name: 'Saved agents' })).not.toBeChecked()
    await page.getByRole('switch', { name: 'Saved agents' }).check()
    await expect.poll(() => calls.at(-1)).toEqual({ off: [], on: ['saved_agents'] })
    await expect(page.getByRole('switch', { name: 'Saved agents' })).toBeChecked()
    await page.getByRole('switch', { name: 'Saved agents' }).uncheck()
    await expect.poll(() => calls.at(-1)).toEqual({ off: [], on: [] })
  })

  test('shows why the saved agents cannot be used', async ({ page }) => {
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [] } }))
    await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: { addons: [], servers: [], connectors: [], off: [], builtins: [
      { group: 'saved_agents', tools: ['list_agents'], off: true, opt_in: true, usable: false, reason: 'saved agents are not enabled for this account', provider: 'builtin' },
    ] } }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByRole('switch', { name: 'Saved agents' })).toBeDisabled()
    await expect(page.getByText('saved agents are not enabled for this account')).toBeVisible()
  })
})

// The whole menu at once: one header with the count and Select all / Disable
// all, a heading per group with its own pair, and rows the bulk actions leave
// alone (one that cannot be used, the opt-in saved agents on Select all).
test.describe('Chat tools menu: switches and bulk actions', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  async function stubTools(page) {
    const state = {
      addons: [{ id: 'office', tools: 4, off: false }, { id: 'sandbox', tools: 1, off: true }],
      builtins: [
        { group: 'web', tools: ['web_search', 'web_fetch'], off: false, usable: true, provider: 'searx', providers: { web_search: 'searx', web_fetch: 'searx' } },
        { group: 'saved_agents', tools: ['list_agents'], off: true, opt_in: true, usable: true, provider: 'builtin' },
      ],
      servers: [{ name: 'crm', off: true, usable: true }, { name: 'manual', off: false, usable: false, reason: 'the model does not use this server unless a saved agent names it' }],
      connectors: [{ id: 'c1', name: 'Docs', off: false, usable: true }, { id: 'c2', name: 'A connector with a name far too long to fit on one line of the menu', off: false, usable: true }],
      off: ['crm'], puts: [],
    }
    const tools = () => ({
      addons: [], builtins: state.builtins, off: state.off,
      servers: state.servers.map(x => ({ ...x, off: state.off.includes(x.name) })),
      connectors: state.connectors.map(c => ({ ...c, off: state.off.includes(`connector:${c.id}`) })),
    })
    const api = newChatsApi(page, { running: false, pending: [] })
    await api.install()
    await page.route('**/api/chats/c1/addons', route => {
      if (route.request().method() === 'PUT') {
        const off = route.request().postDataJSON().off
        state.puts.push({ addons: off })
        state.addons = state.addons.map(a => ({ ...a, off: off.includes(a.id) }))
      }
      return route.fulfill({ json: { addons: state.addons } })
    })
    await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: tools() }))
    await page.route('**/api/chats/c1/mcp', route => {
      state.off = route.request().postDataJSON().off
      state.puts.push({ mcp: state.off })
      return route.fulfill({ json: tools() })
    })
    await page.route('**/api/chats/c1/builtins', route => {
      const body = route.request().postDataJSON()
      state.puts.push({ builtins: body })
      state.builtins = state.builtins.map(b => ({ ...b, off: b.opt_in ? !(body.on || []).includes(b.group) : body.off.includes(b.group) }))
      return route.fulfill({ json: tools() })
    })
    return state
  }
  const open = async (page) => {
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-tools-menu')).toBeVisible()
  }
  const sw = (page, name) => page.getByRole('switch', { name, exact: true })
  const summary = (page) => page.getByTestId('chat-tools-summary')

  test('every row is a switch, and the header counts what can be used', async ({ page }) => {
    await stubTools(page)
    await open(page)
    // 8 rows; manual cannot be used, so 7 count. On: web, office, Docs, long.
    await expect(page.getByTestId('chat-tools-menu').getByRole('switch')).toHaveCount(8)
    await expect(page.getByTestId('chat-tools-menu').getByRole('checkbox')).toHaveCount(0)
    await expect(summary(page)).toHaveText('4 of 7 on')
    await expect(page.getByTestId('chat-tools-count')).toHaveText('4')
    // A row that cannot be used: disabled, described by its reason.
    const manual = sw(page, 'manual')
    await expect(manual).toBeDisabled()
    await expect(manual).toHaveAttribute('aria-checked', 'false')
    await expect(manual).toHaveAccessibleDescription('the model does not use this server unless a saved agent names it')
    // The add-on that serves the web tools is a chip next to the name.
    const web = page.getByTestId('chat-tools-item').filter({ has: sw(page, 'Web') })
    await expect(web.getByTestId('chat-tools-note')).toHaveText('Served by add-on searx')
    await expect(web.locator('.ui-chip')).toBeVisible()
    // The opt-in row says that Select all leaves it.
    await expect(sw(page, 'Saved agents')).toHaveAccessibleDescription('Off until you switch it on. Select all leaves it off.')
    // A long name is cut, with the full name in its tooltip.
    const long = page.getByTestId('chat-tools-item').filter({ has: sw(page, /far too long/) })
    await expect(long.locator('label')).toHaveAttribute('title', /far too long/)
    const name = long.locator('.swl-row__name')
    expect(await name.evaluate(el => el.scrollWidth > el.clientWidth)).toBe(true)
    // The add-on rows give their number of tools.
    await expect(page.getByTestId('chat-tools-item').filter({ has: sw(page, 'office') })).toContainText('4 tools')
    await expect(page.getByTestId('chat-tools-item').filter({ has: sw(page, 'sandbox') })).toContainText('1 tool')
  })

  test('Select all switches on what can be used, but not the saved agents; Disable all switches everything off', async ({ page }) => {
    const state = await stubTools(page)
    await open(page)
    await page.getByTestId('chat-tools-all').click()
    await expect(summary(page)).toHaveText('6 of 7 on')
    await expect(page.getByTestId('chat-tools-all')).toBeDisabled()
    await expect(sw(page, 'Saved agents')).toHaveAttribute('aria-checked', 'false')
    await expect(sw(page, 'manual')).toHaveAttribute('aria-checked', 'false')
    // One request per kind: the add-ons and the MCP servers. Web was on already.
    await expect.poll(() => state.puts).toEqual([{ addons: [] }, { mcp: [] }])

    // Saved agents on by hand, then Disable all switches it off too.
    await sw(page, 'Saved agents').click()
    await expect(summary(page)).toHaveText('7 of 7 on')
    state.puts.length = 0
    await page.getByTestId('chat-tools-none').click()
    await expect(summary(page)).toHaveText('0 of 7 on')
    await expect(page.getByTestId('chat-tools-count')).toHaveCount(0)
    await expect(page.getByTestId('chat-tools-none')).toBeDisabled()
    await expect.poll(() => state.puts.length).toBe(3)
    expect(state.puts[0]).toEqual({ builtins: { off: ['web'], on: [] } })
    expect(state.puts[1]).toEqual({ addons: ['office', 'sandbox'] })
    // The server that cannot be used is left as it was stored.
    expect(state.puts[2].mcp.sort()).toEqual(['connector:c1', 'connector:c2', 'crm'])
  })

  test('a group has its own count and actions, which change that group only', async ({ page }) => {
    const state = await stubTools(page)
    await open(page)
    const group = page.getByTestId('chat-tools-group-connectors')
    await expect(group).toContainText('Your connectors')
    await expect(group).toContainText('2 of 2')
    await group.getByRole('button', { name: 'Switch off all of Your connectors' }).click()
    await expect(group).toContainText('0 of 2')
    await expect.poll(() => state.puts).toEqual([{ mcp: ['crm', 'connector:c1', 'connector:c2'] }])
    await expect(sw(page, 'office')).toHaveAttribute('aria-checked', 'true')
    await expect(group.getByRole('button', { name: 'Switch off all of Your connectors' })).toBeDisabled()
    // A group with one switch that can be used has no actions of its own.
    await expect(page.getByTestId('chat-tools-group-servers').getByRole('button')).toHaveCount(0)
  })

  test('Space on a focused switch toggles it, and Escape closes the menu back to the chip', async ({ page }) => {
    const state = await stubTools(page)
    await open(page)
    await sw(page, 'office').focus()
    await page.keyboard.press('Space')
    await expect(sw(page, 'office')).toHaveAttribute('aria-checked', 'false')
    await expect.poll(() => state.puts.at(-1)).toEqual({ addons: ['office', 'sandbox'] })
    // A click on the name toggles the switch too.
    await page.getByTestId('chat-tools-item').filter({ has: sw(page, 'Docs') }).locator('.swl-row__name').click()
    await expect(sw(page, 'Docs')).toHaveAttribute('aria-checked', 'false')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-tools-menu')).toHaveCount(0)
    await expect(page.getByTestId('chat-tools-button')).toBeFocused()
  })

  test('on a phone the menu is a sheet on the screen and its list scrolls under a fixed header', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 640 })
    await stubTools(page)
    await open(page)
    const menu = page.getByTestId('chat-tools-menu')
    await menu.evaluate(el => Promise.all(el.getAnimations().map(a => a.finished)))
    const box = await menu.boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    expect(box.y + box.height).toBeLessThanOrEqual(640)
    const list = page.getByTestId('chat-tools-list')
    expect(await list.evaluate(el => el.scrollHeight > el.clientHeight)).toBe(true)
    const head = await summary(page).boundingBox()
    await list.evaluate(el => { el.scrollTop = el.scrollHeight })
    expect((await summary(page).boundingBox()).y).toBe(head.y)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  })
})
