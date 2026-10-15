// Shared fixtures for the Skills and Memory specs: stubbed API routes for
// skills, git repositories, collections and the agents that use them. The
// stubs keep the agent configs in memory, so a save changes what the next
// read returns, as the real server does.

export const SKILLS = [
  { name: 'summarise-pdf', description: 'Summarise a PDF into a short brief with page references.', content: 'Read the file the user attached. Write five lines, each with a page number.\n'.repeat(6), license: 'MIT', readOnly: false },
  { name: 'query-database', description: 'Answer questions from the reporting database.', content: 'Use the read-only query tool. Never write.\n'.repeat(4), readOnly: false },
  { name: 'triage-issue', description: 'Sort an incoming issue by area and urgency.', content: 'Label the issue with one area and one urgency.\n'.repeat(3), readOnly: false },
  { name: 'translate-document', description: 'Translate a document and keep the layout.', content: 'Translate the text. Keep headings and lists.\n'.repeat(8), readOnly: false },
  { name: 'weekly-digest', description: 'Collect the week into a digest.', content: 'Collect what changed.\n'.repeat(2), readOnly: true },
]

export const AGENT_CONFIGS = {
  'research-assistant': {
    name: 'research-assistant', model: 'qwen3-14b-instruct', system_prompt: 'You are a careful research assistant. Search first, read two sources, then summarise.',
    enable_skills: true, selected_skills: ['summarise-pdf', 'query-database'], enable_kb: false, skills_mode: 'prompt',
  },
  handbook: {
    name: 'handbook', model: 'qwen3-8b-instruct', system_prompt: 'You answer questions about staff policies.',
    enable_skills: true, selected_skills: ['summarise-pdf'], enable_kb: true, kb_mode: 'auto_search', kb_results: 3,
  },
  'support-desk': {
    name: 'support-desk', model: 'qwen3-8b-instruct', system_prompt: 'You help customers.',
    enable_skills: true, selected_skills: ['triage-issue'], enable_kb: false, skills_mode: 'tools',
  },
  'idle-agent': {
    name: 'idle-agent', model: 'qwen3-8b-instruct', system_prompt: 'Idle.', enable_skills: false, enable_kb: false,
  },
}

export const COLLECTIONS = ['handbook', 'release-notes', 'meeting-notes']

export const PASSAGES = [
  { content: 'Remote work. Employees may work remotely up to three days per week with manager approval.', similarity: 0.74, metadata: { filename: 'employee-handbook.pdf' } },
  { content: 'Connecting from outside the office requires the approved VPN client. Install it from the IT portal.', similarity: 0.69, metadata: { filename: 'employee-handbook.pdf' } },
  { content: 'If the VPN drops every few minutes, switch the protocol to TCP in the client settings.', similarity: 0.51, metadata: { filename: 'vpn-setup.md' } },
]

const META = {
  Fields: [
    { name: 'name', label: 'Name', type: 'text', required: true, tags: { section: 'BasicInfo' } },
    { name: 'model', label: 'Model', type: 'text', required: true, tags: { section: 'ModelSettings' } },
    { name: 'enable_kb', label: 'Knowledge base', type: 'checkbox', tags: { section: 'MemorySettings' } },
    { name: 'system_prompt', label: 'System prompt', type: 'textarea', tags: { section: 'PromptsGoals' } },
    { name: 'enable_skills', label: 'Skills', type: 'checkbox', tags: { section: 'AdvancedSettings' } },
    { name: 'skills_mode', label: 'Skills mode', type: 'text', tags: { section: 'AdvancedSettings' } },
  ],
  Connectors: [], Actions: [], Filters: [], DynamicPrompts: [],
}

const clone = value => JSON.parse(JSON.stringify(value))

