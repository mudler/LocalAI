// Fixtures for the remotes specs: the remotes API stubbed with
// Playwright routes, plus a few remote and model rows. Hosts are
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

// What a cloud service lists: ids only, with no uses.
export const CLOUD_BARE = [{ id: 'cloud-model-a', usecases: [] }, { id: 'cloud-model-b', usecases: [] }]

export const remote = (over = {}) => ({
  id: 'remotes-1',
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

export const CONNECTED = remote({
  new: [{ id: 'chat-small', usecases: ['chat'] }, { id: 'text-to-speech', usecases: ['tts'] }],
})
export const REJECTED = remote({
  id: 'remotes-2', name: 'Hosted service', kind: 'openai', url: 'https://api.example.com/v1', status: 'auth_failed',
  last_error: 'The service answered 401: invalid key', last_refresh: undefined, entries: ['cloud-model-a'],
})
export const UNREACHABLE = remote({
  id: 'remotes-3', name: 'Lab box', url: 'http://lab.example.com:8080', status: 'unreachable',
  last_error: 'dial tcp: connection refused', entries: ['chat-large'], unavailable: ['chat-large'],
})
export const NEVER = remote({ id: 'remotes-4', name: 'New remote', kind: 'anthropic', url: '', status: 'never', last_refresh: undefined, entries: [] })

// The answer of the test route for each outcome.
export const TEST_OK = (models) => ({ ok: true, models })
export const TEST_FAIL = (step, message = '') => ({ ok: false, step, ...(message ? { message } : {}) })

// Stub every route of the remotes API. `state.calls` holds each request as
// { method, path, body }. `answers` can change while a spec runs.
//
// `create`, `adopt` and `refresh` replace the answer of that route: a function
// of ({ body, current, state }) that returns { status, json }, or an object of
// that shape. They are how a spec asks for a partial result with a warning,
// skipped models, or a refresh that ends with an error text.
export async function mockRemotes(page, { remotes = [], test = TEST_OK(LOCALAI_MODELS), status = {}, create = null, adopt = null, refresh = null } = {}) {
  // Copies: a spec edits its remotes, and the next one must start clean.
  const state = { remotes: remotes.map(c => structuredClone(c)), test, calls: [], status, create, adopt, refresh }
  const custom = (fn, ctx) => (typeof fn === 'function' ? fn(ctx) : fn)
  await page.route(/\/api\/remotes(\/.*)?(\?.*)?$/, async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace(/^.*\/api\/remotes/, '') || '/'
    const method = req.method()
    let body = null
    try { body = req.postDataJSON() } catch { body = null }
    state.calls.push({ method, path, body, query: Object.fromEntries(url.searchParams) })
    const forced = state.status[`${method} ${path}`]
    if (forced) return route.fulfill({ status: forced.status, json: forced.json ?? { error: { message: forced.message || 'failed' } } })
    if (path === '/test' && method === 'POST') return route.fulfill({ json: state.test })
    if (path === '/' && method === 'GET') return route.fulfill({ json: { data: state.remotes } })
    if (path === '/' && method === 'POST' && state.create) {
      const answer = custom(state.create, { body, state })
      if (answer.json?.remote) state.remotes.push(structuredClone(answer.json.remote))
      return route.fulfill({ status: answer.status || 201, json: answer.json })
    }
    if (path === '/' && method === 'POST') {
      const created = remote({
        id: 'remotes-new', name: body.name, kind: body.kind, url: body.url, key_set: !!body.api_key,
        entries: body.models.map(m => m.id), new: [],
      })
      state.remotes.push(created)
      return route.fulfill({ status: 201, json: { remote: created, created: body.models.map(m => m.id), skipped: [] } })
    }
    const [, id, action] = path.match(/^\/([^/]+)(?:\/(.+))?$/) || []
    const current = state.remotes.find(c => c.id === id)
    if (!current) return route.fulfill({ status: 404, json: { error: { message: 'unknown remote' } } })
    if (!action && method === 'PATCH') {
      Object.assign(current, { ...(body.name ? { name: body.name } : {}), ...(body.url ? { url: body.url } : {}), ...(body.api_key ? { key_set: true, status: 'ok' } : {}) })
      return route.fulfill({ json: current })
    }
    if (!action && method === 'DELETE') {
      state.remotes = state.remotes.filter(c => c.id !== id)
      return route.fulfill({ status: 204, body: '' })
    }
    if (action === 'refresh') {
      if (state.refresh) {
        const answer = custom(state.refresh, { body, current, state })
        Object.assign(current, answer.json)
        return route.fulfill({ status: answer.status || 200, json: current })
      }
      return route.fulfill({ json: current })
    }
    if (action === 'adopt' && state.adopt) {
      const answer = custom(state.adopt, { body, current, state })
      return route.fulfill({ status: answer.status || 200, json: answer.json })
    }
    if (action === 'adopt') {
      current.entries = [...current.entries, ...body.models.map(m => m.id)]
      current.new = current.new.filter(m => !body.models.some(b => b.id === m.id))
      return route.fulfill({ json: { remote: current, created: body.models.map(m => m.id), skipped: [] } })
    }
    return route.fulfill({ status: 404, json: { error: { message: 'not found' } } })
  })
  return state
}
