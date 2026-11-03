import test from 'node:test'
import assert from 'node:assert/strict'
import { checkLine, extraBody, extraForm, extraValid, githubSecret, parseRepos, reposProblem, tokenProblem } from '../src/repos.js'
import { canSave, connectorPayload, emptyConnector } from '../../../internal/connector-ui/src/connector.js'
import en from '../src/i18n/github.en.json' with { type: 'json' }

test('github: only a fine-grained token is taken, and nothing typed keeps the saved one', () => {
  assert.equal(tokenProblem({}), '')
  assert.equal(tokenProblem({ token: 'github_pat_abc' }), '')
  assert.equal(tokenProblem({ token: 'ghp_classic' }), 'tokenShape')
  assert.equal(tokenProblem({ token: 'github_pat_a b' }), 'tokenSpace')
  assert.equal(githubSecret({ token: ' github_pat_abc ' }), 'github_pat_abc')
  assert.equal(githubSecret({ token: 'ghp_classic' }), '')
})

test('github: the repository list is read from lines, commas and spaces without repeats', () => {
  assert.deepEqual(parseRepos('acme/widgets\n Acme/Tools, acme/widgets  x/y'), ['acme/widgets', 'Acme/Tools', 'x/y'])
  assert.deepEqual(parseRepos(''), [])
  assert.equal(reposProblem('acme/widgets\nx/y'), '')
  assert.equal(reposProblem('widgets'), 'reposShape')
  assert.equal(reposProblem('a/b/c'), 'reposShape')
  assert.equal(reposProblem('a/../b'), 'reposShape')
  assert.equal(reposProblem(Array.from({ length: 51 }, (_, i) => `o/r${i}`).join('\n')), 'reposMany')
})

test('github: the list rides in the request, a binding is never switched on, and a saved one reads back', () => {
  const form = { ...emptyConnector(), name: 'Work', agent_id: 'a', token: 'github_pat_x', ...extraForm({ resources: ['acme/widgets'] }) }
  assert.equal(form.repos_text, 'acme/widgets')
  const body = connectorPayload(form, 'github', githubSecret(form), { enabled: false, accept_any: false, ...extraBody({ ...form, repos_text: 'acme/widgets\nacme/tools' }) })
  assert.equal(body.kind, 'github')
  assert.equal(body.token, 'github_pat_x')
  assert.equal(body.enabled, false)
  assert.deepEqual(body.resources, ['acme/widgets', 'acme/tools'])
  const update = connectorPayload({ ...emptyConnector(), id: 'b1', name: 'Work', agent_id: 'a' }, 'github', '', extraBody({ repos_text: '' }))
  assert.equal('token' in update, false)
  assert.deepEqual(update.resources, [], 'an empty list clears the list')
  assert.equal(canSave(form, false, Boolean(githubSecret(form))), true)
  assert.equal(extraValid({ repos_text: 'nope' }), false)
})

test('github: the Test lines say reachable, read only, or not reachable', () => {
  const t = (key, o = {}) => en.check[key.replace('check.', '')].replace(/\{\{(\w+)\}\}/g, (_, k) => o[k])
  assert.match(checkLine({ repo: 'a/b', reachable: false }, t), /not reachable/)
  assert.match(checkLine({ repo: 'a/b', reachable: true, fullName: 'a/b', canWrite: false }, t), /read only/)
  assert.match(checkLine({ repo: 'a/b', reachable: true, fullName: 'a/b', canWrite: true }, t), /read access only/)
})

test('github: every word the page asks for exists', () => {
  for (const key of ['title', 'intro', 'new', 'token', 'tokenHelp', 'tokenShape', 'tokenSpace', 'repos', 'reposHelp', 'reposShape', 'reposMany', 'writeOff', 'test', 'testOk', 'save', 'cancel', 'empty', 'noRepos', 'reposOf', 'agentOf', 'confirmDelete', 'setup']) {
    assert.ok(en[key], key)
  }
})
