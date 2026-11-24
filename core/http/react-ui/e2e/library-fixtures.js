// Shared fixtures for the Memory specs: stubbed API routes for the skills, the
// collections and the saved agents that use them. The stubs keep the agents in
// memory, so a save changes what the next read returns, as the real server does.

export const SKILLS = [
  { name: 'summarise-pdf', description: 'Summarise a PDF into a short brief with page references.', content: 'Read the file the user attached. Write five lines, each with a page number.\n'.repeat(6), license: 'MIT', readOnly: false },
  { name: 'query-database', description: 'Answer questions from the reporting database.', content: 'Use the read-only query tool. Never write.\n'.repeat(4), readOnly: false },
  { name: 'triage-issue', description: 'Sort an incoming issue by area and urgency.', content: 'Label the issue with one area and one urgency.\n'.repeat(3), readOnly: false },
  { name: 'translate-document', description: 'Translate a document and keep the layout.', content: 'Translate the text. Keep headings and lists.\n'.repeat(8), readOnly: false },
  { name: 'weekly-digest', description: 'Collect the week into a digest.', content: 'Collect what changed.\n'.repeat(2), readOnly: true },
]

// Saved agents of the chats: exact lists of the skills and collections each may use.
export const AGENTS = [
  { id: 'a1', name: 'research-assistant', description: '', instructions: 'You are a careful research assistant. Search first, read two sources, then summarise.', model: 'qwen3-14b-instruct', mcp_servers: [], addons: [], collections: [], skills: ['summarise-pdf', 'query-database'] },
  { id: 'a2', name: 'handbook', description: '', instructions: 'You answer questions about staff policies.', model: 'qwen3-8b-instruct', mcp_servers: [], addons: [], collections: ['handbook'], skills: ['summarise-pdf'] },
  { id: 'a3', name: 'support-desk', description: '', instructions: 'You help customers.', model: 'qwen3-8b-instruct', mcp_servers: [], addons: [], collections: [], skills: ['triage-issue'] },
  { id: 'a4', name: 'idle-agent', description: '', instructions: 'Idle.', model: 'qwen3-8b-instruct', mcp_servers: [], addons: [], collections: [], skills: [] },
]

export const COLLECTIONS = ['handbook', 'release-notes', 'meeting-notes']

export const PASSAGES = [
  { content: 'Remote work. Employees may work remotely up to three days per week with manager approval.', similarity: 0.74, metadata: { filename: 'employee-handbook.pdf' } },
  { content: 'Connecting from outside the office requires the approved VPN client. Install it from the IT portal.', similarity: 0.69, metadata: { filename: 'employee-handbook.pdf' } },
  { content: 'If the VPN drops every few minutes, switch the protocol to TCP in the client settings.', similarity: 0.51, metadata: { filename: 'vpn-setup.md' } },
]

const clone = value => JSON.parse(JSON.stringify(value))

// Stub every endpoint the two pages read or write. Returns what the page sent
// and the live state, so a spec can assert on both.
export async function mockLibrary(page, {
  skills = SKILLS,
  agents = AGENTS,
  collections = COLLECTIONS,
  entries = { handbook: ['employee-handbook.pdf', 'vpn-setup.md'] },
  sources = { handbook: [{ url: 'https://docs.example.org/guide', update_interval: 60, last_update: '2026-10-05T09:30:00Z' }] },
  passages = PASSAGES,
  uploadError = null,
  agentsStatus = 200,
  features = { distributed: false, localai_assistant: true, collections: true, mcp: true },
} = {}) {
  const state = { agents: clone(agents), collections: [...collections], entries: clone(entries), sources: clone(sources) }
  const seen = { saves: [], searches: [], uploads: [], sourcePosts: [], creates: [], resets: [] }
  const json = (route, body, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })

  await page.route('**/api/features', route => json(route, features))

  // Saved agents of the chats: list and save.
  await page.route('**/api/chats/agents', route => {
    if (agentsStatus !== 200) return json(route, { error: 'boom' }, agentsStatus)
    return json(route, { agents: state.agents })
  })
  await page.route(/\/api\/chats\/agents\/[^/?]+$/, route => {
    const id = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[4])
    if (route.request().method() !== 'PUT') return route.fallback()
    const body = route.request().postDataJSON()
    seen.saves.push({ name: body.name, body })
    state.agents = state.agents.map(a => (a.id === id ? { ...a, ...body, id } : a))
    return json(route, state.agents.find(a => a.id === id))
  })

  // Skills of the chats.
  await page.route(/\/api\/chats\/skills(\?.*)?$/, route => json(route, {
    skills: skills.map(sk => ({ name: sk.name, description: sk.description, layer: 'personal', path: `/skills/${sk.name}`, enabled: true, shadowed: '', invalid: '' })),
  }))

  // Collections.
  await page.route(/\/api\/collections(\?.*)?$/, route => {
    const req = route.request()
    if (req.method() === 'POST') {
      const body = req.postDataJSON()
      seen.creates.push(body.name)
      state.collections.push(body.name)
      return json(route, { status: 'ok', name: body.name }, 201)
    }
    return json(route, { collections: state.collections, count: state.collections.length })
  })
  await page.route(/\/api\/collections\/[^/]+\/entries(\?.*)?$/, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[3])
    return json(route, { entries: state.entries[name] || [], count: (state.entries[name] || []).length })
  })
  await page.route(/\/api\/collections\/[^/]+\/entries\/.+/, route => json(route, { content: 'Full text of the entry.', chunk_count: 12 }))
  await page.route(/\/api\/collections\/[^/]+\/sources(\?.*)?$/, route => {
    const req = route.request()
    const name = decodeURIComponent(new URL(req.url()).pathname.split('/')[3])
    if (req.method() === 'POST') {
      const body = req.postDataJSON()
      seen.sourcePosts.push({ name, ...body })
      state.sources[name] = [...(state.sources[name] || []), { url: body.url, update_interval: body.update_interval || 0, last_update: '0001-01-01T00:00:00Z' }]
      return json(route, { status: 'ok' })
    }
    if (req.method() === 'DELETE') {
      const body = req.postDataJSON()
      state.sources[name] = (state.sources[name] || []).filter(s => s.url !== body.url)
      return json(route, { status: 'ok' })
    }
    return json(route, { sources: state.sources[name] || [], count: (state.sources[name] || []).length })
  })
  await page.route(/\/api\/collections\/[^/]+\/search(\?.*)?$/, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[3])
    const body = route.request().postDataJSON()
    seen.searches.push({ name, ...body })
    return json(route, { results: passages.slice(0, body.max_results || passages.length), count: passages.length })
  })
  await page.route(/\/api\/collections\/[^/]+\/upload(\?.*)?$/, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[3])
    seen.uploads.push(name)
    if (uploadError) return json(route, { error: uploadError }, 409)
    state.entries[name] = [...(state.entries[name] || []), 'uploaded.txt']
    return json(route, { status: 'ok', filename: 'uploaded.txt', key: 'uploaded.txt' })
  })
  await page.route(/\/api\/collections\/[^/]+\/reset(\?.*)?$/, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[3])
    seen.resets.push(name)
    state.entries[name] = []
    return json(route, { status: 'ok' })
  })

  return { seen, state }
}
