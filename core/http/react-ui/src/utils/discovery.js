// How LocalAI shows a user what they can change: the "Make LocalAI yours"
// cards on Home, the one-time "New" label of a sidebar entry and the switches
// of the "Help and suggestions" card. What was shown and answered is kept under
// `seen` in user.json, which the server's hint service writes too. Pure
// helpers, no browser globals, so the node unit tests load them.

import { widgetList } from './wellKnown.js'

// The docs page that the examples, the Files pane and Account link to.
export const DOCS_MAKE_YOURS = 'https://localai.io/features/make-localai-yours/'

const DAY = 24 * 60 * 60 * 1000
// How long a "New" label stays when the entry is never opened.
export const NEW_LABEL_DAYS = 7

const isObj = (v) => !!v && typeof v === 'object' && !Array.isArray(v)

// seenOf returns the seen record of a user.json document, or {}.
export function seenOf(doc) {
  const s = doc?.data?.seen
  return isObj(s) ? s : {}
}

// withSeen merges patch into seen: objects merge key by key, null removes a key.
export function withSeen(seen, patch) {
  const out = { ...(isObj(seen) ? seen : {}) }
  for (const [k, v] of Object.entries(patch || {})) {
    if (v === null) delete out[k]
    else if (isObj(v) && isObj(out[k])) out[k] = withSeen(out[k], v)
    else out[k] = v
  }
  return out
}

export function firstName(name) {
  return String(name || '').trim().split(/\s+/)[0] || ''
}

const has = (v) => typeof v === 'string' ? v.trim() !== '' : v !== undefined && v !== null

// fileData drops what the server filled in itself (the account's name) and the
// record of hints: neither is something the user saved.
function saved(doc) {
  if (!doc?.exists) return {}
  const d = { ...(doc.data || {}) }
  for (const k of Object.keys(doc.prefilled || {})) delete d[k]
  delete d.seen
  return d
}

// exampleResults says which example card already has its result: the profile
// card when persona.json or a name or bio of user.json is saved, the page card
// when a page exists, the schedule card when Home has a widget that reads a
// data file.
export function exampleResults({ persona, user, home, pages }) {
  const p = saved(persona)
  const u = saved(user)
  return {
    profile: has(p.name) || has(p.system_prompt) || has(u.name) || has(u.bio),
    page: Array.isArray(pages) && pages.length > 0,
    schedule: widgetList(home).some(w => w.type !== 'chats' && !!w.source),
  }
}

// experienced is a user who already changed LocalAI on their own: two or more
// of the files, one of which no example card makes (shortcuts.json, ui.json).
// The examples never show to them. A first-time user who did some cards keeps
// seeing the others.
export function experienced({ persona, user, shortcuts, home, ui }) {
  const own = [shortcuts, ui].filter(d => d?.exists).length
  const all = own + [persona, home].filter(d => d?.exists).length + (Object.keys(saved(user)).length > 0 ? 1 : 0)
  return own > 0 && all >= 2
}

export const examplesHidden = (seen) => !!seen?.home_examples?.hidden

export const hideExamples = (seen, date) => withSeen(seen, { home_examples: { hidden: date } })

export function showExamples(seen) {
  const out = { ...seen }
  delete out.home_examples
  return out
}

const answered = (v) => isObj(v) && (v.state === 'declined' || v.state === 'later')

// The offers the user turned down or put off: the offers map, and the older
// flat record ({"profile": {"state": "declined"}}).
function offerEntries(seen) {
  const flat = Object.entries(seen || {}).filter(([, v]) => isObj(v) && typeof v.state === 'string')
  return [...Object.values(isObj(seen?.offers) ? seen.offers : {}), ...flat.map(([, v]) => v)]
}

export const dismissedCount = (seen) => offerEntries(seen).filter(answered).length

// forgetHints drops what the assistant learned about offers: the answers, the
// day and chat caps. The switches and the examples stay as they are.
export function forgetHints(seen) {
  const out = {}
  for (const [k, v] of Object.entries(seen || {})) {
    if (k === 'offers' || k === 'last_offer' || k === 'offer_chats') continue
    if (isObj(v) && typeof v.state === 'string') continue
    out[k] = v
  }
  return out
}

// suggestionsOn: the user's own switch, else the server's default.
export function suggestionsOn(seen, serverDefault) {
  return typeof seen?.offers_off === 'boolean' ? !seen.offers_off : !!serverDefault
}

// newLabel says what the "New" label of an area does now: 'first' (show it and
// record today), 'show', or 'hide' (opened, older than a week, or switched off).
export function newLabel(seen, area, now = Date.now()) {
  if (seen?.new_off) return 'hide'
  const v = seen?.new?.[area]
  if (!v) return 'first'
  if (v === 'opened') return 'hide'
  const at = Date.parse(v)
  if (Number.isNaN(at)) return 'hide'
  return now - at < NEW_LABEL_DAYS * DAY ? 'show' : 'hide'
}

export const markNewSeen = (seen, area, date) => withSeen(seen, { new: { [area]: date } })
export const markNewOpened = (seen, area) => withSeen(seen, { new: { [area]: 'opened' } })

// exampleCards are the three cards, in order. Card 1 says the user's own first
// name, so the sentence is true for them; card 3 needs a mail connector and
// falls back to the user's chats.
export function exampleCards({ t, name, mail }) {
  const first = firstName(name)
  return [
    {
      id: 'profile', icon: 'user', kicker: t('discovery.ex.card1.kicker'),
      prompt: first ? t('discovery.ex.card1.prompt', { firstName: first }) : t('discovery.ex.card1.promptNoName'),
      what: t('discovery.ex.card1.what'), whatIcon: 'file',
    },
    {
      id: 'page', icon: 'layers', kicker: t('discovery.ex.card2.kicker'),
      prompt: t('discovery.ex.card2.prompt'), what: t('discovery.ex.card2.what'), whatIcon: 'layers',
    },
    {
      id: 'schedule', icon: 'clock', kicker: t('discovery.ex.card3.kicker'),
      prompt: mail ? t('discovery.ex.card3.prompt') : t('discovery.ex.card3.promptNoMail'),
      what: t('discovery.ex.card3.what'), whatIcon: 'clock',
    },
  ]
}

// mailConnector says whether one of the user's MCP connectors reads mail.
export function mailConnector(connectors) {
  return (Array.isArray(connectors) ? connectors : []).some(c => c && c.enabled !== false
    && /mail|imap|inbox|outlook/i.test(`${c.name || ''} ${c.id || ''} ${c.url || ''}`))
}

export function today(now = new Date()) {
  return now.toISOString().slice(0, 10)
}
