// The hand-off of a first message from Home to its new chat.
//
// Home creates the chat and sends the message, then opens the chat. The chat
// page must not start empty and fill in a moment later: it draws the message
// from what Home already knows (the chat record, the text, the files) and
// takes the transcript over when the server's copy arrives, without drawing
// the message again. Home leaves the hand-off here; the chat page reads it on
// its first render (useComposerTransition).
//
// It is page-local and short-lived: one hand-off at a time, for one chat id,
// dropped when the chat page has taken it or after a few seconds.

const TTL_MS = 10_000

// The key of the row of the handed-off message (chatThread.threadRows): the
// same before and after the server's copy replaces it, so the row is never
// mounted twice.
export const HANDOFF_ROW = 'm-handoff'

let current = null

// Leaves a hand-off: { chat, text, files, started, from }
//   chat     the chat record the create request answered (id, model, title…)
//   text     the message as it was sent
//   files    the files sent with it ({ name, size, type, url? }; url is a
//            local address of an image, for its thumbnail)
//   started  whether the send started a turn (the model is working)
//   from     where the composer and the text were on Home ({ composer,
//            text } rectangles), for the fallback animation
export function offerHandoff(handoff, now = Date.now()) {
  current = handoff?.chat?.id ? { ...handoff, at: now } : null
  return current
}

// The hand-off of chat id, or null. Reading does not take it: React may
// render the first time twice.
export function peekHandoff(id, now = Date.now()) {
  if (!current || !id || current.chat.id !== id) return null
  if (now - current.at > TTL_MS) {
    current = null
    return null
  }
  return current
}

// Drops the hand-off of chat id (all of them without an id).
export function clearHandoff(id) {
  if (!id || current?.chat.id === id) current = null
}

// What the chat page starts from: the record of the chat, with the turn the
// send started, so the page shows the model working at once.
export function handoffSeed(handoff) {
  if (!handoff) return null
  return { id: handoff.chat.id, detail: { ...handoff.chat, running: Boolean(handoff.started), pending: [] } }
}

// The message as the server will keep it ({ seq, turn, role, text,
// attachments }), drawn until the transcript holds it.
export function handoffMessage(handoff) {
  return {
    seq: 0,
    turn: 1,
    role: 'user',
    text: handoff.text,
    attachments: (handoff.files || []).map(f => ({ path: '', name: f.name, size: f.size || 0, mime: f.type || '', url: f.url })),
    ...(handoff.invoked ? { invoked: handoff.invoked } : {}),
    rowKey: HANDOFF_ROW,
    handoff: true,
  }
}

// The transcript of the chat with the handed-off message in it. While the
// server's copy has no message of the person, the hand-off's message is the
// first line. Once it has one, that line takes the row of the hand-off (and
// the local image addresses, so a thumbnail is not loaded again).
export function withHandoff(messages, handoff, chatId) {
  const list = messages || []
  if (!handoff || handoff.chat.id !== chatId) return list
  const i = list.findIndex(m => m.role === 'user')
  if (i < 0) return [handoffMessage(handoff), ...list]
  const local = new Map((handoff.files || []).filter(f => f.url).map(f => [f.name, f.url]))
  const m = list[i]
  const attachments = local.size > 0 && Array.isArray(m.attachments)
    ? m.attachments.map(a => (local.has(a.name) ? { ...a, url: local.get(a.name) } : a))
    : m.attachments
  const out = list.slice()
  out[i] = { ...m, attachments, rowKey: HANDOFF_ROW, handoff: true }
  return out
}

// The chat page says when it has drawn the handed-off chat; Home waits for it
// inside the view transition, so the browser captures the finished page and
// not the frame before the chat's code arrived. A page that never says so
// (an error) releases the wait after timeoutMs.
const waiting = new Map()

export function whenArrived(id, timeoutMs = 1500) {
  return new Promise((resolve) => {
    const done = (ok) => {
      clearTimeout(timer)
      if (waiting.get(id) === done) waiting.delete(id)
      resolve(ok)
    }
    const timer = setTimeout(() => done(false), timeoutMs)
    waiting.get(id)?.(false)
    waiting.set(id, done)
  })
}

export function markArrived(id) {
  waiting.get(id)?.(true)
}
