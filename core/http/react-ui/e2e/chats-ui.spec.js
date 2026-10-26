import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The chats page with the API stubbed, so the moments that are too quick to
// catch on a real server can be held still: messages waiting while a turn runs,
// editing and cancelling them, a Stop, and what happens to the waiting messages
// afterwards.

const composer = (page) => page.getByRole('textbox', { name: 'Message' })

function newChatsApi(page, initial) {
  const state = {
    id: 'c1', title: 'Quotes', model: 'qwen3-8b', running: true, last_turn: '',
    pending: [{ id: 'p1', text: 'use euros', seq: 1 }, { id: 'p2', text: 'add delivery time', seq: 2 }],
    calls: [], ...initial,
  }
  const detail = () => ({
    id: state.id, title: state.title, model: state.model, kind: 'chat',
    created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z',
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
      await page.getByRole('button', { name: 'Send' }).click()
      await expect.poll(() => api.state.calls.filter(c => c.body?.multipart).length).toBe(1)
    })

    test('attaching is off while a turn runs, and says why', async ({ page }) => {
      const api = newChatsApi(page, { running: true })
      await api.install()
      await page.goto('/app/chats/c1')
      await expect(page.getByRole('button', { name: 'Attach a file' })).toBeDisabled()
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
})

test.describe('Chat skills panel', () => {
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
    await page.getByText(/^Skills \(/).click()
    await expect(page.getByRole('checkbox', { name: 'review' })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'notes' })).not.toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'old' })).toHaveCount(0)
    await page.getByRole('checkbox', { name: 'review' }).uncheck()
    await expect.poll(() => calls.at(-1)).toEqual({ off: ['notes', 'review'] })
  })
})
