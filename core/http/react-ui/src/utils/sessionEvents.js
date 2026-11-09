// A one-way line from the request helper to the session provider. The helper
// reports a 401; the provider decides whether it ends the session. It lives
// in its own module so utils/api.js needs no import of React code.
let handler = null

export function setUnauthorizedHandler(fn) {
  handler = fn
  return () => { if (handler === fn) handler = null }
}

export function reportUnauthorized(url) {
  if (handler) handler(url)
}
