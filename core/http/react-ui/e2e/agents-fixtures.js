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
    instructions: 'You review code changes.', mcp_servers: [], addons: [], collections: [], skills: [], network: 'off',
    created: '2026-10-01T10:00:00Z', updated: '2026-10-02T10:00:00Z',
  },
]

export const GRANTS = [
  { name: 'memory.write', title: 'Remember things', description: 'The agent can propose a note in your memory.', sensitive: false },
  { name: 'profile.write', title: 'Change its own rules', description: 'The agent can propose an edit to its profile.', sensitive: true },
  { name: 'skills.write', title: 'Write skills', description: 'The agent can propose a skill.', sensitive: true },
  { name: 'pages.write', title: 'Create pages', description: 'The agent can propose a page.', sensitive: true },
  { name: 'dashboard.write', title: 'Change the dashboard', description: 'The agent can propose a dashboard layout.', sensitive: true },
  { name: 'knowledge.write', title: 'Add to knowledge', description: 'The agent can propose a text for one of its collections.', sensitive: true },
  { name: 'triggers.propose', title: 'Propose triggers', description: 'The agent can propose a task that runs by itself.', sensitive: false },
  { name: 'triggers.run', title: 'Run triggers', description: 'The triggers of this agent may start from a messaging app.', sensitive: false },
  { name: 'talkback.channels', title: 'Reply on known channels', description: 'The agent can send a message to a channel it already talks in.', sensitive: false },
  { name: 'talkback.new_channel', title: 'Start new channels', description: 'The agent can start a conversation on a new channel.', sensitive: true },
  { name: 'files.upload', title: 'Receive files', description: 'Senders can attach files for the agent to read.', sensitive: false },
  { name: 'act.files', title: 'Write files on its own', description: 'A run that nobody watches may write files.', sensitive: true },
  { name: 'act.addons', title: 'Use add-on actions on its own', description: 'A run that nobody watches may call add-on actions.', sensitive: true },
  { name: 'act.send', title: 'Send messages on its own', description: 'A run that nobody watches may send messages.', sensitive: true },
  { name: 'act.spend', title: 'Use paid or external tools on its own', description: 'A run that nobody watches may use paid tools.', sensitive: true },
  { name: 'saved_agents', title: 'Call your saved agents', description: 'The agent can start a saved agent.', sensitive: false },
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
    daily_cap: 24, run_limit: '30m', effective_run_limit: '15m', enabled: true, agent_id: 'a1', model: '', summary: 'Every weekday at 09:00 (Europe/Rome)', next_run: '2026-10-28T08:00:00Z',
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
  { name: 'pdf', description: 'Read and create PDFs with Office tools.', layer: 'addon', addon: 'office', addon_title: 'Office', path: '/addon-skills/office/pdf/SKILL.md', enabled: true, editable: false },
  { name: 'broken', description: '', layer: 'folder', path: '/Notes/.skills/broken/SKILL.md', enabled: true, editable: false, invalid: 'the description is empty' },
]

// The home of the folder picker specs: the person's own folders, and the
// two that LocalAI manages, which cannot be an agent's folder.
export const HOME_FOLDERS = {
  '/': [
    { path: '/Chats', name: 'Chats', kind: 'dir', managed: 'chats' },
    { path: '/LocalAI', name: 'LocalAI', kind: 'dir', managed: 'localai' },
    { path: '/Projects', name: 'Projects', kind: 'dir' },
    { path: '/notes.txt', name: 'notes.txt', kind: 'file', size: 10 },
  ],
  '/Projects': [{ path: '/Projects/Acme', name: 'Acme', kind: 'dir' }, { path: '/Projects/Beta', name: 'Beta', kind: 'dir' }],
  '/Projects/Acme': [],
  '/Projects/Beta': [],
}

// The folder of an agent as the server reads it back after a write.
function folderOf(write, before) {
  if (!write) return before
  if (!write.path) return undefined
  const name = write.path === '/' ? undefined : write.path.split('/').pop()
  return { path: write.path, ...(name ? { name } : {}), mode: write.mode || 'read', state: 'ok' }
}

// The rows of an agent as the server reads them back after a write: `folders`
// when sent, else the one `folder`. The first row is also `folder`. A row
// inside another takes the outer mode as `effective`. undefined: no change.
function applyFolders(a, body) {
  const { folder, folders, ...rest } = body
  let rows
  if (folders) rows = folders.filter(r => r.path).map(r => folderOf(r))
  else if (folder) rows = folder.path ? [folderOf(folder)] : []
  if (rows) {
    const holds = (o, p) => o === '/' || p === o || p.startsWith(o + '/')
    rows = rows.map((r, i) => {
      const outer = rows.filter((o, j) => j !== i && o.path !== r.path && holds(o.path, r.path)).sort((x, y) => x.path.length - y.path.length)[0]
      return { ...r, effective: (outer || r).mode }
    })
    if (rows.length) { a.folder = rows[0]; a.folders = rows } else { delete a.folder; delete a.folders }
  }
  return rest
}

