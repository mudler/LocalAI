// Fixtures for the Updates page and the badges: the backend read model of
// GET /api/backends/updates, the add-on records and host-access overview, and
// one function that routes every call the page makes. Plain data, no Playwright
// import, so a script that is not a test can use it too.

export const minutesAgo = (min) => new Date(Date.now() - min * 60_000).toISOString()

export const okSource = (min = 12) => ({ state: 'ok', checkedAt: minutesAgo(min), lastSuccessAt: minutesAgo(min) })

// The backend read model. `sources` and the rest can be overridden.
export function snapshotOf(items = [], over = {}) {
  return {
    checkedAt: minutesAgo(12),
    sources: { gallery: okSource(12), library: okSource(12) },
    autoUpdate: { backends: false },
    items,
    ...over,
  }
}

export const galleryItem = (name, from, to, extra = {}) => ({
  id: `gallery:${name}`, kind: 'gallery', name, state: 'update', installedVersion: from, availableVersion: to, ...extra,
})
export const libraryItem = (name, from, to, extra = {}) => ({
  id: `library:official/${name}`, kind: 'library', name, state: 'update', sourceId: 'official', signed: true, installedVersion: from, availableVersion: to, ...extra,
})

// The model read model of GET /api/models/updates. A server with no model
// gallery answers the disabled source and no items: that is the default, so a
// spec about backends and add-ons sees no model rows and no model source.
export function modelSnapshotOf(items = [], over = {}) {
  return { checkedAt: minutesAgo(12), sources: { gallery: okSource(12) }, items, ...over }
}
export const noModelGallery = () => ({ sources: { gallery: { state: 'disabled', reason: 'no model gallery is configured on this server' } }, items: [] })

export const DIGEST_A = 'sha256:1a2b3c4d5e6f7a8b9c0d'
export const DIGEST_B = 'sha256:9f8e7d6c5b4a39281706'

// One installed gallery model. The digests are the entry's, before and after.
export const modelItem = (name, state, extra = {}) => ({
  id: `model:${name}`, kind: 'model', name, state,
  needsPerson: ['update-available', 'edited', 'failed'].includes(state),
  gallery: 'localai', entryName: name, installedDigest: DIGEST_A,
  availableDigest: state === 'up-to-date' ? DIGEST_A : DIGEST_B,
  ...extra,
})

export const modelFiles = () => ([
  { path: 'qwen3-8b-Q4_K_M.gguf', role: 'weights', change: 'changed', checksum: true },
  { path: 'mmproj-qwen3-8b-f16.gguf', role: 'mmproj', change: 'added', checksum: true },
  { path: 'qwen3-8b.tmpl', role: 'template', change: 'changed', checksum: true },
])

// An operation of /api/operations for a model upgrade.
export const modelOp = (name, phase, extra = {}) => ({
  id: name, name, fullName: name, jobID: `job-${name}`, progress: 35, taskType: 'upgrade', phase,
  isDeletion: false, isBackend: false, isQueued: false, cancellable: ['preflight', 'staging'].includes(phase), message: phase, ...extra,
})

// An installed add-on record (the owner's `addons-installed`).
export const addonRecord = (id, title, update, extra = {}) => ({
  id, title, version: '1.0.0', generation: 1, digest: 'sha256:aa', sourceId: 'official', update, ...extra,
})
export const updateAvailable = (version) => ({ state: 'update-available', version, digest: 'sha256:bb', available: true, checkedAt: minutesAgo(3) })
export const upToDate = () => ({ state: 'up-to-date', checkedAt: minutesAgo(3) })

export const heldGrant = (id, title, version, delta = ['read_files']) => ({
  id, title, version: '2.3.0', sourceId: 'official', state: 'running', pendingUpgrade: { version, delta },
})

export const runningOp = (name, extra = {}) => ({
  id: name, name, fullName: `localai@${name}`, jobID: `job-${name}`, progress: 62, taskType: 'upgrade',
  isDeletion: false, isBackend: true, isQueued: false, cancellable: true, message: 'downloading', ...extra,
})
export const failedOp = (name, error = 'download interrupted', extra = {}) => ({
  id: name, name, fullName: `localai@${name}`, jobID: `job-${name}`, progress: 40, taskType: 'upgrade',
  isDeletion: false, isBackend: true, isQueued: false, cancellable: false, error, ...extra,
})

const POLICY = {
  chats_egress_ceiling: '', chats_egress_default: '', chats_egress_allow_private: false,
  chats_egress_ports: '', chats_egress_extra_registries: '', chats_egress_allowed_hosts: '',
  chats_tool_providers: {}, auto_upgrade_addons: false, connector_files_ceiling_bytes: 0, sandbox_profiles: { default: '', access: {}, memory_mib: 0, disk_mib: 0, pin_previous: [] },
}

