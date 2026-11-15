// Stubs for the agents of a project: the Projects API of projects-fixtures.js,
// the saved-agents API of agents-fixtures.js and the routes that are new with
// project agents (the list of a project's agents, an import that answers 409
// project_required, a save that clears the folders-changed flag). `s.calls`
// holds what the pages sent; `s.imports` the import requests with their query.
import { mockProjects } from './projects-fixtures.js'
import { installAgents } from './agents-fixtures.js'

const NOW = Date.now()
const ago = (ms) => new Date(NOW - ms).toISOString()
const H = 3600 * 1000

// p1 limits its agents (its lists are ceilings), p2 sets no limit, p3 is archived.
export const PROJECTS = [
  { id: 'p1', name: 'Client site redesign', description: 'Copy and assets.', slug: 'client-site-redesign', status: 'active', defaults: { skills: ['citations'], mcp_servers: ['crm'], connectors: ['m1'], collections: ['handbook'] }, updated: ago(2 * H), chat_count: 0, agent_count: 3 },
  { id: 'p2', name: 'Home lab notes', description: 'Cluster notes.', slug: 'home-lab-notes', status: 'active', defaults: {}, updated: ago(26 * H), chat_count: 0, agent_count: 0 },
  { id: 'p3', name: 'Podcast episode 14', description: 'Show notes.', slug: 'podcast-episode-14', status: 'archived', defaults: {}, updated: ago(96 * H), chat_count: 0, agent_count: 1 },
]

const roots = (slug, mode) => [
  { path: `/Projects/${slug}/files`, name: 'files', mode, state: 'ok' },
  { path: `/Projects/${slug}/memory`, name: 'memory', mode: 'read', state: 'ok' },
]
const owner = { role: 'owner', sharing: true }
const base = { mcp_servers: [], addons: [], collections: [], skills: [], network: 'off', access: owner }

export const AGENTS = [
  { ...base, id: 'ap1', name: 'Copy editor', description: 'Tightens page copy.', model: 'qwen3-8b', instructions: 'Edit copy.', project_id: 'p1', builtins: ['project.memory.write'], folders: roots('client-site-redesign', 'write'), folder: roots('client-site-redesign', 'write')[0], skills: ['citations'] },
  { ...base, id: 'ap2', name: 'Asset checker', description: 'Lists missing alt text.', model: 'qwen3-8b', instructions: 'Check assets.', project_id: 'p1', folders: roots('client-site-redesign', 'read'), folder: roots('client-site-redesign', 'read')[0] },
  { ...base, id: 'ap3', name: 'Client questions', description: 'Collects open questions.', model: 'llama-3.1-8b', instructions: 'Collect.', project_id: 'p1', folders: roots('client-site-redesign', 'read'), folder: roots('client-site-redesign', 'read')[0] },
  { ...base, id: 'ap4', name: 'Podcast notes', description: 'Cleans up a transcript.', model: 'qwen3-8b', instructions: 'Show notes.', project_id: 'p3', folders: roots('podcast-episode-14', 'read'), folder: roots('podcast-episode-14', 'read')[0] },
  { ...base, id: 'a9', name: 'Daily digest', description: 'Summarises the news.', model: 'qwen3-8b', instructions: 'Summarise.' },
]

const filesModeOf = (a) => ((a.folders || []).find(r => r.path && !/\/memory$/.test(r.path))?.mode === 'read' ? 'read' : 'write')

// The file of a project agent as an export writes it (format v5).
export const AGENT_FILE = {
  version: 5, kind: 'localai-agent',
  projectRef: { id: 'old-id', name: 'Triage', slug: 'triage' },
  agent: { name: 'Triager', description: 'Sorts the inbox.', instructions: 'Sort.', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [] },
}

export async function installProjectAgents(page, { projects = PROJECTS, agents = AGENTS, flagged = ['ap3'] } = {}) {
  const ps = await mockProjects(page, { projects, chats: [] })
  const a = await installAgents(page, { agents, chats: [] })
  const s = { projects: ps, agents: a, flagged: new Set(flagged), imports: [], calls: [] }
  const body = (req) => { try { return req.postDataJSON() } catch { return null } }

  // The agents of one project, as the server lists them.
  await page.route(/\/api\/chats\/projects\/[^/?]+\/agents(\?.*)?$/, (route) => {
    const id = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[4])
    const p = ps.projects.find(x => x.id === id)
    if (!p) return route.fulfill({ status: 404, json: { error: { message: 'project not found' } } })
    const rows = a.agents.filter(x => x.project_id === id).map(x => ({
      id: x.id, name: x.name, description: x.description, model: x.model,
      unavailable: p.status !== 'active' || s.flagged.has(x.id), files_mode: filesModeOf(x), memory_write: (x.builtins || []).includes('project.memory.write'),
    }))
    return route.fulfill({ json: { agents: rows } })
  })

  // A save clears the folders-changed flag (the server links the folders
  // again), then the saved-agents stub applies it.
  await page.route(/\/api\/chats\/agents\/[^/?]+$/, (route) => {
    const req = route.request()
    if (req.method() === 'PUT') {
      const id = new URL(req.url()).pathname.split('/').pop()
      s.calls.push({ method: 'PUT', id, body: body(req) })
      s.flagged.delete(id)
    }
    return route.fallback()
  })

  // An import: a v5 file needs a project in the query, as the server does. The
  // request URL and body are recorded so a spec can compare two sends.
  await page.route(/\/api\/chats\/agents\/import(\?.*)?$/, (route) => {
    const req = route.request()
    const u = new URL(req.url())
    const b = body(req)
    s.imports.push({ search: u.search, query: Object.fromEntries(u.searchParams), body: b, raw: req.postData() })
    if (!b?.agent?.name) return route.fulfill({ status: 400, json: { error: { message: 'the body is not a valid agent file' } } })
    if (b.version === 5 && !u.searchParams.get('project_id') && u.searchParams.get('create_project') !== 'true') {
      return route.fulfill({ status: 409, json: { error: { type: 'project_required', message: 'This agent belongs to a project. Choose one.', project_name: b.projectRef?.name || '' } } })
    }
    const agent = { ...base, id: `ai${s.imports.length}`, ...b.agent, project_id: u.searchParams.get('project_id') || 'p-new' }
    a.agents.push(agent)
    return route.fulfill({ status: 201, json: { agent } })
  })
  return s
}
