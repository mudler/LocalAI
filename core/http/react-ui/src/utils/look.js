// The assistant identity (persona.json, home.json headline), the look tokens
// (ui.json look and accent) and the text overrides (ui.json strings): the pure
// rules the loader, the editors and the unit tests share. No browser globals
// and no React, so node runs it.

// The fixed lists of ui.json look, in the order the editor shows them. The
// schema lists the same values.
export const LOOK_OPTIONS = Object.freeze({
  font: ['default', 'system', 'serif', 'mono'],
  radius: ['sharp', 'soft', 'round'],
  bubble: ['card', 'filled', 'tinted', 'outline', 'plain'],
  messageWidth: ['narrow', 'medium', 'wide', 'full'],
  codeTheme: ['auto', 'mono', 'vivid', 'ink'],
})

// LOOK_DEFAULTS is what the app shows when a key is absent: today's look.
export const LOOK_DEFAULTS = Object.freeze({
  font: 'default', radius: 'soft', bubble: 'card', messageWidth: 'medium', codeTheme: 'auto',
})

// lookOf keeps the values of ui.json look that are in their list. A value
// outside it is dropped, so the app shows the default for that key only.
export function lookOf(data) {
  const v = data?.look
  const out = {}
  if (!v || typeof v !== 'object' || Array.isArray(v)) return out
  for (const [k, list] of Object.entries(LOOK_OPTIONS)) {
    if (list.includes(v[k])) out[k] = v[k]
  }
  return out
}

// lookAttributes is what the root element carries for a look: one data
// attribute per key, only for a value that is not the default. The CSS of
// each value sets design tokens under that attribute; there is no inline CSS.
export function lookAttributes(look) {
  const out = {}
  for (const [k, list] of Object.entries(LOOK_OPTIONS)) {
    const v = look?.[k]
    out[`data-look-${k.toLowerCase()}`] = list.includes(v) && v !== LOOK_DEFAULTS[k] ? v : null
  }
  return out
}

// ---- Contrast ----

const HEX = /^#[0-9a-fA-F]{6}$/

// MIN_CONTRAST is the ratio an accent must keep against the page: WCAG AA for
// normal text, since the accent colours links and the text of buttons.
export const MIN_CONTRAST = 4.5