const BODIES = { citations: 'Cite every claim.\n\n1. Number the sources.\n2. List them at the end.\n' }

// What the server lists for a new chat (GET /api/chats/draft/tools): the same
// lists the Tools menu of a chat draws and the agent form draws too.
export const DRAFT_TOOLS = {
  builtins: [
    { group: 'files', tools: ['read_file'], off: false, usable: true, provider: 'builtin', providers: { read_file: 'builtin' }, title: 'Your files', description: 'The model can list, read, write, edit, move, delete and search your files, and read documents.', when_off: 'The model cannot see or change your files.' },
    { group: 'web', tools: ['web_search', 'web_fetch'], off: false, usable: true, provider: 'builtin', providers: { web_search: 'builtin', web_fetch: 'builtin' }, title: 'Look things up online', description: 'The model can search the web and read a public page by its address.', when_off: 'The model cannot look anything up online.' },
    { group: 'saved_agents', tools: ['list_agents'], off: true, opt_in: true, usable: true, provider: 'builtin', providers: { list_agents: 'builtin' }, title: 'Saved agents', description: 'The model can list your saved agents and start one as a sub-agent.', when_off: 'The model cannot see or start your saved agents.' },
    { group: 'triggers', tools: ['propose_trigger'], off: false, usable: true, provider: 'builtin', providers: { propose_trigger: 'builtin' }, title: 'Triggers', description: 'The model can propose, list, change and delete triggers; a new one waits for your confirmation.', when_off: 'The model cannot create or manage triggers.' },
    { group: 'collections', tools: ['list_collections', 'search_collection'], off: false, usable: true, provider: 'builtin', providers: { list_collections: 'builtin', search_collection: 'builtin' }, title: 'Knowledge', description: 'The model can search the collections you chose for it.', when_off: 'The model cannot search your collections.' },
    { group: 'changes', tools: ['propose_change'], off: false, usable: true, provider: 'builtin', providers: { propose_change: 'builtin' }, title: 'Suggested changes', description: 'The model can propose an edit to your memory notes; nothing changes until you confirm.', when_off: 'The model cannot propose edits to your memory.' },
  ],
  addons: [{ id: 'office', tools: 2, off: false, title: 'Office', description: 'Read and write documents and spreadsheets.' }],
}

