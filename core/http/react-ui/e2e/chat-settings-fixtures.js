import { mockChat } from './chat-fixtures.js'

// A server-side chat with stubbed routes, for the specs of the chat settings.
// PATCH /api/chats/c1 keeps the settings it receives, as the server does (it
// replaces them as a whole), and records each body in `patches`. A POST to /api/chats is a new chat, `creates` keeps the bodies.
// `harness` is the window check the server sends with an open chat (see
// schema.ChatsHarness); `models` replaces the chat models the page lists.
export async function stubServerChat(page, { settings = {}, model = 'qwen3-8b', harness, models } = {}) {
  await mockChat(page, models ? { models } : {})
  const chat = { id: 'c1', title: 'Quotes', model, kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, settings, ...(harness ? { harness } : {}) }
  const patches = []
  const creates = []
  await page.route(/\/api\/chats(\?.*)?$/, route => {
    const req = route.request()
    if (req.method() === 'POST') {
      creates.push(req.postDataJSON())
      return route.fulfill({ json: { id: 'c1' } })
    }
    return route.fulfill({ json: { conversations: [chat], persistent: true, files: true } })
  })
  await page.route('**/api/chats/notifications/events', route => route.abort())
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [{ seq: 1, turn: 1, role: 'user', text: 'compare the quotes', at: '2026-10-25T10:00:00Z' }] } }))
  await page.route('**/api/chats/c1', async route => {
    const req = route.request()
    if (req.method() === 'PATCH') {
      const body = req.postDataJSON()
      patches.push(body)
      if (body.settings) chat.settings = body.settings
      if (body.model) chat.model = body.model
      if (body.model && harness) chat.harness = { ...chat.harness, ok: true }
      return route.fulfill({ json: { ...chat, pending: [] } })
    }
    return route.fulfill({ json: { ...chat, pending: [] } })
  })
  return { patches, creates, chat }
}
