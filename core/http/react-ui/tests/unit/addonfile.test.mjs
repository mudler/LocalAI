import test from 'node:test'
import assert from 'node:assert/strict'
import { LOCAL_FILE, isLocalFile, normalizeFile, fileEntry, fileGate, fileAction, fileIdFromParams } from '../../src/components/addons/fileInstall.js'
import { approvalBody } from '../../src/components/addons/hostGrantsState.js'
import { createAddonFilesApi } from '../../src/utils/addonFilesApi.js'
import { createAddonsApi, AddonsError } from '../../src/utils/addonsApi.js'
import { runInstallAndTurnOn } from '../../src/components/addons/installAndTurnOn.js'

const ID = 'a'.repeat(32)
const RAW = {
  fileId: ID, name: 'memory-0.2.0-linux-amd64.zip', size: 1234, sha256: 'AB'.repeat(32), addonId: 'memory', title: 'Short memory',
  version: '0.2.0', platform: 'linux/amd64', api: { min: 2, max: 2 }, required: ['workspace.materialize'], optional: ['egress.check'],
  installed: true, installedVersion: '0.1.0', relation: 'upgrade', origin: 'local file, unsigned',
}

test('addonfile: the answer of the upload is checked field by field', () => {
  const f = normalizeFile(RAW)
  assert.equal(f.sha256, 'ab'.repeat(32))
  assert.deepEqual(f.api, { min: 2, max: 2 })
  assert.equal(f.relation, 'upgrade')
  assert.deepEqual(normalizeFile({ ...RAW, relation: 'weird', required: 'x', size: 'big' }).required, [])
  assert.equal(normalizeFile({ ...RAW, relation: 'weird' }).relation, 'new')
  assert.equal(normalizeFile(null).fileId, '')
})

test('addonfile: the dialog entry names the local file source and the staged file id as the version', () => {
  const e = fileEntry(normalizeFile(RAW))
  assert.equal(e.sourceId, LOCAL_FILE)
  assert.equal(e.version, ID)
  assert.equal(e.id, 'memory')
  assert.equal(e.digest, 'sha256:' + 'ab'.repeat(32))
  assert.equal(e.policy.requireSignature, false)
  assert.equal(isLocalFile(e.sourceId), true)
  assert.equal(isLocalFile('official'), false)
})

test('addonfile: the same version is never installed, an older one needs the tick', () => {
  const f = (relation) => normalizeFile({ ...RAW, relation })
  assert.deepEqual(fileGate(f('same'), { downgradeTicked: true }), { allowed: false, reason: 'same' })
  assert.deepEqual(fileGate(f('downgrade')), { allowed: false, reason: 'downgrade' })
  assert.equal(fileGate(f('downgrade'), { downgradeTicked: true }).allowed, true)
  assert.equal(fileGate(f('upgrade')).allowed, true)
  assert.equal(fileGate(f('new')).allowed, true)
  assert.equal(fileGate(null).allowed, false)
  assert.deepEqual(fileGate(f('new'), { locked: true }), { allowed: false, reason: 'locked' })
})

test('addonfile: an installed add-on is upgraded and a new one installed', () => {
  assert.equal(fileAction(normalizeFile(RAW)), 'upgrade')
  assert.equal(fileAction(normalizeFile({ ...RAW, installed: false, relation: 'new' })), 'install')
})

test('addonfile: only a well formed file id in the link opens the dialog', () => {
  assert.equal(fileIdFromParams(new URLSearchParams(`file=${ID}`)), ID)
  assert.equal(fileIdFromParams(new URLSearchParams('file=../x')), '')
  assert.equal(fileIdFromParams(new URLSearchParams('')), '')
  assert.equal(fileIdFromParams(null), '')
})

test('addonfile: the approval carries the downgrade confirmation only when it was given', () => {
  const preview = { digest: 'sha256:aa', ops: [], revision: 3, nonce: 'n', blockedRequired: [] }
  const base = { action: 'upgrade', sourceId: LOCAL_FILE, addonId: 'memory', version: ID }
  assert.equal('downgrade' in approvalBody(preview, [], base), false)
  assert.equal(approvalBody(preview, [], { ...base, downgrade: true }).downgrade, true)
  assert.equal(approvalBody(preview, [], base).version, ID)
})

test('addonfile: the upload is a multipart form with the file part, and the file routes are the documented ones', async () => {
  const seen = []
  const fetch = async (url, init) => {
    seen.push({ url, init })
    return { ok: true, status: 200, json: async () => RAW }
  }
  const api = createAddonFilesApi(createAddonsApi({ fetch }))
  const file = new File([new Uint8Array([80, 75])], 'x.zip')
  const out = await api.upload(file)
  assert.equal(out.fileId, ID)
  assert.equal(seen[0].url, '/api/addons/files')
  assert.equal(seen[0].init.method, 'POST')
  assert.ok(seen[0].init.body instanceof FormData)
  assert.equal(seen[0].init.body.get('file').name, 'x.zip')
  assert.equal(seen[0].init.headers['Content-Type'], undefined, 'the browser writes the boundary')
  await api.describe(ID)
  assert.equal(seen[1].url, `/api/addons/files/${ID}`)
  await api.remove(ID)
  assert.equal(seen[2].init.method, 'DELETE')
})

test('addonfile: a refusal of the file keeps the reason the owner gave', async () => {
  const fetch = async () => ({ ok: false, status: 400, json: async () => ({ error: { type: 'wrong_platform', message: 'This file is for linux/arm64; this host is linux/amd64' } }) })
  const api = createAddonFilesApi(createAddonsApi({ fetch }))
  await assert.rejects(api.upload(new File([new Uint8Array([1])], 'x.zip')), (e) => {
    assert.ok(e instanceof AddonsError)
    assert.match(e.message, /linux\/arm64/)
    return true
  })
})

test('addonfile: an upgrade from a file does not turn the add-on on, a new install does', async () => {
  const marks = []
  const ran = []
  const run = async (key, op) => { ran.push(key); await op.submit('op1', undefined); return { outcome: 'completed' } }
  const hostGrants = { install: async (body) => ({ payload: { OperationID: body.operationId, AddonID: body.addonId, Phase: 'completed', Generation: 2 } }) }
  const adapter = { submitLifecycle: async () => { throw new Error('plain install must not run') } }
  const api = { installed: async () => [{ id: 'memory', enabled: true }] }
  const entry = { key: 'k', id: 'memory', sourceId: LOCAL_FILE, version: ID }
  const approval = { action: 'upgrade', sourceId: LOCAL_FILE, addonId: 'memory', version: ID, approve: { digest: 'sha256:aa', ops: [], revision: 0, nonce: 'n' } }
  await runInstallAndTurnOn({ run, api, adapter, hostGrants, mark: v => marks.push(v.step) }, entry, { approval, turnOn: false })
  assert.deepEqual(ran, ['install:k'])
  assert.deepEqual(marks, ['install', 'done'])
  ran.length = 0; marks.length = 0
  adapter.submitLifecycle = async () => ({ payload: {} })
  await runInstallAndTurnOn({ run, api, adapter, hostGrants, mark: v => marks.push(v.step) }, entry, { approval })
  assert.deepEqual(ran, ['install:k', 'enable:memory:0'])
})

test('addonfile: a gone file is a typed answer, not an outage', async () => {
  const fetch = async () => ({ ok: false, status: 404, json: async () => ({ error: { type: 'not_found', message: 'No such file' } }) })
  const api = createAddonFilesApi(createAddonsApi({ fetch }))
  await assert.rejects(api.describe(ID), (e) => e.kind === 'invalid' && e.type === 'not_found' && e.status === 404)
})
