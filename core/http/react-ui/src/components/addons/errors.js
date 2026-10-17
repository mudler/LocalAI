// One message per kind of answer, the same on every page. The server's words
// are never shown as success and never hidden behind a generic "failed".
export function describeError(error, t) {
  const kind = error && error.kind
  switch (kind) {
    case 'auth': return t('errors.auth')
    case 'denied': return t('errors.denied')
    case 'invalid': return t('errors.invalid')
    case 'unknown_outcome': return t('errors.unknownOutcome')
    case 'network': return t('errors.network')
    default: return t('errors.unavailable')
  }
}

// The words for the outcome of an action the user started. `completed` has
// none: the list on the page shows the new state.
export function describeOutcome(outcome, t) {
  switch (outcome) {
    case 'completed': return ''
    case 'failed': return t('actions.failed')
    case 'unknown': return t('actions.unknown')
    case 'not_applied': return t('actions.notApplied')
    case 'blocked': return t('actions.blocked')
    default: return describeError({ kind: outcome }, t)
  }
}
