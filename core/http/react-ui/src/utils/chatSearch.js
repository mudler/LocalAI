// Helpers of the chat search (GET /api/chats/search). No React, so a node test
// drives them.

// The words of a hit's snippet, split at the marked ranges. The server gives
// ranges as offsets in UTF-16 code units, which is how a JavaScript string
// counts, and the page draws the parts as text nodes: nothing from a message is
// ever parsed as HTML. Ranges that are out of order, overlap or lie outside the
// text are cut or dropped, so a wrong range never throws or repeats text.
export function highlightParts(text, ranges) {
  const s = String(text || '')
  const parts = []
  let at = 0
  const sorted = [...(Array.isArray(ranges) ? ranges : [])]
    .filter((r) => r && Number.isInteger(r.start) && Number.isInteger(r.end))
    .sort((a, b) => a.start - b.start)
  for (const r of sorted) {
    const start = Math.max(r.start, at)
    const end = Math.min(r.end, s.length)
    if (end <= start) continue
    if (start > at) parts.push({ text: s.slice(at, start), mark: false })
    parts.push({ text: s.slice(start, end), mark: true })
    at = end
  }
  if (at < s.length) parts.push({ text: s.slice(at), mark: false })
  return parts
}

// The date presets of the filter row, as the number of days back.
export const RANGE_DAYS = { day: 1, week: 7, month: 30, year: 365 }

const iso = (d) => d.toISOString()

// The query parameters of the filters: { agent, origin, range, from, to }.
// A preset is a span back from now; a custom range is two dates (YYYY-MM-DD,
// the end day included). Empty values are left out.
export function searchParams(filters, now = new Date()) {
  const f = filters || {}
  const out = {}
  if (f.agent) out.agent = f.agent
  if (f.origin) out.origin = f.origin
  if (RANGE_DAYS[f.range]) out.since = iso(new Date(now.getTime() - RANGE_DAYS[f.range] * 86400000))
  if (f.range === 'custom') {
    if (/^\d{4}-\d{2}-\d{2}$/.test(f.from || '')) out.since = f.from
    if (/^\d{4}-\d{2}-\d{2}$/.test(f.to || '')) out.until = f.to
  }
  return out
}

export const noFilters = { agent: '', origin: '', range: '', from: '', to: '' }
export const filtersActive = (f) => Boolean(f && (f.agent || f.origin || f.range))

// A stable string for the filters, to tell when a search is a new one.
export const filtersKey = (f) => JSON.stringify([f?.agent || '', f?.origin || '', f?.range || '', f?.from || '', f?.to || ''])

// The address of a chat, at one of its messages when seq is given.
export function chatAddress(id, opts) {
  const base = `/app/chats/${encodeURIComponent(id)}`
  const seq = Number(opts?.seq)
  return Number.isInteger(seq) && seq > 0 ? `${base}?seq=${seq}` : base
}

// The seq of a chat address (?seq=14), or 0.
export function seqOfSearch(search) {
  const seq = Number(new URLSearchParams(search || '').get('seq'))
  return Number.isInteger(seq) && seq > 0 ? seq : 0
}

// The rows of a search answer: one per hit, the chat as the list draws it.
export function hitRows(res, toChat) {
  return (res?.hits || []).filter((h) => h?.chat?.id).map((h) => ({
    key: `${h.chat.id}:${h.seq || 0}`,
    chat: toChat(h.chat),
    seq: h.seq || 0,
    role: h.role || '',
    at: h.at ? Date.parse(h.at) || 0 : 0,
    snippet: h.snippet || '',
    highlights: h.highlights || [],
    inTitle: Boolean(h.in_title),
    more: Math.max(0, (h.matches || 0) - 1),
  }))
}
