// What the banner of a failed chat turn says. The server sends a code that names
// the cause (model_backend_missing, upstream_key, ...), the model it is about
// and a cleaned detail. The words come from the locale files, so they are in the
// reader's language; a code the page does not know falls back to the server's
// own English sentence.

// Codes that have words of their own. The ones that are not here (not_allowed,
// quota, context_too_small, unknown_tool, agent) carry a sentence built by the
// server, which shows as it is.
const REASON = {
  model_backend_missing: 'error.reason.model_backend_missing',
  model_cooldown: 'error.reason.model_cooldown',
  model_load_failed: 'error.reason.model_load_failed',
  model_not_found: 'error.reason.model_not_found',
  upstream_key: 'error.reason.upstream_key',
  upstream_path: 'error.reason.upstream_path',
  upstream_unreachable: 'error.reason.upstream_unreachable',
  upstream: 'error.reason.upstream',
  timeout: 'error.reason.timeout',
  cancelled: 'error.reason.cancelled',
}

const ABOUT_MODEL = new Set(['model_backend_missing', 'model_cooldown', 'model_load_failed', 'model_not_found'])

const HINT = {
  model_backend_missing: 'error.hint.model_backend_missing',
  model_cooldown: 'error.hint.model_cooldown',
  model_load_failed: 'error.hint.model_load_failed',
  model_not_found: 'error.hint.model_not_found',
  upstream_key: 'error.hint.upstream_key',
  upstream_path: 'error.hint.upstream_path',
  upstream_unreachable: 'error.hint.upstream_unreachable',
  upstream: 'error.hint.upstream',
  timeout: 'error.hint.timeout',
  cancelled: 'error.hint.cancelled',
}

// failureView turns the failure of a turn into what the banner shows:
// { reason, hint, detail, backendsLink }. t is the translation function of the
// chat namespace. failure is null when the server did not say why (the page was
// reloaded, or the stream missed the event).
export function failureView(t, failure, { isAdmin = false } = {}) {
  if (!failure || !failure.code) {
    return { reason: t('error.reason.unknown'), hint: '', detail: '', backendsLink: false }
  }
  const { code, model, retry, detail, text } = failure
  const key = REASON[code]
  if (!key) return { reason: text || t('error.reason.unknown'), hint: '', detail: detail || '', backendsLink: false }

  const vars = { model, seconds: retry }
  // A reason about a model has one wording that names it, and one for a model
  // the server did not name.
  const reason = ABOUT_MODEL.has(code) && !model ? t(`${key}_anon`, vars) : t(key, vars)
  let hintKey = HINT[code]
  // An administrator can install the backend; the others must ask.
  if (code === 'model_backend_missing' && isAdmin) hintKey += '_admin'
  const hint = t(hintKey, vars)
  return { reason, hint, detail: detail || '', backendsLink: code === 'model_backend_missing' && isAdmin }
}

// A failure older than this, seen after a reload, no longer shows as a banner.
export const STALE_FAILURE_MS = 24 * 60 * 60 * 1000

// isStaleFailure says whether the chat was last touched too long ago for its
// failed turn to still be news. A missing or unreadable time is not stale.
export function isStaleFailure(updated, now = Date.now()) {
  const at = Date.parse(updated || '')
  return Number.isFinite(at) && now - at > STALE_FAILURE_MS
}
