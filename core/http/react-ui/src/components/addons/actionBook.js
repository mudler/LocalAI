// Operation identities for add-on actions, bound to the subject they act on.
//
// An operation ID belongs to ONE user action and its retries. The owner
// replays a known ID: when the command matches and the operation completed, it
// answers with the old status and applies nothing. So an ID must never outlive
// its action, or a later click on the same button would be a silent no-op.
//
// Rules, per subject (one add-on):
//   - begin() with the key that holds the identity returns the same ID (a
//     retry of the same action).
//   - begin() with another key while the held action is unresolved drops that
//     identity and mints a new one, since the user moved on.
//   - begin() with another key while the held action has an UNKNOWN outcome is
//     blocked: the earlier action may still be applied, so it must be read
//     (resolve) or dismissed by the user first.
//   - resolve() drops the identity: the next click mints a fresh ID.
import { newOperationId } from '../catalog/catalogState.js'

export function createActionBook(makeId = newOperationId) {
  const held = new Map()
  return {
    begin(subject, key, meta) {
      const cur = held.get(subject)
      if (cur && cur.key === key) {
        if (meta !== undefined) cur.meta = meta
        return { operationId: cur.operationId }
      }
      if (cur && cur.unknown) return { blocked: true, key: cur.key, operationId: cur.operationId }
      const entry = { key, operationId: makeId(), unknown: false, meta }
      held.set(subject, entry)
      return { operationId: entry.operationId }
    },
    markUnknown(subject, key) {
      const cur = held.get(subject)
      if (cur && cur.key === key) cur.unknown = true
    },
    pending(subject) {
      const cur = held.get(subject)
      return cur ? { ...cur } : null
    },
    resolve(subject, key) {
      const cur = held.get(subject)
      if (cur && cur.key === key) held.delete(subject)
    },
    size: () => held.size,
  }
}

// What a final operation status means for the user. A completed status of an
// operation the owner replayed says nothing about this click, so when the
// caller could read the add-on afterwards and it did not change, the action is
// reported as not applied. `verified` is true, false or undefined (not read).
export function effectOutcome(outcome, verified) {
  if (outcome === 'completed' && verified === false) return 'not_applied'
  return outcome
}
