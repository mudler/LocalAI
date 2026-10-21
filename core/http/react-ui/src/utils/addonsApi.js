// Client of the add-on routes. The server is the authority for every answer:
// this module only shapes requests, classifies responses and keeps one
// operation identity per user action. It takes `fetch` and `base` so the node
// unit tests drive it without a browser.
import { classifyResponse, newOperationId } from '../components/catalog/catalogState.js'
import { classifyRecovery } from '../components/addons/recoveryState.js'
import { classifyUpdate } from '../components/addons/updateState.js'

export class AddonsError extends Error {
  constructor(info) {
    super(info.message || info.kind)
    this.name = 'AddonsError'
    this.kind = info.kind
    this.status = info.status || 0
    this.operationId = info.operationId || ''
    this.blockedBy = info.blockedBy || ''
    this.type = info.type || ''
  }
}

const PHASES_DONE = new Set(['completed', 'failed'])

// OperationStatus has Go field names on the wire; the page uses lower camel.
export function normalizeOperation(payload) {
  const p = payload && typeof payload === 'object' ? payload : {}
  return {
    operationId: p.OperationID || p.operationId || '',
    addonId: p.AddonID || p.addonId || '',
    phase: p.Phase || p.phase || '',
    generation: Number(p.Generation ?? p.generation ?? 0),
    runtimeState: p.runtimeState || '',
  }
}

export function isFinalPhase(phase) {
  return PHASES_DONE.has(phase)
}

export function createAddonsApi({ fetch: fetchImpl, base = p => p } = {}) {
  const doFetch = (...args) => (fetchImpl || globalThis.fetch)(...args)

  async function call(path, { method = 'GET', body, signal, headers: extra, classify = classifyResponse } = {}) {
    let response
    try {
      response = await doFetch(base(path), {
        method,
        signal,
        headers: { ...(body === undefined ? { Accept: 'application/json' } : { Accept: 'application/json', 'Content-Type': 'application/json' }), ...(extra || {}) },
        body: body === undefined ? undefined : JSON.stringify(body),
        credentials: 'same-origin',
      })
    } catch (e) {
      if (e && e.name === 'AbortError') throw e
      throw new AddonsError({ kind: 'network', message: e && e.message })
    }
    let data = null
    try { data = await response.json() } catch { data = null }
    if (!response.ok) throw new AddonsError(classify(response.status, data))
    return data
  }

  // A typed dispatch. The operation identifier is the caller's: one per user
  // action, kept across retries.
  async function dispatch({ operationId, action, addonId = '', generation = 0, args = {}, resourceId = '' }, signal) {
    const out = await call('/api/addons/dispatch', {
      method: 'POST',
      signal,
      body: { operationId, addonId, generation, action, arguments: args, ...(resourceId ? { resourceId } : {}) },
    })
    return out && out.payload !== undefined ? out.payload : null
  }

  // A typed read that may answer with private files. The add-on's output
  // handles never reach the browser: the owner replaced each by a host
  // artifact identifier, and only that identifier with the name, media type
  // and size the host recorded is returned here. Anything else of the answer
  // is the add-on's own payload, as for a plain read.
  async function dispatchFile({ operationId, action, addonId = '', generation = 0, args = {}, resourceId = '' }, signal) {
    const out = await call('/api/addons/dispatch', {
      method: 'POST',
      signal,
      body: { operationId, addonId, generation, action, arguments: args, ...(resourceId ? { resourceId } : {}) },
    })
    const files = []
    for (const o of Array.isArray(out && out.outputs) ? out.outputs : []) {
      if (o && typeof o.handle === 'string' && /^a[0-9a-f]{32}$/.test(o.handle) && files.length < 4) {
        files.push({ id: o.handle, name: String(o.name || '').slice(0, 200), mediaType: String(o.mediaType || '').slice(0, 100), size: Number.isFinite(o.size) ? o.size : 0 })
      }
    }
    return { files, payload: out && out.payload !== undefined ? out.payload : null }
  }

  const api = {
    call,
    dispatch,
    dispatchFile,
    apps: {
      async list(signal) {
        const out = await call('/api/addons/apps', { signal })
        return Array.isArray(out?.apps) ? out.apps : []
      },
      async start(id, { operationId, generation }, signal) {
        const out = await call(`/api/addons/apps/${encodeURIComponent(id)}/start`, { method: 'POST', body: { operationId, generation }, signal })
        return normalizeOperation(out?.payload)
      },
      async stop(id, { operationId, generation }, signal) {
        const out = await call(`/api/addons/apps/${encodeURIComponent(id)}/stop`, { method: 'POST', body: { operationId, generation }, signal })
        return normalizeOperation(out?.payload)
      },
    },
    // Data recovery of one add-on. Every mutation is the administrator's own
    // action: the operation ID is also the confirmation header, which only a
    // same-origin fetch of the host's own page can set.
    recovery: {
      status(id, signal) {
        return call(`/api/addons/${encodeURIComponent(id)}/recovery`, { signal, classify: classifyRecovery })
      },
      mutate(id, route, body, signal) {
        return call(`/api/addons/${encodeURIComponent(id)}/${route}`, {
          method: 'POST', body, signal, classify: classifyRecovery,
          headers: { 'X-LocalAI-Confirm': body.operationId },
        })
      },
      backup(id, { operationId }, signal) { return api.recovery.mutate(id, 'recovery/backup', { operationId }, signal) },
      verify(id, { operationId, backupId }, signal) { return api.recovery.mutate(id, 'recovery/verify', { operationId, backupId }, signal) },
      restore(id, body, signal) { return api.recovery.mutate(id, 'recovery/restore', body, signal) },
      deleteBackup(id, { operationId, backupId }, signal) { return api.recovery.mutate(id, 'recovery/delete-backup', { operationId, backupId }, signal) },
      prune(id, { operationId }, signal) { return api.recovery.mutate(id, 'recovery/prune', { operationId }, signal) },
      purge(id, body, signal) { return api.recovery.mutate(id, 'purge', body, signal) },
    },
    // Update detection and the explicit upgrade. The status is the owner's
    // answer and the page never derives it. Check and upgrade are the
    // administrator's own action: the operation ID is also the confirmation
    // header, which only a same-origin fetch of the host's own page can set.
    updates: {
      status(id, signal) {
        return call(`/api/addons/${encodeURIComponent(id)}/update`, { signal, classify: classifyUpdate })
      },
      check(id, { operationId }, signal) {
        return call(`/api/addons/${encodeURIComponent(id)}/update/check`, {
          method: 'POST', body: { operationId }, signal, classify: classifyUpdate,
          headers: { 'X-LocalAI-Confirm': operationId },
        })
      },
      async upgrade(id, body, signal) {
        const out = await call(`/api/addons/${encodeURIComponent(id)}/upgrade`, {
          method: 'POST', body, signal, classify: classifyUpdate,
          headers: { 'X-LocalAI-Confirm': body.operationId },
        })
        return normalizeOperation(out?.payload)
      },
    },
    // Reads the original operation by its own identifier. It never submits.
    async operationStatus(operationId, addonId, signal) {
      const payload = await dispatch({ operationId, action: 'operation-status', addonId }, signal)
      return normalizeOperation(payload)
    },
    async installed(signal) {
      const payload = await dispatch({ operationId: newOperationId(), action: 'addons-installed' }, signal)
      return Array.isArray(payload?.addons) ? payload.addons : []
    },
  }
  return api
}

