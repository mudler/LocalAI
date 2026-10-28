// Stubbed API of the saved agents, their chats, the triggers and the skills, for
// the specs of the agents, skills and triggers pages. Every call is recorded in
// `s.calls` so a spec can assert what the page sent.
import { mockChat } from './chat-fixtures.js'

export const AGENTS = [
  {
    id: 'a1', name: 'research-assistant', description: 'Searches the web and writes short briefs.', model: 'qwen3-8b',
    instructions: 'You are a careful research assistant. Cite every claim.', mcp_servers: ['crm', 'connector:m1'],
    addons: ['office'], collections: ['handbook'], skills: ['citations'],
    created: '2026-10-01T10:00:00Z', updated: '2026-10-02T10:00:00Z',
  },
  {
    id: 'a2', name: 'code-reviewer', description: 'Reviews a change and lists what to fix first.', model: '',
    instructions: 'You review code changes.', mcp_servers: [], addons: [], collections: [], skills: [],
    created: '2026-10-01T10:00:00Z', updated: '2026-10-02T10:00:00Z',
  },
]

const iso = (minutesAgo) => new Date(Date.now() - minutesAgo * 60_000).toISOString()

export const CHATS = [
  { id: 'c1', title: 'Find sources on speculative decoding', model: 'qwen3-8b', kind: 'chat', agent_id: 'a1', running: false, last_turn: 'done', created: iso(300), updated: iso(298) },
  { id: 'c2', title: 'Digest of the week', model: 'qwen3-8b', kind: 'chat', agent_id: 'a1', origin: 'trigger', origin_id: 't1', running: false, last_turn: 'failed', created: iso(120), updated: iso(118) },
  { id: 'c3', title: 'Unrelated chat', model: 'qwen3-8b', kind: 'chat', running: false, last_turn: 'done', created: iso(60), updated: iso(59) },
]

export const TRIGGERS = [
  {
    id: 't1', name: 'Morning digest', prompt: 'Summarise yesterday.', kind: 'schedule', schedule: 'weekdays 09:00', time_zone: 'Europe/Rome',
    daily_cap: 24, enabled: true, agent_id: 'a1', model: '', summary: 'Every weekday at 09:00 (Europe/Rome)', next_run: '2026-10-28T08:00:00Z',
    last_run: { id: 'r1', status: 'failed', reason: 'the agent was deleted', due: '2026-10-27T08:00:00Z' },
  },
  {
    id: 't2', name: 'New contracts', prompt: 'Read the new file and list its parties.', kind: 'file', folder: '/Contracts', quiet_seconds: 30,
    daily_cap: 10, enabled: false, agent_id: '', model: 'qwen3-8b', summary: 'When a file arrives in /Contracts',
  },
]

export const RUNS = {
  t1: [
    { id: 'r1', status: 'failed', reason: 'the agent was deleted', due: iso(120), started: iso(120), ended: iso(119), chat_id: 'c2' },
    { id: 'r0', status: 'done', due: iso(1560), started: iso(1560), ended: iso(1558), chat_id: 'c7' },
    { id: 'rs', status: 'skipped', reason: 'daily cap reached', due: iso(3000) },
  ],
  t2: [{ id: 'rf', status: 'done', due: iso(200), started: iso(200), ended: iso(199), chat_id: 'c8', file: '/Contracts/a.pdf' }],
}

export const SKILLS = [
  { name: 'citations', description: 'Cite every claim with a numbered source.', layer: 'personal', path: '/skills/citations/SKILL.md', enabled: true, editable: true },
  { name: 'house-style', description: 'The shared writing rules.', layer: 'shared', path: '/shared/house-style/SKILL.md', enabled: true, editable: false },
  { name: 'pdf', description: 'Read and fill PDF forms.', layer: 'builtin', path: '/builtin/pdf/SKILL.md', enabled: true, editable: false },
  { name: 'broken', description: '', layer: 'folder', path: '/Notes/.skills/broken/SKILL.md', enabled: true, editable: false, invalid: 'the description is empty' },
]

