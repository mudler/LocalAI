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
