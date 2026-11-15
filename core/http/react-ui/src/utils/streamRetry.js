// Helpers for the two server-sent event streams of the chats page.

export const RETRY_CAP_MS = 30000

// The wait before reconnect number `attempt` (0 for the first): it doubles each
// time, up to the cap, so a stream that cannot connect does not hit the server
// every second.
export function retryDelay(attempt, base = 1000, cap = RETRY_CAP_MS) {
  const n = Math.max(0, Math.floor(attempt || 0))
  return Math.min(cap, base * 2 ** Math.min(n, 20))
}

// The JSON an event of the server carries, or null. A browser reports a broken
// connection with an event named "error" too, and that one has no data: it is
// not a message of the server and must not be read as one.
export function eventPayload(ev) {
  if (!ev || typeof ev.data !== 'string' || ev.data === '') return null
  try {
    const v = JSON.parse(ev.data)
    return v && typeof v === 'object' ? v : null
  } catch {
    return null
  }
}

// The address to open again after a connection broke. A new EventSource does
// not send the Last-Event-ID header (the browser sends it only when it
// reconnects the same object), so the last event id of this chat goes in the
// query: the server then gives back what was missed. `last` is {chat, id} as
// the page kept it; an id of another chat is not used.
export function resumeUrl(url, last, chat) {
  if (!last || last.chat !== chat || !last.id) return url
  return `${url}${url.includes('?') ? '&' : '?'}after=${encodeURIComponent(last.id)}`
}
