// Client of the host-grants routes: what an add-on may ask of this server,
// who approved it, and the deny rules and the blanket switch. Every write is
// the administrator's own action from this page: the confirm header names the
// subject, which only a same-origin fetch of the host's own page can set. An
// API key, a tool or an agent cannot make these calls; the owner refuses them
// whatever this module sends.
import { classifyResponse } from '../components/catalog/catalogState.js'

const BASE = '/api/addons/host-grants'

// Typed refusals are final answers: nothing was changed.
const TYPES = new Set([
  'not_human_surface', 'locked_by_deployment', 'blocked_by_policy',
  'revision_conflict', 'approval_stale', 'approval_required',
])

export function classifyHostGrants(status, body) {
  const err = body && typeof body === 'object' ? body.error : null
  const type = err && typeof err === 'object' && typeof err.type === 'string' ? err.type : ''
  if (TYPES.has(type)) {
    return { kind: 'invalid', type, status, message: typeof err.message === 'string' ? err.message : '', details: err }
  }
  return classifyResponse(status, body)
}

// The value of the confirm header: the add-on (and the package digest for an
// install) the administrator is acting on.
export function confirmValue(...parts) {
  return parts.filter(Boolean).join('@')
}

export function createHostGrantsApi(addonsApi) {
  const call = (path, opts = {}) => addonsApi.call(`${BASE}${path}`, { classify: classifyHostGrants, ...opts })
  const write = (path, method, body, confirm, signal) => call(path, { method, body, signal, headers: { 'X-LocalAI-Confirm': confirm } })
  const id = encodeURIComponent
  return {
    overview(signal) { return call('', { signal }) },
    preview({ sourceId, addonId, version }, signal) {
      return call('/preview', { method: 'POST', body: { sourceId, addonId, version }, signal })
    },
    // The install or upgrade with the approval. The answer is the lifecycle
    // operation status; the caller follows it as it follows any lifecycle.
    install(body, signal) {
      return write('/install', 'POST', body, confirmValue(body.addonId, body.approve && body.approve.digest), signal)
    },
    setOps(addonId, { revision, ops }, signal) {
      return write(`/${id(addonId)}`, 'PUT', { revision, ops }, confirmValue(addonId), signal)
    },
    revoke(addonId, revision, signal) {
      return write(`/${id(addonId)}?revision=${encodeURIComponent(revision)}`, 'DELETE', undefined, confirmValue(addonId), signal)
    },
    setBlanket(body, signal) { return write('/blanket', 'PUT', body, 'blanket', signal) },
    setDeny({ revision, deny, dryRun = false }, signal) {
      return write('/deny', 'PUT', { revision, deny, ...(dryRun ? { dryRun: true } : {}) }, 'deny', signal)
    },
  }
}