// Stub every endpoint the two pages read or write. Returns what the page sent
// and the live state, so a spec can assert on both.
export async function mockLibrary(page, {
  skills = SKILLS,
  agents = AGENT_CONFIGS,
  collections = COLLECTIONS,
  entries = { handbook: ['employee-handbook.pdf', 'vpn-setup.md'] },
  sources = { handbook: [{ url: 'https://docs.example.org/guide', update_interval: 60, last_update: '2026-10-05T09:30:00Z' }] },
  passages = PASSAGES,
  uploadError = null,
  skillsStatus = 200,
  agentsStatus = 200,
  repos = [],
  resources = { scripts: [{ path: 'scripts/extract.py', size: 1200 }], references: [{ path: 'references/style.md', size: 300 }], assets: [] },
  features = { distributed: false, localai_assistant: true, agents: true, skills: true, collections: true, mcp: true },
} = {}) {
  const state = { agents: clone(agents), collections: [...collections], entries: clone(entries), sources: clone(sources), repos: clone(repos) }
  const seen = { saves: [], searches: [], uploads: [], sourcePosts: [], creates: [], resets: [], imports: 0 }
  const json = (route, body, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })

  await page.route('**/api/features', route => json(route, features))
  await page.route('**/api/agents/config/metadata', route => json(route, META))

  // Agents: list, config and save.
  await page.route(/\/api\/agents(\?.*)?$/, route => {
    if (agentsStatus !== 200) return json(route, { error: 'boom' }, agentsStatus)
    return json(route, { agents: Object.keys(state.agents), statuses: Object.fromEntries(Object.keys(state.agents).map(n => [n, true])) })
  })
  await page.route(/\/api\/agents\/[^/?]+\/config(\?.*)?$/, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[3])
    return state.agents[name] ? json(route, state.agents[name]) : json(route, { error: 'Agent not found' }, 404)
  })
  await page.route(/\/api\/agents\/[^/?]+(\?.*)?$/, async route => {
    const url = new URL(route.request().url())
    const name = decodeURIComponent(url.pathname.split('/')[3])
    if (['skills', 'collections', 'git-repos', 'config', 'import', 'actions'].includes(name)) return route.fallback()
    if (route.request().method() === 'PUT') {
      const body = route.request().postDataJSON()
      seen.saves.push({ name, body })
      state.agents[name] = body
      return json(route, { status: 'ok' })
    }
    return state.agents[name] ? json(route, { active: true }) : json(route, { error: 'Agent not found' }, 404)
  })

  // Skills.
  await page.route(/\/api\/agents\/skills(\?.*)?$/, route => {
    if (skillsStatus !== 200) return json(route, { error: 'skills unavailable' }, skillsStatus)
    return json(route, { skills })
  })
  await page.route('**/api/agents/skills/search?**', route => {
    const q = (new URL(route.request().url()).searchParams.get('q') || '').toLowerCase()
    return json(route, skills.filter(s => s.name.includes(q) || (s.description || '').toLowerCase().includes(q)))
  })
  await page.route(/\/api\/agents\/skills\/[^/]+\/resources(\?.*)?$/, route => json(route, { ...resources, readOnly: false }))
  await page.route('**/api/agents/skills/import', route => { seen.imports += 1; return json(route, { status: 'ok' }) })
  await page.route(/\/api\/agents\/git-repos(\/.*)?(\?.*)?$/, route => {
    const req = route.request()
    if (req.method() === 'POST' && !req.url().includes('/sync') && !req.url().includes('/toggle')) {
      const body = req.postDataJSON()
      state.repos.push({ id: `r${state.repos.length + 1}`, url: body.url, name: body.url.split('/').pop(), enabled: true })
      return json(route, { status: 'ok' })
    }
    return json(route, req.method() === 'GET' ? state.repos : { status: 'ok' })
  })

  // Collections.
  await page.route(/\/api\/agents\/collections(\?.*)?$/, route => {
    const req = route.request()
    if (req.method() === 'POST') {
      const body = req.postDataJSON()
      seen.creates.push(body.name)
      state.collections.push(body.name)
      return json(route, { status: 'ok', name: body.name }, 201)
    }
    return json(route, { collections: state.collections, count: state.collections.length })
  })
  await page.route(/\/api\/agents\/collections\/[^/]+\/entries(\?.*)?$/, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[4])
    return json(route, { entries: state.entries[name] || [], count: (state.entries[name] || []).length })
  })
  await page.route(/\/api\/agents\/collections\/[^/]+\/entries\/.+/, route => json(route, { content: 'Full text of the entry.', chunk_count: 12 }))
  await page.route(/\/api\/agents\/collections\/[^/]+\/sources(\?.*)?$/, route => {
    const req = route.request()
    const name = decodeURIComponent(new URL(req.url()).pathname.split('/')[4])
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
  await page.route(/\/api\/agents\/collections\/[^/]+\/search(\?.*)?$/, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[4])
    const body = route.request().postDataJSON()
    seen.searches.push({ name, ...body })
    return json(route, { results: passages.slice(0, body.max_results || passages.length), count: passages.length })
  })
  await page.route(/\/api\/agents\/collections\/[^/]+\/upload(\?.*)?$/, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[4])
    seen.uploads.push(name)
    if (uploadError) return json(route, { error: uploadError }, 409)
    state.entries[name] = [...(state.entries[name] || []), 'uploaded.txt']
    return json(route, { status: 'ok', filename: 'uploaded.txt', key: 'uploaded.txt' })
  })
  await page.route(/\/api\/agents\/collections\/[^/]+\/reset(\?.*)?$/, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[4])
    seen.resets.push(name)
    state.entries[name] = []
    return json(route, { status: 'ok' })
  })

  return { seen, state }
}
