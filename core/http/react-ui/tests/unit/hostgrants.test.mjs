import test from 'node:test'
import assert from 'node:assert/strict'
import {
  normalizePreview, initialTicks, approvedOps, approvalBody, groupOps, installBlock, tickable,
  normalizeOverview, editedOps, opEditable, blanketHolders, pendingApprovals, blanketBody, confirmMatches,
  BLANKET_CONFIRM, withDenyRule, validateDenyDraft, denyRows, hostErrorKey, sourceOf, needsStep,
} from '../../src/components/addons/hostGrantsState.js'
import { createHostGrantsApi, classifyHostGrants } from '../../src/utils/hostGrantsApi.js'
import { createAddonsApi, AddonsError } from '../../src/utils/addonsApi.js'
import { runInstallAndTurnOn } from '../../src/components/addons/installAndTurnOn.js'
import { describeError } from '../../src/components/addons/errors.js'

const json = (status, body) => ({ ok: status >= 200 && status < 300, status, json: async () => body })

const PREVIEW = {
  digest: 'sha256:aa', version: '1.1.0', revision: 7, nonce: 'n1', official: true,
  requested: { required: ['workspace.materialize', 'workspace.commit'], optional: ['egress.check', 'quantize.start'] },
  ops: [
    { name: 'workspace.materialize', title: 'Copy files in', text: 'Copy files.', risk: 'changes data', status: 'not allowed' },
    { name: 'workspace.commit', title: 'Write files back', text: 'Write.', risk: 'changes data', status: 'allowed', source: 'file' },
    { name: 'egress.check', title: 'Ask about network', text: 'Ask.', risk: 'network', status: 'not_allowed' },
    { name: 'quantize.start', title: 'Start quantization', text: 'Run.', risk: 'runs jobs', status: 'blocked', reason: 'denied_by_admin' },
  ],
}

test('hostgrants: a preview marks required operations from the requested list and keeps statuses', () => {
  const p = normalizePreview(PREVIEW)
  assert.deepEqual(p.ops.map(o => [o.name, o.required, o.status]), [
    ['workspace.materialize', true, 'not_allowed'],
    ['workspace.commit', true, 'allowed'],
    ['egress.check', false, 'not_allowed'],
    ['quantize.start', false, 'blocked'],
  ])
  assert.equal(p.ops[0].risk, 'changes_data')
  assert.equal(p.ops[1].source, 'deployment')
  assert.equal(p.official, true)
  assert.equal(normalizePreview({}).official, null)
  assert.equal(needsStep(normalizePreview({})), false)
  assert.equal(needsStep(p), true)
})

test('hostgrants: required operations start ticked, optional ones start unticked, a hint ticks an optional one', () => {
  const p = normalizePreview(PREVIEW)
  assert.deepEqual(initialTicks(p), ['workspace.materialize'])
  const hinted = normalizePreview({ ...PREVIEW, ops: PREVIEW.ops.map(o => (o.name === 'egress.check' ? { ...o, hint: true } : o)) })
  assert.deepEqual(initialTicks(hinted), ['workspace.materialize', 'egress.check'])
})

test('hostgrants: the approval never leaves out a required operation and never names a blocked one', () => {
  const p = normalizePreview(PREVIEW)
  assert.deepEqual(approvedOps(p, []), ['workspace.materialize'])
  assert.deepEqual(approvedOps(p, ['egress.check', 'quantize.start', 'nope.x']), ['workspace.materialize', 'egress.check'])
  const body = approvalBody(p, ['egress.check'], { operationId: 'op1', action: 'install', sourceId: 'official', addonId: 'sandbox', version: '1.1.0' })
  assert.deepEqual(body, {
    operationId: 'op1', action: 'install', sourceId: 'official', addonId: 'sandbox', version: '1.1.0',
    approve: { digest: 'sha256:aa', ops: ['workspace.materialize', 'egress.check'], revision: 7, nonce: 'n1' },
  })
  assert.equal(tickable(p.ops[1]), false)
})

test('hostgrants: install is blocked when a required operation is blocked or unsupported', () => {
  assert.equal(installBlock(normalizePreview(PREVIEW)), null)
  const blocked = normalizePreview({ ...PREVIEW, ops: PREVIEW.ops.map(o => (o.name === 'workspace.materialize' ? { ...o, status: 'blocked' } : o)) })
  assert.deepEqual(installBlock(blocked), { kind: 'blocked', ops: ['workspace.materialize'] })
  const unsupported = normalizePreview({ ...PREVIEW, ops: PREVIEW.ops.map(o => (o.name === 'workspace.materialize' ? { ...o, status: 'unsupported' } : o)) })
  assert.deepEqual(installBlock(unsupported), { kind: 'unsupported', ops: ['workspace.materialize'] })
  assert.deepEqual(installBlock(normalizePreview({ ...PREVIEW, blockedRequired: ['x.y'] })), { kind: 'blocked', ops: ['x.y'] })
  assert.equal(normalizePreview({ ...PREVIEW, blockedRequired: ['x.y'] }).canInstall, false)
})

