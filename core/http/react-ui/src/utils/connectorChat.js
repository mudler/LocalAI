// What the transcript of a chat that a messaging connection started shows
// beyond the words: who wrote each message, the earlier messages of the thread
// that the model read, and what the host did on its own. The server stores
// these with the chat (sender on a user line, a thread line before the first
// message, host-note lines); nothing here asks the messaging platform. Pure
// helpers: the components translate the keys they return. Nothing here knows a
// platform.

// How the host saw a sender, from the most to the least trusted. A value this
// file never heard of reads as "anyone", the least trusted, so a newer server
// never makes the page claim more than it knows.
export const STANDINGS = ['owner', 'verified', 'listed', 'channel', 'anyone']
export const standingOf = (s) => (STANDINGS.includes(s?.standing) ? s.standing : 'anyone')

// The name to read for a sender: the resolved name, else the handle, else a
// short label with the platform id in small print. The id is never the only
// thing shown. { name, small, resolved }: small is the id to draw in small type.
export function senderName(sender, t) {
  const name = (sender?.name || '').trim()
  const handle = (sender?.username || '').trim()
  const id = (sender?.id || '').trim()
  if (name) return { name, small: handle ? `@${handle}` : '', resolved: true }
  if (handle) return { name: `@${handle}`, small: '', resolved: true }
  return { name: t('connector.unknownSender'), small: id, resolved: false }
}

// The key that tells two senders apart in the people list.
const senderKey = (s) => s?.id || s?.username || s?.name || ''

// The people who wrote in the chat, in order of first message, each with the
// standing they had at their last message: [{ key, sender, standing, count }].
export function peopleOf(messages) {
  const seen = new Map()
  for (const m of messages || []) {
    if (m.role !== 'user' || !m.sender) continue
    const key = senderKey(m.sender)
    if (!key) continue
    const cur = seen.get(key)
    if (cur) {
      cur.count++
      cur.standing = standingOf(m.sender)
      cur.sender = m.sender
    } else {
      seen.set(key, { key, sender: m.sender, standing: standingOf(m.sender), count: 1 })
    }
  }
  return [...seen.values()]
}

// "Author: text" as the model read it, split at the first colon and space.
// A line without an author keeps all its text.
export function threadLine(line) {
  const i = String(line || '').indexOf(': ')
  if (i <= 0) return { author: '', text: String(line || '') }
  return { author: line.slice(0, i), text: line.slice(i + 2) }
}

// The codes of a host note that the page has words for; any other reads as a
// plain note with the sentence the server kept.
export const NOTE_CODES = ['paired', 'unsupported', 'too_long', 'no_upload', 'rate', 'held_back', 'new_chat', 'refused']
export const noteCode = (code) => (NOTE_CODES.includes(code) ? code : 'other')

// The tone of a host note: ok for a pairing that worked, info for a note that
// only informs (tools held back), warn for a message the bot did not take.
export function noteTone(code) {
  if (code === 'paired') return 'ok'
  if (code === 'held_back' || code === 'new_chat') return 'info'
  return 'warn'
}

// Rows of host notes that follow each other with the same code and sentence
// show once, with how many times it happened: another frontend may note the
// same held-back line again, and a sender that keeps sending a file the bot
// refuses is one note, not twenty.
export function foldNotes(rows) {
  const out = []
  for (const row of rows) {
    const prev = out[out.length - 1]
    if (row.kind === 'hostnote' && prev?.kind === 'hostnote'
      && prev.msg.host_note?.code === row.msg.host_note?.code && prev.msg.text === row.msg.text) {
      prev.count++
      continue
    }
    out.push(row.kind === 'hostnote' ? { ...row, count: 1 } : row)
  }
  return out
}

// Whether the chat is one a messaging connection started.
export const isConnectorChat = (chat) => chat?.origin === 'connector'

// The thread line of the transcript as the block draws it: the number of
// messages, whether older ones were left out and the split lines.
export function threadView(msg) {
  const lines = (msg?.thread?.lines || []).map(threadLine)
  return { count: lines.length, cut: Boolean(msg?.thread?.cut), lines }
}

// The place line of the header: direct, or the channel by name, and whether a
// thread. { direct, name, thread, link }; link is kept only when it is an https
// address, whatever the server sent.
export function placeView(place) {
  const link = typeof place?.link === 'string' && /^https:\/\/[^\s]+$/.test(place.link) ? place.link : ''
  return { direct: Boolean(place?.direct), name: (place?.name || '').trim(), thread: Boolean(place?.thread), link }
}
