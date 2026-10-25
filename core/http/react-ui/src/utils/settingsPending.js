// The pending changes of the Settings page: what differs between the values
// the server returned and the values on screen, the checks that can be made
// in the browser, the request body, and the change log kept in this browser.
//
// Nothing here talks to the server. The checks cover only facts the page can
// ground: a duration parses the way Go parses it, a VRAM budget is one the
// server's parser accepts, a JSON box holds JSON, and the warnings repeat what
// the handler or the field's own description says.

import {
  FIELDS, FIELD_BY_KEY, getValue, valuesEqual, parseGoDuration, vramBudgetError, compactDuration,
} from './settingsSchema.js'

// Fields in the order of the page that differ from `initial`. A field the
// page saves at once (the logos) never appears.
export function pendingChanges(initial, draft) {
  if (!initial || !draft) return []
  const out = []
  for (const field of FIELDS) {
    if (field.immediate) continue
    const from = getValue(field, initial)
    const to = getValue(field, draft)
    if (!valuesEqual(field, from, to)) {
      out.push({ field, from, to, silent: !!field.composite })
    }
  }
  return out
}

// What a person sees in the count and in the diff: the master watchdog switch
// is a shortcut for the two checks below it, which are listed themselves.
export function visibleChanges(changes) {
  return changes.filter(c => !c.silent)
}

export function restartChanges(changes) {
  return visibleChanges(changes).filter(c => c.field.apply === 'restart')
}

// ---- Showing a value ---------------------------------------------------------

function clip(text, n = 44) {
  const t = String(text).replace(/\s+/g, ' ').trim()
  return t.length > n ? `${t.slice(0, n - 1)}…` : t
}

export function jsonCount(text) {
  try {
    const v = JSON.parse(String(text || '').trim() || '[]')
    return Array.isArray(v) ? v.length : null
  } catch { return null }
}

export function linesOf(text) {
  return String(text || '').split(/[\n,]/).map(s => s.trim()).filter(Boolean)
}

export function displayValue(field, v) {
  if (field.sensitive) {
    if (field.key === 'p2p_token' && v === '0') return 'new token'
    return String(v ?? '').trim() ? 'set' : 'empty'
  }
  switch (field.kind) {
    case 'bool': return v ? 'on' : 'off'
    case 'percent': return `${Math.round(Number(v) * 100)}%`
    case 'duration': return String(v ?? '').trim() ? compactDuration(String(v).trim()) : 'empty'
    case 'json': {
      const n = jsonCount(v)
      return n === null ? clip(v) : `${n} ${n === 1 ? 'entry' : 'entries'}`
    }
    case 'lines': {
      const n = linesOf(v).length
      return `${n} ${n === 1 ? 'key' : 'keys'}`
    }
    default: {
      const t = String(v ?? '').trim()
      return t ? clip(t) : 'empty'
    }
  }
}

// ---- Checks ------------------------------------------------------------------
// Each check is { level: 'ok' | 'warn' | 'error', text }. An 'error' stops
// Apply: the server would refuse the same value.

