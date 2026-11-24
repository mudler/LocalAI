// Stubs for the project sharing specs, on top of the Projects stub of
// projects-fixtures.js: the projects shared with the person (list, one
// project, its memory), the folder-share routes of the owner's Share dialog,
// the change proposals of the owner, and a named session. `s.shared.calls`
// lists every request to the shared routes.
import { mockProjects } from './projects-fixtures.js'

const NOW = Date.now()
const H = 3600 * 1000
const json = (route, body, status = 200) => route.fulfill({ status, json: body })

// Cross-owner data. The extra fields are what the server must never send:
// the page must not draw them, so a spec can look for them in the page text.
export const LEAK_ID = 'u-9f3a77c1-owner-account'
export const LEAK_PATH = '/users/u-9f3a77c1/Projects/brand-guide'

export const SHARED = [
  { ref: 's:12', name: 'Brand guide', description: 'Voice, colours and logo rules for every client site.', status: 'active', mode: 'read', owner_name: 'Dana Reyes', owner_id: LEAK_ID, path: LEAK_PATH },
  { ref: 's:14', name: 'Launch checklist', description: 'Steps, owners and dates for the January launch.', status: 'active', mode: 'write', owner_name: 'Sam Okafor', owner_id: LEAK_ID, path: LEAK_PATH },
  { ref: 's:15', name: 'Podcast episode 14', description: 'Show notes and a transcript to clean up.', status: 'archived', mode: 'read', owner_name: 'Dana Reyes', owner_id: LEAK_ID, path: LEAK_PATH },
  { ref: 's:16', name: 'Old plan', description: '', status: 'broken', mode: 'read', owner_name: 'Sam Okafor', error: 'The project cannot be read.', owner_id: LEAK_ID },
]

export const SHARED_MEMORY = {
  index: '# Brand guide\n\nVoice: **plain**, short sentences.\n\n![tracker](https://tracker.example.test/pixel.png)\n\n- Primary colour is a quiet blue.\n',
  files: ['brand-voice.md', 'logo-rules.md'],
}

// A memory index that tries every way to make the browser fetch an address
// the owner chose. The text around it must still show.
export const EVIL = 'https://evil.example'
export const HOSTILE_MEMORY = {
  index: [
    '# Hostile index', '', 'Safe text stays visible.', '',
    `<img src="${EVIL}/img.png" onerror="fetch('${EVIL}/onerror')">`,
    `<picture><source srcset="${EVIL}/picture.png"><img src="${EVIL}/picture-img.png"></picture>`,
    `<video poster="${EVIL}/poster.png" src="${EVIL}/video.mp4"></video>`,
    `<svg><image href="${EVIL}/svg.png"/></svg>`,
    `<p style="background:url(${EVIL}/style.png)">Styled text stays visible.</p>`,
    `<style>body{background:url(${EVIL}/css.png)}</style>`,
    `<iframe src="${EVIL}/frame"></iframe>`,
    `![md](${EVIL}/markdown.png)`,
    `[a link](javascript:alert(1)) and [relative](//evil.example/x)`,
  ].join('\n'),
  files: ['topic.md'],
}

export const FOLDER_ID = 42
export const OWNER_SLUG = 'client-site-redesign'

// A named session. sharing false is a server where folders cannot be shared.
export async function signIn(page, { authEnabled = true } = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: authEnabled
      ? { authEnabled: true, user: { id: 'u-alice', name: 'Alice', role: 'user', permissions: { chats: true } } }
      : { authEnabled: false },
  }))
}

// The folder-share routes over the project folder, with the requests seen.
export function mockFolderShares(page, { sharing = true, grants = [{ userId: 'u-bob', displayName: 'Bob', role: 'read' }] } = {}) {
  const state = { grants: [...grants], seen: [], found: [] }
  const folder = () => ({ id: FOLDER_ID, name: OWNER_SLUG, path: `/Projects/${OWNER_SLUG}`, grants: state.grants })
  page.route(/\/api\/chats\/files\/shares(\/.*)?(\?.*)?$/, (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/chats/files/shares', '')
    state.seen.push({ method: req.method(), path, confirm: req.headers()['x-localai-confirm'] || '', body: req.postData() ? req.postDataJSON() : null })
    if (path === '' && req.method() === 'GET') {
      const folders = state.grants.length > 0 ? [{ id: FOLDER_ID, name: OWNER_SLUG, path: `/Projects/${OWNER_SLUG}`, sharedWith: state.grants.length, everyone: false }] : []
      return json(route, { sharing, folders })
    }
    if (path === '' && req.method() === 'POST') {
      state.found.push(req.postDataJSON().path)
      return json(route, { id: FOLDER_ID, name: OWNER_SLUG, path: req.postDataJSON().path, sharedWith: state.grants.length, everyone: false })
    }
    if (path === `/${FOLDER_ID}/users`) return json(route, { users: [{ id: 'u-bob', displayName: 'Bob' }, { id: 'u-carol', displayName: 'Carol' }] })
    const m = path.match(/^\/42\/grants(?:\/(.+))?$/)
    if (!m) return json(route, { error: { message: 'you cannot share this folder', type: 'authorization_error' } }, 403)
    if (m[1]) {
      const who = decodeURIComponent(m[1])
      const names = { 'u-bob': 'Bob', 'u-carol': 'Carol', '@everyone': '' }
      state.grants = state.grants.filter(g => g.userId !== who)
      if (req.method() === 'PUT') state.grants = [...state.grants, { userId: who, displayName: names[who] ?? who, role: req.postDataJSON().role }]
    }
    return json(route, folder())
  })
  return state
}

