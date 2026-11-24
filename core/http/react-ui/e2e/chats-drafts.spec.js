import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// What you type, attach or send belongs to one chat. Two chats with the API
// stubbed: the draft of each chat stays with it when you switch, and a message
// sent while a turn runs goes to the chat it was written in, also when the
// answer of the server arrives after you opened another chat.

const composer = (page) => page.getByTestId('chat-input')

// Two chats, c1 (running a turn) and c2 (idle). A send to a chat can be held:
// the request waits until the spec releases it.
function twoChats(page) {
  const state = {
    c1: { id: 'c1', title: 'Quotes', running: true, pending: [] },
    c2: { id: 'c2', title: 'Recipes', running: false, pending: [] },
    posts: [],
    answered: 0,
    hold: null,
    fail: false,
  }
  const detail = (c) => ({
    id: c.id, title: c.title, model: 'qwen3-8b', kind: 'chat',
    created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z',
    running: c.running, last_turn: c.running ? '' : 'done', pending: c.pending,
  })
  const install = async () => {
    await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
    // The event streams stay open and quiet, as a server's do between turns:
    // a stream that breaks would reconnect and read the chat again, which
    // hides a state that leaked from another chat.
    await page.route(/\/api\/chats\/c[12]\/events$/, () => {})
    await page.route(/\/api\/chats\/c[12]\/messages(\?.*)?$/, async (route) => {
      const req = route.request()
      const id = new URL(req.url()).pathname.split('/')[3]
      if (req.method() !== 'POST') {
        return route.fulfill({ json: { messages: [{ seq: 1, turn: 1, role: 'user', text: `first message of ${id}`, at: '2026-10-25T10:00:00Z' }] } })
      }
      const multipart = (req.headers()['content-type'] || '').startsWith('multipart/')
      const text = multipart ? req.postData() || '' : req.postDataJSON().text
      state.posts.push({ chat: id, text })
      if (state.hold) await state.hold
      state.answered++
      if (state.fail) return route.fulfill({ status: 500, json: { error: { message: 'the model is gone' } } })
      const c = state[id]
      if (c.running) {
        c.pending = [...c.pending, { id: `p${state.posts.length}`, text, seq: state.posts.length }]
        return route.fulfill({ json: { started: false, pending_id: `p${state.posts.length}` } })
      }
      c.running = true
      return route.fulfill({ json: { started: true } })
    })
    await page.route(/\/api\/chats\/c[12]$/, route => {
      const id = new URL(route.request().url()).pathname.split('/')[3]
      return route.fulfill({ json: detail(state[id]) })
    })
    await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
      json: { conversations: [state.c1, state.c2].map(c => ({ ...detail(c), pending: undefined })), persistent: true, files: true },
    }))
  }
  // Holds every send until the returned function is called.
  const holdSends = () => {
    let release
    state.hold = new Promise((r) => { release = r })
    return () => { state.hold = null; release() }
  }
  return { state, install, holdSends }
}

// Opens a chat from the conversations menu, as a person does: the page stays,
// only the address changes.
async function openChat(page, title) {
  await page.keyboard.press('Control+k')
  await page.getByTestId('chats-row').filter({ hasText: title }).click()
  await expect(page.getByTestId('chat-thread')).toContainText(title === 'Quotes' ? 'first message of c1' : 'first message of c2')
}

