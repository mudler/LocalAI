// Maps what the host SDK throws to what the person is told and what they can
// do next. The server is the authority: this only chooses words and a
// recovery, never a permission.

// describeError returns { kind, key, retry }.
//   kind   stale | denied | blocked | unavailable | unknown | invalid |
//          sharing | quota | not-selected | withdrawn | network | error
//   key    the message key of the add-on's own bundle
//   retry  whether repeating the same action can help without a reload
export function describeError(error) {
  const e = error || {}
  if (e.code === 'withdrawn' || e.name === 'AbortError') return { kind: 'withdrawn', key: 'error.withdrawn', retry: false }
  switch (e.kind) {
    case 'stale': return { kind: 'stale', key: 'error.stale', retry: false }
    case 'sharing_disabled': return { kind: 'sharing', key: 'error.sharingDisabled', retry: false }
    case 'not_selected': return { kind: 'not-selected', key: 'error.notSelected', retry: false }
    case 'unknown_outcome': return { kind: 'unknown', key: 'error.unknownOutcome', retry: true, operationId: e.operationId || '' }
    case 'invalid': return { kind: 'invalid', key: 'error.invalid', retry: false }
    case 'auth': return { kind: 'denied', key: 'error.auth', retry: false }
    case 'denied':
      if (e.type === 'quota_reached') return { kind: 'quota', key: 'error.quota', retry: false }
      if (e.type === 'outcome_blocked') return { kind: 'blocked', key: 'error.blocked', retry: false }
      return { kind: 'denied', key: 'error.denied', retry: false }
    case 'network': return { kind: 'network', key: 'error.network', retry: true }
    case 'unavailable': return { kind: 'unavailable', key: 'error.unavailable', retry: true }
    default: return { kind: 'error', key: 'error.generic', retry: false }
  }
}
