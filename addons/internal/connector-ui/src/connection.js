// What a connection row and its pane say: the state, the cause of a problem and
// the fix, the three permission presets, and the last test kept in the browser.
// No React, so a node test drives it. Nothing here decides what a sender may
// do: a preset only sets the ceiling (caps) the way "Reset to the usual set"
// always did, and the host works out what follows from it.
import { agentHeld, duplicateOther, usualFor, withUsualCaps } from './connector.js'

// ---- The state of a connection ----------------------------------------------

// A reason in the words of the host that says the token is the cause: the
// platform refused it, or the check could not log in.
const TOKEN_REASON = /token|unauthori[sz]ed|not authori[sz]ed|forbidden|credential|api key|\b40[13]\b|invalid_auth|refused|rejected/i

export const looksLikeTokenFailure = (reason) => TOKEN_REASON.test(String(reason || ''))

// Why a connection needs attention, or null. The host gives two causes itself
// (`active` false with a `reason`, and `duplicate`); the others are read from
// the connection (no token saved) and from the last test of this browser.
//   check: { ok, reason } | undefined
// The cause is { cause, reason }, with cause one of 'inactive', 'noToken',
// 'duplicate', 'tokenRejected' or 'testFailed'.
export function attentionOf(c, check, { credentialsOnly = false } = {}) {
  if (!c) return null
  if (c.active === false) return { cause: 'inactive', reason: c.reason || '' }
  if (c.has_token === false && !credentialsOnly) return { cause: 'noToken', reason: '' }
  const other = c.enabled !== false ? duplicateOther(c) : null
  if (other) return { cause: 'duplicate', reason: other.name || '' }
  if (check && check.ok === false) {
    return looksLikeTokenFailure(check.reason)
      ? { cause: 'tokenRejected', reason: check.reason || '' }
      : { cause: 'testFailed', reason: check.reason || '' }
  }
  return null
}

// The state of a row: 'running', 'off' or 'attention'. A connection that holds
// credentials only never listens, so it is 'ready' instead of running; it can
// still need attention. A switched-off connection is 'off' whatever else is
// wrong with it, except that a connection that is not served by a running
// add-on says so.
export function connectionState(c, check, { messaging = true } = {}) {
  const attention = attentionOf(c, check, { credentialsOnly: !messaging || Boolean(c?.credentials_only) })
  if (attention?.cause === 'inactive') return { state: 'attention', attention }
  if (messaging && !c.credentials_only && c.enabled === false) return { state: 'off', attention: null }
  if (attention) return { state: 'attention', attention }
  return { state: messaging && !c.credentials_only ? 'running' : 'ready', attention: null }
}

// The fix a row offers in place of its Test button: 'token' (open the form on
// the token), 'duplicate' (switch this connection off), or null.
export function fixOf(attention) {
  if (!attention) return null
  if (attention.cause === 'noToken' || attention.cause === 'tokenRejected') return 'token'
  if (attention.cause === 'duplicate') return 'duplicate'
  return null
}

// ---- The last test, kept in this browser -------------------------------------

export const CHECKS_KEY = 'localai.connector-checks.v1'
const MAX_CHECKS = 200

function storage() {
  try { return typeof window !== 'undefined' ? window.localStorage : null } catch { return null }
}

// { [id]: { ok, text, reason, at } }. A damaged or missing store reads as empty.
export function readChecks(store = storage()) {
  try {
    const raw = store?.getItem(CHECKS_KEY)
    const data = raw ? JSON.parse(raw) : {}
    if (!data || typeof data !== 'object' || Array.isArray(data)) return {}
    return Object.fromEntries(Object.entries(data).filter(([, v]) => v && typeof v === 'object' && typeof v.ok === 'boolean'))
  } catch { return {} }
}

export function writeChecks(checks, store = storage()) {
  try {
    const entries = Object.entries(checks).sort((a, b) => (b[1].at || 0) - (a[1].at || 0)).slice(0, MAX_CHECKS)
    store?.setItem(CHECKS_KEY, JSON.stringify(Object.fromEntries(entries)))
  } catch { /* a private window keeps nothing; the page works without it */ }
}

// The record of a test: what the page shows, with the time.
export function checkRecord({ ok, text = '', reason = '' }, now = Date.now()) {
  return ok ? { ok: true, text, at: now } : { ok: false, reason, at: now }
}

// A test made before the connection was saved again no longer says anything
// about it: the record is dropped when a save changes the token.
export function dropCheck(checks, id) {
  const { [id]: _gone, ...rest } = checks
  return rest
}

// ---- The permission presets ---------------------------------------------------

// Three presets for what a connection may do. They set the ceiling only:
//   answer    no permission ticked. The bot answers messages and does nothing
//             else. The host accepts an empty ceiling for every kind
//             ("Empty means none").
//   standard  the usual set the host marks, that the agent holds, and never a
//             sensitive one: what "Reset to the usual set" ticks.
//   custom    the whole matrix.
export const PRESETS = ['answer', 'standard', 'custom']

const sameSet = (a, b) => a.length === b.length && a.every((x) => b.includes(x))

// The preset that describes a form now: no permission is 'answer', exactly the
// usual set for the agent is 'standard', anything else is 'custom'.
export function presetOf(form, capabilities, agent) {
  const caps = form?.caps || []
  if (caps.length === 0 && (form?.grants || []).length === 0) return 'answer'
  const usual = usualFor(capabilities, agentHeld(agent)).caps
  return sameSet(caps, usual) && (form?.grants || []).every((g) => g.caps.every((x) => usual.includes(x))) ? 'standard' : 'custom'
}

// The form with a preset applied. 'custom' leaves the permissions as they are.
// A role never keeps a cap that the ceiling lost.
export function applyPreset(form, preset, capabilities, agent) {
  if (preset === 'answer') return { ...form, caps: [], grants: [] }
  if (preset === 'standard') return withUsualCaps(form, capabilities, agentHeld(agent))
  return form
}

// The titles of the permissions the standard preset turns on for an agent, in
// the order of the host's list, to say in words what it gives.
export function standardTitles(capabilities, agent) {
  const caps = usualFor(capabilities, agentHeld(agent)).caps
  return (capabilities || []).filter((c) => caps.includes(c.name)).map((c) => c.title)
}