export async function installAgents(page, {
  agents = AGENTS, chats = CHATS, triggers = TRIGGERS, runs = RUNS, skills = SKILLS, proposals = [], importWarnings = [], files = null,
  models, collections = ['handbook', 'notes'], bindings = [], connectors = [{ id: 'm1', name: 'Docs', enabled: true }, { id: 'm2', name: 'Parked', enabled: false }],
} = {}) {
  await mockChat(page, { models })
  const s = {
    agents: structuredClone(agents), triggers: structuredClone(triggers), skills: structuredClone(skills), proposals: structuredClone(proposals),
    calls: [], createdChats: [],
  }
  const rec = (req) => { const u = new URL(req.url()); s.calls.push({ method: req.method(), url: u.pathname, search: u.search, body: req.postData() ? req.postDataJSON() : null }) }
  const err = (route, status, message) => route.fulfill({ status, json: { error: { message, type: 'invalid_request_error', code: status } } })

  await page.route('**/api/collections*', route => route.fulfill({ json: { collections } }))
  await page.route('**/v1/mcp/servers/*', route => route.fulfill({ json: { model: 'qwen3-8b', servers: [{ name: 'crm' }, { name: 'mail' }] } }))
  // The messaging connections: the agent form names the ones of this agent.
  await page.route(/\/api\/chats\/connectors$/, route => route.fulfill({ json: { connectors: bindings } }))
  await page.route(/\/api\/chats\/connectors\/kinds$/, route => route.fulfill({ json: { kinds: [{ kind: 'slack', addon: 'slack', title: 'Slack' }], capabilities: [] } }))
  await page.route('**/api/chats/mcp-connectors*', route => route.fulfill({ json: { connectors } }))
  await page.route('**/api/chats/draft/tools*', route => route.fulfill({
    json: { ...DRAFT_TOOLS, servers: [], off: [], connectors: connectors.map(c => ({ id: c.id, name: c.name, off: false, usable: c.enabled, reason: c.enabled ? undefined : 'off' })) },
  }))
  // With `files` (a map of folder to entries) the listing follows the path
  // asked for; without, every folder lists one folder.
  await page.route('**/api/chats/files*', route => {
    const path = new URL(route.request().url()).searchParams.get('path') || '/'
    if (!files) return route.fulfill({ json: { path: '/', entries: [{ path: '/Contracts', name: 'Contracts', kind: 'dir' }] } })
    if (!files[path]) return err(route, 404, 'not found')
    return route.fulfill({ json: { path, entries: files[path] } })
  })
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
    if (req.method() === 'PUT') {
      Object.assign(found, applyFolders(found, req.postDataJSON()))
      return route.fulfill({ json: found })
    }
    return route.fulfill({ json: found })
  })
  await page.route(/\/api\/chats\/agents(\?.*)?$/, route => {
    const req = route.request()
    if (req.method() === 'POST') {
      rec(req)
      const a = { id: `a${s.agents.length + 1}`, mcp_servers: [], addons: [], collections: [], skills: [], network: 'off' }
      Object.assign(a, applyFolders(a, req.postDataJSON()))
      s.agents.push(a)
      return route.fulfill({ status: 201, json: a })
    }
    return route.fulfill({ json: { agents: s.agents } })
  })

  // Agent files and run history, as the server answers them: an export is the
  // definition without id or owner, an import always makes a new agent, and
  // the runs are the chats that ran with the agent.
  await page.route('**/api/chats/agents/builtin-grants', route => route.fulfill({ json: { grants: GRANTS } }))
  await page.route(/\/api\/chats\/agents\/import$/, route => {
    const req = route.request()
    rec(req)
    const b = req.postDataJSON()
    const def = b && b.kind === 'localai-agent' ? b.agent : b
    if (!def || typeof def !== 'object' || !def.name) return err(route, 400, 'the body is not a valid agent file')
    const { folder, folders, ...rest } = def
    const a = { id: `a${s.agents.length + 1}`, mcp_servers: [], addons: [], collections: [], skills: [], ...rest }
    // A version 2 or 3 file names folders by path; one that is not found here
    // stays a row with the mode and no folder, as the server does.
    const named = folders || (folder ? [folder] : [])
    if (named.length) {
      a.folders = named.map(r => (files?.[r.path] ? folderOf(r) : { mode: r.mode, state: 'unset' }))
      a.folder = a.folders[0]
    }
    s.agents.push(a)
    return route.fulfill({ status: 201, json: { agent: a, ...(importWarnings.length ? { warnings: importWarnings } : {}) } })
  })
  await page.route(/\/api\/chats\/agents\/[^/?]+\/export$/, route => {
    rec(route.request())
    const id = new URL(route.request().url()).pathname.split('/')[4]
    const found = s.agents.find(a => a.id === id)
    if (!found) return err(route, 404, 'no such agent')
    const { name, description, instructions, model, mcp_servers, addons, collections, skills, folder, folders } = found
    const agent = { name, description, instructions, model, mcp_servers, addons, collections, skills }
    if (folders?.length > 1) return route.fulfill({ json: { version: 3, kind: 'localai-agent', agent: { ...agent, folders: folders.map(r => ({ path: r.path || '', mode: r.mode })) } } })
    if (folder) return route.fulfill({ json: { version: 2, kind: 'localai-agent', agent: { ...agent, folder: { path: folder.path || '', mode: folder.mode } } } })
    return route.fulfill({ json: { version: 1, kind: 'localai-agent', agent } })
  })
  await page.route(/\/api\/chats\/agents\/[^/?]+\/runs(\?.*)?$/, route => {
    const id = new URL(route.request().url()).pathname.split('/')[4]
    const mine = chats.filter(c => c.agent_id === id)
    return route.fulfill({
      json: {
        runs: mine.map(c => ({ chat_id: c.id, title: c.title, model: c.model, origin: c.origin, origin_id: c.origin_id, created: c.created, updated: c.updated, running: c.running, last_turn: c.last_turn })),
        total: mine.length, failed: mine.filter(c => c.last_turn === 'failed').length, running: mine.filter(c => c.running).length,
      },
    })
  })

  // Chats.
  await page.route(/\/api\/chats(\?.*)?$/, route => {
    const req = route.request()
    if (req.method() === 'POST') {
      const b = req.postDataJSON()
      rec(req)
      // A chat with an agent whose folder is missing or not chosen does not start.
      const agent = s.agents.find(a => a.id === b.agent_id)
      if (agent?.folder && agent.folder.state !== 'ok') {
        return route.fulfill({ status: 409, json: { error: { message: 'The folder of this agent is missing. Choose a folder in the agent\'s settings.', type: 'agent_folder_missing', code: 409 } } })
      }
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
