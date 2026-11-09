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
  chats_tool_providers: {}, auto_upgrade_addons: false, connector_files_ceiling_bytes: 0,
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
export async function mockUpdates(page, options = {}) {
  const state = {
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
  await page.route(/\/api\/addons\/[^/]+\/update\/check$/, route => route.fulfill({ json: { state: 'up-to-date', checkedAt: minutesAgo(0) } }))
  return state
}
