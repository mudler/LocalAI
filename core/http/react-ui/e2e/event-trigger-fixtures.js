// An event trigger proposed in a chat, for the specs and the screenshots: the
// proposal, and a small model of the server that answers the card. The secret
// is made when the proposal is confirmed and again when it is rotated, and the
// server never says it a second time.
import { mockChat } from './chat-fixtures.js'

export const AT = '2026-11-24T09:00:00Z'
export const PID = 'ev1'
export const SECRET = 'whsec_3f9a1c7d52e04b68a1d9'
export const ROTATED = 'whsec_b81e6d04c7a35f29e2c0'

export const eventTrigger = (extra = {}) => ({
  id: PID, name: 'Review new pull requests', prompt: 'Read the pull request and write a short review.', kind: 'event',
  source: 'github', event_type: 'pull_request', matches: ['action=opened'], enabled: false, model: 'qwen3-8b', source_chat_id: 'c1',
  summary: 'When github sends pull_request', daily_cap: 10, plan: {}, cost: { max_runs_per_day: 10 }, ...extra,
})

const HISTORY = [
  { seq: 1, turn: 1, role: 'user', text: 'Review every pull request that opens on my repo.', at: AT },
  { seq: 2, turn: 1, role: 'tool', text: 'propose_trigger: Review new pull requests\nProposed it.', card: { kind: 'trigger_proposal', id: PID }, at: AT },
  { seq: 3, turn: 1, role: 'assistant', text: 'I can review each new pull request when GitHub tells me about it. Create it below if that looks right.', at: AT },
]

// The server, in memory. `s.calls` lists "METHOD verb" of every call to the proposal and the secret.
export async function serveEventProposal(page) {
  const s = { state: 'pending', calls: [], secret: '' }
  const view = () => ({
    state: s.state,
    trigger: eventTrigger(s.state === 'accepted' ? { enabled: true, hook_path: `/api/chats/triggers/${PID}/hook` } : {}),
    answered_at: s.state === 'pending' ? undefined : AT,
    can_undo: s.state === 'accepted',
  })
  await mockChat(page)
  const detail = { id: 'c1', title: 'Pull requests', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done', pending: [] }
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: HISTORY } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.route(/\/api\/chats\/triggers\/proposals\/ev1(\/[a-z]+)?$/, (route) => {
    const req = route.request()
    const verb = new URL(req.url()).pathname.split('/').pop()
    const tail = verb === PID ? '' : verb
    s.calls.push(`${req.method()} ${tail}`.trim())
    if (req.method() === 'GET') return route.fulfill({ json: view() })
    if (tail === 'confirm') {
      s.state = 'accepted'
      s.secret = SECRET
      return route.fulfill({ json: { ...view().trigger, secret: SECRET } })
    }
    if (tail === 'undo') s.state = 'undone'
    return route.fulfill({ json: view() })
  })
  await page.route(/\/api\/chats\/triggers\/ev1\/secret$/, (route) => {
    s.calls.push('POST secret')
    s.secret = ROTATED
    return route.fulfill({ json: { secret: ROTATED } })
  })
  return s
}
