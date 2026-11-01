// Pure rules of the update display, so the node unit tests drive them without
// a browser. The owner decides whether an update exists: this module never
// compares versions and never reads a failed or unknown check as current.
import { classifyResponse } from '../catalog/catalogState.js'

export const UPDATE_STATES = ['unknown', 'up-to-date', 'update-available', 'incompatible', 'check-failed', 'integrity-error']

// The typed refusals of the update routes. Each one says that nothing was
// applied (or, for upgrade_failed, that the installed version is kept).
export const UPDATE_TYPES = new Set([
  'stale_installed', 'candidate_changed', 'upgrade_conflict', 'incompatible', 'integrity_error',
  'upgrade_failed', 'operation_conflict', 'check_failed',
])

// A typed refusal is a final answer: the operation identity is dropped and the
// next click is a new operation. Everything else is classified as on the other
// add-on routes.
export function classifyUpdate(status, body) {
  const err = body && typeof body === 'object' ? body.error : null
  const type = err && typeof err === 'object' && typeof err.type === 'string' ? err.type : ''
  if (UPDATE_TYPES.has(type)) {
    return { kind: 'invalid', type, status, message: typeof err.message === 'string' ? err.message : '' }
  }
  return classifyResponse(status, body)
}

const TONES = {
  'up-to-date': 'ok',
  'update-available': 'warn',
  incompatible: 'warn',
  'check-failed': 'error',
  'integrity-error': 'error',
  unknown: 'idle',
}

const REASONS = new Set(['platform', 'host-version', 'api-version', 'protocol-version', 'sdk-version', 'data-format', 'digest-changed'])
const CLASSES = new Set(['source-unavailable', 'refresh-failed', 'credential-unavailable', 'not-listed', 'ambiguous-release', 'canceled', 'internal'])

// The view of the `update` object of an installed add-on. Anything the page
// does not know is `unknown`, never `up-to-date`.
export function updateView(update) {
  const u = update && typeof update === 'object' ? update : null
  if (!u) return { state: 'unknown', tone: 'idle', canUpgrade: false, reasons: [], error: '' }
  let state = typeof u.state === 'string' && UPDATE_STATES.includes(u.state) ? u.state : ''
  // An owner of an earlier release sent only `available`: true is an update,
  // false says nothing.
  if (!state) state = u.available === true ? 'update-available' : 'unknown'
  const digest = typeof u.digest === 'string' ? u.digest : ''
  return {
    state,
    tone: TONES[state] || 'idle',
    version: typeof u.version === 'string' ? u.version : '',
    digest,
    checkedAt: typeof u.checkedAt === 'string' ? u.checkedAt : '',
    lastSuccess: typeof u.lastSuccess === 'string' ? u.lastSuccess : '',
    stale: u.stale === true,
    reasons: Array.isArray(u.reasons) ? u.reasons.filter(r => REASONS.has(r)) : [],
    error: typeof u.error === 'string' && CLASSES.has(u.error) ? u.error : (typeof u.error === 'string' && u.error ? 'internal' : ''),
    // The upgrade is only offered for an owner-confirmed candidate with a
    // digest to pin.
    canUpgrade: state === 'update-available' && u.available !== false && digest !== '',
  }
}

// The body of an upgrade request: the installed package the page showed and
// the candidate the check resolved. The owner checks both again.
export function upgradeBody(addon, operationId) {
  const view = updateView(addon && addon.update)
  if (!view.canUpgrade || !addon.digest) return null
  return { operationId, expectedDigest: addon.digest, candidateDigest: view.digest, generation: Number(addon.generation) || 0 }
}

// The key of the words for a refusal or a failed outcome of an update action.
export function updateErrorKey(error) {
  if (!error) return 'unavailable'
  if (error.type && UPDATE_TYPES.has(error.type)) return error.type
  if (error.kind === 'unknown_outcome') return 'unknownOutcome'
  if (error.kind === 'denied') return 'denied'
  if (error.kind === 'auth') return 'auth'
  if (error.kind === 'network') return 'network'
  return 'unavailable'
}

// A time of the owner as the reader's own, or nothing.
export function formatWhen(iso, language) {
  const t = Date.parse(iso)
  if (!iso || Number.isNaN(t)) return ''
  try {
    return new Date(t).toLocaleString(language || undefined, { dateStyle: 'medium', timeStyle: 'short' })
  } catch {
    return new Date(t).toISOString()
  }
}

// Whether an upgrade can start for an add-on, and when it cannot, why. The page
// shows the reason next to the notice instead of hiding the action. `pending`
// is true when the list already carries the "update available" notice, so a
// record that disagrees (or is missing) is explained rather than ignored.
// `canCheck` says a new check could change the answer. `checked` is true when
// a check this page ran has answered: a candidate that still has no digest is
// then explained, not sent back to another check that gives the same answer.
export function upgradeAvailability(addon, { isAdmin, pending = false, checked = false } = {}) {
  const view = updateView(addon && addon.update)
  const notice = pending || view.state === 'update-available'
  const no = (reason, extra = {}) => ({ startable: false, reason, reasons: [], canCheck: false, ...extra })
  if (!notice && view.state !== 'incompatible' && view.state !== 'integrity-error') return no('')
  if (!isAdmin) return no('role')
  if (addon && addon.recovery) return no('recovery')
  switch (view.state) {
    case 'update-available':
      if (view.canUpgrade) return { startable: true, reason: '', reasons: [], canCheck: false }
      return checked ? no('unpinned', { canCheck: true }) : no('needs-check', { canCheck: true })
    case 'incompatible': return no('incompatible', { reasons: view.reasons })
    case 'integrity-error': return no('integrity')
    case 'check-failed': return no('check-failed', { canCheck: true })
    case 'up-to-date': return no('current', { canCheck: true })
    default: return no('needs-check', { canCheck: true })
  }
}

// How long after a check its answer is "just now".
const JUST_NOW_MS = 60 * 1000

// What the page says after the "Check again" or "Check for updates" it ran:
// the state the owner answered, with its time, or why the check failed. The
// check route answers the owner's status, whose candidate fields are named
// candidateVersion and candidateDigest. `entry` is the check record of
// useAddonUpgrade; null means there is nothing to say yet.
export function checkFeedback(entry, now = Date.now()) {
  if (!entry || entry.phase === 'working') return null
  if (entry.phase === 'error') {
    return { tone: 'error', state: '', version: '', justNow: false, checkedAt: '', throttled: false, unpinned: false, errorKey: updateErrorKey(entry.error) }
  }
  const raw = entry.status && typeof entry.status === 'object' ? entry.status : null
  const view = updateView(raw ? { ...raw, version: raw.version ?? raw.candidateVersion, digest: raw.digest ?? raw.candidateDigest } : null)
  const at = Date.parse(view.checkedAt || '')
  return {
    tone: view.tone,
    state: view.state,
    version: view.version || '',
    justNow: !Number.isNaN(at) && Math.abs(now - at) < JUST_NOW_MS,
    checkedAt: view.checkedAt || '',
    throttled: Boolean(entry.throttled),
    unpinned: view.state === 'update-available' && !view.canUpgrade,
    errorKey: '',
  }
}
