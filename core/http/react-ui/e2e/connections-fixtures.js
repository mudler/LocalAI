// Fixtures for the remote connections specs: the connections API stubbed with
// Playwright routes, plus a few connection and model rows. Hosts are
// example.com style and the model names are neutral.

export const LOCALAI_MODELS = [
  { id: 'chat-large', usecases: ['chat'] },
  { id: 'chat-small', usecases: ['chat'] },
  { id: 'embed-base', usecases: ['embeddings'] },
  { id: 'speech-to-text', usecases: ['transcript'] },
  { id: 'text-to-speech', usecases: ['tts'] },
]

export const CLOUD_MODELS = [
  { id: 'cloud-model-a', usecases: ['chat'] },
  { id: 'cloud-model-b', usecases: ['chat'] },
  { id: 'cloud-model-c', usecases: ['chat'] },
]

export const connection = (over = {}) => ({
  id: 'conn-1',
  name: 'Office server',
  kind: 'localai',
  url: 'https://office.example.com',
  key_set: true,
  status: 'ok',
  last_refresh: new Date(Date.now() - 12 * 60_000).toISOString(),
  entries: ['chat-large', 'embed-base'],
  unavailable: [],
  new: [],
  ...over,
})

export const CONNECTED = connection({
  new: [{ id: 'chat-small', usecases: ['chat'] }, { id: 'text-to-speech', usecases: ['tts'] }],
})
export const REJECTED = connection({
  id: 'conn-2', name: 'Hosted service', kind: 'openai', url: 'https://api.example.com/v1', status: 'auth_failed',
  last_error: 'The service answered 401: invalid key', last_refresh: undefined, entries: ['cloud-model-a'],
})
export const UNREACHABLE = connection({
  id: 'conn-3', name: 'Lab box', url: 'http://lab.example.com:8080', status: 'unreachable',
  last_error: 'dial tcp: connection refused', entries: ['chat-large'], unavailable: ['chat-large'],
})
export const NEVER = connection({ id: 'conn-4', name: 'New remote', kind: 'anthropic', url: 'https://api.anthropic.com', status: 'never', last_refresh: undefined, entries: [] })

// The answer of the test route for each outcome.
export const TEST_OK = (models) => ({ ok: true, models })
export const TEST_FAIL = (step, message = '') => ({ ok: false, step, ...(message ? { message } : {}) })

// Stub every route of the connections API. `state.calls` holds each request as
// { method, path, body }. `answers` can change while a spec runs.
export async function mockConnections(page, { connections = [], test = TEST_OK(LOCALAI_MODELS), status = {} } = {}) {
  // Copies: a spec edits its connections, and the next one must start clean.
  const state = { connections: connections.map(c => structuredClone(c)), test, calls: [], status }
  await page.route(/\/api\/connections(\/.*)?(\?.*)?$/, async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace(/^.*\/api\/connections/, '') || '/'
    const method = req.method()
    let body = null
    try { body = req.postDataJSON() } catch { body = null }
    state.calls.push({ method, path, body, query: Object.fromEntries(url.searchParams) })
    const forced = state.status[`${method} ${path}`]
    if (forced) return route.fulfill({ status: forced.status, json: forced.json ?? { error: { message: forced.message || 'failed' } } })
    if (path === '/test' && method === 'POST') return route.fulfill({ json: state.test })
    if (path === '/' && method === 'GET') return route.fulfill({ json: { data: state.connections } })
    if (path === '/' && method === 'POST') {
      const created = connection({
        id: 'conn-new', name: body.name, kind: body.kind, url: body.url, key_set: !!body.api_key,
        entries: body.models.map(m => m.id), new: [],
      })
      state.connections.push(created)
      return route.fulfill({ status: 201, json: { connection: created, created: body.models.map(m => m.id), skipped: [] } })
    }
    const [, id, action] = path.match(/^\/([^/]+)(?:\/(.+))?$/) || []
    const current = state.connections.find(c => c.id === id)
    if (!current) return route.fulfill({ status: 404, json: { error: { message: 'unknown connection' } } })
    if (!action && method === 'PATCH') {
      Object.assign(current, { ...(body.name ? { name: body.name } : {}), ...(body.url ? { url: body.url } : {}), ...(body.api_key ? { key_set: true, status: 'ok' } : {}) })
      return route.fulfill({ json: current })
    }
    if (!action && method === 'DELETE') {
      state.connections = state.connections.filter(c => c.id !== id)
      return route.fulfill({ status: 204, body: '' })
    }
    if (action === 'refresh') return route.fulfill({ json: current })
    if (action === 'adopt') {
      current.entries = [...current.entries, ...body.models.map(m => m.id)]
      current.new = current.new.filter(m => !body.models.some(b => b.id === m.id))
      return route.fulfill({ json: { connection: current, created: body.models.map(m => m.id), skipped: [] } })
    }
    return route.fulfill({ status: 404, json: { error: { message: 'not found' } } })
  })
  return state
}
