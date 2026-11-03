import test from 'node:test'
import assert from 'node:assert/strict'
import { checkLine, extraBody, extraForm, extraValid, githubSecret, parseRepos, reposProblem, tokenProblem, WRITE_MODES, writeModeOf } from '../src/repos.js'
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
  assert.match(checkLine({ repo: 'a/b', reachable: true, fullName: 'a/b', canWrite: true }, t), /can write/)
})

test('github: every word the page asks for exists', () => {
  for (const key of ['title', 'intro', 'new', 'token', 'tokenHelp', 'tokenShape', 'tokenSpace', 'repos', 'reposHelp', 'reposShape', 'reposMany', 'writeMode', 'test', 'testOk', 'save', 'cancel', 'empty', 'noRepos', 'reposOf', 'agentOf', 'confirmDelete', 'setup']) {
    assert.ok(en[key], key)
  }
})

test('github: the write mode defaults to off, rides in the request and reads back from a saved binding', () => {
  assert.deepEqual(WRITE_MODES, ['off', 'propose', 'auto'])
  assert.equal(writeModeOf(undefined), 'off')
  assert.equal(writeModeOf('always'), 'off', 'an unknown value is never taken as a permission')
  assert.equal(extraForm({}).write_mode, 'off')
  assert.equal(extraForm({ write_mode: 'propose' }).write_mode, 'propose')
  assert.equal(extraBody({ repos_text: 'a/b', write_mode: 'auto' }).write_mode, 'auto')
  assert.equal(extraBody({ repos_text: 'a/b' }).write_mode, 'off', 'a form without the field sends off')
  const body = connectorPayload({ ...emptyConnector(), id: 'b1', name: 'Work', agent_id: 'a' }, 'github', '', extraBody({ repos_text: 'a/b', write_mode: 'propose' }))
  assert.equal(body.write_mode, 'propose')
})

test('github: every write mode has its words, and the note says the text is public', () => {
  for (const m of WRITE_MODES) {
    assert.ok(en.writeMode[m], m)
    assert.ok(en.writeMode.note[m], m)
    assert.ok(en.writeMode.row[m], m)
  }
  assert.match(en.writeMode.note.propose, /public/)
  assert.match(en.writeMode.note.auto, /public/)
})

test('github: a connection can be saved without an agent, and the page has words for it', () => {
  const form = { ...emptyConnector(), name: 'Work', token: 'github_pat_x' }
  assert.equal(canSave(form, false, true), false, 'a kind that needs an agent refuses an empty one')
  assert.equal(canSave(form, false, true, null, true), true, 'agentOptional lets the connection hold credentials only')
  assert.equal(connectorPayload(form, 'github', 'github_pat_x', { enabled: false }).agent_id, '')
  for (const key of ['agentCredentialsOnly', 'agentOptionalHint', 'credentialsOnly']) assert.ok(en[key], key)
})
