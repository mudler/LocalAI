import test from 'node:test'
import assert from 'node:assert/strict'
import { createPluginsApi, PluginsError } from '../../src/utils/pluginsApi.js'
import {
  addedReasons, cardAction, categoriesOf, configForm, configPayload, contributions, diffLines, errorKey, groupInstalls,
  hookHandlerText, installChip, jobLanded, installRights, mcpServerText, marketplaceChip, pluginOps, resolveView, reviewTarget, scopeChoices, shortSha, skippedKey, textOnly, updateReady,
} from '../../src/components/plugins/pluginState.js'
import { resolveTab, visibleTabs } from '../../src/components/addons/addonState.js'

// A fetch that records each call and answers from a table.
function fakeFetch(answers) {
  const calls = []
  const fn = async (url, init) => {
    calls.push({ url, init })
    const a = typeof answers === 'function' ? answers(url, init) : answers
    const body = a.body === undefined ? {} : a.body
    return {
      ok: (a.status || 200) < 400,
      status: a.status || 200,
      json: async () => (typeof body === 'string' ? JSON.parse(body) : body),
      text: async () => (typeof body === 'string' ? body : JSON.stringify(body)),
    }
  }
  return { fn, calls }
}

test('plugins tab: shows to everyone with the plugins feature, after the add-on tabs', () => {
  assert.deepEqual(visibleTabs(false, { plugins: true }), ['apps', 'plugins'])
  assert.deepEqual(visibleTabs(true, { plugins: true }), ['apps', 'browse', 'libraries', 'access', 'plugins'])
  assert.deepEqual(visibleTabs(false, { plugins: false }), ['apps'])
  assert.equal(resolveTab('plugins', false, { plugins: true }), 'plugins')
  assert.equal(resolveTab('plugins', true, { plugins: false }), 'apps')
  assert.equal(resolveView('installed'), 'installed')
  assert.equal(resolveView('nope'), 'browse')
})

test('plugins client: a code install sends the plugin ID in the confirm header; a text install sends none', async () => {
  const f = fakeFetch({ status: 202, body: { jobID: 'j1', plugin: 'lint@acme' } })
  const api = createPluginsApi({ base: (p) => `/base${p}`, fetch: f.fn })
  await api.install({ token: 't1', plugin: 'lint@acme', runsCode: true }, 'instance')
  await api.install({ token: 't2', plugin: 'docs@acme', runsCode: false })
  assert.equal(f.calls[0].url, '/base/api/plugins/installs')
  assert.equal(f.calls[0].init.headers['X-LocalAI-Confirm'], 'lint@acme')
  assert.deepEqual(JSON.parse(f.calls[0].init.body), { token: 't1', scope: 'instance' })
  assert.equal(f.calls[0].init.credentials, 'same-origin')
  assert.equal(f.calls[1].init.headers['X-LocalAI-Confirm'], undefined)
  assert.deepEqual(JSON.parse(f.calls[1].init.body), { token: 't2', scope: 'user' })
})

test('plugins client: an update with a code review sends the ticked files and the install ID', async () => {
  const f = fakeFetch({ status: 202, body: { jobID: 'j2' } })
  const api = createPluginsApi({ fetch: f.fn })
  await api.update('in1', { token: 'u1', needsCodeReview: true }, ['hooks/hooks.json'])
  await api.update('in2', { token: 'u2', needsCodeReview: false }, ['ignored'])
  assert.equal(f.calls[0].init.headers['X-LocalAI-Confirm'], 'in1')
  assert.deepEqual(JSON.parse(f.calls[0].init.body), { token: 'u1', approved_files: ['hooks/hooks.json'] })
  assert.equal(f.calls[1].init.headers['X-LocalAI-Confirm'], undefined)
  assert.deepEqual(JSON.parse(f.calls[1].init.body), { token: 'u2' })
  await api.rollback('in3')
  assert.equal(f.calls[2].init.headers['X-LocalAI-Confirm'], 'in3')
})

test('plugins client: a refusal carries the status, the type and the message', async () => {
  const f = fakeFetch({ status: 409, body: { error: { type: 'needs_reenable', message: 'turn it on again', param: '' } } })
  const api = createPluginsApi({ fetch: f.fn })
  await assert.rejects(api.setMe('in1', { enabled: true }), (e) => e instanceof PluginsError && e.status === 409 && e.type === 'needs_reenable' && e.message === 'turn it on again')
  assert.deepEqual(JSON.parse(f.calls[0].init.body), { enabled: true })
  assert.equal(f.calls[0].init.method, 'PUT')
})

test('plugins client: a file for review is text, with its path kept and each part encoded', async () => {
  const f = fakeFetch({ body: '<script>alert(1)</script>' })
  const api = createPluginsApi({ fetch: f.fn })
  const text = await api.previewFile('tok', 'hooks/my hook.sh')
  assert.equal(text, '<script>alert(1)</script>')
  assert.equal(f.calls[0].url, '/api/plugins/inspect/tok/files/hooks/my%20hook.sh')
})

