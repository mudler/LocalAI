// The reason the server gave for a refusal, as sent. AddonsError falls back to
// the kind when the body had no message, and that is not a reason.
function serverReason(error) {
  const m = error && typeof error.message === 'string' ? error.message.trim() : ''
  return m && m !== error.kind ? m : ''
}

// The cause of a failed install or upgrade of an add-on that needs a newer LocalAI.
const NEEDS_NEWER_HOST = 'needs_newer_host'

const HOST_REFUSALS = new Set(['not_human_surface', 'locked_by_deployment', 'blocked_by_policy', 'revision_conflict', 'approval_stale', 'approval_required'])

// One message per kind of answer, the same on every page. The server's words
// are never shown as success and never hidden behind a generic "failed". A
// refusal (403 or 400) shows the server's own reason when the body has one,
// untranslated, and the generic text only when it has none.
export function describeError(error, t) {
  const kind = error && error.kind
  // The typed refusals of the host access routes have words of their own.
  if (error && error.type && HOST_REFUSALS.has(error.type)) return t(`hostAccess.errors.${error.type}`)
  if (error && error.type === NEEDS_NEWER_HOST) return t('errors.needsNewerHost')
  switch (kind) {
    case 'auth': return t('errors.auth')
    case 'denied': return serverReason(error) || t('errors.denied')
    case 'invalid': return serverReason(error) || t('errors.invalid')
    case 'unknown_outcome': return t('errors.unknownOutcome')
    case 'network': return t('errors.network')
    case 'busy': return t('errors.busy')
    default: return t('errors.unavailable')
  }
}

// The words for the outcome of an action the user started. `completed` has
// none: the list on the page shows the new state.
export function describeOutcome(outcome, t, error) {
  // A failed install or upgrade of an add-on that needs a newer LocalAI says so.
  if (error && error.type === NEEDS_NEWER_HOST) return describeError(error, t)
  switch (outcome) {
    case 'completed': return ''
    case 'failed': return t('actions.failed')
    case 'unknown': return t('actions.unknown')
    case 'not_applied': return t('actions.notApplied')
    case 'blocked': return t('actions.blocked')
    default: return describeError(error && error.type ? error : { kind: outcome, message: error && error.message }, t)
  }
}
