// Stubs for the tools a project attaches and for its own skills, on top of the
// Projects API of projects-fixtures.js: the server's refusal of an attached
// entry outside a non-empty limit, the skills of the project layer (list,
// read, create, update, delete), and the lists a chat of the project reads.
import { mockProjects, PROJECTS } from './projects-fixtures.js'

const NOW = Date.now()
const json = (route, body, status = 200) => route.fulfill({ status, json: body })

// p1 attaches one tool of each kind, all inside its limit; p2 has no limit and
// no attachment; p3 is archived; p9 is a project whose folder has another writer.
export const ATTACHED_PROJECTS = [
  { ...PROJECTS[0], attached: { skills: ['brand-voice'], addons: ['mail-addon'], mcp_servers: ['docs'], connectors: ['mail'], collections: ['papers'] } },
  { ...PROJECTS[1] },
  { ...PROJECTS[2] },
  { id: 'p9', name: 'Shared garden', description: 'Planting notes.', slug: 'shared-garden', status: 'active', defaults: {}, attached: {}, skills_blocked: true, attached_blocked: true, updated: new Date(NOW).toISOString(), chat_count: 0 },
]

export const PROJECT_SKILLS = [
  { name: 'house-style', layer: 'project', project: 'p1', description: 'Plain words, short sentences.', editable: true, enabled: true },
  { name: 'launch-checklist', layer: 'project', project: 'p1', description: 'Steps before a launch.', editable: true, enabled: true },
]

// The rule of the server: a non-empty limit must hold every attached entry.
export function outsideMessage(body, current) {
  if (!body.attached) return ''
  const limit = body.defaults || current?.defaults || {}
  for (const key of ['skills', 'addons', 'mcp_servers', 'connectors', 'collections']) {
    const cap = limit[key] || []
    if (cap.length === 0) continue
    const bad = (body.attached[key] || []).find(v => !cap.includes(v))
    if (bad) return `attached ${key} entry "${bad}" is outside the limit of this project (${cap.join(', ')})`
  }
  return ''
}

export async function mockAttached(page, { projects = ATTACHED_PROJECTS, skills = PROJECT_SKILLS, ...rest } = {}) {
  const s = await mockProjects(page, { projects, ...rest })
  s.projectSkills = structuredClone(skills)
  s.skillWrites = []
  const body = (req) => { try { return req.postDataJSON() } catch { return null } }

  // The server's check on write. Registered after mockProjects, so it runs first.
  await page.route(/\/api\/chats\/projects(\/[^/?]+)?(\?.*)?$/, (route) => {
    const req = route.request()
    if (!['POST', 'PATCH'].includes(req.method())) return route.fallback()
    const b = body(req)
    const id = decodeURIComponent(new URL(req.url()).pathname.split('/')[4] || '')
    const msg = outsideMessage(b || {}, s.projects.find(p => p.id === id))
    if (!msg) return route.fallback()
    s.calls.push({ method: req.method(), path: new URL(req.url()).pathname, body: b, refused: true })
    return json(route, { error: { message: msg } }, 400)
  })

  // The skills of the project layer.
  await page.route(/\/api\/chats\/skills(\?.*)?$/, (route) => {
    const req = route.request()
    const url = new URL(req.url())
    if (req.method() === 'POST') {
      const b = body(req)
      s.skillWrites.push({ method: 'POST', path: url.pathname, query: url.search, body: b })
      s.projectSkills.push({ name: b.name, layer: 'project', project: b.project, description: b.description, body: b.body, editable: true, enabled: true })
      return json(route, { name: b.name, layer: b.layer }, 201)
    }
    if (url.searchParams.get('layer') === 'project') {
      return json(route, { skills: s.projectSkills.filter(k => k.project === url.searchParams.get('project')) })
    }
    return route.fallback()
  })
  await page.route(/\/api\/chats\/skills\/project\/[^/?]+(\?.*)?$/, (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const name = decodeURIComponent(url.pathname.split('/').at(-1))
    const found = s.projectSkills.find(k => k.name === name && k.project === url.searchParams.get('project'))
    if (req.method() === 'GET') return found ? json(route, { ...found, body: found.body || 'Steps.' }) : json(route, { error: { message: 'skill not found' } }, 404)
    s.skillWrites.push({ method: req.method(), path: url.pathname, query: url.search, body: body(req) })
    if (req.method() === 'DELETE') s.projectSkills = s.projectSkills.filter(k => k !== found)
    if (req.method() === 'PUT' && found) Object.assign(found, body(req))
    return json(route, { ok: true })
  })
  return s
}