test('hostgrants: operations group by family in the order the owner sent them', () => {
  const g = groupOps(normalizePreview(PREVIEW).ops)
  assert.deepEqual(g.map(x => [x.family, x.ops.length]), [['workspace', 2], ['egress', 1], ['quantize', 1]])
})

const OVERVIEW = {
  revision: 3, locked: false,
  blanket: { mode: 'official', includeOptional: false, setBy: 'ui', revision: 2 },
  deny: { file: { '*': ['connector.'] }, ui: { sandbox: ['workspace.admin'] } },
  addons: [
    {
      id: 'sandbox', title: 'Sandbox', version: '1.0.0', state: 'running',
      requested: { required: ['workspace.materialize'], optional: ['egress.check'] },
      ops: [
        { name: 'workspace.materialize', status: 'allowed', source: 'blanket', at: '2026-11-03T10:00:00Z' },
        { name: 'egress.check', status: 'allowed', source: 'install' },
        { name: 'workspace.commit', status: 'allowed', source: 'file' },
      ],
      noLongerRequested: ['egress.audit'],
      pendingUpgrade: { version: '1.1.0', delta: ['workspace.export'] },
    },
    { id: 'plain', title: 'Plain', version: '0.1.0', state: 'stopped', ops: [] },
  ],
}

test('hostgrants: the overview keeps sources in three words and finds blanket holders and waiting upgrades', () => {
  const o = normalizeOverview(OVERVIEW)
  assert.deepEqual(o.addons[0].ops.map(x => x.source), ['blanket', 'you', 'deployment'])
  assert.deepEqual(blanketHolders(o), [{ id: 'sandbox', title: 'Sandbox', ops: ['workspace.materialize'] }])
  assert.deepEqual(pendingApprovals(o).map(a => a.id), ['sandbox'])
  assert.equal(o.blanket.mode, 'official')
  assert.equal(normalizeOverview({ blanket: { mode: 'weird' } }).blanket.mode, 'off')
  assert.equal(normalizeOverview({ blanket: { setBy: 'deployment' } }).blanket.setBy, 'deployment')
  assert.equal(sourceOf('upgrade'), 'you')
})

test('hostgrants: deployment grants are read-only and an edit never carries them or a blocked operation', () => {
  const o = normalizeOverview(OVERVIEW)
  const a = o.addons[0]
  assert.equal(opEditable(a.ops[2]), false)
  assert.equal(opEditable(a.ops[0]), true)
  assert.equal(opEditable(a.ops[0], true), false)
  assert.deepEqual(editedOps(a, {}), ['workspace.materialize', 'egress.check', 'egress.audit'])
  assert.deepEqual(editedOps(a, { 'egress.check': false }), ['workspace.materialize', 'egress.audit'])
})

test('hostgrants: the blanket confirm text must match exactly and differs per scope', () => {
  assert.equal(confirmMatches('official', 'grant official add-ons what they declare'), true)
  assert.equal(confirmMatches('official', '  grant official add-ons what they declare '), true)
  assert.equal(confirmMatches('official', BLANKET_CONFIRM.any), false)
  assert.equal(confirmMatches('any', 'Grant any library add-on what it declares'), false)
  assert.equal(confirmMatches('off', ''), false)
})

test('hostgrants: the blanket body carries the revision and, for off, the revoke choice', () => {
  const o = normalizeOverview(OVERVIEW)
  assert.deepEqual(blanketBody(o, { mode: 'any', includeOptional: true, applyToInstalled: true, confirm: BLANKET_CONFIRM.any }),
    { revision: 3, mode: 'any', includeOptional: true, confirm: BLANKET_CONFIRM.any, applyToInstalled: true })
  assert.deepEqual(blanketBody(o, { mode: 'off', includeOptional: false, revokeOnOff: true }),
    { revision: 3, mode: 'off', includeOptional: false, revokeOnOff: true })
})

test('hostgrants: deny rules list file and ui rules, and a star stands alone', () => {
  const o = normalizeOverview(OVERVIEW)
  assert.deepEqual(denyRows(o.deny.file, 'file').map(r => r.key), ['file:*:connector.'])
  assert.deepEqual(withDenyRule({ sandbox: ['egress.'] }, { target: 'sandbox', entry: '*' }, true), { sandbox: ['*'] })
  assert.deepEqual(withDenyRule({ sandbox: ['*'] }, { target: 'sandbox', entry: 'egress.' }, true), { sandbox: ['*'] })
  assert.deepEqual(withDenyRule({ sandbox: ['egress.'] }, { target: 'sandbox', entry: 'egress.' }, false), {})
  assert.deepEqual(withDenyRule({}, { target: '*', entry: 'egress.' }, true), { '*': ['egress.'] })
  assert.equal(validateDenyDraft({ target: '*', entry: 'egress.' }), '')
  assert.equal(validateDenyDraft({ target: 'sandbox', entry: 'workspace.admin' }), '')
  assert.equal(validateDenyDraft({ target: '*', entry: '*' }), '')
  assert.equal(validateDenyDraft({ target: 'Bad Id', entry: 'egress.' }), 'target')
  assert.equal(validateDenyDraft({ target: '*', entry: 'Egress Check' }), 'entry')
})