const BODIES = { citations: 'Cite every claim.\n\n1. Number the sources.\n2. List them at the end.\n' }

export async function installAgents(page, {
  agents = AGENTS, chats = CHATS, triggers = TRIGGERS, runs = RUNS, skills = SKILLS, proposals = [],
  collections = ['handbook', 'notes'], connectors = [{ id: 'm1', name: 'Docs', enabled: true }, { id: 'm2', name: 'Parked', enabled: false }],
} = {}) {
  await mockChat(page)
  const s = {
    agents: structuredClone(agents), triggers: structuredClone(triggers), skills: structuredClone(skills), proposals: structuredClone(proposals),
    calls: [], createdChats: [],
  }
  const rec = (req) => { const u = new URL(req.url()); s.calls.push({ method: req.method(), url: u.pathname, search: u.search, body: req.postData() ? req.postDataJSON() : null }) }
  const err = (route, status, message) => route.fulfill({ status, json: { error: { message, type: 'invalid_request_error', code: status } } })

  await page.route('**/api/collections*', route => route.fulfill({ json: { collections } }))
  await page.route('**/v1/mcp/servers/*', route => route.fulfill({ json: { model: 'qwen3-8b', servers: [{ name: 'crm' }, { name: 'mail' }] } }))
  await page.route('**/api/chats/mcp-connectors*', route => route.fulfill({ json: { connectors } }))
  await page.route('**/api/chats/files*', route => route.fulfill({ json: { path: '/', entries: [{ path: '/Contracts', name: 'Contracts', kind: 'dir' }] } }))
  await page.route('**/api/chats/notifications/events', route => route.abort())
  await page.route(/\/api\/chats\/notifications(\?.*)?$/, route => route.fulfill({ json: { notifications: [], unread: 0 } }))
  await page.route('**/api/chats/push/**', route => err(route, 409, 'push off'))

  // Agents.
  await page.route(/\/api\/chats\/agents\/[^/?]+$/, route => {
    const req = route.request()
    rec(req)
    const id = new URL(req.url()).pathname.split('/').pop()
    const found = s.agents.find(a => a.id === id)
    if (!found) return err(route, 404, 'no such agent')
    if (req.method() === 'DELETE') { s.agents = s.agents.filter(a => a.id !== id); return route.fulfill({ status: 204, body: '' }) }
    if (req.method() === 'PUT') { Object.assign(found, req.postDataJSON()); return route.fulfill({ json: found }) }
    return route.fulfill({ json: found })
  })
  await page.route(/\/api\/chats\/agents(\?.*)?$/, route => {
    const req = route.request()
    if (req.method() === 'POST') {
      rec(req)
      const b = req.postDataJSON()
      const a = { id: `a${s.agents.length + 1}`, mcp_servers: [], addons: [], collections: [], skills: [], ...b }
      s.agents.push(a)
      return route.fulfill({ status: 201, json: a })
    }
    return route.fulfill({ json: { agents: s.agents } })
  })

  // Chats.
  await page.route(/\/api\/chats(\?.*)?$/, route => {
    const req = route.request()
    if (req.method() === 'POST') {
      const b = req.postDataJSON()
      rec(req)
      s.createdChats.push(b)
      return route.fulfill({ status: 201, json: { id: 'cnew', title: 'New chat', model: b.model || 'qwen3-8b', kind: 'chat', agent_id: b.agent_id, running: false, created: iso(0), updated: iso(0) } })
    }
    return route.fulfill({ json: { conversations: chats, persistent: true, files: true } })
  })

  // Triggers.
  await page.route('**/api/chats/triggers/preview*', route => {
    const when = new URL(route.request().url()).searchParams.get('when') || ''
    if (when.includes('25:00')) return err(route, 400, 'hour must be 0 to 23')
    return route.fulfill({ json: { summary: 'Every day at 08:00', times: ['2026-10-28T08:00:00Z', '2026-10-29T08:00:00Z', '2026-10-30T08:00:00Z'] } })
  })
  await page.route('**/api/chats/triggers/proposals/*/confirm', route => {
    rec(route.request())
    s.proposals = []
    return route.fulfill({ json: { ...TRIGGERS[0], id: 't9', name: 'Proposed digest' } })
  })
  await page.route(/\/api\/chats\/triggers\/proposals\/[^/?]+$/, route => {
    rec(route.request())
    s.proposals = []
    return route.fulfill({ status: 204, body: '' })
  })
  await page.route(/\/api\/chats\/triggers\/proposals(\?.*)?$/, route => route.fulfill({ json: { triggers: s.proposals } }))
  await page.route(/\/api\/chats\/triggers\/[^/?]+\/runs$/, route => {
    const id = new URL(route.request().url()).pathname.split('/')[4]
    return route.fulfill({ json: { runs: runs[id] || [] } })
  })
  await page.route(/\/api\/chats\/triggers\/[^/?]+\/(run|enable|disable)$/, route => {
    const req = route.request()
    rec(req)
    const parts = new URL(req.url()).pathname.split('/')
    const verb = parts.pop()
    const id = parts.pop()
    const found = s.triggers.find(x => x.id === id)
    if (verb !== 'run' && found) found.enabled = verb === 'enable'
    return route.fulfill({ status: verb === 'run' ? 202 : 200, json: verb === 'run' ? { id: 'rnew', status: 'queued', due: iso(0) } : found })
  })
  await page.route(/\/api\/chats\/triggers\/(?!proposals|preview)[^/?]+$/, route => {
    const req = route.request()
    rec(req)
    const id = new URL(req.url()).pathname.split('/').pop()
    const found = s.triggers.find(x => x.id === id)
    if (req.method() === 'DELETE') { s.triggers = s.triggers.filter(x => x.id !== id); return route.fulfill({ status: 204, body: '' }) }
    Object.assign(found, req.postDataJSON())
    return route.fulfill({ json: found })
  })
  await page.route(/\/api\/chats\/triggers(\?.*)?$/, route => {
    const req = route.request()
    if (req.method() === 'POST') {
      rec(req)
      const t = { id: `t${s.triggers.length + 5}`, summary: 'Every day at 08:00', ...req.postDataJSON() }
      s.triggers.push(t)
      return route.fulfill({ status: 201, json: t })
    }
    return route.fulfill({ json: { triggers: s.triggers } })
  })

  // Skills.
  await page.route(/\/api\/chats\/skills\/[^/?]+\/[^/?]+$/, route => {
    const req = route.request()
    rec(req)
    const [, , , , layer, name] = new URL(req.url()).pathname.split('/')
    const found = s.skills.find(x => x.layer === layer && x.name === name)
    if (!found) return err(route, 404, 'no such skill')
    if (req.method() === 'DELETE') { s.skills = s.skills.filter(x => x !== found); return route.fulfill({ status: 204, body: '' }) }
    if (req.method() === 'PUT') { Object.assign(found, { description: req.postDataJSON().description }); return route.fulfill({ json: found }) }
    return route.fulfill({ json: { name, layer, description: found.description, body: BODIES[name] || `Instructions of ${name}.\n`, editable: found.editable } })
  })
  await page.route(/\/api\/chats\/skills(\?.*)?$/, route => {
    const req = route.request()
    if (req.method() === 'POST') {
      rec(req)
      const b = req.postDataJSON()
      s.skills.push({ name: b.name, description: b.description, layer: b.layer, path: `/skills/${b.name}/SKILL.md`, enabled: true, editable: true })
      return route.fulfill({ status: 201, json: b })
    }
    return route.fulfill({ json: { skills: s.skills, notes: [] } })
  })
  return s
}
