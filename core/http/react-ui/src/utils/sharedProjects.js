// Pure rules for the projects that other people share with you. No React and
// no network, so a node test drives them directly.
//
// A shared project is named only by its ref, "s:<number>", which the server
// hands out. It is opaque: the owner's account, the paths and the project id
// never reach the page. An own project id never starts with the prefix, so a
// chat's pulled list (own ids and refs together) is told apart by it.

export const SHARED_PREFIX = 's:'
const REF = /^s:[0-9]{1,18}$/

export const isSharedRef = (id) => typeof id === 'string' && id.startsWith(SHARED_PREFIX)

// The page of a shared project. The colon is encoded, as for the API.
export const sharedPath = (ref) => `/app/projects/shared/${encodeURIComponent(ref)}`

// The ref a route param names, or '' when it is not a ref. The router decodes
// the param already; a still-encoded value is accepted too.
export function decodeSharedRef(param) {
  if (typeof param !== 'string') return ''
  let value = param
  try { value = decodeURIComponent(param) } catch { return '' }
  return REF.test(value) ? value : ''
}

// What the person may do in the folder: 'write' or 'read'. Memory is read-only
// for every grantee; this is about the files only.
export const sharedAccess = (mode) => (mode === 'write' ? 'write' : 'read')

// Only an active project can be opened for use and pulled into a chat. An
// archived or broken one is listed, as unavailable.
export const sharedUsable = (p) => p?.status === 'active'
export const sharedUnavailable = (p) => !sharedUsable(p)

const matches = (p, query) => {
  const q = (query || '').trim().toLowerCase()
  return !q || (p.name || '').toLowerCase().includes(q)
}

// The usable shared projects a search leaves, for the composer list.
export const composerShared = (shared, query = '') => (shared || []).filter(p => sharedUsable(p) && matches(p, query))

// The usable shared projects a chat can still pull in.
export function sharedChoices(shared, chat) {
  const taken = new Set(chat?.pulled_projects || [])
  return composerShared(shared).filter(p => !taken.has(p.ref))
}

// The pulled list of a chat split into own ids and shared refs.
export function splitPulled(ids) {
  const list = ids || []
  return { own: list.filter(id => !isSharedRef(id)), shared: list.filter(isSharedRef) }
}

const stateOf = (p) => (p?.status === 'active' ? 'mounted' : p?.status === 'archived' ? 'archived' : 'unavailable')

// The chips of the shared refs in a pulled list, in order. A ref the shared
// list does not name is 'unlisted': the owner may have stopped sharing it, but
// the list is also built from reads that can fail for a moment, and it carries
// no sign of that. So the page claims nothing: the entry is unavailable, and
// the person can check again or remove it. While the shared list is not known
// (null) no entry is made at all, so a failed read shows nothing.
export function pulledSharedEntries(refs, shared) {
  if (!Array.isArray(shared)) return []
  return (refs || []).map((ref) => {
    const p = shared.find(x => x.ref === ref)
    if (!p) return { id: ref, name: '', state: 'unlisted', shared: true, owner: '', mode: 'read' }
    return { id: ref, name: p.name, state: stateOf(p), shared: true, owner: p.owner_name || '', mode: sharedAccess(p.mode) }
  })
}

// A short plain-text look at a Markdown index: headings, emphasis, images and
// link targets left out, the first few lines joined.
export function memorySnippet(index, lines = 4) {
  return String(index || '')
    .split('\n')
    .filter(l => l.trim() && !l.trim().startsWith('#'))
    .map(l => l.replace(/<(style|script)[^>]*>.*?<\/\1>/gi, '').replace(/<[^>]*>/g, '').replace(/!\[[^\]]*\]\([^)]*\)/g, '').replace(/\[([^\]]*)\]\([^)]*\)/g, '$1').replace(/[*_`>]/g, '').replace(/^\s*-\s+/, '').trim())
    .filter(Boolean)
    .slice(0, lines)
    .join(' ')
}
