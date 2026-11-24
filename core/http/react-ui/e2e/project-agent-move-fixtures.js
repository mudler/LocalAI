// Stubs for the Project field of an agent and the move of an agent between
// projects: the project agents API of project-agents-fixtures.js plus the move
// route. `s.moves` holds each request with its body; `s.report` is what the
// dry run answers (a spec changes it to show a blocker or an effect).
import { installProjectAgents, AGENTS, PROJECTS } from './project-agents-fixtures.js'

const owner = { role: 'owner', sharing: true }
const base = { mcp_servers: [], addons: [], collections: [], skills: [], network: 'off', access: owner }

// An agent whose project was deleted: the project is not in the list and its
// folders are gone.
export const ORPHAN = {
  ...base, id: 'ap9', name: 'Orphan helper', description: 'Its project was deleted.', model: 'qwen3-8b', instructions: 'Help.',
  project_id: 'gone', folders: [{ mode: 'write', state: 'missing' }, { mode: 'read', state: 'missing' }], folder: { mode: 'write', state: 'missing' },
}

export const REPORT = {
  dry_run: true, moved: false, chats: 0, loops: 0, triggers_outside: [], shares: 0, proposals: 0,
  dropped_tools: { skills: [], addons: [], mcp_servers: [], collections: [] }, memory_grant_dropped: false, blockers: [],
}

export async function installMove(page, { agents = [...AGENTS, ORPHAN], projects = PROJECTS, report = {} } = {}) {
  const s = await installProjectAgents(page, { agents, projects })
  s.moves = []
  s.report = { ...REPORT, ...report }
  // After the agents routes, so it is matched first.
  await page.route(/\/api\/chats\/agents\/[^/?]+\/project$/, (route) => {
    const req = route.request()
    const id = new URL(req.url()).pathname.split('/')[4]
    const body = req.postDataJSON()
    s.moves.push({ id, body })
    const agent = s.agents.agents.find(a => a.id === id)
    if (!agent) return route.fulfill({ status: 404, json: { error: { message: 'agent not found' } } })
    const blocked = (s.report.blockers || []).some(b => (b === 'shared' && !body.revoke_shares) || (b === 'triggers_outside' && !body.disable_triggers) || b === 'busy')
    if (body.dry_run) return route.fulfill({ json: { ...s.report, dry_run: true, from: agent.project_id || '', to: body.project_id } })
    if (blocked) return route.fulfill({ status: 409, json: { error: { type: 'agent_move_blocked', message: 'a file trigger watches a folder outside the new folders' } } })
    const target = s.projects.projects.find(p => p.id === body.project_id)
    agent.project_id = body.project_id || undefined
    if (target) {
      agent.folders = [{ path: `/Projects/${target.slug}/files`, mode: body.files_mode || 'write', state: 'ok' }, { path: `/Projects/${target.slug}/memory`, mode: 'read', state: 'ok' }]
    } else {
      agent.folders = body.whole_home ? [] : (body.folders || []).map(f => ({ ...f, state: 'ok' }))
    }
    agent.folder = agent.folders[0]
    return route.fulfill({ json: { ...s.report, dry_run: false, moved: true, from: '', to: body.project_id } })
  })
  return s
}