export function runChecks(changes, initial, draft) {
  const checks = []
  const changed = key => changes.find(c => c.field.key === key)
  for (const { field, to } of visibleChanges(changes)) {
    if (field.kind === 'duration') {
      const zero = field.allowZero && (String(to).trim() === '' || String(to).trim() === '0')
      if (zero || parseGoDuration(to) !== null) {
        checks.push({ level: 'ok', text: `${String(to).trim() || '0'} is a valid duration for ${field.label.toLowerCase()}.` })
      } else {
        checks.push({ level: 'error', text: `${field.label}: "${String(to).trim()}" is not a duration. Use values such as 30s, 15m or 1h.` })
      }
    }
    if (field.key === 'vram_budget') {
      const err = vramBudgetError(to)
      checks.push(err ? { level: 'error', text: `${field.label}: ${err}` } : { level: 'ok', text: `${String(to).trim() || 'No cap'} is a valid GPU memory budget.` })
    }
    if (field.kind === 'json') {
      const text = String(to || '').trim()
      if (text) {
        let parsed = null
        try { parsed = JSON.parse(text) } catch { /* reported below */ }
        if (!Array.isArray(parsed)) checks.push({ level: 'error', text: `${field.label}: this must be a JSON list.` })
        else if (parsed.some(g => !g || typeof g !== 'object' || !g.url)) checks.push({ level: 'error', text: `${field.label}: every entry needs a url.` })
        else checks.push({ level: 'ok', text: `${field.label} is valid JSON with ${parsed.length} ${parsed.length === 1 ? 'entry' : 'entries'}.` })
      }
    }
  }
  const restart = restartChanges(changes)
  if (restart.length) {
    checks.push({ level: 'warn', text: `Restart LocalAI for ${restart.map(c => c.field.label).join(', ')} to take effect.` })
  }
  const token = changed('p2p_token')
  if (token && !String(token.to).trim() && String(token.from).trim()) {
    checks.push({ level: 'warn', text: 'An empty P2P token stops peer-to-peer networking.' })
  }
  const force = changed('force_eviction_when_busy')
  if (force && force.to) {
    checks.push({ level: 'warn', text: 'Evicting while busy can interrupt requests in flight.' })
  }
  const csrf = changed('csrf')
  if (csrf && !csrf.to) {
    checks.push({ level: 'warn', text: 'With CSRF protection off, forged cross-site browser requests are not blocked.' })
  }
  const engine = String(draft.agent_pool_vector_engine || 'chromem')
  if (engine === 'postgres' && (changed('agent_pool_vector_engine') || changed('agent_pool_database_url')) && !String(draft.agent_pool_database_url || '').trim()) {
    checks.push({ level: 'warn', text: 'The postgres vector engine needs a database URL.' })
  }
  if (changed('api_keys')) {
    checks.push({ level: 'warn', text: 'Shared keys set in the environment stay in force; only the keys listed here change.' })
  }
  return checks
}

export function hasBlockingCheck(checks) {
  return checks.some(c => c.level === 'error')
}

// ---- Request body ------------------------------------------------------------
// POST /api/settings merges the keys it receives over the saved settings, so
// the page sends only the fields that changed. (The old page sent every field
// each time, which also restarted peer-to-peer networking on every save.)

export function payloadFor(changes, source) {
  const body = {}
  for (const { field } of changes) {
    if (field.composite) {
      body.watchdog_enabled = !!(source.watchdog_idle_enabled || source.watchdog_busy_enabled)
      body.watchdog_idle_enabled = !!source.watchdog_idle_enabled
      body.watchdog_busy_enabled = !!source.watchdog_busy_enabled
      continue
    }
    switch (field.key) {
      case 'galleries':
      case 'backend_galleries': {
        const text = String(getValue(field, source) || '').trim()
        body[field.key] = text ? JSON.parse(text) : []
        break
      }
      case 'api_keys':
        body.api_keys = linesOf(getValue(field, source))
        break
      case 'csrf':
        body.csrf = !!source.csrf
        break
      default:
        body[field.key] = getValue(field, source)
    }
  }
  return body
}

// ---- Change log ---------------------------------------------------------------
// LocalAI keeps no log of settings changes. This one lives in this browser's
// storage, holds the last HISTORY_LIMIT changes applied from it, and says so
// where it is shown. A secret is logged as changed, never with its value.

export const HISTORY_KEY = 'localai_settings_history'
export const HISTORY_LIMIT = 50

export function loadHistory() {
  try {
    const raw = JSON.parse(localStorage.getItem(HISTORY_KEY) || '[]')
    return Array.isArray(raw) ? raw.filter(e => e && FIELD_BY_KEY[e.key]).slice(0, HISTORY_LIMIT) : []
  } catch { return [] }
}

export function historyEntries(changes, at = Date.now()) {
  return visibleChanges(changes).map(({ field, from, to }) => ({
    at,
    key: field.key,
    from: field.sensitive ? null : from,
    to: field.sensitive ? null : to,
  }))
}

export function appendHistory(entries) {
  const next = [...entries.slice().reverse(), ...loadHistory()].slice(0, HISTORY_LIMIT)
  try { localStorage.setItem(HISTORY_KEY, JSON.stringify(next)) } catch { /* storage may be blocked */ }
  return next
}

export function clearHistory() {
  try { localStorage.removeItem(HISTORY_KEY) } catch { /* storage may be blocked */ }
  return []
}
