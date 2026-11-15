import test from 'node:test'
import assert from 'node:assert/strict'
import { allowsClassic, ANY_RESOURCE_CONFIRM, checkFailure, checkItem, checkLine, defaultName, extraBody, extraForm, extraValid, followName, githubSecret, hasAnyRepo, parseRepos, PERMISSIONS, reposIssue, reposProblem, tokenProblem, WIDER_TOKEN_CONFIRM, WRITE_MODES, writeModeOf } from '../src/repos.js'
import { canSave, connectorPayload, emptyConnector } from '../../../internal/connector-ui/src/connector.js'
import en from '../src/i18n/github.en.json' with { type: 'json' }

test('github: a fine-grained token is taken, a classic one only when allowed, and nothing typed keeps the saved one', () => {
  assert.equal(tokenProblem({}), '')
  assert.equal(tokenProblem({ token: 'github_pat_abc' }), '')
  assert.equal(tokenProblem({ token: 'ghp_classic' }), 'tokenClassic')
  assert.equal(tokenProblem({ token: 'ghp_classic', classic_token: true }), '')
  assert.equal(tokenProblem({ token: 'gho_other' }), 'tokenShape', 'only the two shapes are known')
  assert.equal(tokenProblem({ token: 'gho_other', classic_token: true }), 'tokenShape')
  assert.equal(tokenProblem({ token: 'github_pat_a b' }), 'tokenSpace')
  assert.equal(githubSecret({ token: ' github_pat_abc ' }), 'github_pat_abc')
  assert.equal(githubSecret({ token: 'ghp_classic' }), '')
  assert.equal(githubSecret({ token: ' ghp_classic ', classic_token: true }), 'ghp_classic')
})

test('github: the classic token choice rides as an option and needs its tick only when it is new', () => {
  assert.equal(allowsClassic({ options: { classicToken: 'allow' } }), true)
  assert.equal(allowsClassic({ options: { classicToken: 'refuse' } }), false)
  assert.equal(allowsClassic({}), false)
  const fresh = extraForm({})
  assert.deepEqual(extraBody(fresh).options, { classicToken: 'refuse' })
  assert.equal('confirm_wider_token' in extraBody(fresh), false)
  const on = { ...fresh, classic_token: true }
  assert.equal(extraValid(on), false, 'a new choice needs the tick')
  assert.deepEqual(extraBody({ ...on, classic_ack: true }).options, { classicToken: 'allow' })
  assert.equal(extraBody({ ...on, classic_ack: true }).confirm_wider_token, WIDER_TOKEN_CONFIRM)
  assert.equal(extraValid({ ...on, classic_ack: true }), true)
  const saved = extraForm({ options: { classicToken: 'allow' } })
  assert.equal(saved.classic_token, true)
  assert.equal(extraValid(saved), true, 'a saved choice is not asked again')
  assert.equal('confirm_wider_token' in extraBody(saved), false)
  assert.deepEqual(extraBody({ ...saved, classic_token: false }).options, { classicToken: 'refuse' }, 'switching it off needs no tick')
})

test('github: owner/* and a lone * are valid entries, * needs its tick, and partial stars are refused', () => {
  assert.equal(reposProblem('acme/*'), '')
  assert.equal(reposProblem('acme/*\nother/widgets'), '')
  assert.equal(reposProblem('*'), '')
  assert.equal(reposProblem('*/widgets'), 'reposShape')
  assert.equal(reposProblem('acme/wid*'), 'reposShape')
  assert.equal(reposProblem('a/*/b'), 'reposShape')
  assert.equal(reposProblem('**'), 'reposShape')
  assert.equal(reposProblem('*\nacme/widgets'), 'reposAnyAlone')
  assert.equal(hasAnyRepo('acme/*'), false)
  assert.equal(hasAnyRepo('x/y\n*'), true)
  const f = { ...extraForm({}), repos_text: '*' }
  assert.equal(extraValid(f), false)
  assert.equal(extraBody({ ...f, any_ack: true }).confirm_any_resource, ANY_RESOURCE_CONFIRM)
  assert.equal(extraValid({ ...f, any_ack: true }), true)
  assert.equal(extraValid({ ...extraForm({ resources: ['*'] }) }), true, 'a saved * is not asked again')
  assert.equal('confirm_any_resource' in extraBody({ ...extraForm({ resources: ['*'] }) }), false)
  assert.equal(extraValid({ ...extraForm({}), repos_text: 'acme/*' }), true, 'owner/* needs no tick')
  assert.equal(defaultName('acme/*'), 'GitHub: acme/*')
})

