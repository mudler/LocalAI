// Stubs and fixtures of the chat search specs: a list API, the chats a search
// can find, a long transcript and a search route that records its parameters.
import { mockChat } from './chat-fixtures.js'

export const minutesAgo = (n) => new Date(Date.now() - n * 60_000).toISOString()

export const RECENT = [
  { id: 'c1', title: 'Vector database setup', model: 'qwen3-8b', kind: 'chat', created: minutesAgo(300), updated: minutesAgo(20), running: false },
  { id: 'c2', title: 'Plan the week', model: 'qwen3-8b', kind: 'chat', agent_id: 'helper', created: minutesAgo(900), updated: minutesAgo(600), running: false },
]
// A chat the list of recent chats does not hold: only the search finds it.
export const OLD = { id: 'old1', title: 'Backup notes', model: 'qwen3-8b', kind: 'chat', created: '2025-01-10T09:00:00Z', updated: '2025-01-10T09:30:00Z', running: false }
export const TRIG = { id: 't1', title: 'Morning digest', model: 'qwen3-8b', kind: 'chat', origin: 'trigger', origin_id: 'trig1', created: minutesAgo(200), updated: minutesAgo(180), running: false }

export const hit = (chat, seq, role, snippet, highlights, extra = {}) => ({ chat, in_title: false, seq, role, at: minutesAgo(30), snippet, highlights, matches: 1, ...extra })

// A long transcript, so that a deep link has to scroll.
export const TRANSCRIPT = Array.from({ length: 60 }, (_, i) => ({
  seq: i + 1, turn: Math.floor(i / 2) + 1, role: i % 2 === 0 ? 'user' : 'assistant',
  text: i === 41 ? 'The Qdrant port is 6333.' : `Message number ${i + 1}. ${'Filler text for the transcript. '.repeat(6)}`, at: minutesAgo(400 - i),
}))

export async function mockSearch(page, { answer, delay = 0, fail = false } = {}) {
  const requests = []
  await mockChat(page)
  await page.route(/\/api\/chats(\?.*)?$/, (route) => {
    const origin = new URL(route.request().url()).searchParams.get('origin') || ''
    const conversations = origin === 'automated' ? [TRIG] : origin === 'person' ? RECENT : [...RECENT, TRIG]
    route.fulfill({ json: { conversations, persistent: true, files: true } })
  })
  await page.route('**/api/chats/agents', route => route.fulfill({ json: { agents: [{ id: 'helper', name: 'Helper' }, { id: 'writer', name: 'Writer' }] } }))
  await page.route(/\/api\/chats\/connectors(\?.*)?$/, route => route.fulfill({ json: { connectors: [] } }))
  await page.route('**/api/chats/connectors/kinds', route => route.fulfill({ json: { kinds: [] } }))
  await page.route(/\/api\/chats\/triggers(\?.*)?$/, route => route.fulfill({ json: { triggers: [{ id: 'trig1', name: 'Morning digest', kind: 'schedule', enabled: true }] } }))
  await page.route(/\/api\/chats\/notifications(\?.*)?$/, route => route.fulfill({ json: { notifications: [], unread: 0, total: 0 } }))
  await page.route('**/api/chats/notifications/events', route => route.abort())
  await page.route(/\/api\/chats\/search(\?.*)?$/, async (route) => {
    const params = Object.fromEntries(new URL(route.request().url()).searchParams)
    requests.push(params)
    if (delay) await new Promise(r => setTimeout(r, delay))
    if (fail) return route.fulfill({ status: 500, json: { error: { message: 'boom' } } })
    return route.fulfill({ json: typeof answer === 'function' ? answer(params) : answer })
  })
  for (const c of [...RECENT, OLD, TRIG]) {
    await page.route(`**/api/chats/${c.id}/events`, route => route.abort())
    await page.route(new RegExp(`/api/chats/${c.id}/messages(\\?.*)?$`), route => route.fulfill({ json: { messages: c.id === 'c1' ? TRANSCRIPT : [{ seq: 1, turn: 1, role: 'user', text: 'hello', at: c.created }] } }))
    await page.route(new RegExp(`/api/chats/${c.id}$`), route => route.fulfill({ json: { ...c, pending: [] } }))
  }
  return { requests }
}

export const TWO_HITS = {
  engine: 'fts5',
  hits: [
    hit(RECENT[0], 42, 'assistant', 'The Qdrant port is 6333.', [{ start: 4, end: 10 }, { start: 11, end: 15 }], { matches: 3 }),
    hit(OLD, 7, 'user', 'Which <b>port</b> does <img src=x onerror="window.__pwned=1"> qdrant use?', [{ start: 9, end: 13 }]),
  ],
}