// Wire an Add-ons catalog adapter over the client. Entries are the library
// releases for the owner target platform; an entry's `id` is its key, which the
// details call resolves exactly as an install would.
export function createAddonsCatalogAdapter(api, { pageSize = 24 } = {}) {
  const toEntry = (release) => ({
    key: `${release.sourceId}/${release.addonId}/${release.version}`,
    id: release.addonId,
    title: release.name || release.addonId,
    description: release.description || release.ui?.description || '',
    version: release.version,
    sourceId: release.sourceId,
    channel: release.channel || '',
    digest: release.digest || '',
    platform: { os: release.os, arch: release.arch },
    policy: release.policy || { requireSignature: false },
    ui: release.ui || null,
  })
  return {
    kind: 'addons',
    pageSize,
    async search(query, signal) {
      const offset = ((Number(query?.page) || 1) - 1) * pageSize
      const payload = await api.dispatch({
        operationId: newOperationId(),
        action: 'catalog-search',
        args: { text: query?.text || '', sourceId: query?.sourceId || '', offset, limit: pageSize },
      }, signal)
      const releases = Array.isArray(payload?.releases) ? payload.releases : []
      return { entries: releases.map(toEntry), total: Number(payload?.total) || 0, offset }
    },
    async details(key, signal) {
      const [sourceId, addonId, version] = String(key).split('/')
      const payload = await api.dispatch({
        operationId: newOperationId(),
        action: 'catalog-details',
        addonId,
        args: { sourceId, version },
      }, signal)
      return { ...toEntry({ ...payload.release, sourceId: payload.release?.sourceId || sourceId }), installed: payload.installed || null }
    },
    async listSources(signal) {
      const payload = await api.dispatch({ operationId: newOperationId(), action: 'catalog-sources' }, signal)
      return Array.isArray(payload?.sources) ? payload.sources : []
    },
    // The source is sent as the editor built it: a reference, never a secret.
    // `operationId` is the caller's, kept across retries of one save.
    async saveSource(source, signal, operationId = newOperationId()) {
      return api.dispatch({ operationId, action: 'catalog-source-save', args: source }, signal)
    },
    async refreshSource(id, signal, operationId = newOperationId()) {
      return api.dispatch({ operationId, action: 'catalog-source-refresh', args: { sourceId: id } }, signal)
    },
    // Add-on lifecycle is its own action set; it never reuses the backend
    // gallery's install and delete calls.
    async submitLifecycle(command, signal) {
      const args = {}
      if (command.sourceId) args.sourceId = command.sourceId
      if (command.version) args.version = command.version
      const payload = await api.dispatch({
        operationId: command.operationId,
        action: command.action,
        addonId: command.addonId,
        generation: command.generation || 0,
        args,
      }, signal)
      return normalizeOperation(payload)
    },
  }
}