// The projects shared with the person. Install it after mockProjects, whose
// project route would otherwise take "shared" for a project id.
export async function mockShared(page, s, { shared = SHARED, memory = SHARED_MEMORY } = {}) {
  s.shared = { pullFail: false, list: structuredClone(shared), memory, calls: [], fail: false, gone: new Set(), memoryFail: false }
  await page.route(/\/api\/chats\/projects\/shared(\/.*)?(\?.*)?$/, (route) => {
    const req = route.request()
    const rest = new URL(req.url()).pathname.replace('/api/chats/projects/shared', '').split('/').filter(Boolean)
    s.shared.calls.push({ method: req.method(), path: new URL(req.url()).pathname })
    if (s.shared.fail) return json(route, { error: { message: 'database is locked' } }, 500)
    if (rest.length === 0) return json(route, { projects: s.shared.list })
    const ref = decodeURIComponent(rest[0])
    const p = s.shared.list.find(x => x.ref === ref)
    if (!p || s.shared.gone.has(ref)) return json(route, { error: { message: 'project not found' } }, 404)
    if (rest[1] === 'memory') {
      if (s.shared.memoryFail) return json(route, { error: { message: 'boom' } }, 500)
      return p.status === 'active' ? json(route, s.shared.memory) : json(route, { error: { message: 'project not found' } }, 404)
    }
    return json(route, p)
  })
  // A new chat takes the shared ref the way the chat routes of a listed chat do.
  await page.route(/\/api\/chats\/c-new\/projects$/, (route) => {
    const req = route.request()
    const b = req.postDataJSON()
    s.calls.push({ method: req.method(), path: new URL(req.url()).pathname, body: b })
    if (s.shared.pullFail) return json(route, { error: { message: 'a turn is running' } }, 409)
    return json(route, { id: 'c-new', title: '', model: 'qwen3-8b', kind: 'chat', project_id: '', pulled_projects: b.project_ids, messages: [], updated: new Date().toISOString() })
  })
}

export async function installSharing(page, { projectsOpts, sharedOpts } = {}) {
  const s = await mockProjects(page, projectsOpts)
  // After mockProjects: its home stub also answers the auth status.
  await signIn(page)
  await mockShared(page, s, sharedOpts)
  s.folderShares = mockFolderShares(page)
  return s
}

// A proposal another person made for this person's project.
export const PROPOSAL = {
  v: 1, id: 'cp1', kind: 'memory', scope: 'project', target: `Projects/${OWNER_SLUG}/memory/brand-voice.md`,
  summary: 'Brand voice', content: '# Brand voice\nPlain, short sentences. No jargon.\n', status: 'pending',
  proposer: 'Dana Reyes', source: { kind: '' }, created: new Date(NOW - H).toISOString(), expires: new Date(NOW + 22 * H).toISOString(),
}

// The proposal routes: the pending list, one proposal, and Confirm. voided
// makes Confirm answer 409 and settle the proposal as stale.
export async function mockProposals(page, { proposal = PROPOSAL, voided = false } = {}) {
  const s = { proposal: { ...proposal }, calls: [] }
  await page.route(/\/api\/chats\/changes(\/.*)?(\?.*)?$/, (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    s.calls.push({ method: req.method(), path })
    if (path === '/api/chats/changes') return json(route, s.proposal.status === 'pending' ? [s.proposal] : [])
    if (path.endsWith('/confirm') && voided) {
      s.proposal = { ...s.proposal, status: 'stale' }
      return json(route, { error: { message: 'studio: Dana Reyes can no longer edit this project (the grant ended). Nothing was written.' } }, 409)
    }
    return json(route, s.proposal)
  })
  return s
}