test('hostgrants: typed refusals are final and have words of their own', () => {
  const c = classifyHostGrants(409, { error: { type: 'approval_stale', message: 'stale', ops: ['a.b'] } })
  assert.equal(c.kind, 'invalid')
  assert.equal(c.type, 'approval_stale')
  assert.equal(classifyHostGrants(403, { error: { type: 'not_human_surface' } }).type, 'not_human_surface')
  assert.equal(classifyHostGrants(503, {}).kind, 'unavailable')
  assert.equal(hostErrorKey({ type: 'revision_conflict' }), 'revision_conflict')
  assert.equal(hostErrorKey({ kind: 'network' }), 'network')
  const t = (k) => k
  assert.equal(describeError(new AddonsError(c), t), 'hostAccess.errors.approval_stale')
})

test('hostgrants: writes send the confirm header and the strict bodies, reads send none', async () => {
  const seen = []
  const fetch = async (url, init) => { seen.push({ url, init }); return json(200, { revision: 4 }) }
  const addons = createAddonsApi({ fetch })
  const api = createHostGrantsApi(addons)
  await api.overview()
  await api.preview({ sourceId: 'official', addonId: 'sandbox', version: '1.0.0' })
  await api.install({ operationId: 'o1', action: 'install', sourceId: 'official', addonId: 'sandbox', version: '1.0.0', approve: { digest: 'sha256:aa', ops: ['egress.check'], revision: 1, nonce: 'n' } })
  await api.setOps('sandbox', { revision: 4, ops: ['egress.check'] })
  await api.revoke('sandbox', 4)
  await api.setBlanket({ revision: 4, mode: 'off', includeOptional: false, revokeOnOff: false })
  await api.setDeny({ revision: 4, deny: { '*': ['egress.'] }, dryRun: true })
  const by = (suffix) => seen.find(s => s.url.endsWith(suffix))
  assert.equal(by('/api/addons/host-grants').init.method, 'GET')
  assert.equal(by('/api/addons/host-grants').init.headers['X-LocalAI-Confirm'], undefined)
  assert.equal(by('/preview').init.headers['X-LocalAI-Confirm'], undefined)
  assert.equal(by('/install').init.headers['X-LocalAI-Confirm'], 'sandbox@sha256:aa')
  assert.equal(by('/sandbox').init.method, 'PUT')
  assert.equal(by('/sandbox').init.headers['X-LocalAI-Confirm'], 'sandbox')
  assert.equal(by('/sandbox?revision=4').init.method, 'DELETE')
  assert.equal(by('/blanket').init.headers['X-LocalAI-Confirm'], 'blanket')
  assert.deepEqual(JSON.parse(by('/deny').init.body), { revision: 4, deny: { '*': ['egress.'] }, dryRun: true })
})

test('hostgrants: install with an approval goes through the host-grants route, and without one through the plain install', async () => {
  const entry = { key: 'official/sandbox/1.0.0', id: 'sandbox', sourceId: 'official', version: '1.0.0' }
  const calls = []
  const run = async (key, { submit }) => { calls.push(key); await submit('op-1'); return { outcome: 'completed' } }
  const mk = () => ({
    run,
    api: { installed: async () => [] },
    adapter: { submitLifecycle: async (c) => { calls.push(`plain:${c.action}`); return { phase: 'completed' } } },
    hostGrants: { install: async (b) => { calls.push(`grants:${b.operationId}:${b.approve.ops.join(',')}`); return { payload: { Phase: 'completed' } } } },
    mark: () => {},
  })
  await runInstallAndTurnOn(mk(), entry, { approval: { approve: { ops: ['egress.check'] } } })
  assert.ok(calls.includes('grants:op-1:egress.check'))
  assert.ok(!calls.includes('plain:install'))
  calls.length = 0
  await runInstallAndTurnOn(mk(), entry)
  assert.ok(calls.includes('plain:install'))
  assert.ok(!calls.some(c => c.startsWith('grants:')))
})

test('hostgrants: a locked preview ticks nothing and approves no operation', () => {
  const p = normalizePreview({ ...PREVIEW, locked: true, lockedReason: 'no_auth' })
  assert.equal(p.locked, true)
  assert.equal(p.lockedReason, 'no_auth')
  assert.deepEqual(initialTicks(p), [])
  assert.deepEqual(approvedOps(p, ['egress.check']), [])
  assert.equal(normalizeOverview({ locked: true, lockedReason: 'no_auth' }).lockedReason, 'no_auth')
  assert.equal(normalizePreview(PREVIEW).locked, false)
})