// Routes the calls of the Updates page and the shell provider. Install it after
// mockOperate (later routes win) and read the mutable state it returns:
//
//   snapshot      the read model GET answers; a spec replaces it between steps
//   updatesStatus the HTTP status of GET /api/backends/updates (500 for an error)
//   onCheck()     returns the read model a check answers (default: the same one)
//   onUpgrade()   called with (name, jobID) when an update is started
//   operations    what GET /api/operations lists
//   installed     GET /backends, for the up to date line
//   addons        the owner's installed records
//   grants        the host-access overview
//   autoAddons    the owner's automatic update switch for add-ons
//   delayMs       holds the first read, for a loading state
//   models        the model read model GET /api/models/updates answers
//   onModelCheck() returns the model read model a check answers (counted in modelChecks)
//   modelCalls    every model action the page sent: { action, name, body }
//   modelReply(action, name, body) returns { status, json } to refuse one
//   onModelAction(action, name, body, state) changes the answers once an action was accepted;
//                 set state there, not before the click, or a refresh in between redraws the row
//   loaded        the loaded models GET /system lists (for the dialog)
export async function mockUpdates(page, options = {}) {
  const state = {
    models: noModelGallery(),
    onModelCheck: null,
    modelCalls: [],
    modelChecks: 0,
    modelReply: null,
    onModelAction: null,
    loaded: null,
    snapshot: snapshotOf([]),
    updatesStatus: 200,
    onCheck: null,
    onUpgrade: null,
    operations: [],
    installed: [],
    addons: [],
    grants: { revision: 1, addons: [] },
    autoAddons: false,
    delayMs: 0,
    reads: 0,
    checks: 0,
    upgrades: [],
    upgradeTimes: [],
    ...options,
  }
  await page.route('**/api/backends/updates', async (route) => {
    state.reads += 1
    if (state.delayMs) await new Promise(resolve => setTimeout(resolve, state.delayMs))
    if (state.updatesStatus !== 200) return route.fulfill({ status: state.updatesStatus, json: { error: 'registry unavailable' } })
    return route.fulfill({ json: state.snapshot })
  })
  await page.route('**/api/backends/updates/check', (route) => {
    state.checks += 1
    if (state.onCheck) state.snapshot = state.onCheck(state) || state.snapshot
    return route.fulfill({ json: state.snapshot })
  })
  await page.route('**/api/backends/upgrade/*', (route) => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    const jobID = `job-${name}`
    state.upgrades.push(name)
    state.upgradeTimes.push(Date.now())
    if (state.onUpgrade) state.onUpgrade(name, jobID, state)
    return route.fulfill({ json: { jobID, uuid: jobID, message: 'Backend upgrade started' } })
  })
  await page.route('**/api/operations', route => route.fulfill({ json: { operations: state.operations } }))
  await page.route('**/backends', (route) => {
    if (new URL(route.request().url()).pathname === '/backends') return route.fulfill({ json: state.installed })
    return route.continue()
  })
  await page.route('**/api/addons/dispatch', (route) => {
    const body = route.request().postDataJSON() || {}
    if (body.action === 'addons-installed') return route.fulfill({ json: { payload: { addons: state.addons } } })
    if (body.action === 'host-settings-read') {
      return route.fulfill({ json: { payload: { data: { schema_version: 1, revision: 1, policy: { ...POLICY, auto_upgrade_addons: state.autoAddons } } } } })
    }
    return route.fulfill({ json: { payload: null } })
  })
  await page.route('**/api/addons/host-grants', route => route.fulfill({ json: state.grants }))
  await page.route('**/api/models/updates', route => route.fulfill({ json: state.models }))
  await page.route('**/api/models/updates/check', (route) => {
    state.modelChecks += 1
    if (state.onModelCheck) state.models = state.onModelCheck(state) || state.models
    return route.fulfill({ json: state.models })
  })
  const modelAction = (action) => async (route) => {
    const req = route.request()
    const parts = new URL(req.url()).pathname.split('/')
    const name = decodeURIComponent(action === 'upgrade' ? parts[4] : parts[3])
    let body = {}
    try { body = req.postDataJSON() || {} } catch { body = {} }
    state.modelCalls.push({ action, name, body })
    const refused = state.modelReply ? state.modelReply(action, name, body, state) : null
    if (refused) return route.fulfill({ status: refused.status, json: refused.json })
    if (state.onModelAction) state.onModelAction(action, name, body, state)
    if (action === 'remove-old') return route.fulfill({ json: { name, removed: ['old-1.gguf', 'old-2.gguf'] } })
    if (action === 'adopt') return route.fulfill({ json: { name, gallery: body.gallery || 'localai', entry: body.entry || name, filesMatch: true } })
    if (action === 'hold') return route.fulfill({ json: { name, hold: Boolean(body.hold) } })
    return route.fulfill({ json: { id: `job-${name}`, jobID: `job-${name}`, status_url: `/models/jobs/job-${name}`, message: 'Upgrade started' } })
  }
  await page.route(/\/api\/models\/upgrade\/[^/]+$/, modelAction('upgrade'))
  await page.route(/\/api\/models\/[^/]+\/upgrade\/retry$/, modelAction('retry'))
  await page.route(/\/api\/models\/[^/]+\/remove-old$/, modelAction('remove-old'))
  await page.route(/\/api\/models\/[^/]+\/hold$/, modelAction('hold'))
  await page.route(/\/api\/models\/[^/]+\/adopt$/, modelAction('adopt'))
  if (state.loaded) {
    await page.route('**/system', route => route.fulfill({ json: { backends: [], loaded_models: state.loaded } }))
  }
  // No update run unless a spec installs mockUpdateRuns after this.
  await page.route(/\/api\/updates\/runs(\?.*)?$/, route => route.fulfill({ json: { runs: [] } }))
  await page.route(/\/api\/addons\/[^/]+\/update\/check$/, route => route.fulfill({ json: { state: 'up-to-date', checkedAt: minutesAgo(0) } }))
  return state
}

