// Helpers of the GitHub page. No React, so a node test drives them.

// A fine-grained token is limited to the repositories it was made for. A
// classic token (ghp_) has scopes for the whole account, wider than the
// repository list, so a connection takes one only after the person allowed it
// with the classic_token choice and confirmed it. The host enforces that
// (token_guards of the kind); this page asks for it and says why.
export const TOKEN_PREFIX = 'github_pat_'
export const CLASSIC_PREFIX = 'ghp_'

// What the request must carry, as the host defines it.
export const WIDER_TOKEN_CONFIRM = 'accept wider token'
export const ANY_RESOURCE_CONFIRM = 'allow any resource'

// The option of the kind that allows a classic token.
export const CLASSIC_OPTION = 'classicToken'

// What is wrong with the typed token, as a key of the page's words, or ''.
// Nothing typed is no problem: an update without a token keeps the stored one.
export function tokenProblem(form) {
  const token = (form.token || '').trim()
  if (!token) return ''
  if (/\s/.test(token)) return 'tokenSpace'
  if (token.startsWith(CLASSIC_PREFIX)) return form.classic_token ? '' : 'tokenClassic'
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

// A repository is owner/name; owner/* stands for every repository of one
// owner and * for any repository the token reaches. The host matches them.
const OWNER_ALL = /^[A-Za-z0-9_.-]{1,100}\/\*$/
export const ANY_REPO = '*'
const validRepo = (r) => r === ANY_REPO || OWNER_ALL.test(r) || (REPO.test(r) && !r.includes('..'))

// Whether the list holds the entry that allows every repository.
export const hasAnyRepo = (text) => parseRepos(text).includes(ANY_REPO)

// The first thing wrong with the typed list: { key, entry } where key is a key
// of the page's words and entry is the line to name, or null.
export function reposIssue(text) {
  const list = parseRepos(text)
  if (list.length > MAX_REPOS) return { key: 'reposMany', entry: '' }
  const bad = list.find((r) => !validRepo(r))
  if (bad === undefined && list.includes(ANY_REPO) && list.length > 1) return { key: 'reposAnyAlone', entry: '' }
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
//
// classic_token is the person's choice to allow a classic token; classic_was
// and any_was say the saved connection already had a choice, which the host
// does not ask to confirm again. classic_ack and any_ack are the ticks of this
// edit and are never saved.
export const allowsClassic = (c) => c?.options?.[CLASSIC_OPTION] === 'allow'
export const extraForm = (c) => ({
  repos_text: (c.resources || []).join('\n'), write_mode: writeModeOf(c.write_mode),
  classic_token: allowsClassic(c), classic_was: allowsClassic(c), classic_ack: false,
  any_was: (c.resources || []).includes(ANY_REPO), any_ack: false,
})
export const extraBody = (form) => {
  const body = {
    resources: parseRepos(form.repos_text), write_mode: writeModeOf(form.write_mode),
    options: { [CLASSIC_OPTION]: form.classic_token ? 'allow' : 'refuse' },
  }
  if (form.classic_token && !form.classic_was && form.classic_ack) body.confirm_wider_token = WIDER_TOKEN_CONFIRM
  if (hasAnyRepo(form.repos_text) && !form.any_was && form.any_ack) body.confirm_any_resource = ANY_RESOURCE_CONFIRM
  return body
}
// Saving needs the ticks for a choice that is new: * and a classic token.
export const extraValid = (form) => !reposProblem(form.repos_text)
  && (!hasAnyRepo(form.repos_text) || form.any_was || Boolean(form.any_ack))
  && (!form.classic_token || form.classic_was || Boolean(form.classic_ack))

// What the Test button shows for one repository: { tone, key, vars }, where
// key is a key of the page's words under check. A failure of the check itself
// is r.failed: 'refused' when GitHub turned the token down, else 'other'.
export function checkItem(r) {
  const repo = r?.repo || ''
  if (r?.failed === 'refused') return { tone: 'warn', key: 'check.refused', vars: { repo } }
  if (r?.failed) return { tone: 'warn', key: 'check.failed', vars: { repo } }
  if (r?.wildcard === 'owner') return { tone: 'ok', key: 'check.owner', vars: { repo } }
  if (r?.wildcard === 'any') return { tone: 'ok', key: r.more ? 'check.anyMore' : 'check.any', vars: { count: r.count } }
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
