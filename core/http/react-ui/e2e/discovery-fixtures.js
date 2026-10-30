// Stubs for the discovery specs (the Home examples, the change notes of a
// chat, the Pages entry and the Help and suggestions card). Every API call is
// answered here, so the specs run against any server that serves the UI.
//
// The well-known files live in state.files ({persona: {...}, user: {...}}) and
// behave like the server's: a write merges its keys, names the version it read
// in If-Match, and an older version is a 409 with what the file holds now.

const json = (body, status = 200) => ({ status, contentType: 'application/json', body: JSON.stringify(body) })

export const NOW = new Date().toISOString()

export const TASK_PAGE = {
  name: 'tasks', path: '/pages/tasks', title: 'Task board', icon: 'checklist', description: 'Three columns of tasks',
  source: 'own', author: 'assistant', updated_at: NOW, updated_by: 'assistant', rev: 7, size: 100,
}

export function newState(over = {}) {
  return {
    files: {},
    versions: {},
    puts: [],
    pages: [],
    connectors: [],
    features: { chats_suggestions: true },
    user: { id: 'marta', name: 'Marta Rossi', role: 'admin', provider: 'local' },
    ...over,
  }
}

function doc(state, name) {
  const data = state.files[name]
  const out = { name, path: `/${name}.json`, exists: !!data, version: data ? (state.versions[name] || 1) : 0, data: { ...(data || {}) }, valid: true, errors: [] }
  if (name === 'user' && state.user?.name && !data?.name) {
    out.data.name = state.user.name
    out.prefilled = { name: 'account' }
  }
  return out
}

// stubFeed answers the change feed. send(paths) delivers one change.
export async function stubFeed(page) {
  const queue = []
  let wake = null
  let revision = 10
  await page.route('**/api/workspace/events*', async (route) => {
    while (queue.length === 0) await new Promise((resolve) => { wake = resolve })
    const body = queue.splice(0).join('')
    await route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' }, body }).catch(() => {})
  })
  return (paths) => {
    revision++
    queue.push(`id: ${revision}\nevent: change\ndata: ${JSON.stringify({ revision, paths })}\n\n`)
    wake?.()
  }
}

export async function mockDiscovery(page, state, { models = ['qwen3-8b-instruct'] } = {}) {
  await page.route('**/api/**', (route) => route.fulfill(json({ error: { message: 'not stubbed' } }, 404)))
  await page.route('**/api/auth/status', (route) => route.fulfill(json({
    authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user: state.user,
  })))
  await page.route('**/api/features', (route) => route.fulfill(json(state.features)))
  await page.route('**/system', (route) => route.fulfill(json({ backends: ['llama-cpp'], loaded_models: [] })))
  await page.route('**/v1/models', (route) => route.fulfill(json({ data: models.map(id => ({ id })) })))
  await page.route('**/api/models/capabilities', (route) => route.fulfill(json({ data: models.map(id => ({ id, capabilities: ['FLAG_CHAT'] })) })))
  await page.route('**/api/resources', (route) => route.fulfill(json({ type: 'cpu', available: true, ram: { total: 1, used: 0, free: 1, available: 1, usage_percent: 0 } })))
  await page.route('**/api/operations', (route) => route.fulfill(json([])))
  await page.route(/\/api\/chats(\?.*)?$/, (route) => route.fulfill(json({ conversations: state.chats || [], persistent: true, files: true })))
  await page.route('**/api/chats/mcp-connectors', (route) => route.fulfill(json({ connectors: state.connectors })))
  await page.route(/\/api\/chats\/pages(\?.*)?$/, (route) => route.fulfill(json({ pages: state.pages })))
  await page.route(/\/api\/chats\/page-starters\/([a-z-]+)\/copy$/, (route) => {
    const id = /page-starters\/([a-z-]+)\/copy/.exec(route.request().url())[1]
    const p = { ...TASK_PAGE, name: id, title: id === 'reading-list' ? 'Reading list' : 'Task board', author: 'you', updated_by: 'you' }
    state.pages = [...state.pages, p]
    state.copied = id
    return route.fulfill(json(p, 201))
  })
  await page.route(/\/api\/chats\/pages\/[^/]+(\?.*)?$/, (route) => {
    const name = decodeURIComponent(/\/api\/chats\/pages\/([^/?]+)/.exec(route.request().url())[1])
    const p = state.pages.find(x => x.name === name)
    return p ? route.fulfill(json({ page: p, html: '<!doctype html><p>page</p>' })) : route.fulfill(json({ error: { message: 'no page' } }, 404))
  })
  await page.route(/\/api\/chats\/wellknown\/[a-z-]+\/schema$/, (route) => route.fulfill(json({ type: 'object', properties: {} })))
  await page.route(/\/api\/chats\/wellknown\/[a-z-]+(\?.*)?$/, (route) => {
    const req = route.request()
    const name = /\/wellknown\/([a-z-]+)/.exec(req.url())[1]
    if (req.method() !== 'PUT') return route.fulfill(json(doc(state, name)))
    const body = req.postDataJSON()
    const base = Number(String(req.headers()['if-match'] || '').replace(/"/g, ''))
    state.puts.push({ name, base, body })
    if (!Number.isNaN(base) && req.headers()['if-match'] !== undefined && base !== doc(state, name).version) {
      return route.fulfill(json({ error: { message: 'the file changed since you read it', type: 'conflict' }, current: doc(state, name) }, 409))
    }
    const next = body.replace ? {} : { ...(state.files[name] || {}) }
    for (const [k, v] of Object.entries(body.values)) {
      if (v === null) delete next[k]
      else next[k] = v
    }
    state.files[name] = next
    state.versions[name] = (state.versions[name] || (doc(state, name).exists ? 1 : 0)) + 1
    return route.fulfill(json(doc(state, name)))
  })
}
