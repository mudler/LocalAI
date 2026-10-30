// The change notes of a chat: what a turn changed in the well-known files and
// the pages of the user's home, read from the transcript the server keeps. A
// file tool call carries a card {kind files_change, id "<rev>:<path>"}; the
// checkpoint line of a turn carries the change set of the sandbox (rev) and
// its paths. Pure helpers, so the node unit tests load them.

export const CHANGE_CARD = 'files_change'

const WELL_KNOWN = {
  '/persona.json': 'persona',
  '/user.json': 'profile',
  '/shortcuts.json': 'shortcuts',
  '/home.json': 'home',
  '/ui.json': 'ui',
}

const PAGE_RE = /^\/pages\/([^/]+)\//

// wellKnownTag is the tag the Files pane gives a path: profile, persona,
// shortcuts, home, ui, page, or '' for any other file.
export function wellKnownTag(path) {
  const p = String(path || '')
  if (WELL_KNOWN[p]) return WELL_KNOWN[p]
  return PAGE_RE.test(p) ? 'page' : ''
}

const noted = (p) => !!wellKnownTag(p)

// turnChanges maps a turn to its changes [{rev, paths}] in the order they
// happened, keeping only the turns that touched a well-known file or a page.
export function turnChanges(messages) {
  const out = new Map()
  const seen = new Set()
  const add = (turn, rev, paths) => {
    const kept = paths.filter(noted)
    if (!rev || kept.length === 0 || seen.has(rev)) return
    seen.add(rev)
    if (!out.has(turn)) out.set(turn, [])
    out.get(turn).push({ rev, paths: kept })
  }
  for (const m of messages || []) {
    if (m.role === 'tool' && m.card?.kind === CHANGE_CARD) {
      const at = String(m.card.id || '').indexOf(':')
      if (at > 0) add(m.turn, Number(m.card.id.slice(0, at)), [m.card.id.slice(at + 1)])
    } else if (m.role === 'checkpoint' && m.rev) {
      add(m.turn, Number(m.rev), String(m.text || '').split('\n').filter(Boolean))
    }
  }
  return out
}

// describeChanges says what one turn's changes mean for the user: the notes to
// show (key of the sentence, the action next to Undo, the toast it may raise)
// and the change sets that Undo undoes, newest first. pagesSeen holds the
// pages already noted in this chat, so a page is "created" once.
export function describeChanges(changes, pagesSeen = new Set()) {
  const paths = new Set()
  for (const c of changes) c.paths.forEach(p => paths.add(p))
  const notes = []
  const persona = paths.has('/persona.json')
  const user = paths.has('/user.json')
  if (persona && user) notes.push({ key: 'profilePersona', action: 'account' })
  else if (user) notes.push({ key: 'profile', action: 'account' })
  else if (persona) notes.push({ key: 'persona', action: 'account' })
  if (paths.has('/shortcuts.json')) notes.push({ key: 'shortcuts', action: '', toast: 'shortcut' })
  if (paths.has('/home.json')) notes.push({ key: 'home', action: 'home', toast: 'home' })
  if (paths.has('/ui.json')) notes.push({ key: 'ui', action: 'appearance', toast: 'ui' })
  const pages = new Map()
  for (const p of paths) {
    const m = PAGE_RE.exec(p)
    if (!m) continue
    if (!pages.has(m[1])) pages.set(m[1], [])
    pages.get(m[1]).push(p)
  }
  for (const [page, files] of pages) {
    if (!pagesSeen.has(page)) {
      pagesSeen.add(page)
      notes.push({ key: 'pageCreated', page, action: 'page', toast: 'page' })
    } else {
      notes.push({ key: 'pageChanged', page, file: files[0].replace(/^\//, ''), action: 'page' })
    }
  }
  const revs = changes.map(c => c.rev).sort((a, b) => b - a)
  return { notes, revs }
}

const same = (a, b) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null)

// changedKeys lists the top-level keys whose value differs between two
// versions of a file.
export function changedKeys(before, after) {
  const keys = new Set([...Object.keys(before || {}), ...Object.keys(after || {})])
  return [...keys].filter(k => !same(before?.[k], after?.[k]))
}

// newLabels lists the shortcuts that after has and before has not.
export function newLabels(before, after) {
  const had = new Set((before?.shortcuts || []).map(s => s?.label))
  return (after?.shortcuts || []).map(s => s?.label).filter(l => typeof l === 'string' && !had.has(l))
}

// liveTurns is the set of turns that ended after the chat was opened in this
// tab (base is the last turn the first read showed, null while unknown). Only
// their changes raise toasts: a reload or another tab's change does not.
export function liveTurns(base, messages) {
  const out = new Set()
  if (base === null || base === undefined) return out
  for (const m of messages || []) if (m.turn > base) out.add(m.turn)
  return out
}
