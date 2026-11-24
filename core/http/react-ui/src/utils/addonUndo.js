// The undo of a change a chat made at once. The route is the person's own
// action: a same-origin fetch of this page, never a tool, so the model cannot
// call it. The server decides every answer; this module shapes the request and
// turns the typed refusals into what the change card says.
import { classifyResponse } from '../components/catalog/catalogState.js'
import { AddonsError } from './addonsApi.js'

const TYPED = { 404: ['not_found'], 409: ['stale', 'already_undone', 'cannot_undo'], 410: ['expired'] }

// The refusals of an undo are typed by their error type; the status confirms
// it. Everything else is a denial, an unknown outcome or an outage, as for any
// other add-on route.
export function classifyUndo(status, body) {
  const err = body && typeof body === 'object' ? body.error : null
  const type = err && typeof err === 'object' && typeof err.type === 'string' ? err.type : ''
  if ((TYPED[status] || []).includes(type)) {
    return { kind: type, status, type, message: typeof err.message === 'string' ? err.message : '' }
  }
  return classifyResponse(status, body)
}

// The key of the sentence for an outcome, in the chats locale.
export function undoMessageKey(kind) {
  switch (kind) {
    case 'done': return 'tool.undoDone'
    case 'stale': return 'tool.undoStale'
    case 'not_found': return 'tool.undoNotFound'
    case 'expired': return 'tool.undoExpired'
    case 'already_undone': return 'tool.undoAlready'
    case 'cannot_undo': return 'tool.undoCannot'
    case 'denied': return 'tool.undoDenied'
    case 'unknown': return 'tool.undoUnknown'
    default: return 'tool.undoUnavailable'
  }
}

// Runs one undo. operationId is the person's own and is kept by the card, so a
// repeat after an unknown outcome is the same operation. The generation is the
// one the add-on has now: the host refuses a stale one.
export async function runUndo({ api, addonId, resourceId, changeId, operationId, signal }) {
  try {
    const apps = await api.apps.list(signal)
    const app = apps.find(a => a && a.id === addonId)
    const generation = Number(app && app.generation)
    if (!generation) return { kind: 'unavailable' }
    const out = await api.changes.undo(resourceId, changeId, { operationId, generation }, signal)
    return { kind: 'done', deleted: !!(out && out.deleted) }
  } catch (e) {
    if (e && e.name === 'AbortError') throw e
    if (e instanceof AddonsError) {
      if (['stale', 'not_found', 'expired', 'already_undone', 'cannot_undo'].includes(e.kind)) return { kind: e.kind, message: e.message || '' }
      if (e.kind === 'denied') return { kind: 'denied' }
      if (e.kind === 'unknown_outcome') return { kind: 'unknown' }
    }
    return { kind: 'unavailable' }
  }
}
