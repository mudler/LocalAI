// Local "install requested" flags of the model gallery, one timestamp per
// model id. A flag covers the gap between the click and the first operation
// showing up in the operations list. It must always end: when the request is
// rejected, when the operation finishes, or after FALLBACK_MS if no operation
// ever appears. All functions return the same Map when nothing changes.

export const FALLBACK_MS = 5000

// Adds a flag, or returns the same map when one is already set (a double
// click on the same model).
export function addFlag(flags, id, now) {
  if (flags.has(id)) return flags
  return new Map(flags).set(id, now)
}

// Removes a flag. With `stamp`, only the flag set at that time: an old timer
// must not clear the flag of a newer attempt.
export function removeFlag(flags, id, stamp) {
  if (!flags.has(id)) return flags
  if (stamp !== undefined && flags.get(id) !== stamp) return flags
  const next = new Map(flags)
  next.delete(id)
  return next
}