test('plugins client: the catalog query leaves out empty fields; the chat switch sends the off list', async () => {
  const f = fakeFetch((url) => ({ body: url.includes('/catalog') ? { items: [{ plugin: 'a@m' }], total: 1 } : { off: ['in1'] } }))
  const api = createPluginsApi({ fetch: f.fn })
  const page = await api.catalog({ q: 'lint', marketplace: '', limit: 48 })
  assert.equal(f.calls[0].url, '/api/plugins/catalog?q=lint&limit=48')
  assert.equal(page.total, 1)
  assert.deepEqual(await api.setChatOff('c 1', ['in1']), ['in1'])
  assert.equal(f.calls[1].url, '/api/chats/c%201/plugins')
  assert.deepEqual(JSON.parse(f.calls[1].init.body), { off: ['in1'] })
})

test('plugins: a user sees Install only on a plugin that is not known to run code', () => {
  const text = { plugin: 'a@m', supported: true, runsCode: false, installs: [] }
  const unknown = { plugin: 'b@m', supported: true, installs: [] }
  const code = { plugin: 'c@m', supported: true, runsCode: true, installs: [] }
  assert.equal(cardAction(text, { isAdmin: false }), 'install')
  assert.equal(cardAction(unknown, { isAdmin: false }), 'install')
  assert.equal(cardAction(code, { isAdmin: false }), 'admin-only')
  assert.equal(cardAction(code, { isAdmin: true }), 'install')
  assert.equal(cardAction(text, { isAdmin: false, userInstalls: false }), 'off')
  assert.equal(cardAction({ ...text, supported: false }, { isAdmin: true }), 'unsupported')
  assert.equal(cardAction(text, { busy: true }), 'installing')
})

test('plugins: a card offers Update to who may approve it, Installed otherwise', () => {
  const inst = { plugin: 'a@m', supported: true, installs: [{ id: 'i1', scope: 'instance', state: 'update_available' }] }
  const mine = { plugin: 'a@m', supported: true, installs: [{ id: 'i2', scope: 'user', state: 'update_available' }] }
  assert.equal(cardAction(inst, { isAdmin: true }), 'update')
  assert.equal(cardAction(inst, { isAdmin: false }), 'installed')
  assert.equal(cardAction(mine, { isAdmin: false }), 'update')
  assert.equal(cardAction({ ...inst, installs: [{ id: 'i1', scope: 'instance', state: 'installed' }] }, { isAdmin: true }), 'installed')
})

test('plugins: the scopes of the review follow spec 9.2', () => {
  assert.deepEqual(scopeChoices({ isAdmin: false, runsCode: false }), ['user'])
  assert.deepEqual(scopeChoices({ isAdmin: true, runsCode: true }), ['instance'])
  assert.deepEqual(scopeChoices({ isAdmin: true, runsCode: false }), ['instance', 'user'])
})

test('plugins: what a plugin adds, with each remote host once', () => {
  const model = {
    skills: [{ name: 'a' }, { name: 'b' }], commands: [{ name: 'c' }], agents: [],
    mcpServers: [{ name: 's', type: 'http', url: 'https://mcp.example.com/x' }, { name: 'l', type: 'stdio', command: 'x' }],
    hooks: [{ event: 'PreToolUse' }],
  }
  assert.deepEqual(contributions(model, { hosts: ['mcp.example.com'] }), [
    { key: 'skills', count: 2 }, { key: 'commands', count: 1 }, { key: 'connects', host: 'mcp.example.com' }, { key: 'hooks', count: 1 },
  ])
  assert.deepEqual(contributions(null), [])
})

test('plugins: installs group by scope and say what the viewer may change', () => {
  const list = [
    { id: '1', name: 'zeta', scope: 'instance' }, { id: '2', name: 'alpha', scope: 'user', owner: 'u1' }, { id: '3', name: 'beta', scope: 'instance' },
  ]
  const g = groupInstalls(list)
  assert.deepEqual(g.instance.map((i) => i.id), ['3', '1'])
  assert.deepEqual(g.user.map((i) => i.id), ['2'])
  assert.deepEqual(installRights({ scope: 'instance', state: 'update_available', previousDigest: 'd' }, { isAdmin: false, userId: 'u1' }), { update: false, rollback: false, remove: false })
  assert.deepEqual(installRights({ scope: 'user', owner: 'u1', state: 'update_available', previousDigest: 'd' }, { userId: 'u1' }), { update: true, rollback: true, remove: true })
  assert.deepEqual(installRights({ scope: 'user', owner: 'u2', state: 'installed' }, { isAdmin: true }), { update: false, rollback: false, remove: true })
  assert.equal(installChip({ needsReenable: true }).key, 'needsReenable')
  assert.equal(installChip({ state: 'update_available' }).status, 'update-available')
  assert.equal(installChip({ state: 'installed' }), null)
})