function luminance(hex) {
  const n = parseInt(hex.slice(1), 16)
  const ch = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((c) => {
    const s = c / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2]
}

// contrastRatio is the WCAG ratio of two #rrggbb colours, or 0 when one is not
// a colour.
export function contrastRatio(a, b) {
  if (!HEX.test(a || '') || !HEX.test(b || '')) return 0
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

// themePalette reads the colours of one mode (light or dark) from the theme
// file (src/theme/theme-localai.json), the source of the CSS tokens.
export function themePalette(theme, mode) {
  return theme?.[mode === 'light' ? 'light' : 'dark'] || {}
}

// accentCheck says whether accent keeps MIN_CONTRAST against the surfaces of
// a palette (the page and a card). It answers {ok, ratio}; ratio is the
// lowest of the two, rounded down to one decimal so the badge never claims
// more than the colour gives.
export function accentCheck(accent, palette) {
  if (!HEX.test(accent || '')) return { ok: false, ratio: 0 }
  const surfaces = [palette?.canvas, palette?.card].filter((c) => HEX.test(c || ''))
  if (surfaces.length === 0) return { ok: true, ratio: 0 }
  const ratio = Math.min(...surfaces.map((s) => contrastRatio(accent, s)))
  return { ok: ratio >= MIN_CONTRAST, ratio: Math.floor(ratio * 10) / 10 }
}

// onAccentRole picks the token for text on a filled accent (a button, a
// filled bubble): the text colour or the page colour of the theme, whichever
// reads better on it. The answer is a token name, never a colour.
export function onAccentRole(accent, palette) {
  const text = contrastRatio(accent, palette?.text)
  const canvas = contrastRatio(accent, palette?.canvas)
  return text >= canvas ? '--dk-text' : '--dk-canvas'
}

// ---- Placeholders ----

const PLACEHOLDER = /\{[^{}]*\}/g

// templateProblem checks the placeholders of a text against the ones it may
// use. It answers null, or {unknown: [...], brace: bool}: the server refuses
// the same text with the same rule (templateErrors in Go).
export function templateProblem(text, allowed = []) {
  if (typeof text !== 'string' || text === '') return null
  const unknown = (text.match(PLACEHOLDER) || []).filter((p) => !allowed.includes(p))
  const brace = /[{}]/.test(text.replace(PLACEHOLDER, ''))
  return unknown.length || brace ? { unknown: [...new Set(unknown)], brace } : null
}

// fillTemplate puts the values in a text's placeholders and tidies what an
// empty value leaves behind ("Good morning, " without a name).
export function fillTemplate(text, vars = {}) {
  if (typeof text !== 'string') return ''
  const out = text.replace(PLACEHOLDER, (p) => {
    const v = vars[p.slice(1, -1)]
    return v === undefined || v === null ? '' : String(v)
  })
  return out
    .replace(/[ \t]+/g, ' ')
    .trim()
    .replace(/\s+([,.!?;:])/g, '$1')
    .replace(/([,;:])(?=[.!?]|$)/g, '')
    .replace(/^[\s,;:]+/, '')
    .trim()
}

// The placeholders of the identity texts (greeting, empty chat, headline).
export const IDENTITY_PLACEHOLDERS = ['{user.name}', '{time_of_day}', '{date}']

// ---- Text overrides (ui.json strings) ----

// STRING_KEYS is the allow-list of the text a user may change, with the
// placeholders each key takes. The server keeps the same list
// (overridableStrings in Go) and refuses every other key, so security,
// permission and consent prompts cannot be reworded by a file.
export const STRING_KEYS = Object.freeze({
  'home.headline': IDENTITY_PLACEHOLDERS,
  'composer.placeholder': ['{model}'],
  'chat.empty.title': [],
  'nav.newChat': [],
  'nav.home': [],
  'nav.recentChats': [],
  'nav.allChats': [],
})

// uiStrings keeps the overrides of ui.json strings that are on the list, are
// not empty and have no broken placeholder. The server refuses the rest, so
// this only guards a value that reached the page around it.
export function uiStrings(data) {
  const v = data?.strings
  const out = {}
  if (!v || typeof v !== 'object' || Array.isArray(v)) return out
  for (const [k, allowed] of Object.entries(STRING_KEYS)) {
    const s = v[k]
    if (typeof s === 'string' && s.trim() && !templateProblem(s, allowed)) out[k] = s
  }
  return out
}

// ---- Identity ----

// identityOf reads the identity the shell shows from the documents: persona,
// user, home and ui (their data). vars fills the placeholders
// ({user.name}, {time_of_day}, {date}); the caller passes the words of its
// language. Each text is the first one set: the file of the identity, the
// override of ui.json strings, then nothing (the caller's default).
export function identityOf({ persona, home, ui } = {}, vars = {}) {
  const str = (v) => (typeof v === 'string' && v.trim() ? v : '')
  const strings = uiStrings(ui)
  const ok = (v, allowed = IDENTITY_PLACEHOLDERS) => (str(v) && !templateProblem(v, allowed) ? v : '')
  const ec = persona?.emptyChat && typeof persona.emptyChat === 'object' ? persona.emptyChat : {}
  const fill = (v) => (v ? fillTemplate(v, vars) : '')
  return {
    name: str(persona?.name).trim(),
    avatar: avatarPath(persona?.avatar),
    greeting: fill(ok(persona?.greeting)),
    headline: fill(ok(home?.headline) || strings['home.headline'] || ''),
    emptyTitle: fill(ok(ec.title) || strings['chat.empty.title'] || ''),
    emptySubtitle: fill(ok(ec.subtitle)),
    showShortcuts: ec.showShortcuts !== false,
  }
}

const IMAGE_EXT = /\.(png|jpe?g|webp|gif|avif)$/i

// avatarPath keeps the avatar of persona.json when it is the path of a
// picture in the home: the rule of the backgrounds (BackgroundImageOK in Go).
export function avatarPath(p) {
  if (typeof p !== 'string' || p === '' || p.length > 400) return ''
  if (p.startsWith('/') || /[\\:?#\u0000-\u001f\u007f]/.test(p)) return ''
  if (p.split('/').some((seg) => seg === '' || seg === '.' || seg === '..')) return ''
  return IMAGE_EXT.test(p) ? p : ''
}
