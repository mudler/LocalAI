// The client of the plugin routes (/api/plugins, and the plugin switch of a
// chat). Pure: the base path and fetch are injected, so the unit tests run it
// with a fake fetch. utils/pluginsClient.js binds it to the page.
//
// A refused call throws a PluginsError with the HTTP status, the error type
// the server names (invalid_token, needs_admin, needs_reenable, ...), its
// param and its message. A plugin that runs code is installed, updated and
// rolled back with the X-LocalAI-Confirm header, which only a same-origin
// fetch of this page can set: the plugin ID for an install, the install ID
// for an update and a rollback.

export class PluginsError extends Error {
  constructor({ status = 0, type = '', param = '', message = '' } = {}) {
    super(message || type || `HTTP ${status}`)
    this.name = 'PluginsError'
    this.status = status
    this.type = type
    this.param = param
  }
}

const enc = encodeURIComponent
// A file path keeps its slashes: the route takes the rest of the path.
const encPath = (p) => String(p || '').split('/').map(enc).join('/')

export function createPluginsApi({ base = (p) => p, fetch: doFetch = (...a) => globalThis.fetch(...a) } = {}) {
  async function send(path, { method = 'GET', body, signal, headers } = {}) {
    let response
    try {
      response = await doFetch(base(path), {
        method,
        signal,
        credentials: 'same-origin',
        headers: { Accept: 'application/json', ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...(headers || {}) },
        body: body === undefined ? undefined : JSON.stringify(body),
      })
    } catch (e) {
      if (e && e.name === 'AbortError') throw e
      throw new PluginsError({ type: 'network', message: e && e.message })
    }
    return response
  }

  async function call(path, opts) {
    const response = await send(path, opts)
    let data = null
    try { data = await response.json() } catch { data = null }
    if (!response.ok) {
      const err = data && typeof data.error === 'object' && data.error ? data.error : {}
      throw new PluginsError({
        status: response.status,
        type: typeof err.type === 'string' ? err.type : '',
        param: typeof err.param === 'string' ? err.param : '',
        message: typeof err.message === 'string' ? err.message : (typeof data?.error === 'string' ? data.error : ''),
      })
    }
    return data
  }

  // A file for review comes as plain text. It is returned as a string and
  // must be drawn as text, never as HTML.
  async function text(path, signal) {
    const response = await send(path, { signal, headers: { Accept: 'text/plain' } })
    if (!response.ok) {
      let data = null
      try { data = await response.json() } catch { data = null }
      throw new PluginsError({ status: response.status, type: data?.error?.type || '', message: data?.error?.message || '' })
    }
    return response.text()
  }

  const confirm = (id) => ({ 'X-LocalAI-Confirm': id })

  return {
    marketplaces: {
      async list(signal) {
        const out = await call('/api/plugins/marketplaces', { signal })
        return Array.isArray(out?.marketplaces) ? out.marketplaces : []
      },
      add: ({ source, ref = '', id = '' }) => call('/api/plugins/marketplaces', {
        method: 'POST', body: { source: String(source || '').trim(), ...(ref.trim() ? { ref: ref.trim() } : {}), ...(id.trim() ? { id: id.trim() } : {}) },
      }),
      update: (id, fields) => call(`/api/plugins/marketplaces/${enc(id)}`, { method: 'PUT', body: fields }),
      remove: (id) => call(`/api/plugins/marketplaces/${enc(id)}`, { method: 'DELETE' }),
      refresh: (id) => call(`/api/plugins/marketplaces/${enc(id)}/refresh`, { method: 'POST' }),
    },
    // query: { q, marketplace, category, runs_code, offset, limit }
    async catalog(query = {}, signal) {
      const params = new URLSearchParams()
      for (const [k, v] of Object.entries(query)) if (v !== undefined && v !== null && v !== '') params.set(k, String(v))
      const qs = params.toString()
      const out = await call(`/api/plugins/catalog${qs ? `?${qs}` : ''}`, { signal })
      return { items: Array.isArray(out?.items) ? out.items : [], total: out?.total || 0, offset: out?.offset || 0, limit: out?.limit || 0 }
    },
    entry: (marketplace, name, signal) => call(`/api/plugins/catalog/${enc(marketplace)}/${enc(name)}`, { signal }),
    inspect: (plugin, signal) => call('/api/plugins/inspect', { method: 'POST', body: { plugin }, signal }),
    previewFile: (token, path, signal) => text(`/api/plugins/inspect/${enc(token)}/files/${encPath(path)}`, signal),
    // preview: the answer of inspect. A code plugin sends the confirmation.
    install: (preview, scope = 'user') => call('/api/plugins/installs', {
      method: 'POST', body: { token: preview.token, scope }, headers: preview.runsCode ? confirm(preview.plugin) : undefined,
    }),
    async installs({ all = false } = {}, signal) {
      const out = await call(`/api/plugins/installs${all ? '?all=true' : ''}`, { signal })
      return Array.isArray(out?.installs) ? out.installs : []
    },
    detail: (id, signal) => call(`/api/plugins/installs/${enc(id)}`, { signal }),
    installFile: (id, path, signal) => text(`/api/plugins/installs/${enc(id)}/files/${encPath(path)}`, signal),
    previewUpdate: (id) => call(`/api/plugins/installs/${enc(id)}/update/preview`, { method: 'POST' }),
    // preview: the answer of previewUpdate; approved: the code files ticked.
    update: (id, preview, approved = []) => call(`/api/plugins/installs/${enc(id)}/update`, {
      method: 'POST',
      body: { token: preview.token, ...(preview.needsCodeReview ? { approved_files: approved } : {}) },
      headers: preview.needsCodeReview ? confirm(id) : undefined,
    }),
    rollback: (id) => call(`/api/plugins/installs/${enc(id)}/rollback`, { method: 'POST', headers: confirm(id) }),
    remove: (id) => call(`/api/plugins/installs/${enc(id)}`, { method: 'DELETE' }),
    me: (id, signal) => call(`/api/plugins/installs/${enc(id)}/me`, { signal }),
    // fields: { enabled, reenable, config }
    setMe: (id, fields) => call(`/api/plugins/installs/${enc(id)}/me`, { method: 'PUT', body: fields }),
    async setChatOff(chatId, off) {
      const out = await call(`/api/chats/${enc(chatId)}/plugins`, { method: 'PUT', body: { off } })
      return Array.isArray(out?.off) ? out.off : off
    },
  }
}
