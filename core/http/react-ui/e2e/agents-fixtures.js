// Shared fixtures for the Agents specs: stubbed agent API routes, a fake event
// stream the test drives by hand, and run records written to the browser
// storage the pages read.

export const RUNS_KEY = (agent, userId) => `localai_agent_runs_${userId ? `${userId}:` : ''}${agent}`

export const RESEARCH_CONFIG = {
  name: 'research-assistant',
  description: 'Searches the web, reads sources and writes short briefs.',
  model: 'qwen3-14b-instruct',
  system_prompt: 'You are a careful research assistant. Search first, read at least two sources, then summarise in plain language.',
  actions: [{ name: 'web_search', config: '{}' }, { name: 'read_page', config: '{}' }, { name: 'write_file', config: '{}' }],
  mcp_stdio_servers: JSON.stringify({ mcpServers: { filesystem: { command: 'npx', args: [], env: {} } } }),
  enable_kb: true,
  enable_skills: true,
  selected_skills: ['citations'],
}

export const AGENTS = {
  'research-assistant': { active: true, config: RESEARCH_CONFIG },
  'code-reviewer': {
    active: true,
    config: { name: 'code-reviewer', description: 'Reviews a change and lists what to fix first.', model: 'qwen3-14b-instruct', system_prompt: 'You review code changes.' },
  },
  'daily-digest': {
    active: false,
    config: { name: 'daily-digest', description: 'Collects what is new and summarises it.', model: 'qwen3-8b-instruct', system_prompt: 'You collect news.' },
  },
}

const META = {
  Fields: [
    { name: 'name', label: 'Name', type: 'text', required: true, tags: { section: 'BasicInfo' } },
    { name: 'description', label: 'Description', type: 'textarea', tags: { section: 'BasicInfo' } },
    { name: 'model', label: 'Model', type: 'text', required: true, tags: { section: 'ModelSettings' } },
    { name: 'enable_kb', label: 'Knowledge base', type: 'checkbox', tags: { section: 'MemorySettings' } },
    { name: 'system_prompt', label: 'System prompt', type: 'textarea', tags: { section: 'PromptsGoals' } },
    { name: 'enable_skills', label: 'Skills', type: 'checkbox', tags: { section: 'AdvancedSettings' } },
  ],
  Connectors: [], Actions: [], Filters: [], DynamicPrompts: [],
}

// Replace EventSource with one the test drives: window.__emit(type, data).
export async function installFakeStream(page) {
  await page.addInitScript(() => {
    const sources = []
    class FakeES {
      constructor(url) {
        this.url = url
        this.listeners = {}
        this.readyState = 1
        sources.push(this)
        setTimeout(() => this.onopen && this.onopen({}), 0)
      }
      addEventListener(type, fn) { (this.listeners[type] = this.listeners[type] || []).push(fn) }
      removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter(f => f !== fn) }
      close() { this.readyState = 2; const i = sources.indexOf(this); if (i >= 0) sources.splice(i, 1) }
    }
    window.EventSource = FakeES
    window.__emit = (type, data) => {
      sources.forEach(s => (s.listeners[type] || []).forEach(fn => fn({ data: typeof data === 'string' ? data : JSON.stringify(data) })))
    }
    window.__streams = () => sources.length
  })
}

export function emit(page, type, data) {
  return page.evaluate(([t, d]) => window.__emit(t, d), [type, data])
}

// Stub every agent endpoint the area reads. Returns the requests the page
// sent, so a spec can assert on them.
export async function mockAgents(page, {
  agents = AGENTS,
  observables = {},
  chatStatus = 202,
  chatError = 'agent unavailable',
  features = { distributed: false, localai_assistant: true, agents: true, mcp: true },
  skills = [{ name: 'citations', description: 'Cite sources' }],
} = {}) {
  const seen = { chats: [], saves: [], deletes: [], pauses: [], resumes: [], clears: [] }
  await page.route('**/api/features', route => route.fulfill({ json: features }))
  await page.route('**/api/agents/skills', route => route.fulfill({ json: { skills } }))
  await page.route('**/api/agents/config/metadata', route => route.fulfill({ json: META }))
  await page.route('**/api/agents', async route => {
    const req = route.request()
    if (req.method() === 'POST') {
      seen.saves.push({ method: 'POST', body: req.postDataJSON() })
      return route.fulfill({ json: { status: 'ok' } })
    }
    const statuses = Object.fromEntries(Object.entries(agents).map(([n, a]) => [n, a.active]))
    return route.fulfill({ json: { agents: Object.keys(agents), statuses } })
  })
  await page.route(/\/api\/agents\/[^/?]+(\/[a-z]+)?(\?.*)?$/, async route => {
    const req = route.request()
    const url = new URL(req.url())
    const m = url.pathname.match(/\/api\/agents\/([^/]+)(?:\/([a-z]+))?$/)
    if (!m) return route.fallback()
    const name = decodeURIComponent(m[1])
    const tail = m[2]
    if (['skills', 'collections', 'actions', 'config', 'git-repos', 'import'].includes(name)) return route.fallback()
    const agent = agents[name]
    if (!agent) return route.fulfill({ status: 404, json: { error: 'Agent not found' } })
    if (tail === 'chat' && req.method() === 'POST') {
      seen.chats.push({ name, ...req.postDataJSON() })
      if (chatStatus !== 202) return route.fulfill({ status: chatStatus, json: { error: chatError } })
      return route.fulfill({ status: 202, json: { status: 'message_received', message_id: `m${seen.chats.length}` } })
    }
    if (tail === 'config') return route.fulfill({ json: agent.config })
    if (tail === 'observables') {
      if (req.method() === 'DELETE') { seen.clears.push(name); return route.fulfill({ json: { cleared: true } }) }
      return route.fulfill({ json: { Name: name, History: observables[name] || [] } })
    }
    if (tail === 'status') return route.fulfill({ json: { Name: name, History: [] } })
    if (tail === 'pause') { seen.pauses.push(name); return route.fulfill({ json: { status: 'ok' } }) }
    if (tail === 'resume') { seen.resumes.push(name); return route.fulfill({ json: { status: 'ok' } }) }
    if (tail === 'export') return route.fulfill({ json: agent.config })
    if (req.method() === 'PUT') { seen.saves.push({ method: 'PUT', name, body: req.postDataJSON() }); return route.fulfill({ json: { status: 'ok' } }) }
    if (req.method() === 'DELETE') { seen.deletes.push(name); return route.fulfill({ json: { status: 'ok' } }) }
    return route.fulfill({ json: { active: agent.active } })
  })
  return seen
}

// Seed run records. Each run: { id, task, status, outcome, ... } is expanded
// into the stored shape.
export function storedRun(agent, { id, task, status = 'done', outcome = '', error = '', steps = [], startedAt, seconds = 30, metadata = null, turns = null }) {
  const first = {
    task, startedAt, endedAt: status === 'running' ? null : startedAt + seconds * 1000, status, outcome, error, steps,
    live: { reasoning: '', content: '' }, metadata,
  }
  return { id, agent, startedAt, updatedAt: startedAt + seconds * 1000, turns: turns || [first] }
}

export async function seedRuns(page, agent, runs, userId) {
  await page.addInitScript(([key, data]) => {
    try { localStorage.setItem(key, JSON.stringify(data)) } catch { /* ignore */ }
  }, [RUNS_KEY(agent, userId), { version: 1, runs }])
}
