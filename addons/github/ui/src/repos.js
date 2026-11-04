// Helpers of the GitHub page. No React, so a node test drives them.

// A fine-grained token. A classic token (ghp_) reaches every repository of
// the account, so the page does not take it.
export const TOKEN_PREFIX = 'github_pat_'

// What is wrong with the typed token, as a key of the page's words, or ''.
// Nothing typed is no problem: an update without a token keeps the stored one.
export function tokenProblem(form) {
  const token = (form.token || '').trim()
  if (!token) return ''
  if (/\s/.test(token)) return 'tokenSpace'
  if (!token.startsWith(TOKEN_PREFIX)) return 'tokenShape'
  return ''
}

// The secret the field makes: the token, or '' when not typed or not right.
export function githubSecret(form) {
  return tokenProblem(form) ? '' : (form.token || '').trim()
}

export const MAX_REPOS = 50
const REPO = /^[A-Za-z0-9_.-]{1,100}\/[A-Za-z0-9_.-]{1,100}$/

// The repositories of a text box: one per line, or separated by commas. A
// space is not a separator, so "not a repo" is one entry and is named when it
// is wrong. Blank entries and repeats (any case) are dropped.
export function parseRepos(text) {
  const seen = new Set()
  const out = []
  for (const part of String(text || '').split(/[\n\r,]+/)) {
    const r = part.trim()
    if (!r || seen.has(r.toLowerCase())) continue
    seen.add(r.toLowerCase())
    out.push(r)
  }
  return out
}

const validRepo = (r) => REPO.test(r) && !r.includes('..')

// The first thing wrong with the typed list: { key, entry } where key is a key
// of the page's words and entry is the line to name, or null.
export function reposIssue(text) {
  const list = parseRepos(text)
  if (list.length > MAX_REPOS) return { key: 'reposMany', entry: '' }
  const bad = list.find((r) => !validRepo(r))
  return bad === undefined ? null : { key: 'reposShape', entry: bad.length > 60 ? `${bad.slice(0, 57)}...` : bad }
}

// The same, as a key only, or ''.
export const reposProblem = (text) => reposIssue(text)?.key || ''

// The name a new connection gets from its first valid repository.
export function defaultName(text) {
  const first = parseRepos(text).find(validRepo)
  return first ? `GitHub: ${first}` : ''
}

// The name after the repositories changed: an empty name, or one that this
// page made itself from the earlier text, follows the new first repository. A
// name the person typed stays.
export function followName(name, oldText, newText) {
  const current = String(name || '')
  if (current !== '' && current !== defaultName(oldText)) return current
  return defaultName(newText)
}

// What the agent may change on GitHub with this connection. The host keeps it
// on the binding and enforces it: off runs no write tool, propose stores each
// change for you to confirm, auto lets a chat of yours make it at once.
export const WRITE_MODES = ['off', 'propose', 'auto']

// The mode of a saved binding, off for one that has none or an unknown one.
export const writeModeOf = (m) => (WRITE_MODES.includes(m) ? m : 'off')

// The fields this page adds to a binding: its form values, the request body
// they make, and whether they allow saving.
export const extraForm = (c) => ({ repos_text: (c.resources || []).join('\n'), write_mode: writeModeOf(c.write_mode) })
export const extraBody = (form) => ({ resources: parseRepos(form.repos_text), write_mode: writeModeOf(form.write_mode) })
export const extraValid = (form) => !reposProblem(form.repos_text)

// What the Test button shows for one repository: { tone, key, vars }, where
// key is a key of the page's words under check. A failure of the check itself
// is r.failed: 'refused' when GitHub turned the token down, else 'other'.
export function checkItem(r) {
  const repo = r?.repo || ''
  if (r?.failed === 'refused') return { tone: 'warn', key: 'check.refused', vars: { repo } }
  if (r?.failed) return { tone: 'warn', key: 'check.failed', vars: { repo } }
  if (!r || r.reachable === false) return { tone: 'warn', key: 'check.unreachable', vars: { repo } }
  return { tone: 'ok', key: r.canWrite ? 'check.readWrite' : 'check.readOnly', vars: { repo: r.fullName || repo } }
}

// The sentence of one repository, for a test or a plain-text summary.
export const checkLine = (r, t) => { const i = checkItem(r); return t(i.key, i.vars) }

// Why a call of the check tool failed, as the failed field of checkItem. A 401
// or 403, or the words GitHub uses for a token it turned down, is a refusal.
export function checkFailure(e) {
  const status = Number(e?.status || e?.code)
  if (status === 401 || status === 403) return 'refused'
  return /bad credentials|unauthori[sz]ed|forbidden|expired|\b40[13]\b/i.test(String(e?.message || '')) ? 'refused' : 'other'
}

// The rows of the permission table: what to give the token for each mode.
export const PERMISSIONS = [
  { id: 'contents', read: 'read', write: 'read' },
  { id: 'issues', read: 'read', write: 'readwrite' },
  { id: 'pulls', read: 'read', write: 'readwrite' },
  { id: 'metadata', read: 'auto', write: 'auto' },
]