test('plugins: the update review needs a tick for each code file when it asks for a code review', () => {
  const p = { needsCodeReview: true, codeFiles: ['hooks/hooks.json', 'bin/run'] }
  assert.equal(updateReady(p, new Set()), false)
  assert.equal(updateReady(p, new Set(['hooks/hooks.json'])), false)
  assert.equal(updateReady(p, ['hooks/hooks.json', 'bin/run']), true)
  assert.equal(updateReady({ needsCodeReview: false }, []), true)
  assert.equal(updateReady(null, []), false)
  assert.deepEqual(addedReasons({ oldCodeReasons: ['bin'], newCodeReasons: ['bin', 'hook-command'] }), ['hook-command'])
})

test('plugins: the update review needs a tick for the code components when the new version adds some', () => {
  const p = { needsCodeReview: true, codeFiles: [], codeItems: ['hook SessionStart: curl x | sh'] }
  assert.equal(updateReady(p, new Set()), false)
  assert.equal(updateReady(p, new Set(), true), true)
  assert.equal(updateReady({ needsCodeReview: true, codeFiles: [] }, new Set()), true)
})

test('plugins: a diff reads as lines with their kind', () => {
  assert.deepEqual(diffLines('--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n same').map((l) => l.kind), ['file', 'file', 'hunk', 'del', 'add', 'ctx'])
})

test('plugins: small helpers', () => {
  assert.equal(shortSha('0123456789abcdef'), '0123456')
  assert.equal(shortSha('sha256:abcdef0123'), 'abcdef0')
  assert.equal(errorKey({ type: 'invalid_token' }), 'plugins.errors.invalid_token')
  assert.equal(errorKey({ type: 'weird' }), '')
  assert.equal(skippedKey('unsupported-component'), 'plugins.skipped.unsupported')
  assert.equal(skippedKey('ignored-field'), 'plugins.skipped.ignored')
  assert.equal(skippedKey('not-found'), 'plugins.skipped.invalid')
  assert.equal(skippedKey('other'), '')
  assert.deepEqual(categoriesOf([{ category: 'b' }, { category: 'a' }, {}, { category: 'b' }]), ['a', 'b'])
  assert.deepEqual(textOnly([{ runsCode: true }, { runsCode: false }, {}], true).length, 2)
  assert.equal(marketplaceChip({ enabled: true, refreshedAt: 'x' }).key, 'ready')
  assert.equal(marketplaceChip({ enabled: true, lastError: 'fetch' }).key, 'error')
  assert.equal(marketplaceChip({ enabled: true, suspended: true, refreshedAt: 'x' }).key, 'suspended')
  assert.equal(marketplaceChip({ enabled: false, refreshedAt: 'x' }).key, 'off')
  assert.equal(marketplaceChip({ enabled: true }).key, 'pending')
  const ops = pluginOps([{ isPlugin: true, name: 'a@m' }, { isAddon: true, name: 'x' }])
  assert.deepEqual([...ops.keys()], ['a@m'])
})

test('plugins: the settings form keeps sensitive options out and saves numbers as numbers', () => {
  const opts = [
    { key: 'url', type: 'string', default: 'https://x' }, { key: 'n', type: 'number' }, { key: 'on', type: 'boolean', default: true },
    { key: 'token', type: 'string', sensitive: true },
  ]
  const form = configForm(opts, { n: 3 })
  assert.deepEqual(form, { url: 'https://x', n: '3', on: true })
  assert.deepEqual(configPayload(opts, { ...form, url: '' }), { n: 3, on: true })
})

test('plugins: the review link names a plugin as name@marketplace', () => {
  assert.deepEqual(reviewTarget('lint-hooks@acme'), { name: 'lint-hooks', marketplace: 'acme' })
  assert.equal(reviewTarget('lint-hooks'), null)
  assert.equal(reviewTarget('a/b@c'), null)
  assert.equal(reviewTarget(''), null)
})

test('plugins: the review shows the arguments of a command hook, the URL of an http hook and a headersHelper', () => {
  assert.equal(hookHandlerText({ type: 'command', command: 'run.sh', args: ['--fix', 'a b'] }), 'run.sh --fix a b')
  assert.equal(hookHandlerText({ type: 'http', url: 'https://collect.example.com/hook' }), 'http https://collect.example.com/hook')
  assert.equal(hookHandlerText({ type: 'prompt' }), 'prompt')
  assert.equal(mcpServerText({ url: 'https://api.example.com/mcp', headersHelper: './bin/token.sh' }), 'https://api.example.com/mcp · headersHelper: ./bin/token.sh')
  assert.equal(mcpServerText({ command: 'node', args: ['s.js'] }), 'node s.js')
})

test('plugins: a queued install has landed when the plugin shows in the list, an update when its commit changed', () => {
  const list = [{ plugin: 'fmt@team', sha: 'b' }]
  assert.equal(jobLanded(list, 'fmt@team'), true)
  assert.equal(jobLanded(list, 'lint@team'), false)
  assert.equal(jobLanded(list, 'fmt@team', 'a'), true)
  assert.equal(jobLanded(list, 'fmt@team', 'b'), false)
  assert.equal(jobLanded(null, 'fmt@team'), false)
})
