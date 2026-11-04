// Stubs for the specs about chats that a person did not start: a list API that
// honours the origin filter, the lists that name the origins and an empty inbox.
import { mockChat } from './chat-fixtures.js'

const minutesAgo = (n) => new Date(Date.now() - n * 60_000).toISOString()

export const PERSON = [
  { id: 'p1', title: 'Plan the week', model: 'qwen3-8b', kind: 'chat', created: minutesAgo(300), updated: minutesAgo(120), running: false },
  { id: 'p2', title: 'Compare the quotes', model: 'qwen3-8b', kind: 'chat', created: minutesAgo(900), updated: minutesAgo(600), running: false },
]

export const AUTOMATED = [
  { id: 'b1', title: 'Chat App · alice', model: 'qwen3-8b', kind: 'chat', origin: 'connector', origin_id: 'bind1', created: minutesAgo(50), updated: minutesAgo(2), running: true, last_turn: 'done' },
  { id: 'b2', title: 'Chat App · bob', model: 'qwen3-8b', kind: 'chat', origin: 'connector', origin_id: 'bind1', created: minutesAgo(80), updated: minutesAgo(30), running: false, last_turn: 'paused' },
  { id: 'b3', title: 'Chat App · carol', model: 'qwen3-8b', kind: 'chat', origin: 'connector', origin_id: 'bind2', created: minutesAgo(700), updated: minutesAgo(400), running: false, last_turn: 'done' },
  { id: 't1', title: 'Morning digest', model: 'qwen3-8b', kind: 'chat', origin: 'trigger', origin_id: 'trig1', created: minutesAgo(200), updated: minutesAgo(180), running: false, last_turn: 'done' },
  { id: 'j1', title: 'Nightly report', model: 'qwen3-8b', kind: 'chat', origin: 'job', origin_id: 'nightly', created: minutesAgo(1000), updated: minutesAgo(900), running: false, last_turn: 'done' },
  { id: 'f1', title: 'From a future origin', model: 'qwen3-8b', kind: 'chat', origin: 'future', origin_id: 'x1', created: minutesAgo(1100), updated: minutesAgo(1000), running: false, last_turn: 'done' },
]

const CONNECTORS = [
  { id: 'bind1', name: 'Team assistant', kind: 'chatapp', addon: 'chatapp', enabled: true },
  { id: 'bind2', name: '', kind: 'chatapp', addon: 'chatapp', enabled: true },
]
const KINDS = [{ addon: 'chatapp', kind: 'chatapp', title: 'Chat App', page: 'bot' }]
const TRIGGERS = [{ id: 'trig1', name: 'Morning digest', kind: 'schedule', enabled: true }]

// Stub the chat pages with a list that filters by the origin parameter the way
// the server does. `lists` records the origin of every list request.
export async function mockOriginChats(page, { person = PERSON, automated = AUTOMATED, notifications = [] } = {}) {
  const lists = []
  await mockChat(page)
  await page.route(/\/api\/chats(\?.*)?$/, (route) => {
    const params = new URL(route.request().url()).searchParams
    const origin = params.get('origin') || ''
    const limit = Number(params.get('limit')) || 50
    lists.push(origin)
    const all = [...person, ...automated]
    const conversations = origin === 'person' ? person
      : origin === 'automated' ? automated
        : origin ? automated.filter(c => c.origin === origin) : all
    route.fulfill({ json: { conversations: conversations.slice(0, limit), persistent: true, files: true } })
  })
  await page.route(/\/api\/chats\/connectors(\?.*)?$/, route => route.fulfill({ json: { connectors: CONNECTORS } }))
  await page.route('**/api/chats/connectors/kinds', route => route.fulfill({ json: { kinds: KINDS } }))
  await page.route(/\/api\/chats\/triggers(\?.*)?$/, route => route.fulfill({ json: { triggers: TRIGGERS } }))
  await page.route(/\/api\/chats\/notifications(\?.*)?$/, route => route.fulfill({ json: { notifications, unread: notifications.length, total: notifications.length } }))
  await page.route('**/api/chats/notifications/events', route => route.abort())
  await page.route(/\/api\/chats\/search(\?.*)?$/, (route) => {
    const q = (new URL(route.request().url()).searchParams.get('q') || '').toLowerCase()
    const hits = [...person, ...automated].filter(c => c.title.toLowerCase().includes(q)).map(c => ({ chat: c, in_title: true }))
    route.fulfill({ json: { hits } })
  })
  for (const c of [...person, ...automated]) {
    await page.route(`**/api/chats/${c.id}/events`, route => route.abort())
    await page.route(new RegExp(`/api/chats/${c.id}/messages(\\?.*)?$`), route => route.fulfill({ json: { messages: [{ seq: 1, turn: 1, role: 'user', text: 'hello', at: c.created }] } }))
    await page.route(new RegExp(`/api/chats/${c.id}$`), route => route.fulfill({ json: { ...c, pending: [] } }))
  }
  return { lists }
}
