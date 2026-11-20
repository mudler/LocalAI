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
  await page.route(/\/api\/addons\/[^/]+\/update\/check$/, route => route.fulfill({ json: { state: 'up-to-date', checkedAt: minutesAgo(0) } }))
  return state
}
