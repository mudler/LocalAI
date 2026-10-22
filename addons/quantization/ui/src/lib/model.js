// The model a page works on. A moved link carries it as ?model=<id>, because
// a model id may hold "/" or ":" that an add-on path segment refuses; a link
// typed by hand may still carry it as the first path segment.
export function modelFrom(search, path) {
  const fromQuery = new URLSearchParams(search || '').get('model')
  if (fromQuery) return fromQuery
  return String(path || '').split('/')[0]
}
