// A stateful stand-in for the add-on routes: the apps list, start and stop,
// and the typed dispatch actions the management tabs use. It records every
// request so a spec can assert what the page sent, and it can answer with the
// refusals the real server gives (403, 503, 503 outcome_unknown).

export const ADMIN = { id: 'u-alice', email: 'alice@lab.example', name: 'alice', role: 'admin', provider: 'local' }
export const MEMBER = { id: 'u-carol', email: 'carol@lab.example', name: 'carol', role: 'user', provider: 'local' }

const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64')

export function app(over = {}) {
  return {
    id: 'notes', name: 'notes', title: 'Notes', description: 'Keep notes.', icon: '/api/addons/apps/notes/icon?generation=3',
    version: '1.2.0', generation: 3, enabled: true, state: 'running', mainPage: '/app/addons/notes/home', hasUI: true,
    launchMode: 'always', tools: ['add', 'list'], toolCount: 2, ...over,
  }
}

export const HEADLESS = app({ id: 'indexer', name: 'indexer', title: 'indexer', description: '', icon: undefined, mainPage: undefined, hasUI: false, tools: ['query', 'export', 'reindex'], toolCount: 3, generation: 1, version: '0.4.0' })

export function installedAddon(over = {}) {
  return {
    id: 'notes', title: 'Notes', description: 'Keep notes.', icon: '/api/addons/apps/notes/icon?generation=3',
    version: '1.2.0', generation: 3, enabled: true, state: 'running', sourceId: 'main', selection: '1.2.0',
    verification: 'sha256+sigstore', digest: 'sha256:0123456789abcdef0123456789abcdef', platform: { os: 'linux', arch: 'amd64' },
    compatibility: { host: { min: 1, max: 2 }, api: { min: 1, max: 1 }, protocol: { min: 1, max: 1 }, dataFormat: { min: 1, max: 2 } },
    tools: ['add', 'list'], toolCount: 2, hasUI: true, mainPage: '/app/addons/notes/home', launchMode: 'always',
    generations: 3, restarts: 0, storage: { bytes: 12288, truncated: false }, ...over,
  }
}

export function release(id, over = {}) {
  return {
    addonId: id, name: id[0].toUpperCase() + id.slice(1), description: `${id} add-on`, version: '1.0.0', sourceId: 'main',
    artifact: 'https://catalog.example/' + id, digest: 'sha256:' + 'a'.repeat(64), os: 'linux', arch: 'amd64',
    policy: { requireSignature: false }, ...over,
  }
}

export function sessionFor(user, permissions = {}) {
  return { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], hasUsers: true, user: { ...user, permissions } }
}

const ok = (payload) => ({ status: 200, contentType: 'application/json', body: JSON.stringify({ payload }) })
const fail = (status, type, extra = {}) => ({ status, contentType: 'application/json', body: JSON.stringify({ error: { type, message: type, ...extra } }) })