test.describe('Chat drafts belong to one chat', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('a draft stays in its chat and comes back when you return', async ({ page }) => {
    const api = twoChats(page)
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-thread')).toContainText('first message of c1')

    await composer(page).fill('half a thought for quotes')
    await openChat(page, 'Recipes')
    await expect(composer(page)).toHaveValue('')

    await composer(page).fill('a draft for recipes')
    await openChat(page, 'Quotes')
    await expect(composer(page)).toHaveValue('half a thought for quotes')
    await openChat(page, 'Recipes')
    await expect(composer(page)).toHaveValue('a draft for recipes')
    expect(api.state.posts).toEqual([])
  })

  test('a draft survives a reload of its chat', async ({ page }) => {
    const api = twoChats(page)
    await api.install()
    await page.goto('/app/chats/c2')
    await expect(page.getByTestId('chat-thread')).toContainText('first message of c2')
    await composer(page).fill('kept across a reload')
    // The draft is written to the browser storage a moment after the last key.
    await expect.poll(() => page.evaluate(() => Object.keys(localStorage).some(k => (localStorage.getItem(k) || '').includes('kept across a reload')))).toBe(true)

    await page.reload()
    await expect(page.getByTestId('chat-thread')).toContainText('first message of c2')
    await expect(composer(page)).toHaveValue('kept across a reload')
  })

  test('a sent message clears the draft of its chat only', async ({ page }) => {
    const api = twoChats(page)
    await api.install()
    await page.goto('/app/chats/c2')
    await expect(page.getByTestId('chat-thread')).toContainText('first message of c2')
    await composer(page).fill('send this to recipes')
    await composer(page).press('Enter')
    await expect.poll(() => api.state.posts).toEqual([{ chat: 'c2', text: 'send this to recipes' }])
    await expect(composer(page)).toHaveValue('')

    await openChat(page, 'Quotes')
    await expect(composer(page)).toHaveValue('')
    await openChat(page, 'Recipes')
    await expect(composer(page)).toHaveValue('')
  })

  test('a staged file does not follow you to another chat', async ({ page }) => {
    const api = twoChats(page)
    await api.install()
    await page.goto('/app/chats/c2')
    await expect(page.getByTestId('chat-thread')).toContainText('first message of c2')
    await page.getByTestId('chat-file-input').setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('alpha') })
    await expect(page.getByTestId('attachment')).toContainText('notes.txt')

    await openChat(page, 'Quotes')
    await expect(page.getByTestId('attachment')).toHaveCount(0)
    await openChat(page, 'Recipes')
    await expect(page.getByTestId('attachment')).toContainText('notes.txt')
  })

  test('a message queued while a turn runs goes to its own chat, also when you switch before the server answers', async ({ page }) => {
    const api = twoChats(page)
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-thread')).toContainText('first message of c1')
    await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible()

    const release = api.holdSends()
    await composer(page).fill('one more thing for quotes')
    await composer(page).press('Enter')
    await expect.poll(() => api.state.posts).toEqual([{ chat: 'c1', text: 'one more thing for quotes' }])

    // Recipes opens while the send to Quotes is still on its way; then the
    // server answers it and the turn of Quotes ends.
    await openChat(page, 'Recipes')
    release()
    await expect.poll(() => api.state.answered).toBe(1)
    api.state.c1.running = false
    await page.waitForTimeout(500)

    // Recipes is idle, empty and got nothing.
    await expect(composer(page)).toHaveValue('')
    await expect(page.getByRole('button', { name: 'Stop' })).toHaveCount(0)
    await expect(page.getByTestId('chat-foot')).not.toContainText('Working')
    await expect(page.getByTestId('pending-list')).toHaveCount(0)
    expect(api.state.posts.filter(p => p.chat === 'c2')).toEqual([])

    // Quotes holds the waiting message.
    await openChat(page, 'Quotes')
    await expect(page.getByTestId('pending-row')).toHaveText([/one more thing for quotes/])
  })

  test('a turn started in one chat does not show as running in the chat you switched to', async ({ page }) => {
    const api = twoChats(page)
    api.state.c1.running = false
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-thread')).toContainText('first message of c1')

    const release = api.holdSends()
    await composer(page).fill('start a turn in quotes')
    await composer(page).press('Enter')
    await expect.poll(() => api.state.posts.length).toBe(1)

    await openChat(page, 'Recipes')
    release()
    await expect.poll(() => api.state.answered).toBe(1)
    await page.waitForTimeout(500)
    // The turn runs in Quotes; Recipes has no Stop button and is not working.
    await expect(page.getByRole('button', { name: 'Stop' })).toHaveCount(0)
    await expect(page.getByTestId('chat-foot')).not.toContainText('Working')
    expect(api.state.posts).toEqual([{ chat: 'c1', text: 'start a turn in quotes' }])
  })

  test('a message that fails after you switched comes back in its own chat', async ({ page }) => {
    const api = twoChats(page)
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-thread')).toContainText('first message of c1')

    const release = api.holdSends()
    api.state.fail = true
    await composer(page).fill('this one fails')
    await composer(page).press('Enter')
    await expect.poll(() => api.state.posts.length).toBe(1)

    await openChat(page, 'Recipes')
    release()
    await expect.poll(() => api.state.answered).toBe(1)
    await page.waitForTimeout(500)
    await expect(composer(page)).toHaveValue('')
    await expect(page.getByRole('alert')).toHaveCount(0)

    await openChat(page, 'Quotes')
    await expect(composer(page)).toHaveValue('this one fails')
  })

  test('the waiting messages of a chat show only in that chat and can be cancelled there', async ({ page }) => {
    const api = twoChats(page)
    api.state.c1.pending = [{ id: 'p1', text: 'use euros', seq: 1 }]
    await api.install()
    let cancelled = ''
    await page.route('**/api/chats/c1/pending/*', route => {
      cancelled = new URL(route.request().url()).pathname
      api.state.c1.pending = []
      return route.fulfill({ json: { ok: true } })
    })
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('pending-row')).toHaveText([/use euros/])

    await openChat(page, 'Recipes')
    await expect(page.getByTestId('pending-list')).toHaveCount(0)

    await openChat(page, 'Quotes')
    await page.getByTestId('pending-row').getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByTestId('pending-list')).toHaveCount(0)
    expect(cancelled).toBe('/api/chats/c1/pending/p1')
  })

  test('dictated text lands in the chat the dictation started in', async ({ page }) => {
    // A microphone and a recorder that give one chunk, and a transcription
    // model whose answer the spec holds.
    await page.addInitScript(() => {
      navigator.mediaDevices.getUserMedia = async () => new MediaStream()
      window.MediaRecorder = class {
        constructor(stream) { this.stream = stream; this.state = 'inactive'; this.mimeType = 'audio/webm' }
        start() { this.state = 'recording' }
        stop() {
          this.state = 'inactive'
          this.ondataavailable?.({ data: new Blob(['voice']) })
          this.onstop?.()
        }
      }
    })
    await page.route('**/api/models/capabilities', route => route.fulfill({
      json: { data: [{ id: 'qwen3-8b', capabilities: ['FLAG_CHAT'] }, { id: 'whisper-1', capabilities: ['FLAG_TRANSCRIPT'] }] },
    }))
    let answer
    const answered = new Promise((r) => { answer = r })
    await page.route('**/v1/audio/transcriptions', async (route) => {
      await answered
      return route.fulfill({ json: { text: 'spoken words' } })
    })
    const api = twoChats(page)
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-thread')).toContainText('first message of c1')
    await composer(page).fill('typed in quotes')

    await page.getByTestId('chat-dictate').click()
    await expect(page.getByTestId('chat-dictate')).toHaveAttribute('data-state', 'recording')
    await page.getByTestId('chat-dictate').click()
    await expect(page.getByTestId('chat-dictate')).toHaveAttribute('data-state', 'transcribing')

    await openChat(page, 'Recipes')
    answer()
    await expect(page.getByTestId('chat-dictate')).toHaveAttribute('data-state', 'idle')
    await expect(composer(page)).toHaveValue('')

    await openChat(page, 'Quotes')
    await expect(composer(page)).toHaveValue('typed in quotes spoken words')
  })

  test('a new chat starts with an empty draft', async ({ page }) => {
    const api = twoChats(page)
    await api.install()
    await page.goto('/app/chats/c2')
    await expect(page.getByTestId('chat-thread')).toContainText('first message of c2')
    await composer(page).fill('not for the new chat')
    await page.getByRole('button', { name: 'More' }).click()
    await page.getByRole('menuitem', { name: 'New chat' }).click()
    await expect(page).toHaveURL(/\/app\/?$/)
    await expect(page.getByTestId('home-composer').getByRole('combobox')).toHaveValue('')
  })

  test('deleting a chat drops its draft', async ({ page }) => {
    const api = twoChats(page)
    await api.install()
    await page.route(/\/api\/chats\/c2$/, route => (route.request().method() === 'DELETE'
      ? route.fulfill({ status: 204, body: '' })
      : route.fallback()))
    await page.goto('/app/chats/c2')
    await expect(page.getByTestId('chat-thread')).toContainText('first message of c2')
    await composer(page).fill('draft of a chat that goes')
    await expect.poll(() => page.evaluate(() => Object.keys(localStorage).some(k => (localStorage.getItem(k) || '').includes('draft of a chat that goes')))).toBe(true)

    await page.getByRole('button', { name: 'More' }).click()
    await page.getByRole('menuitem', { name: 'Delete chat' }).click()
    await expect(composer(page)).toHaveValue('')
    await page.getByTestId('chat-undo-toast').getByRole('button', { name: 'Dismiss' }).click()
    await expect.poll(() => page.evaluate(() => Object.keys(localStorage).some(k => (localStorage.getItem(k) || '').includes('draft of a chat that goes')))).toBe(false)
  })
})
