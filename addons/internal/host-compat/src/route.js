// Turns the addresses the Studio and Talk pages were written with
// (/app/studio/images/<model>?work=..) into the addresses an add-on may use.
// Pure: no React, no SDK, so a node test drives it directly.
//
// An add-on page has one flat path under /app/addons/<id>/<page>, and its
// segments cannot hold "/" or ":" , so the model id travels as ?model=<id> and
// the one path segment that remains is the tab.

// The pages that used to be core routes and are add-on pages now.
export const MOVED = Object.freeze([
  { prefix: '/app/studio', addon: 'studio', page: 'studio' },
  { prefix: '/app/talk', addon: 'talk', page: 'talk' },
])

export function parseAddress(to) {
  let rest = String(to ?? '')
  let hash = ''
  const h = rest.indexOf('#')
  if (h >= 0) { hash = rest.slice(h); rest = rest.slice(0, h) }
  let search = ''
  const q = rest.indexOf('?')
  if (q >= 0) { search = rest.slice(q + 1); rest = rest.slice(0, q) }
  return { pathname: rest, search, hash }
}

function decode(segment) {
  try { return decodeURIComponent(segment) } catch { return segment }
}

// resolveTarget says where an address leads from the add-on `addonId`:
//   { own: true, subpath }       a page of this add-on ("studio/images?model=x")
//   { own: false, address }      anything else under /app, as an address
//   null                         not an address of the app
export function resolveTarget(to, addonId) {
  const { pathname, search, hash } = parseAddress(to)
  const trimmed = pathname.replace(/\/+$/, '')
  const moved = MOVED.find((m) => trimmed === m.prefix || trimmed.startsWith(`${m.prefix}/`))
  if (moved) {
    const segments = trimmed.slice(moved.prefix.length).split('/').filter(Boolean).map(decode)
    const params = new URLSearchParams(search)
    if (segments[1]) params.set('model', segments[1])
    const query = params.toString()
    const tab = segments[0] ? `/${encodeURIComponent(segments[0])}` : ''
    if (moved.addon === addonId) return { own: true, subpath: `${moved.page}${tab}${query ? `?${query}` : ''}` }
    return { own: false, address: `/app/addons/${moved.addon}/${moved.page}${tab}${query ? `?${query}` : ''}${hash}` }
  }
  if (trimmed === '/app' || trimmed.startsWith('/app/')) return { own: false, address: `${trimmed}${search ? `?${search}` : ''}${hash}` }
  return null
}

// What a page reads from its address: the tab is the first path segment and
// the model comes from ?model= (or, for an address typed by hand, the second
// path segment).
export function paramsOf({ path = '', search = '' }) {
  const segments = String(path).split('/').filter(Boolean).map(decode)
  const model = new URLSearchParams(search).get('model') || segments[1] || undefined
  return { tab: segments[0] || undefined, model }
}
