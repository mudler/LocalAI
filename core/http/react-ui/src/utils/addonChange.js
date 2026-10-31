// What the chat shows under the tool entry of an add-on change. The server writes
// the entry as "name: summary" on the first line and the host's result document
// after it (capped, so a long one may not parse: then nothing extra is shown).
// A change tool of an enabled add-on is applied at once; the person's switch for
// the add-on is the consent, so there is no Confirm or Cancel. The safety net is
// undo: when the add-on kept a change set, the card has an Undo button. Only the
// fields the host sets are read, never model text.

const NAME = /^addon__([a-z][a-z0-9-]{0,63})__([a-z][a-z0-9-]{0,63})$/
// The shapes the host uses: an operation ID and a resource ID. A button is drawn
// only for IDs of these shapes, so an ID never reaches a URL as anything else.
const CHANGE_ID = /^[a-z][a-z0-9-]{0,63}$/
const RESOURCE_ID = /^[a-z][a-z0-9-]{0,63}$/

function document(text) {
  if (typeof text !== 'string') return null
  const nl = text.indexOf('\n')
  if (nl < 0) return null
  try {
    const v = JSON.parse(text.slice(nl + 1))
    return v && typeof v === 'object' ? v : null
  } catch {
    return null
  }
}

const str = (v) => (typeof v === 'string' ? v : '')

// { kind: 'applied', addonId, action, count, undoable, changeId, resourceId,
// undo, reason } for a change made at once, { kind: 'proposed', addonId } for one
// that is left waiting (the host could not apply it), or null for any other
// entry. undoable is true only with a change ID and a resource ID that can be
// sent; reason says why a change cannot be undone, when the add-on said so.
export function addonChange(text) {
  const first = typeof text === 'string' ? text.split('\n')[0] : ''
  const m = NAME.exec(first.split(':')[0] || '')
  if (!m) return null
  const doc = document(text)
  if (!doc) return null
  if (doc.kind === 'applied' && doc.applied && typeof doc.applied === 'object') {
    const a = doc.applied
    const changeId = str(a.changeId)
    const resourceId = str(a.resourceId)
    const undoable = a.undoable === true && CHANGE_ID.test(changeId) && RESOURCE_ID.test(resourceId)
    return {
      kind: 'applied',
      addonId: m[1],
      action: typeof a.action === 'string' ? a.action : '',
      count: Number.isFinite(a.count) ? a.count : 0,
      undoable,
      changeId: undoable ? changeId : '',
      resourceId: undoable ? resourceId : '',
      undo: undoable ? str(a.undo) : '',
      reason: undoable ? '' : str(a.reason).replace(/^cannot be undone:\s*/i, ''),
    }
  }
  if (doc.kind === 'proposal') return { kind: 'proposed', addonId: m[1] }
  return null
}

// The typed refusals of the add-on tools that the chat explains in plain words.
// invalid_request is listed only when the add-on said why (message).
export const REFUSAL_CODES = ['not_permitted', 'resource_unreachable', 'not_running', 'invalid_request']

// { code, addonId, message } for a tool entry whose result is a typed refusal
// ({"kind":"error","code":...}), or null. message is the add-on's own short
// reason, when it gave one; it is shown as text, never as markup.
export function toolRefusal(text) {
  const doc = document(text)
  if (!doc || doc.kind !== 'error' || !REFUSAL_CODES.includes(doc.code)) return null
  const message = str(doc.message).slice(0, 300)
  if (doc.code === 'invalid_request' && !message) return null
  const first = typeof text === 'string' ? text.split('\n')[0] : ''
  const m = NAME.exec(first.split(':')[0] || '')
  return { code: doc.code, addonId: m ? m[1] : '', message }
}
