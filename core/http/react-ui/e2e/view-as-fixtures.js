// Fixtures for the View as specs: an administrator named morgan, three people
// who can be viewed (alice, bob, carol), one more administrator, one person
// waiting for approval and one who is disabled. The Users and Projects stubs
// are the ones the other specs use; `seen` lists every API request the page
// made, with its query, so a spec can check where user_id went.
import { mockAccess, usersFixture } from './access-fixtures.js'
import { mockProjects } from './projects-fixtures.js'

export const MORGAN = { id: 'u-morgan', email: 'morgan@lab.example', name: 'morgan', role: 'admin', provider: 'local', permissions: {} }

export function viewAsUsers() {
  const [, bob, carol, dave, erin] = usersFixture()
  const base = { createdAt: bob.createdAt, apiKeyCount: 0, permissions: bob.permissions }
  return [
    { ...base, id: 'u-morgan', email: 'morgan@lab.example', name: 'morgan', role: 'admin', status: 'active', provider: 'local', lastActiveAt: new Date().toISOString() },
    { ...base, id: 'u-riley', email: 'riley@lab.example', name: 'riley', role: 'admin', status: 'active', provider: 'local', lastActiveAt: null },
    { ...base, id: 'u-alice', email: 'alice@lab.example', name: 'alice', role: 'user', status: 'active', provider: 'local', lastActiveAt: bob.lastActiveAt },
    { ...bob, id: 'u-bob', email: 'bob@lab.example', name: 'bob' },
    { ...carol, id: 'u-carol', email: 'carol@lab.example', name: 'carol' },
    { ...dave, id: 'u-dave', email: 'dave@lab.example', name: 'dave' },
    { ...erin, id: 'u-erin', email: 'erin@lab.example', name: 'erin' },
  ]
}

export async function setupViewAs(page) {
  const access = await mockAccess(page, { statusExtra: { user: MORGAN }, users: viewAsUsers() })
  const projects = await mockProjects(page)
  const seen = []
  page.on('request', (req) => {
    const url = new URL(req.url())
    if (url.pathname.startsWith('/api/')) seen.push({ method: req.method(), path: url.pathname, user: url.searchParams.get('user_id') })
  })
  return { access, projects, seen }
}

// The requests to the project routes, with the user_id each carried.
export const projectCalls = (seen, method) => seen.filter(c => c.path.startsWith('/api/chats/projects') && (!method || c.method === method))
