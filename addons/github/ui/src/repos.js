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

// The repositories of a text box: one per line, or separated by commas or
// spaces. Blank entries and repeats (any case) are dropped.
export function parseRepos(text) {
  const seen = new Set()
  const out = []
  for (const part of String(text || '').split(/[\s,]+/)) {
    const r = part.trim()
    if (!r || seen.has(r.toLowerCase())) continue
    seen.add(r.toLowerCase())
    out.push(r)
  }
  return out
}

// What is wrong with the typed list, as a key of the page's words, or ''.
export function reposProblem(text) {
  const list = parseRepos(text)
  if (list.length > MAX_REPOS) return 'reposMany'
  if (list.some((r) => !REPO.test(r) || r.includes('..'))) return 'reposShape'
  return ''
}

// The fields this page adds to a binding: its form values, the request body
// they make, and whether they allow saving.
export const extraForm = (c) => ({ repos_text: (c.resources || []).join('\n') })
export const extraBody = (form) => ({ resources: parseRepos(form.repos_text) })
export const extraValid = (form) => !reposProblem(form.repos_text)

// The line the Test button adds for each repository, from the check tool.
export function checkLine(r, t) {
  if (!r || r.reachable === false) return t('check.unreachable', { repo: r?.repo || '' })
  return t(r.canWrite ? 'check.readWrite' : 'check.readOnly', { repo: r.fullName || r.repo })
}
