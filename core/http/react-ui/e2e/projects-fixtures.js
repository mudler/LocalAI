// A stateful stub of the Projects API for the Projects specs: the list, one
// project, its chats and memory, archive and restore, delete, the chat create
// call, and what the chat page reads once a chat opens. `calls` lists every
// request the page made, so a spec can assert on a body.
import { mockChat } from './chat-fixtures.js'

const NOW = Date.now()
const ago = (ms) => new Date(NOW - ms).toISOString()
const H = 3600 * 1000

export const PROJECTS = [
  { id: 'p1', name: 'Client site redesign', description: 'Copy, assets and open questions for the new marketing site.', slug: 'client-site-redesign', status: 'active', defaults: { skills: ['brand-voice'], mcp_servers: ['docs'], connectors: ['mail'], collections: ['papers', 'faq'] }, updated: ago(2 * H), chat_count: 2 },
  { id: 'p2', name: 'Home lab notes', description: 'Cluster upgrades, backups and what broke last month.', slug: 'home-lab-notes', status: 'active', defaults: {}, updated: ago(26 * H), chat_count: 0 },
  { id: 'p3', name: 'Podcast episode 14', description: 'Show notes and a transcript to clean up.', slug: 'podcast-episode-14', status: 'archived', defaults: {}, updated: ago(96 * H), chat_count: 4 },
  { id: 'p4', name: 'Kitchen renovation', description: 'Quotes and measurements.', slug: 'kitchen-renovation', status: 'broken', error: 'The project folder is missing its manifest. Restore Projects/kitchen-renovation/project.json or remove the project.', defaults: {}, updated: ago(240 * H), chat_count: 2 },
]

// What is installed, for the tool-default pickers.
export const SKILLS = [
  { name: 'brand-voice', description: 'Keeps the tone plain.', layer: 'personal', enabled: true },
  { name: 'summarize', description: 'Short summaries of long text.', layer: 'builtin', enabled: true },
]
export const ADDONS = [{ id: 'mail-addon', title: 'Mail', tools: 3 }]
export const CONNECTORS = [
  { id: 'mail', name: 'Work mail', usable: true },
  { id: 'old', name: 'Old archive', usable: false, reason: 'Switched off on the MCP connectors page.' },
]

export const CHATS = [
  { id: 'c1', title: 'Pricing page copy', model: 'qwen3-8b', kind: 'chat', project_id: 'p1', updated: ago(2 * H) },
  { id: 'c2', title: 'Hero image options', model: 'qwen3-8b', kind: 'chat', project_id: 'p1', updated: ago(26 * H) },
]

export const MEMORY = {
  index: '# Client site\n\nBrand voice: **plain**, short sentences.\n\n- Pricing page is the first milestone.\n',
  files: [
    { name: 'brand-voice.md', size: 120, modified: ago(48 * H) },
    { name: 'index.md', size: 90, modified: ago(2 * H) },
  ],
}

const json = (route, body, status = 200) => route.fulfill({ status, json: body })
const err = (route, status, message, type) => route.fulfill({ status, json: { error: { message, ...(type ? { type } : {}) } } })