// ---- Update runs ---------------------------------------------------------------
//
// The server owns an update run: it plans it (preview), runs it and reports it.
// These fixtures stand in for /api/updates/runs. Their shape is what the
// server answers (core/http/endpoints/localai/upgrade_runs.go and
// core/services/upgraderun): empty strings and empty lists are left out, the
// numbers of a risk are decimal strings, a run has no counts, a queued item is
// never marked cancellable, and an add-on shows versions while it is pinned by
// the digest of its candidate (expected_to). Do not add fields the server
// does not send: a fixture that sends more hides a page that reads them.

const STAGES = { gallery: 1, library: 1, model: 2, addon: 3 }

// The digest an add-on item is pinned to, for a version.
export const addonPin = (version) => `sha256:${Array.from(String(version)).map(c => c.charCodeAt(0).toString(16)).join('').padEnd(16, '0').slice(0, 16)}`

// One item of a preview. `risk` is a list of { code, params }; params are
// strings, as the server sends them. An add-on is pinned to a digest.
export const previewItem = (key, from, to, extra = {}) => {
  const at = key.indexOf(':')
  const kind = key.slice(0, at)
  const risk = (extra.risk || []).map(r => ({ code: r.code, params: Object.fromEntries(Object.entries(r.params || {}).map(([k, v]) => [k, String(v)])) }))
  const startable = extra.startable !== undefined ? extra.startable : true
  const selected = extra.default_selected !== undefined ? extra.default_selected : (startable && kind !== 'model')
  const out = {
    key, kind, name: key.slice(at + 1), from, to,
    expected_to: kind === 'addon' ? addonPin(to) : to,
    stage: STAGES[kind] || 1, order: 0, startable,
    risks: risk.map(r => r.code), selected, default_selected: selected,
    ...extra,
    risk,
  }
  for (const k of ['from', 'to', 'expected_to', 'title', 'reason']) if (!out[k]) delete out[k]
  return out
}

// One item of a run. Only a running item can be cancellable (the server marks
// it from its operation); the other fields the server leaves out when empty.
export const runItem = (key, phase, extra = {}) => {
  const at = key.indexOf(':')
  const kind = key.slice(0, at)
  const to = extra.to || 'v1.1.0'
  const out = {
    key, kind, name: key.slice(at + 1), from: 'v1.0.0', to,
    expected_to: kind === 'addon' ? addonPin(to) : to,
    phase, progress: phase === 'updated' ? 100 : 0, retryable: false, cancellable: phase === 'running',
    ...extra,
  }
  if (out.job_id === undefined && (phase !== 'queued' && phase !== 'skipped' && phase !== 'cancelled')) out.job_id = `job-${out.name}`
  for (const k of ['message', 'error_key', 'error', 'queued_reason', 'title', 'job_id']) if (!out[k]) delete out[k]
  if (Array.isArray(out.nodes) && out.nodes.length === 0) delete out.nodes
  return out
}

