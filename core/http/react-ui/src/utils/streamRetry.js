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
