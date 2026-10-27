// What the chat shows under the tool entry of an add-on change. The server writes
// the entry as "name: summary" on the first line and the host's result document
// after it (capped, so a long one may not parse: then nothing extra is shown).
// A change tool of an enabled add-on is applied at once; the person's switch for
// the add-on is the consent, so there is no Confirm or Cancel. Only the fields
// the host sets are read, never model text.

const NAME = /^addon__([a-z][a-z0-9-]{0,63})__([a-z][a-z0-9-]{0,63})$/

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

// { kind: 'applied', addonId, action, count, undoable } for a change made at
// once, { kind: 'proposed', addonId } for one that is left waiting (the host
// could not apply it), or null for any other entry.
export function addonChange(text) {
  const first = typeof text === 'string' ? text.split('\n')[0] : ''
  const m = NAME.exec(first.split(':')[0] || '')
  if (!m) return null
  const doc = document(text)
  if (!doc) return null
  if (doc.kind === 'applied' && doc.applied && typeof doc.applied === 'object') {
    const a = doc.applied
    return {
      kind: 'applied',
      addonId: m[1],
      action: typeof a.action === 'string' ? a.action : '',
      count: Number.isFinite(a.count) ? a.count : 0,
      undoable: a.undoable === true,
    }
  }
  if (doc.kind === 'proposal') return { kind: 'proposed', addonId: m[1] }
  return null
}