// A run as the server answers it: no counts (the page counts the items), and
// finished_at only once it ended.
export function runOf(items, extra = {}) {
  const ended = !items.some(i => i.phase === 'queued' || i.phase === 'running')
  const out = {
    id: 'run-1', source: 'manual', actor: 'admin', created_at: minutesAgo(1), state: ended ? 'finished' : 'running',
    items, ...extra,
  }
  if (out.state !== 'running' && !out.finished_at) out.finished_at = minutesAgo(0)
  if (out.state === 'running') delete out.finished_at
  return out
}

// Routes /api/updates/runs. Read and change the returned state:
//
//   preview(items)  the answer of POST /preview for the requested { kind, name } list
//                   (default: one startable item each, models not selected)
//   active          the runs GET lists: not dismissed, the running one first (as
//                   the server lists them); with ?active=1 only the running ones
//   polls           the answers of GET /runs/:id in order; the last one repeats
//   startReply(body, state) the answer of POST /runs: a run, or { status, json } to refuse
//   previews, starts, cancels, itemCancels, retries, dismissed: what the page sent
//   startHeaders, retryHeaders: the request headers of each start and retry
//   onRetry(state)  the run the retry answers (default: the current poll)
export async function mockUpdateRuns(page, options = {}) {
  const state = {
    preview: null,
    active: [],
    polls: [],
    reads: 0,
    startReply: null,
    onRetry: null,
    previews: [],
    starts: [],
    startHeaders: [],
    retryHeaders: [],
    cancels: [],
    itemCancels: [],
    retries: [],
    dismissed: [],
    ...options,
  }
  const current = () => state.polls[Math.min(state.reads, state.polls.length - 1)] || null
  await page.route('**/api/updates/runs/preview', (route) => {
    const body = route.request().postDataJSON() || {}
    state.previews.push(body)
    const items = state.preview
      ? state.preview(body.items || [])
      : (body.items || []).map(i => previewItem(`${i.kind}:${i.name}`, 'v1.0.0', 'v1.1.0'))
    const out = Array.isArray(items) ? { items, order: items.map(i => i.key), complete: true } : { complete: true, ...items }
    // The server numbers the items in run order.
    const order = Array.isArray(out.order) ? out.order : out.items.map(i => i.key)
    out.items = out.items.map(i => ({ ...i, order: Math.max(0, order.indexOf(i.key)) }))
    return route.fulfill({ json: out })
  })
  await page.route(/\/api\/updates\/runs(\?.*)?$/, (route) => {
    const req = route.request()
    if (req.method() === 'POST') {
      const body = req.postDataJSON() || {}
      state.starts.push(body)
      state.startHeaders.push(req.headers())
      const out = state.startReply ? state.startReply(body, state) : current()
      if (out && out.status) return route.fulfill({ status: out.status, json: out.json })
      if (out && out.id) state.active = [out]
      return route.fulfill({ status: 202, json: out })
    }
    const url = new URL(req.url())
    const onlyRunning = url.searchParams.get('active') === '1' || url.searchParams.get('active') === 'true'
    const runs = onlyRunning ? state.active.filter(r => r.state === 'running') : state.active
    const limit = Number(url.searchParams.get('limit')) || 0
    state.lists = (state.lists || 0) + 1
    return route.fulfill({ json: { runs: limit > 0 ? runs.slice(0, limit) : runs } })
  })
  await page.route(/\/api\/updates\/runs\/[^/?]+$/, (route) => {
    const req = route.request()
    const id = decodeURIComponent(new URL(req.url()).pathname.split('/').pop())
    if (id === 'preview') return route.fallback()
    if (req.method() === 'DELETE') {
      state.dismissed.push(id)
      state.active = state.active.filter(r => r.id !== id)
      return route.fulfill({ status: 204, body: '' })
    }
    const run = current()
    state.reads += 1
    if (!run || run.id !== id) return route.fulfill({ status: 404, json: { error: 'not found' } })
    return route.fulfill({ json: run })
  })
  await page.route(/\/api\/updates\/runs\/[^/]+\/cancel$/, (route) => {
    state.cancels.push(route.request().url())
    return route.fulfill({ json: current() })
  })
  await page.route(/\/api\/updates\/runs\/[^/]+\/items\/[^/]+\/cancel$/, (route) => {
    const parts = new URL(route.request().url()).pathname.split('/')
    state.itemCancels.push(decodeURIComponent(parts[parts.length - 2]))
    return route.fulfill({ json: current() })
  })
  await page.route(/\/api\/updates\/runs\/[^/]+\/retry$/, (route) => {
    state.retries.push(route.request().url())
    state.retryHeaders.push(route.request().headers())
    const out = state.onRetry ? state.onRetry(state) : current()
    if (out && out.id) state.active = [out]
    return route.fulfill({ json: out })
  })
  return state
}
