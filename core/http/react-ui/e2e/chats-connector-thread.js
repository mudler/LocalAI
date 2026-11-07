// Stubs for the specs about a chat that a messaging connection started: the
// chat with its place, a transcript that names each sender, the block of
// earlier thread messages and the notes of the host.
import { mockChat } from './chat-fixtures.js'

export const AT = (minutes) => new Date(Date.UTC(2026, 10, 5, 9, 0, 0) + minutes * 60_000).toISOString()

export const CONNECTORS = [{ id: 'conn1', name: 'Team workspace', kind: 'slack', addon: 'slack', enabled: true }]
export const KINDS = [{ addon: 'slack', kind: 'slack', title: 'Slack', page: 'apps' }]

export const THREAD_LINES = [
  'Ada Lovelace: the nightly deploy failed again',
  'Grace Hopper: it is the migration step, I think',
  'Assistant (you): I looked at the logs and the migration timed out.',
]

export const MESSAGES = [
  { seq: 1, turn: 1, role: 'thread', text: '', thread: { lines: THREAD_LINES, cut: true }, at: AT(0) },
  { seq: 2, turn: 1, role: 'user', text: 'can you retry the migration?', sender: { id: 'U02ABC', name: 'Edsger Dijkstra', username: 'edsger', standing: 'channel' }, at: AT(1) },
  { seq: 3, turn: 1, role: 'assistant', text: 'I retried the migration and it finished in 40 seconds.', at: AT(2) },
  { seq: 4, turn: 2, role: 'user', text: 'thanks, and the dashboard?', sender: { id: 'U09XYZ', standing: 'anyone' }, at: AT(5) },
  { seq: 5, turn: 2, role: 'hostnote', host_note: { code: 'held_back' }, text: 'The owner has not enabled reminders for this bot.', at: AT(5) },
  { seq: 6, turn: 2, role: 'assistant', text: 'The dashboard is up again.', at: AT(6) },
  { seq: 7, turn: 3, role: 'user', text: 'and the changelog?', sender: { id: 'U02ABC', name: 'Edsger Dijkstra', standing: 'channel' }, at: AT(8) },
  { seq: 8, turn: 3, role: 'hostnote', host_note: { code: 'unsupported' }, text: 'I can only read text and files.', at: AT(8) },
  { seq: 9, turn: 3, role: 'hostnote', host_note: { code: 'unsupported' }, text: 'I can only read text and files.', at: AT(9) },
  { seq: 10, turn: 3, role: 'assistant', text: 'The changelog is in the release notes.', at: AT(10) },
]

export const CHAT = {
  id: 'cc1', title: 'Slack · Edsger Dijkstra', model: 'qwen3-8b', kind: 'chat', origin: 'connector', origin_id: 'conn1',
  created: AT(0), updated: AT(9), running: false, last_turn: 'done',
  place: { thread: true, name: 'ops-deploys', link: 'https://acme.example.test/archives/C1/p1700000000000100?thread_ts=1700000000.000100&cid=C1' },
}

// A chat of the same connection stored before senders and blocks were kept.
export const OLD_CHAT = {
  id: 'cc0', title: 'Slack · Old chat', model: 'qwen3-8b', kind: 'chat', origin: 'connector', origin_id: 'conn1',
  created: AT(-600), updated: AT(-590), running: false, last_turn: 'done',
}
export const OLD_MESSAGES = [
  { seq: 1, turn: 1, role: 'user', text: 'an older question', at: AT(-600) },
  { seq: 2, turn: 1, role: 'assistant', text: 'an older answer', at: AT(-599) },
]

export async function mockConnectorThread(page, { chats = [CHAT, OLD_CHAT], messages = { cc1: MESSAGES, cc0: OLD_MESSAGES } } = {}) {
  await mockChat(page)
  await page.route(/\/api\/chats(\?.*)?$/, (route) => {
    const origin = new URL(route.request().url()).searchParams.get('origin') || ''
    const list = origin === 'person' ? [] : chats
    route.fulfill({ json: { conversations: list, persistent: true, files: true } })
  })
  await page.route(/\/api\/chats\/connectors(\?.*)?$/, route => route.fulfill({ json: { connectors: CONNECTORS } }))
  await page.route('**/api/chats/connectors/kinds', route => route.fulfill({ json: { kinds: KINDS } }))
  await page.route(/\/api\/chats\/triggers(\?.*)?$/, route => route.fulfill({ json: { triggers: [] } }))
  await page.route(/\/api\/chats\/notifications(\?.*)?$/, route => route.fulfill({ json: { notifications: [], unread: 0, total: 0 } }))
  await page.route('**/api/chats/notifications/events', route => route.abort())
  for (const c of chats) {
    await page.route(`**/api/chats/${c.id}/events`, route => route.abort())
    await page.route(new RegExp(`/api/chats/${c.id}/messages(\\?.*)?$`), route => route.fulfill({ json: { messages: messages[c.id] || [] } }))
    await page.route(new RegExp(`/api/chats/${c.id}$`), route => route.fulfill({ json: { ...c, pending: [] } }))
  }
}