// Installs the routes and returns the mutable state the spec reads and edits.
export async function mockAddons(page, options = {}) {
  const state = {
    apps: options.apps || [app()],
    installed: options.installed || [installedAddon()],
    sources: options.sources || [{ id: 'main', name: 'Main', url: 'https://catalog.example/releases.json', enabled: true, policy: { requireSignature: false }, refresh: { checkedAt: new Date().toISOString(), lastSuccess: new Date().toISOString(), count: 2, failed: false } }],
    releases: options.releases || [release('notes'), release('tables', { policy: { requireSignature: true, issuer: 'https://issuer.example', identity: 'release@catalog.example' } })],
    log: { appsList: 0, start: [], stop: [], dispatch: [], check: [], upgrade: [] },
    // Overrides for the update routes: each takes the parsed body.
    check: options.check || null,
    upgrade: options.upgrade || null,
    // Overrides: each takes the parsed body and may return a route fulfilment.
    appsList: options.appsList || null,
    start: options.start || null,
    stop: options.stop || null,
    dispatch: options.dispatch || null,
  }

  await page.route(/\/api\/addons\/apps\/[^/]+\/icon/, route => route.fulfill({ status: 200, contentType: 'image/png', body: PNG }))
  await page.route(/\/api\/addons\/apps$/, route => {
    state.log.appsList += 1
    if (state.appsList) {
      const custom = state.appsList(state.log.appsList)
      if (custom) return route.fulfill(custom)
    }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ apps: state.apps }) })
  })
  for (const verb of ['start', 'stop']) {
    await page.route(new RegExp(`/api/addons/apps/[^/]+/${verb}$`), route => {
      const id = new URL(route.request().url()).pathname.split('/').at(-2)
      const body = JSON.parse(route.request().postData() || '{}')
      state.log[verb].push({ id, ...body })
      if (state[verb]) {
        const custom = state[verb]({ id, ...body }, state.log[verb].length)
        if (custom) return route.fulfill(custom)
      }
      const target = state.apps.find(a => a.id === id)
      if (target) {
        target.enabled = verb === 'start'
        target.state = verb === 'start' ? 'running' : 'stopped'
        target.admin = { canStart: !target.enabled, canStop: target.enabled, managementUrl: `/app/addons?manage=${id}` }
      }
      return route.fulfill(ok({ OperationID: body.operationId, AddonID: id, Phase: 'completed', Generation: body.generation }))
    })
  }
  // The update routes. A check answers with the add-on's own `update` object,
  // as the owner's status; an upgrade moves the add-on to its candidate.
  await page.route(/\/api\/addons\/[^/]+\/update\/check$/, route => {
    const id = new URL(route.request().url()).pathname.split('/').at(-3)
    const body = JSON.parse(route.request().postData() || '{}')
    state.log.check.push({ id, ...body, confirm: route.request().headers()['x-localai-confirm'] })
    if (state.check) {
      const custom = state.check({ id, ...body }, state.log.check.length)
      if (custom) return route.fulfill(custom)
    }
    const a = state.installed.find(x => x.id === id)
    const u = a && a.update ? a.update : { state: 'up-to-date', available: false }
    if (a) a.update = { ...u, checkedAt: new Date().toISOString() }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ addonId: id, state: u.state, throttled: false }) })
  })
  await page.route(/\/api\/addons\/[^/]+\/upgrade$/, route => {
    const id = new URL(route.request().url()).pathname.split('/').at(-2)
    const body = JSON.parse(route.request().postData() || '{}')
    state.log.upgrade.push({ id, ...body, confirm: route.request().headers()['x-localai-confirm'] })
    if (state.upgrade) {
      const custom = state.upgrade({ id, ...body }, state.log.upgrade.length)
      if (custom) return route.fulfill(custom)
    }
    const a = state.installed.find(x => x.id === id)
    if (a && a.update) { a.generation += 1; a.version = a.update.version; a.digest = a.update.digest; delete a.update }
    return route.fulfill(ok({ OperationID: body.operationId, AddonID: id, Phase: 'completed', Generation: a?.generation }))
  })
  await page.route(/\/api\/addons\/dispatch$/, route => {
    const body = JSON.parse(route.request().postData() || '{}')
    state.log.dispatch.push(body)
    if (state.dispatch) {
      const custom = state.dispatch(body, state)
      if (custom) return route.fulfill(custom)
    }
    const args = body.arguments || {}
    switch (body.action) {
      case 'catalog-search': {
        let list = state.releases.filter(r => (!args.sourceId || r.sourceId === args.sourceId) && (!args.text || (r.name + r.addonId + r.description).toLowerCase().includes(args.text.toLowerCase())))
        const total = list.length
        list = list.slice(args.offset || 0, (args.offset || 0) + (args.limit || 50))
        return route.fulfill(ok({ releases: list, total }))
      }
      case 'catalog-details': {
        const r = state.releases.find(x => x.addonId === body.addonId && x.version === args.version && x.sourceId === args.sourceId)
        if (!r) return route.fulfill(fail(403, 'addon_error'))
        const have = state.installed.find(a => a.id === r.addonId)
        return route.fulfill(ok({ release: r, installed: have ? { version: have.version, generation: have.generation, enabled: have.enabled } : null }))
      }
      case 'catalog-sources':
        return route.fulfill(ok({ sources: state.sources }))
      case 'catalog-source-save': {
        if (args.removed) state.sources = state.sources.filter(s => s.id !== args.id)
        else state.sources = [...state.sources.filter(s => s.id !== args.id), args]
        return route.fulfill(ok(args))
      }
      case 'catalog-source-refresh':
        return route.fulfill(ok({ sourceId: args.sourceId, count: state.releases.length }))
      case 'addons-installed':
        return route.fulfill(ok({ addons: state.installed }))
      case 'install': {
        const r = state.releases.find(x => x.addonId === body.addonId)
        state.installed.push(installedAddon({ id: body.addonId, title: r?.name || body.addonId, enabled: false, state: 'stopped', generation: 1, version: args.version }))
        return route.fulfill(ok({ OperationID: body.operationId, AddonID: body.addonId, Phase: 'completed', Generation: 1 }))
      }
      case 'enable':
      case 'disable': {
        const a = state.installed.find(x => x.id === body.addonId)
        if (a) { a.enabled = body.action === 'enable'; a.state = a.enabled ? 'running' : 'stopped' }
        return route.fulfill(ok({ OperationID: body.operationId, AddonID: body.addonId, Phase: 'completed', Generation: body.generation }))
      }
      case 'upgrade': {
        const a = state.installed.find(x => x.id === body.addonId)
        if (a) { a.generation += 1; a.version = args.version; delete a.update }
        return route.fulfill(ok({ OperationID: body.operationId, AddonID: body.addonId, Phase: 'completed', Generation: a?.generation }))
      }
      case 'uninstall':
        state.installed = state.installed.filter(x => x.id !== body.addonId)
        return route.fulfill(ok({ OperationID: body.operationId, AddonID: body.addonId, Phase: 'completed', Generation: 0 }))
      case 'operation-status':
        return route.fulfill(ok({ OperationID: body.operationId, AddonID: body.addonId, Phase: 'completed', Generation: 0 }))
      default:
        return route.fulfill(fail(403, 'addon_error'))
    }
  })
  return state
}

export { ok, fail }