test('github: the repository list is read from lines and commas, never from spaces, without repeats', () => {
  assert.deepEqual(parseRepos('acme/widgets\n Acme/Tools, acme/widgets\r\nx/y'), ['acme/widgets', 'Acme/Tools', 'x/y'])
  assert.deepEqual(parseRepos('not a repo'), ['not a repo'], 'a space does not split an entry')
  assert.deepEqual(parseRepos(''), [])
  assert.equal(reposProblem('acme/widgets\nx/y'), '')
  assert.equal(reposProblem('widgets'), 'reposShape')
  assert.equal(reposProblem('a/b/c'), 'reposShape')
  assert.equal(reposProblem('a/../b'), 'reposShape')
  assert.equal(reposProblem('acme/widgets  x/y'), 'reposShape')
  assert.equal(reposProblem(Array.from({ length: 51 }, (_, i) => `o/r${i}`).join('\n')), 'reposMany')
})

test('github: the error names the line that is wrong', () => {
  assert.deepEqual(reposIssue('a/b\nnot a repo\nc/d'), { key: 'reposShape', entry: 'not a repo' })
  assert.equal(reposIssue('a/b'), null)
  assert.ok(reposIssue('x'.repeat(200)).entry.length <= 60)
})

test('github: the name follows the first repository until the person types one', () => {
  assert.equal(defaultName('acme/widgets\nx/y'), 'GitHub: acme/widgets')
  assert.equal(defaultName('nope'), '')
  assert.equal(followName('', '', 'acme/widgets'), 'GitHub: acme/widgets')
  assert.equal(followName('GitHub: acme/widgets', 'acme/widgets', 'acme/tools\nacme/widgets'), 'GitHub: acme/tools')
  assert.equal(followName('Docs', 'acme/widgets', 'acme/tools'), 'Docs', 'a typed name stays')
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

test('github: the Test words for a wildcard', () => {
  const t = (key, o = {}) => key.split('.').reduce((n, k) => n[k], en).replace(/\{\{(\w+)\}\}/g, (_, k) => o[k])
  assert.match(checkLine({ repo: 'acme/*', wildcard: 'owner' }, t), /every repository of this owner/)
  assert.equal(checkLine({ repo: '*', wildcard: 'any', count: 3 }, t), 'Any repository: the token reaches 3 repositories.')
  assert.match(checkLine({ repo: '*', wildcard: 'any', count: 100, more: true }, t), /at least 100/)
})

test('github: the Test says reachable, read only, not reachable, refused or unfinished, each with a tone', () => {
  const t = (key, o = {}) => key.split('.').reduce((n, k) => n[k], en).replace(/\{\{(\w+)\}\}/g, (_, k) => o[k])
  assert.match(checkLine({ repo: 'a/b', reachable: false }, t), /cannot reach/)
  assert.match(checkLine({ repo: 'a/b', reachable: true, fullName: 'a/b', canWrite: false }, t), /read only/)
  assert.match(checkLine({ repo: 'a/b', reachable: true, fullName: 'a/b', canWrite: true }, t), /can write/)
  assert.match(checkLine({ repo: 'a/b', failed: 'refused' }, t), /refused this token.*expired/)
  assert.equal(checkItem({ repo: 'a/b', reachable: true }).tone, 'ok')
  assert.equal(checkItem({ repo: 'a/b', reachable: false }).tone, 'warn')
  assert.equal(checkItem({ repo: 'a/b', failed: 'other' }).tone, 'warn')
  assert.equal(checkFailure({ status: 401 }), 'refused')
  assert.equal(checkFailure(new Error('Bad credentials')), 'refused')
  assert.equal(checkFailure(new Error('timeout')), 'other')
})

test('github: the permission table covers the permissions the steps name', () => {
  assert.deepEqual(PERMISSIONS.map((p) => p.id), ['contents', 'issues', 'pulls', 'metadata'])
  for (const p of PERMISSIONS) {
    assert.ok(en.setup.table[p.id], p.id)
    assert.ok(en.setup.table.levels[p.read] && en.setup.table.levels[p.write], p.id)
  }
  assert.equal(PERMISSIONS.find((p) => p.id === 'issues').write, 'readwrite')
  assert.equal(PERMISSIONS.find((p) => p.id === 'contents').write, 'read', 'writing never needs to push code')
})

test('github: every word the page asks for exists', () => {
  for (const key of ['title', 'intro', 'new', 'token', 'tokenHelp', 'tokenShape', 'tokenSpace', 'repos', 'reposHelp', 'reposShape', 'reposMany', 'writeMode', 'test', 'testOk', 'save', 'cancel', 'empty', 'noRepos', 'reposOf', 'agentOf', 'confirmDelete', 'setup', 'limits', 'approval', 'check']) {
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
  for (const key of ['agentCredentialsOnly', 'credentialsOnly', 'checkSeveral']) assert.ok(en[key], key)
  assert.equal(en.agentOptionalHint, undefined, 'a tool-only kind has no agent field, so no hint about one')
})

test('github: the page says what it does not do and links the token page', () => {
  assert.match(en.limits, /does not answer @mentions/)
  assert.match(en.setup.linkLabel, /new tab/)
})

test('github: every write mode has a title and a consequence line', () => {
  for (const m of WRITE_MODES) assert.ok(en.writeMode.line[m], m)
  assert.notEqual(en.writeMode.auto, 'Allowed')
})
