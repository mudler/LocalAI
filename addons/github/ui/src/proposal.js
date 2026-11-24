// What the proposal card of a GitHub write shows, from the plain data the host
// hands it. No React, so a node test drives it. Nothing here comes from model
// prose except the text that will be posted, which is shown as text.

const REPO = /^[A-Za-z0-9_.-]{1,100}\/[A-Za-z0-9_.-]{1,100}$/
const str = (v) => (typeof v === 'string' ? v : '')

// The facts of one proposal: the tool, the repository, the number, the link,
// and the parts that will be posted. `args` are the stored arguments (the full
// text); `sample` is the short preview the add-on made.
export function proposalView(tool, args, sample) {
  const a = args && typeof args === 'object' ? args : {}
  const s = sample && typeof sample === 'object' ? sample : {}
  const repo = str(a.repo) || str(s.repo)
  const number = Number.isInteger(a.number) ? a.number : (Number.isInteger(s.number) ? s.number : 0)
  const body = str(a.body) || str(s.body)
  const labels = (Array.isArray(a.labels) ? a.labels : Array.isArray(s.labels) ? s.labels : []).filter((l) => typeof l === 'string')
  const bytes = Number.isInteger(s.bodyBytes) ? s.bodyBytes : body.length
  return {
    tool: str(tool),
    repo: REPO.test(repo) ? repo : '',
    number,
    url: REPO.test(repo) && number > 0 ? `https://github.com/${repo}/issues/${number}` : '',
    targetTitle: str(s.targetTitle),
    title: str(a.title) || str(s.title),
    body,
    // The stored text is whole; the preview is cut. Say so only when the card
    // could not get the whole text.
    cut: s.bodyCut === true && str(a.body) === '',
    bytes,
    labels,
    state: str(a.state) || str(s.state),
    reason: str(a.reason) || str(s.reason),
    path: str(a.path) || str(s.path),
    line: Number.isInteger(a.line) ? a.line : (Number.isInteger(s.line) ? s.line : 0),
    side: str(a.side) === 'LEFT' ? 'LEFT' : 'RIGHT',
  }
}

// The connections that list a repository, by name, in the order of the list.
export function connectionsFor(connectors, repo) {
  const want = String(repo || '').toLowerCase()
  if (!want) return []
  return (Array.isArray(connectors) ? connectors : [])
    .filter((c) => c && c.kind === 'github' && (c.resources || []).some((r) => String(r).toLowerCase() === want))
    .map((c) => String(c.name || ''))
    .filter(Boolean)
}