// Installs the stubs and returns the state: `s.projects`, `s.calls`, and the
// switches `s.failList` (the list answers 500) and `s.off` (409 projects_off).
export async function mockProjects(page, { projects = PROJECTS, chats = CHATS, memory = MEMORY, topics = { 'brand-voice.md': 'Plain words.\n' } } = {}) {
  const s = {
    projects: structuredClone(projects), chats: structuredClone(chats), memory: structuredClone(memory), topics: { ...topics },
    calls: [], failList: false, off: false, created: [], busy: false, listChats: false,
    // What the tool-default pickers read. `failSources` names the sources that answer 500.
    skills: SKILLS, addons: ADDONS, connectors: CONNECTORS, collections: ['papers', 'faq', 'notes'], failSources: [], triggers: [],
  }
  const rec = (req, body) => s.calls.push({ method: req.method(), path: new URL(req.url()).pathname, body: body ?? null })
  const body = (req) => { try { return req.postDataJSON() } catch { return null } }

  await mockChat(page)
  const sourceRoute = (kind, fn) => (r) => (s.failSources.includes(kind) ? err(r, 500, 'unavailable') : fn(r))
  await page.route(/\/api\/chats\/skills(\?.*)?$/, sourceRoute('skills', r => json(r, { skills: s.skills })))
  await page.route(/\/api\/chats\/draft\/tools(\?.*)?$/, sourceRoute('tools', r => json(r, { builtins: [], addons: s.addons, servers: [], connectors: s.connectors, off: [] })))
  await page.route(/\/api\/collections(\?.*)?$/, sourceRoute('collections', r => json(r, { collections: s.collections })))
  await page.route('**/v1/mcp/servers/*', sourceRoute('servers', (r) => {
    const model = decodeURIComponent(new URL(r.request().url()).pathname.split('/').at(-1))
    return json(r, { servers: model === 'qwen3-8b' ? [{ name: 'docs' }, { name: 'search' }] : [] })
  }))
  await page.route('**/api/chats/notifications*', r => json(r, { notifications: [], unread: 0 }))
  await page.route('**/api/chats/agents*', r => json(r, { agents: [] }))
  await page.route('**/api/chats/connectors*', r => json(r, { connectors: [] }))
  await page.route('**/api/workspace/events*', r => r.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body: ': ping\n\n' }))
  await page.route(/\/api\/chats(\?.*)?$/, (r) => {
    const req = r.request()
    if (req.method() === 'POST') {
      const b = body(req)
      rec(req, b)
      const chat = { id: 'c-new', title: '', model: b.model, kind: 'chat', project_id: b.project_id || '', updated: new Date().toISOString(), messages: [] }
      s.created.push(chat)
      return json(r, chat, 201)
    }
    const origin = new URL(req.url()).searchParams.get('origin') || ''
    const conversations = s.listChats && origin !== 'automated' ? s.chats : []
    return json(r, { conversations, persistent: true, files: true })
  })
  await page.route('**/api/chats/c-new*', r => json(r, { id: 'c-new', title: '', model: 'qwen3-8b', kind: 'chat', project_id: 'p1', messages: [], updated: new Date().toISOString() }))
  await page.route('**/api/chats/files/content*', (r) => {
    const path = new URL(r.request().url()).searchParams.get('path') || ''
    const name = path.split('/').at(-1)
    return r.fulfill({ status: 200, contentType: 'text/plain', body: s.topics[name] ?? '' })
  })

  // The chats of the list: each opens, and the two calls that move a chat
  // between projects change it (409 while s.busy, as the server does during a turn).
  const reply = (r, req, id, change) => {
    rec(req, body(req))
    if (s.busy) return err(r, 409, 'a turn is running')
    const c = s.chats.find(x => x.id === id)
    change(c, body(req))
    return json(r, c)
  }
  for (const c of s.chats) {
    await page.route(`**/api/chats/${c.id}/events`, r => r.abort())
    await page.route(new RegExp(`/api/chats/${c.id}/messages(\\?.*)?$`), r => json(r, { messages: [{ seq: 1, turn: 1, role: 'user', text: 'Draft three short plan blurbs.', at: c.updated }] }))
    await page.route(new RegExp(`/api/chats/${c.id}$`), (r) => {
      const req = r.request()
      if (req.method() === 'DELETE') {
        rec(req, null)
        s.chats = s.chats.filter(x => x.id !== c.id)
        return json(r, { deleted: true })
      }
      return json(r, { ...s.chats.find(x => x.id === c.id), pending: [], running: false })
    })
    await page.route(new RegExp(`/api/chats/${c.id}/project$`), r => reply(r, r.request(), c.id, (chat, b) => { chat.project_id = b.project_id; chat.pulled_projects = (chat.pulled_projects || []).filter(x => x !== b.project_id) }))
    await page.route(new RegExp(`/api/chats/${c.id}/projects$`), r => reply(r, r.request(), c.id, (chat, b) => { chat.pulled_projects = b.project_ids }))
  }
  await page.route(/\/api\/chats\/projects(\?.*)?$/, (r) => {
    const req = r.request()
    if (s.off) return err(r, 409, 'Projects are off on this server.', 'projects_off')
    if (req.method() === 'POST') {
      const b = body(req)
      rec(req, b)
      const slug = b.name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
      const p = { id: `p${s.projects.length + 1}`, name: b.name, description: b.description || '', slug, status: 'active', defaults: b.defaults || {}, attached: b.attached || {}, updated: new Date().toISOString(), chat_count: 0 }
      s.projects.push(p)
      return json(r, p, 201)
    }
    if (s.failList) return err(r, 500, 'database is locked')
    return json(r, { projects: s.projects })
  })
  await page.route(/\/api\/chats\/projects\/[^/?]+(\/.*)?(\?.*)?$/, (r) => {
    const req = r.request()
    const parts = new URL(req.url()).pathname.split('/') // '', api, chats, projects, id, ...
    const id = decodeURIComponent(parts[4])
    const rest = parts.slice(5)
    const p = s.projects.find(x => x.id === id)
    rec(req, body(req))
    if (s.off) return err(r, 409, 'Projects are off on this server.', 'projects_off')
    if (!p) return err(r, 404, 'project not found')
    if (rest.length === 0) {
      if (req.method() === 'DELETE') { s.projects = s.projects.filter(x => x.id !== id); return json(r, p) }
      if (req.method() === 'PATCH') { Object.assign(p, body(req), { updated: new Date().toISOString() }); return json(r, p) }
      return json(r, p)
    }
    if (rest[0] === 'archive') { p.status = 'archived'; return json(r, p) }
    if (rest[0] === 'restore') { p.status = 'active'; return json(r, p) }
    if (rest[0] === 'chats') return json(r, { conversations: s.chats.filter(c => c.project_id === id) })
    if (rest[0] === 'triggers') return json(r, { triggers: s.triggers })
    if (rest[0] === 'memory' && rest.length === 1) return json(r, s.memory)
    if (rest[0] === 'memory' && req.method() === 'PUT') {
      const file = decodeURIComponent(rest[1])
      const content = body(req).content
      if (file === 'index.md') s.memory.index = content
      else {
        s.topics[file] = content
        if (!s.memory.files.some(f => f.name === file)) s.memory.files.push({ name: file, size: content.length, modified: new Date().toISOString() })
      }
      return json(r, { name: file, size: content.length, modified: new Date().toISOString() })
    }
    return err(r, 404, 'not found')
  })
  return s
}

export const writes = (s, method, suffix) => s.calls.filter(c => c.method === method && c.path.endsWith(suffix))
