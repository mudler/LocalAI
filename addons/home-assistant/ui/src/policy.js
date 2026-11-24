// The model of the policy editor. No React, so a node test drives it. The
// add-on is the authority: it refuses a hard-excluded entry when it saves.
// These helpers repeat the rules only to show the problem next to the field and
// to tell a quiet service from one that waits for a confirmation.

const QUIET = ['light', 'switch', 'input_boolean', 'media_player']
const MEDIA_CONFIRM = ['play_media', 'select_source', 'join', 'unjoin']
const SHAPE = /^[a-z][a-z0-9_]{0,63}$/

export const splitLines = (text) => {
  const seen = new Set()
  const out = []
  for (const part of String(text || '').split(/[\n\r,]+/)) {
    const v = part.trim()
    if (!v || seen.has(v)) continue
    seen.add(v)
    out.push(v)
  }
  return out
}

// Whether a service waits for a person's confirmation on each call.
export function isPhysical(domain, service) {
  if (domain === 'media_player' && MEDIA_CONFIRM.includes(service)) return true
  return !QUIET.includes(domain)
}

// The class of an allowlist entry: 'quiet', 'confirm' or '' when it is not an entry.
export function classOf(entry) {
  const [domain, service, ...rest] = String(entry).split('.')
  if (rest.length || !SHAPE.test(domain || '') || !(service === '*' || SHAPE.test(service || ''))) return ''
  if (service === '*') return QUIET.includes(domain) ? 'quiet' : 'confirm'
  return isPhysical(domain, service) ? 'confirm' : 'quiet'
}

// Why an entry can never be allowed, from the hard rules the add-on sent, or ''.
export function hardIssue(entry, hard) {
  const [domain, service] = String(entry).split('.')
  if (!domain || !service) return ''
  const domains = hard?.domains || []
  if (domains.includes(domain)) return 'hard'
  if (service !== '*' && ((hard?.serviceNames || []).includes(service) || (hard?.servicePrefixes || []).some((p) => service.startsWith(p)))) return 'hard'
  return ''
}

// The first thing wrong with the typed allowlist: { key, entry } or null.
export function allowIssue(text, hard) {
  for (const e of splitLines(text)) {
    if (!classOf(e)) return { key: 'allowShape', entry: e }
    if (hardIssue(e, hard)) return { key: 'allowHard', entry: e }
  }
  return null
}

export function patternIssue(text) {
  for (const p of splitLines(text.replace(/,/g, '\n'))) {
    try { new RegExp(`^(?:${p})$`) } catch (_e) { return { key: 'patternShape', entry: p } }
  }
  return null
}

const FINGERPRINT = /^[0-9a-f]{64}$/
export function pinIssue(text) {
  for (const line of String(text || '').split(/\n+/).map((l) => l.trim()).filter(Boolean)) {
    const [hp, fp] = line.split('=')
    if (!hp || !hp.includes(':') || !fp || !FINGERPRINT.test(fp.replace(/:/g, '').toLowerCase())) return { key: 'pinShape', entry: line.length > 60 ? `${line.slice(0, 57)}...` : line }
  }
  return null
}

// The draft the form edits, from the policy the add-on sent.
export function draftOf(policy) {
  const p = policy || {}
  return {
    allow: (p.allow || []).join('\n'),
    excludeDomains: (p.excludeDomains || []).join('\n'),
    excludeAreas: (p.excludeAreas || []).join('\n'),
    excludePatterns: (p.excludePatterns || []).join('\n'),
    excludeLabel: p.excludeLabel || '',
    maxTargets: String(p.maxTargets || 5),
    pins: (p.pins || []).join('\n'),
  }
}

// The first problem of a draft, as { key, entry } for the field's words, or null.
export function draftIssue(d, hard) {
  const a = allowIssue(d.allow, hard)
  if (a) return { field: 'allow', ...a }
  const p = patternIssue(d.excludePatterns)
  if (p) return { field: 'excludePatterns', ...p }
  const n = Number(d.maxTargets)
  if (!Number.isInteger(n) || n < 1 || n > 20) return { field: 'maxTargets', key: 'targetsShape', entry: '' }
  const pin = pinIssue(d.pins)
  if (pin) return { field: 'pins', ...pin }
  return null
}

// The arguments of set-policy that a draft makes.
export function argsOf(d) {
  return {
    allow: splitLines(d.allow),
    excludeDomains: splitLines(d.excludeDomains),
    excludeAreas: splitLines(d.excludeAreas),
    excludePatterns: splitLines(d.excludePatterns),
    excludeLabel: d.excludeLabel.trim(),
    maxTargets: Number(d.maxTargets),
    pins: String(d.pins || '').split(/\n+/).map((l) => l.trim()).filter(Boolean),
  }
}

export const same = (a, b) => JSON.stringify(a) === JSON.stringify(b)

// The allowlist entries that wait for a confirmation, grouped by domain, for the summary line.
export function confirmDomains(allowText) {
  const out = new Set()
  for (const e of splitLines(allowText)) if (classOf(e) === 'confirm') out.add(e.split('.')[0])
  return [...out].sort()
}
