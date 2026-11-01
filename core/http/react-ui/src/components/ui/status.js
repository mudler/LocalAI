// One status vocabulary for every chip in the product. A status word maps to
// a tone (the colour), an icon and a label key in common.json, so a state is
// always told three ways: colour, glyph and words. Colour alone fails for
// people who do not see it, and words alone are slow to scan in a long list.
//
// Tones: ok (it works), warn (it needs a decision), error (it failed),
// idle (nothing is happening) and accent (it is busy or new).

export const TONES = ['ok', 'warn', 'error', 'idle', 'accent']

export const TONE_ICON = {
  ok: 'check-circle',
  warn: 'warning',
  error: 'alert-circle',
  idle: 'circle',
  accent: 'info',
}

// The product's state words. `label` is a key under common:chip. Keep one
// state per chip: "Off, starting" is two states and reads as a contradiction.
export const STATUS = {
  running: { tone: 'ok', icon: 'check-circle', label: 'running' },
  ready: { tone: 'ok', icon: 'check-circle', label: 'ready' },
  starting: { tone: 'accent', icon: 'spinner', label: 'starting', busy: true },
  stopped: { tone: 'idle', icon: 'circle', label: 'stopped' },
  failed: { tone: 'error', icon: 'alert-circle', label: 'failed' },
  'update-available': { tone: 'warn', icon: 'arrow-up', label: 'updateAvailable' },
  'no-runs': { tone: 'idle', icon: 'circle', label: 'noRunsYet' },
}

// Words other parts of the app already use, and the state they mean. The
// failover screens and the add-on SDK pass these to StatusPill.
export const STATUS_ALIASES = {
  healthy: 'running',
  online: 'running',
  primary: 'running',
  loading: 'starting',
  installing: 'starting',
  idle: 'stopped',
  offline: 'stopped',
  missing: 'stopped',
  error: 'failed',
  unhealthy: 'failed',
  degraded: 'failed',
  down: 'failed',
}

// The tone of a word that is a warning but not one of the states above
// (a failover chain on its fallback target, a node that drains).
const WARN_WORDS = new Set(['warning', 'draining', 'fallback', 'recovering'])

// Resolves a status word, or an explicit tone, to { tone, icon, label, busy }.
// `label` is undefined for a word outside the vocabulary: the caller passes
// its own text then. An unknown word is idle, never ok: a chip must not claim
// health it cannot prove.
export function resolveStatus({ status, tone } = {}) {
  if (tone && TONES.includes(tone)) {
    const known = STATUS[STATUS_ALIASES[status] || status]
    return { tone, icon: known?.tone === tone ? known.icon : TONE_ICON[tone], label: known?.tone === tone ? known.label : undefined, busy: !!(known?.tone === tone && known.busy) }
  }
  const key = STATUS_ALIASES[status] || status
  if (STATUS[key]) return { ...STATUS[key], busy: !!STATUS[key].busy }
  if (WARN_WORDS.has(status)) return { tone: 'warn', icon: TONE_ICON.warn, label: undefined, busy: false }
  return { tone: 'idle', icon: TONE_ICON.idle, label: undefined, busy: false }
}

// The class the old StatusPill put on its root, kept so existing selectors
// (and the specs that read them) still match.
const LEGACY_TONE = { ok: 'success', warn: 'warning', error: 'error', idle: 'muted', accent: 'info' }
export function legacyPillTone(tone) {
  return LEGACY_TONE[tone] || 'muted'
}
